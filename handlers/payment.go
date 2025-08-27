package handlers

import (
    "context"
    "database/sql"
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
    "prosecure-payment-api/types"
)

type checkoutCache struct {
    data      *models.CheckoutData
    timestamp time.Time
}

type PaymentHandler struct {
    db             *database.Connection
    paymentService *payment.Service
    emailService   *email.SMTPService
    queue          *queue.Queue
    checkoutCache  map[string]checkoutCache // Changed from sync.Map to regular map
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
        checkoutCache:  make(map[string]checkoutCache),
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


func (h *PaymentHandler) ProcessPayment(w http.ResponseWriter, r *http.Request) {
    requestID := uuid.New().String()
    log.Printf("[RequestID: %s] Starting payment processing", requestID)

    var req models.PaymentRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        log.Printf("[RequestID: %s] Invalid request body: %v", requestID, err)
        sendErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %v", err))
        return
    }

    log.Printf("[RequestID: %s] Processing payment for checkout ID: %s", requestID, req.CheckoutID)

    // Verificar se o checkout já foi processado
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

    // Adquirir lock para evitar processamento duplo
    acquired, err := h.db.LockCheckout(req.CheckoutID)
    if err != nil {
        log.Printf("[RequestID: %s] Error acquiring lock: %v", requestID, err)
        sendErrorResponse(w, http.StatusInternalServerError, "Internal server error")
        return
    }
    if !acquired {
        log.Printf("[RequestID: %s] Checkout is being processed: %s", requestID, req.CheckoutID)
        sendErrorResponse(w, http.StatusConflict, "Este checkout já está sendo processado")
        return
    }
    
    defer h.db.ReleaseLock(req.CheckoutID)
    
    // Buscar dados do checkout (usar cache se disponível)
    var checkout *models.CheckoutData
    if cachedData, found := h.checkoutCache[req.CheckoutID]; found {
        if time.Since(cachedData.timestamp) < 5*time.Minute {
            checkout = cachedData.data
            log.Printf("[RequestID: %s] Using cached checkout data", requestID)
        }
    }
    
    if checkout == nil {
        var checkoutErr error
        checkout, checkoutErr = h.db.GetCheckoutData(req.CheckoutID)
        
        if checkoutErr != nil {
            log.Printf("[RequestID: %s] Invalid checkout ID: %v", requestID, checkoutErr)
            sendErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("Invalid checkout ID: %v", checkoutErr))
            return
        }
        
        h.checkoutCache[req.CheckoutID] = checkoutCache{
            data:      checkout,
            timestamp: time.Now(),
        }
    }

    // Adicionar informações de billing ao request
    req.CustomerEmail = checkout.Email
    req.BillingInfo = &types.BillingInfoType{
        FirstName:   strings.Split(checkout.Name, " ")[0],
        LastName:    strings.Join(strings.Split(checkout.Name, " ")[1:], " "),
        Address:     checkout.Street,
        City:        checkout.City,
        State:       checkout.State,
        Zip:         checkout.ZipCode,
        Country:     "US",
        PhoneNumber: checkout.PhoneNumber,
    }

    // Validar dados do cartão
    if !h.paymentService.ValidateCard(&req) {
        log.Printf("[RequestID: %s] Invalid card data", requestID)
        sendErrorResponse(w, http.StatusBadRequest, "Dados do cartão inválidos: verifique o número, data de validade e código de segurança")
        return
    }

    // Salvar dados de pagamento temporários
    ctxTemp, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    
    _, err = h.db.GetDB().ExecContext(ctxTemp,
        `INSERT INTO temp_payment_data
         (checkout_id, card_number, card_expiry, card_cvv, card_name, created_at)
         VALUES (?, ?, ?, ?, ?, NOW())
         ON DUPLICATE KEY UPDATE
         card_number = VALUES(card_number),
         card_expiry = VALUES(card_expiry),
         card_cvv = VALUES(card_cvv),
         card_name = VALUES(card_name),
         created_at = NOW()`,
        checkout.ID, req.CardNumber, req.Expiry, req.CVV, req.CardName)
    
    if err != nil {
        log.Printf("[RequestID: %s] Failed to store temporary payment data: %v", requestID, err)
        sendErrorResponse(w, http.StatusInternalServerError, "Falha ao armazenar dados de pagamento")
        return
    }

    // Registrar status inicial do pagamento
    _, err = h.db.GetDB().ExecContext(ctxTemp,
        `INSERT INTO payment_results 
         (request_id, checkout_id, status, created_at)
         VALUES (?, ?, 'scheduled', NOW())`,
        requestID, checkout.ID)
    
    if err != nil {
        log.Printf("[RequestID: %s] Warning: Failed to store initial payment status: %v", requestID, err)
    }

    // CRIAR CONTA IMEDIATAMENTE para uso do cliente
    log.Printf("[RequestID: %s] Creating user account immediately", requestID)
    
    err = h.createAccountsAndNotify(checkout, &req, "PENDING")
    if err != nil {
        log.Printf("[RequestID: %s] Error creating account: %v", requestID, err)
        sendErrorResponse(w, http.StatusInternalServerError, "Falha ao criar conta")
        return
    }
    
    // NOVO: Definir payment_status = 0 (processamento iniciado) para o usuário criado
    log.Printf("[RequestID: %s] Setting initial payment status to processing (0)", requestID)
    err = h.db.SetPaymentProcessingStarted(checkout.Email, checkout.Username)
    if err != nil {
        log.Printf("[RequestID: %s] Warning: Failed to set initial payment status: %v", requestID, err)
        // Não falha o processo por causa disso
    }
    
    log.Printf("[RequestID: %s] Account created successfully, scheduling payment processing", requestID)

    // AGENDAR: Processamento de pagamento (transação teste + void + ARB)
    // TODO: ALTERAR PARA 1 HORA EM PRODUÇÃO (time.Hour)
    paymentDelay := 40 * time.Second // TESTE: 2 minutos | PRODUÇÃO: time.Hour
    
    ctx := context.Background()
    err = h.queue.EnqueueDelayed(ctx, queue.JobTypeDelayedPayment, map[string]interface{}{
        "checkout_id": checkout.ID,
        "request_id":  requestID,
    }, paymentDelay)
    
    if err != nil {
        log.Printf("[RequestID: %s] Error enqueueing delayed payment job: %v", requestID, err)
        // Se falhar ao agendar, marcar como erro no payment_status
        h.db.SetPaymentProcessingFailed(checkout.Email, checkout.Username)
        sendErrorResponse(w, http.StatusInternalServerError, "Falha ao agendar processamento de pagamento")
        return
    }

    log.Printf("[RequestID: %s] Payment processing scheduled for %v from now", requestID, paymentDelay)

    // Calcular horário estimado de processamento
    processingTime := time.Now().Add(paymentDelay)

    sendSuccessResponse(w, models.APIResponse{
        Status:  "success",
        Message: "Conta criada com sucesso! Processamento de pagamento agendado.",
        Data: map[string]interface{}{
            "request_id":        requestID,
            "checkout_id":       checkout.ID,
            "account_created":   true,
            "processing_time":   processingTime.Format("2006-01-02 15:04:05"),
            "status_url":       fmt.Sprintf("/api/check-payment-status?request_id=%s", requestID),
            "message":          "Sua conta foi criada e já está disponível para uso. O processamento do pagamento será realizado em breve.",
        },
    })
}

