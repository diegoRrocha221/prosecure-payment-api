package handlers

import (
    "context"
    "encoding/json"
    "encoding/base64"
    "fmt"
    "log"
    "net/http"
    "strings"
    "time"
    "github.com/google/uuid"
    "prosecure-payment-api/models"
    "prosecure-payment-api/services/payment"
    "prosecure-payment-api/services/email"
    "prosecure-payment-api/database"
    "prosecure-payment-api/utils"
    "prosecure-payment-api/queue"
)

type PaymentHandler struct {
    db             *database.Connection
    paymentService *payment.Service
    emailService   *email.SMTPService
    queue          *queue.Queue
}

func NewPaymentHandler(db *database.Connection, ps *payment.Service, es *email.SMTPService, q *queue.Queue) (*PaymentHandler, error) {
    if db == nil {
        return nil, fmt.Errorf("database connection is required")
    }
    if ps == nil {
        return nil, fmt.Errorf("payment service is required")
    }
    if es == nil {
        return nil, fmt.Errorf("email service is required")
    }
    if q == nil {
        return nil, fmt.Errorf("queue is required")
    }

    return &PaymentHandler{
        db:             db,
        paymentService: ps,
        emailService:   es,
        queue:          q,
    }, nil
}

func sendErrorResponse(w http.ResponseWriter, status int, message string) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    json.NewEncoder(w).Encode(models.APIResponse{
        Status:  "error",
        Message: message,
    })
}

func sendSuccessResponse(w http.ResponseWriter, response models.APIResponse) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(http.StatusOK)
    json.NewEncoder(w).Encode(response)
}

// ProcessPayment processa um pagamento
func (h *PaymentHandler) ProcessPayment(w http.ResponseWriter, r *http.Request) {
    requestID := uuid.New().String()
    log.Printf("[RequestID: %s] Starting payment processing", requestID)

    captchaToken := r.Header.Get("h-captcha-response")
    if err := validateHCaptcha(captchaToken); err != nil {
        log.Printf("[RequestID: %s] Captcha validation failed: %v", requestID, err)
        sendErrorResponse(w, http.StatusBadRequest, "Security verification failed. Please try again.")
        return
    }

    var req models.PaymentRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        log.Printf("[RequestID: %s] Invalid request body: %v", requestID, err)
        sendErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %v", err))
        return
    }

    log.Printf("[RequestID: %s] Processing payment for checkout ID: %s", requestID, req.CheckoutID)

    // Check if checkout was already processed
    processed, err := h.db.IsCheckoutProcessed(req.CheckoutID)
    if err != nil {
        log.Printf("[RequestID: %s] Error checking checkout status: %v", requestID, err)
        sendErrorResponse(w, http.StatusInternalServerError, "Internal server error")
        return
    }

    if processed {
        log.Printf("[RequestID: %s] Checkout already processed: %s", requestID, req.CheckoutID)
        sendSuccessResponse(w, models.APIResponse{
            Status:  "success",
            Message: "Payment has been processed successfully",
        })
        return
    }

    acquired, err := h.db.LockCheckout(req.CheckoutID)
    if err != nil {
        log.Printf("[RequestID: %s] Error acquiring lock: %v", requestID, err)
        sendErrorResponse(w, http.StatusInternalServerError, "Internal server error")
        return
    }
    if !acquired {
        log.Printf("[RequestID: %s] Checkout is being processed: %s", requestID, req.CheckoutID)
        sendErrorResponse(w, http.StatusConflict, "This checkout is already being processed")
        return
    }
    
    defer h.db.ReleaseLock(req.CheckoutID)
    
    checkout, err := h.db.GetCheckoutData(req.CheckoutID)
    if err != nil {
        log.Printf("[RequestID: %s] Invalid checkout ID: %v", requestID, err)
        sendErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("Invalid checkout ID: %v", err))
        return
    }

    // Preencher informações adicionais do cliente para evitar duplicações e melhorar taxas de aprovação
    req.CustomerEmail = checkout.Email
    req.BillingInfo = &authorizenet.BillingInfoType{
        FirstName:   strings.Split(checkout.Name, " ")[0],
        LastName:    strings.Join(strings.Split(checkout.Name, " ")[1:], " "),
        Address:     checkout.Street,
        City:        checkout.City,
        State:       checkout.State,
        Zip:         checkout.ZipCode,
        Country:     "US",
        PhoneNumber: checkout.PhoneNumber,
    }

    // Validate card data before processing
    if !h.paymentService.ValidateCard(&req) {
        log.Printf("[RequestID: %s] Invalid card data", requestID)
        sendErrorResponse(w, http.StatusBadRequest, "Invalid card data: please check card number, expiration date and CVV")
        return
    }

    // Process initial payment authorization only
    resp, err := h.paymentService.ProcessInitialAuthorization(&req)
    if err != nil {
        log.Printf("[RequestID: %s] Payment processing failed: %v", requestID, err)
        sendErrorResponse(w, http.StatusInternalServerError, fmt.Sprintf("Payment processing failed: %v", err))
        return
    }

    if !resp.Success {
        log.Printf("[RequestID: %s] Payment unsuccessful: %s", requestID, resp.Message)
        sendErrorResponse(w, http.StatusBadRequest, resp.Message)
        return
    }

    // Create user accounts and send emails
    if err := h.createAccountsAndNotify(checkout, &req, resp.TransactionID); err != nil {
        log.Printf("[RequestID: %s] Failed to create account: %v", requestID, err)
        sendErrorResponse(w, http.StatusInternalServerError, fmt.Sprintf("Failed to create account: %v", err))
        return
    }

    // Verificar se a transação foi aprovada antes de enfileirar tarefas em background
    // Somente enfileiramos jobs para transações aprovadas
    if resp.Success {
        // Enqueue background tasks for void transaction and subscription creation
        h.enqueueBackgroundTasks(requestID, &req, resp.TransactionID)
    } else {
        log.Printf("[RequestID: %s] Transaction not approved. No background tasks will be queued.", requestID)
    }

    log.Printf("[RequestID: %s] Payment processed successfully for checkout ID: %s", requestID, req.CheckoutID)

    sendSuccessResponse(w, models.APIResponse{
        Status:  "success",
        Message: "Payment processed successfully",
        Data: map[string]interface{}{
            "transaction_id": resp.TransactionID,
            "checkout_id":   req.CheckoutID,
            "amount":       checkout.Total,
        },
    })
}

func (h *PaymentHandler) enqueueBackgroundTasks(requestID string, payment *models.PaymentRequest, transactionID string) {
    // Enqueue transaction void job
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    
    voidErr := h.queue.Enqueue(ctx, queue.JobTypeVoidTransaction, map[string]interface{}{
        "transaction_id": transactionID,
        "checkout_id":    payment.CheckoutID,
    })
    
    if voidErr != nil {
        log.Printf("[RequestID: %s] Error enqueueing void transaction job: %v", requestID, voidErr)
    }
    
    // Enqueue subscription creation job
    subscriptionErr := h.queue.Enqueue(ctx, queue.JobTypeCreateSubscription, map[string]interface{}{
        "checkout_id": payment.CheckoutID,
        "card_name":   payment.CardName,
        "card_number": payment.CardNumber,
        "expiry":      payment.Expiry,
        "cvv":         payment.CVV,
    })
    
    if subscriptionErr != nil {
        log.Printf("[RequestID: %s] Error enqueueing subscription creation job: %v", requestID, subscriptionErr)
    }
}

func (h *PaymentHandler) createAccountsAndNotify(checkout *models.CheckoutData, payment *models.PaymentRequest, transactionID string) error {
    tx, err := h.db.BeginTransaction()
    if err != nil {
        return fmt.Errorf("failed to begin transaction: %v", err)
    }

    masterUUID := uuid.New().String()
    log.Printf("Generated master UUID: %s", masterUUID)

    names := strings.Fields(checkout.Name)
    firstName := names[0]
    lastName := strings.Join(names[1:], " ")

    masterAccount := &models.MasterAccount{
        Name:             firstName,
        LastName:         lastName,
        ReferenceUUID:    masterUUID,
        Email:            checkout.Email,
        Username:         checkout.Username,
        PhoneNumber:      checkout.PhoneNumber,
        IsAnnually:      0,
        IsTrial:         1,
        State:           checkout.State,
        City:            checkout.City,
        Street:          checkout.Street,
        ZipCode:         checkout.ZipCode,
        AdditionalInfo:  checkout.Additional,
        Plan:            checkout.PlanID,
        PurchasedPlans:  checkout.PlansJSON,
        SimultaneousUsers: len(checkout.Plans),
        RenewDate:       time.Now().AddDate(0, 1, 0),
    }

    var total float64
    for _, plan := range checkout.Plans {
        if plan.Annually == 1 {
            masterAccount.IsAnnually = 1
            total += plan.Price * 10 
        } else {
            total += plan.Price
        }
    }
    masterAccount.TotalPrice = total

    if err := tx.SaveMasterAccount(masterAccount); err != nil {
        tx.Rollback()
        return fmt.Errorf("failed to create master account: %v", err)
    }

    user := &models.User{
        MasterReference: masterUUID,
        Username:        checkout.Username,
        Email:          checkout.Email,
        Passphrase:     checkout.Passphrase,
        IsMaster:       1,
        PlanID:         checkout.Plans[0].PlanID,
    }

    if err := tx.SaveUser(user); err != nil {
        tx.Rollback()
        return fmt.Errorf("failed to create user: %v", err)
    }

    cardData := &models.CardData{
        Number: payment.CardNumber,
        Expiry: payment.Expiry,
    }

    if err := tx.SavePaymentMethod(masterUUID, cardData); err != nil {
        tx.Rollback()
        return fmt.Errorf("failed to save payment method: %v", err)
    }

    trialInvoice := &models.Invoice{
        MasterReference: masterUUID,
        IsTrial:        1,
        Total:          0,
        DueDate:        time.Now(),
        IsPaid:         1,
    }

    if err := tx.SaveInvoice(trialInvoice); err != nil {
        tx.Rollback()
        return fmt.Errorf("failed to create trial invoice: %v", err)
    }

    futureDate := time.Now().AddDate(0, 1, 0)
    futureInvoice := &models.Invoice{
        MasterReference: masterUUID,
        IsTrial:        0,
        Total:          total,
        DueDate:        futureDate,
        IsPaid:         0,
    }

    if err := tx.SaveInvoice(futureInvoice); err != nil {
        tx.Rollback()
        return fmt.Errorf("failed to create future invoice: %v", err)
    }

    if err := tx.SaveTransaction(masterUUID, checkout.ID, 1.00, "authorized", transactionID); err != nil {
        tx.Rollback()
        return fmt.Errorf("failed to save transaction: %v", err)
    }

    // Inicialmente, salvar com subscription_id vazio, será atualizado pelo worker
    if err := tx.SaveSubscription(masterUUID, "pending", futureDate, ""); err != nil {
        tx.Rollback()
        return fmt.Errorf("failed to save subscription: %v", err)
    }

    if err := tx.Commit(); err != nil {
        return fmt.Errorf("failed to commit transaction: %v", err)
    }

    // Armazenar temporariamente os dados do cartão para uso pelo webhook
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    
    _, err = h.db.GetDB().ExecContext(ctx,
        `INSERT INTO temp_payment_data
         (checkout_id, card_number, card_expiry, card_cvv, card_name, created_at)
         VALUES (?, ?, ?, ?, ?, NOW())
         ON DUPLICATE KEY UPDATE
         card_number = VALUES(card_number),
         card_expiry = VALUES(card_expiry),
         card_cvv = VALUES(card_cvv),
         card_name = VALUES(card_name),
         created_at = NOW()`,
        checkout.ID, payment.CardNumber, payment.Expiry, payment.CVV, payment.CardName)
    
    if err != nil {
        log.Printf("Warning: Failed to store temporary payment data: %v", err)
    }

    code := utils.GenerateActivationCode()
    encodedUser := base64.StdEncoding.EncodeToString([]byte(checkout.Username))
    encodedEmail := base64.StdEncoding.EncodeToString([]byte(checkout.Email))
    encodedCode := base64.StdEncoding.EncodeToString([]byte(code))

    if err := h.db.UpdateUserActivationCode(checkout.Email, checkout.Username, code); err != nil {
        log.Printf("Warning: Failed to update activation code: %v", err)
    }

    activationURL := fmt.Sprintf(
        "https://prosecurelsp.com/users/active/activation.php?act=%s&emp=%s&cct=%s",
        encodedUser, encodedEmail, encodedCode,
    )

    activationEmailContent := h.generateActivationEmail(checkout.Username, activationURL)
    if err := h.emailService.SendEmail(
        checkout.Email,
        "Please Confirm Your Email Address",
        activationEmailContent,
    ); err != nil {
        log.Printf("Warning: Failed to send activation email: %v", err)
    }

    invoiceEmailContent := h.generateInvoiceEmail(checkout)
    if err := h.emailService.SendEmail(
        checkout.Email,
        "Your invoice has been delivered :)",
        invoiceEmailContent,
    ); err != nil {
        log.Printf("Warning: Failed to send invoice email: %v", err)
    }

    log.Printf("Successfully created accounts and sent notifications for master reference: %s", masterUUID)
    return nil
}