func (h *PaymentHandler) CheckPaymentStatus(w http.ResponseWriter, r *http.Request) {
    requestID := r.URL.Query().Get("request_id")
    if requestID == "" {
        sendErrorResponse(w, http.StatusBadRequest, "Missing request_id parameter")
        return
    }
    
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    
    var status, transactionID, errorMessage string
    var createdAt time.Time
    var checkoutID string
    
    err := h.db.GetDB().QueryRowContext(ctx,
        `SELECT status, COALESCE(transaction_id, ''), COALESCE(error_message, ''), created_at, checkout_id 
         FROM payment_results 
         WHERE request_id = ?
         ORDER BY created_at DESC
         LIMIT 1`,
        requestID).Scan(&status, &transactionID, &errorMessage, &createdAt, &checkoutID)
    
    if err == sql.ErrNoRows {
        sendErrorResponse(w, http.StatusNotFound, "Payment request not found")
        return
    } else if err != nil {
        log.Printf("Error checking payment status: %v", err)
        sendErrorResponse(w, http.StatusInternalServerError, "Error checking payment status")
        return
    }

    var accountCreated bool = false
    if status == "success" && checkoutID != "" {
        h.db.GetDB().QueryRowContext(ctx,
            `SELECT EXISTS(
                SELECT 1 FROM master_accounts ma 
                JOIN transactions t ON t.master_reference = ma.reference_uuid 
                WHERE t.checkout_id = ?
            )`,
            checkoutID).Scan(&accountCreated)
    }
    
    response := models.APIResponse{
        Status:  status,
        Message: "Payment status retrieved",
        Data: map[string]interface{}{
            "transaction_id":  transactionID,
            "payment_status":  status,
            "account_created": accountCreated,
            "created_at":      createdAt,
        },
    }
    
    if errorMessage != "" && (status == "failed" || status == "error") {
        response.Data.(map[string]interface{})["error"] = errorMessage
    }
    
    sendSuccessResponse(w, response)
}