// ResetCheckoutStatus resets a checkout's status
func (h *PaymentHandler) ResetCheckoutStatus(w http.ResponseWriter, r *http.Request) {
    var req struct {
        CheckoutID string `json:"checkout_id"`
    }

    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        sendErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %v", err))
        return
    }

    if req.CheckoutID == "" {
        sendErrorResponse(w, http.StatusBadRequest, "Missing checkout ID")
        return
    }

    // Release any lock on this checkout
    if err := h.db.ReleaseLock(req.CheckoutID); err != nil {
        sendErrorResponse(w, http.StatusInternalServerError, fmt.Sprintf("Failed to reset checkout status: %v", err))
        return
    }

    sendSuccessResponse(w, models.APIResponse{
        Status:  "success",
        Message: "Checkout status reset successfully",
    })
}

// GenerateCheckoutID generates a new checkout ID
func (h *PaymentHandler) GenerateCheckoutID(w http.ResponseWriter, r *http.Request) {
    checkoutID := uuid.New().String()

    sendSuccessResponse(w, models.APIResponse{
        Status: "success",
        Data: map[string]interface{}{
            "checkout_id": checkoutID,
        },
    })
}

// UpdateCheckoutID updates a checkout with a new ID
func (h *PaymentHandler) UpdateCheckoutID(w http.ResponseWriter, r *http.Request) {
    var req struct {
        OldID string `json:"old_id"`
        NewID string `json:"new_id"`
    }

    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        sendErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %v", err))
        return
    }
    
    if req.OldID == "" || req.NewID == "" {
        sendErrorResponse(w, http.StatusBadRequest, "Missing old_id or new_id parameter")
        return
    }

    // In a real implementation, you'd update the checkout ID in the database
    // For this example, we'll just return success
    sendSuccessResponse(w, models.APIResponse{
        Status:  "success",
        Message: fmt.Sprintf("Checkout ID updated from %s to %s", req.OldID, req.NewID),
    })
}