// createAccountsAndNotify - Mantido para compatibilidade (usado pelo worker)
func (h *PaymentHandler) createAccountsAndNotify(checkout *models.CheckoutData, payment *models.PaymentRequest, transactionID string) error {
    startTime := time.Now()
    defer func() {
        log.Printf("Account creation and notifications completed in %v for checkout ID: %s", 
            time.Since(startTime), checkout.ID)
    }()
    
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

    if err := tx.SaveSubscription(masterUUID, "pending", futureDate, ""); err != nil {
        tx.Rollback()
        return fmt.Errorf("failed to save subscription: %v", err)
    }

    if err := tx.Commit(); err != nil {
        return fmt.Errorf("failed to commit transaction: %v", err)
    }

    code := utils.GenerateActivationCode()
    encodedUser := base64.StdEncoding.EncodeToString([]byte(checkout.Username))
    encodedEmail := base64.StdEncoding.EncodeToString([]byte(checkout.Email))
    encodedCode := base64.StdEncoding.EncodeToString([]byte(code))

    go func() {
        updateErr := h.db.UpdateUserActivationCode(checkout.Email, checkout.Username, code)
        if updateErr != nil {
            log.Printf("Warning: Failed to update activation code: %v", updateErr)
        }
    }()

    // Criar URL de ativação
    activationURL := fmt.Sprintf(
        "https://prosecurelsp.com/users/active/activation.php?act=%s&emp=%s&cct=%s",
        encodedUser, encodedEmail, encodedCode,
    )

    // Preparar e enviar APENAS email de ativação
    activationEmailContent := h.generateActivationEmail(checkout.Name, activationURL)

    // Enviar email de ativação
    activationErr := h.emailService.SendEmail(
        checkout.Email,
        "Please Confirm Your Email Address",
        activationEmailContent,
    )
    
    if activationErr != nil {
        log.Printf("Warning: Failed to send activation email: %v", activationErr)
        // Continua mesmo com erro no email - conta já foi criada
    } else {
        log.Printf("Activation email sent successfully to %s", checkout.Email)
    }
    
    // NOTA: Email de invoice será enviado apenas após sucesso completo do pagamento

    log.Printf("Successfully created accounts and sent notifications for master reference: %s", masterUUID)
    return nil
}

func (h *PaymentHandler) generateActivationEmail(username, activationURL string) string {
    content := "In order to activate your account, we need to confirm your email address. Once we do, " +
        "you will be able to log into your Administrator Portal and begin setting up your devices " +
        "on the most advanced security service on the planet."
    
    footer := "Thank you so much,\nThe ProSecureLSP Team"
    
    return fmt.Sprintf(
        email.ActivationEmailTemplate,
        username,        // %s - Hi username!
        content,         // %s - Conteúdo da mensagem
        activationURL,   // %s - Link do botão
        activationURL,   // %s - URL fallback
        footer,          // %s - Footer message
    )
}