// CheckCheckoutStatus checks the status of a checkout
func (h *PaymentHandler) CheckCheckoutStatus(w http.ResponseWriter, r *http.Request) {
    checkoutID := r.URL.Query().Get("id")
    if checkoutID == "" {
        sendErrorResponse(w, http.StatusBadRequest, "Missing checkout ID")
        return
    }

    // Check if checkout is processed
    processed, err := h.db.IsCheckoutProcessed(checkoutID)
    if err != nil {
        sendErrorResponse(w, http.StatusInternalServerError, fmt.Sprintf("Error checking checkout status: %v", err))
        return
    }

    // Check if checkout is locked
    locked, err := h.isCheckoutLocked(checkoutID)
    if err != nil {
        sendErrorResponse(w, http.StatusInternalServerError, fmt.Sprintf("Error checking checkout lock: %v", err))
        return
    }

    sendSuccessResponse(w, models.APIResponse{
        Status: "success",
        Data: map[string]interface{}{
            "checkout_id": checkoutID,
            "processed":   processed,
            "locked":      locked,
        },
    })
}

// Helper to check if a checkout is locked
func (h *PaymentHandler) isCheckoutLocked(checkoutID string) (bool, error) {
    // Try to acquire the lock - if we can't, it's locked
    acquired, err := h.db.LockCheckout(checkoutID)
    if err != nil {
        return false, err
    }
    
    // If we acquired the lock, release it and return false (not locked)
    if acquired {
        if err := h.db.ReleaseLock(checkoutID); err != nil {
            return false, err
        }
        return false, nil
    }
    
    // We couldn't acquire the lock, so it's locked
    return true, nil
}

func (h *PaymentHandler) generateActivationEmail(username, activationURL string) string {
    content := fmt.Sprintf(
        "In order to activate your account, we need to confirm your email address. Once we do, "+
            "you will be able to log into your Administrator Portal and begin setting up your devices "+
            "on the most advanced security service on the planet.",
    )
    
    footer := "Thank you so much,\nThe ProSecureLSP Team"
    
    return fmt.Sprintf(
        email.ActivationEmailTemplate,
        username,
        content,
        activationURL,
        footer,
    )
}

func (h *PaymentHandler) generateInvoiceEmail(checkout *models.CheckoutData) string {
    var total float64
    plansTable := "<table><thead><tr><th>Plans</th><th>Price</th></tr></thead><tbody>"
    
    for _, plan := range checkout.Plans {
        if plan.Annually == 1 {
            total += plan.Price * 10
        } else {
            total += plan.Price
        }
        plansTable += fmt.Sprintf("<tr><td>%s</td><td>$%.2f</td></tr>", plan.PlanName, plan.Price)
    }
    plansTable += "</tbody></table>"

    totalsSection := fmt.Sprintf(`
        <p><strong>Subtotal:</strong> $%.2f</p>
        <p><strong>Discount:</strong> $%.2f</p>
        <p><strong>Tax validation card (refunded):</strong> $0.01</p>
        <p><strong>Total:</strong> $0.01</p>
    `, total, total-0.01)

    footer := fmt.Sprintf(
        "Thank you %s for choosing our services. If you have any questions, please contact our support team.",
        checkout.Name,
    )

    return fmt.Sprintf(
        email.InvoiceEmailTemplate,
        time.Now().Format("20060102150405"),
        plansTable,
        totalsSection,
        "Paid",
        footer,
    )
}