func (h *PaymentHandler) generateInvoiceEmail(checkout *models.CheckoutData) string {
    var total float64
    
    // Gerar tabela de planos de forma mais simples e robusta
    plansTable := `<table class="plans-table">
        <thead>
            <tr>
                <th>Plan Name</th>
                <th>Price</th>
            </tr>
        </thead>
        <tbody>`
    
    for _, plan := range checkout.Plans {
        planPrice := plan.Price
        if plan.Annually == 1 {
            total += plan.Price * 10
            planPrice = plan.Price * 10
        } else {
            total += plan.Price
        }
        
        plansTable += fmt.Sprintf(`
            <tr>
                <td>%s</td>
                <td>$%.2f</td>
            </tr>`, 
            plan.PlanName, 
            planPrice,
        )
    }
    plansTable += `
        </tbody>
    </table>`

    // Seção de totais mais clara
    totalsSection := fmt.Sprintf(`
        <p><strong>Subtotal:</strong> $%.2f</p>
        <p><strong>Discount:</strong> $%.2f</p>
        <p style="font-size: 18px; font-weight: bold; color: #28a745;"><strong>Total Paid:</strong> $0.01</p>
    `, total, total-0.01)

    footer := fmt.Sprintf(
        "Thank you %s for choosing our services. If you have any questions, please contact our support team.",
        checkout.Name,
    )

    // Gerar número da invoice único
    invoiceNumber := fmt.Sprintf("INV-%s", time.Now().Format("20060102-150405"))

    return fmt.Sprintf(
        email.InvoiceEmailTemplate,
        invoiceNumber,   // %s - Invoice number
        plansTable,      // %s - Plans table HTML
        totalsSection,   // %s - Totals section HTML
        "Paid",          // %s - Status
        footer,          // %s - Footer message
    )
}

// ResetCheckoutStatus redefine o status de um checkout
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

    // Liberar qualquer lock neste checkout
    if err := h.db.ReleaseLock(req.CheckoutID); err != nil {
        sendErrorResponse(w, http.StatusInternalServerError, fmt.Sprintf("Failed to reset checkout status: %v", err))
        return
    }

    // Remover do cache se existir
    delete(h.checkoutCache, req.CheckoutID)

    sendSuccessResponse(w, models.APIResponse{
        Status:  "success",
        Message: "Checkout status reset successfully",
    })
}

// GenerateCheckoutID gera um novo ID de checkout
func (h *PaymentHandler) GenerateCheckoutID(w http.ResponseWriter, r *http.Request) {
    checkoutID := uuid.New().String()

    sendSuccessResponse(w, models.APIResponse{
        Status: "success",
        Data: map[string]interface{}{
            "checkout_id": checkoutID,
        },
    })
}

// UpdateCheckoutID atualiza um checkout com um novo ID
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

    sendSuccessResponse(w, models.APIResponse{
        Status:  "success",
        Message: fmt.Sprintf("Checkout ID updated from %s to %s", req.OldID, req.NewID),
    })
}

func (h *PaymentHandler) CheckCheckoutStatus(w http.ResponseWriter, r *http.Request) {
    checkoutID := r.URL.Query().Get("id")
    if checkoutID == "" {
        sendErrorResponse(w, http.StatusBadRequest, "Missing checkout ID")
        return
    }

    processed, err := h.db.IsCheckoutProcessed(checkoutID)
    if err != nil {
        sendErrorResponse(w, http.StatusInternalServerError, fmt.Sprintf("Error checking checkout status: %v", err))
        return
    }

    locked, err := h.isCheckoutLocked(checkoutID)
    if err != nil {
        sendErrorResponse(w, http.StatusInternalServerError, fmt.Sprintf("Error checking checkout lock: %v", err))
        return
    }

    var paymentStatus string
    var paymentDate time.Time
    
    err = h.db.GetDB().QueryRow(
        `SELECT status, created_at 
         FROM payment_results 
         WHERE checkout_id = ? 
         ORDER BY created_at DESC 
         LIMIT 1`, 
        checkoutID).Scan(&paymentStatus, &paymentDate)
    
    if err != nil && err != sql.ErrNoRows {
        log.Printf("Error checking payment status: %v", err)
    }
    
    if err == sql.ErrNoRows {
        paymentStatus = "not_started"
    }
    
    sendSuccessResponse(w, models.APIResponse{
        Status: "success",
        Data: map[string]interface{}{
            "checkout_id":     checkoutID,
            "processed":       processed,
            "locked":          locked,
            "payment_status":  paymentStatus,
            "payment_date":    paymentDate,
        },
    })
}

func (h *PaymentHandler) isCheckoutLocked(checkoutID string) (bool, error) {
    acquired, err := h.db.LockCheckout(checkoutID)
    if err != nil {
        return false, err
    }
    
    if acquired {
        if err := h.db.ReleaseLock(checkoutID); err != nil {
            return false, err
        }
        return false, nil
    }
    
    return true, nil
}