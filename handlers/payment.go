package handlers

import (
    "crypto/sha256"
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
)

type PaymentHandler struct {
    db             *database.Connection
    paymentService *payment.Service
    emailService   *email.SMTPService
}

func NewPaymentHandler(db *database.Connection, ps *payment.Service, es *email.SMTPService) (*PaymentHandler, error) {
    if db == nil {
        return nil, fmt.Errorf("database connection is required")
    }
    if ps == nil {
        return nil, fmt.Errorf("payment service is required")
    }
    if es == nil {
        return nil, fmt.Errorf("email service is required")
    }

    return &PaymentHandler{
        db:             db,
        paymentService: ps,
        emailService:   es,
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

func generateShortID(prefix string, length int) string {
    uuid := uuid.New()
    hexUUID := uuid.String()
    hexUUID = strings.ReplaceAll(hexUUID, "-", "")
    startIndex := sha256.Sum256([]byte(hexUUID))
    start := int(startIndex[0]) % (len(hexUUID) - length)
    shortID := hexUUID[start : start+length]
    
    if prefix != "" {
        shortID = prefix + shortID
    }
    
    return shortID
}

func (h *PaymentHandler) GenerateCheckoutID(w http.ResponseWriter, r *http.Request) {
    checkoutID := generateShortID("CHK", 6)
    sendSuccessResponse(w, models.APIResponse{
        Status:  "success",
        Message: "Checkout ID generated successfully",
        Data: map[string]string{
            "checkout_id": checkoutID,
        },
    })
}

func (h *PaymentHandler) CheckCheckoutStatus(w http.ResponseWriter, r *http.Request) {
    checkoutID := r.URL.Query().Get("checkout_id")
    if checkoutID == "" {
        sendErrorResponse(w, http.StatusBadRequest, "Checkout ID is required")
        return
    }

    processed, err := h.db.IsCheckoutProcessed(checkoutID)
    if err != nil {
        sendErrorResponse(w, http.StatusInternalServerError, "Internal server error")
        return
    }

    sendSuccessResponse(w, models.APIResponse{
        Status:  "success",
        Message: "Checkout status retrieved successfully",
        Data: map[string]bool{
            "processed": processed,
        },
    })
}

func (h *PaymentHandler) PopulateCheckoutHistorics(w http.ResponseWriter, r *http.Request) {
    var req struct {
        CheckoutID   string  `json:"checkout_id"`
        Name         string  `json:"name"`
        Email        string  `json:"email"`
        PhoneNumber  string  `json:"phoneNumber"`
        ZipCode      string  `json:"zipcode"`
        State        string  `json:"state"`
        City         string  `json:"city"`
        Street       string  `json:"street"`
        Additional   string  `json:"additional"`
        PlansJSON    string  `json:"plans_json"`
        Plan         int     `json:"plan"`
        Username     string  `json:"username"`
        Passphrase   string  `json:"passphrase"`
    }

    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        sendErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %v", err))
        return
    }

    query := `
        INSERT INTO checkout_historics (
            checkout_id, name, email, phoneNumber, zipcode, state, city, street, additional, plans_json, plan, username, passphrase, status, created_at
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', NOW())
    `

    _, err := h.db.GetDB().Exec(query,
        req.CheckoutID,
        req.Name,
        req.Email,
        req.PhoneNumber,
        req.ZipCode,
        req.State,
        req.City,
        req.Street,
        req.Additional,
        req.PlansJSON,
        req.Plan,
        req.Username,
        req.Passphrase,
    )

    if err != nil {
        sendErrorResponse(w, http.StatusInternalServerError, fmt.Sprintf("Failed to populate checkout historics: %v", err))
        return
    }

    sendSuccessResponse(w, models.APIResponse{
        Status:  "success",
        Message: "Checkout historics populated successfully",
    })
}


func (h *PaymentHandler) ProcessPayment(w http.ResponseWriter, r *http.Request) {
    requestID := generateShortID("REF", 15)
    log.Printf("[RequestID: %s] Starting payment processing", requestID)

    var req models.PaymentRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        log.Printf("[RequestID: %s] Invalid request body: %v", requestID, err)
        sendErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %v", err))
        return
    }

    log.Printf("[RequestID: %s] Processing payment for checkout ID: %s", requestID, req.CheckoutID)

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

    checkout, err := h.db.GetCheckoutData(req.CheckoutID)
    if err != nil {
        log.Printf("[RequestID: %s] Invalid checkout ID: %v", requestID, err)
        sendErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("Invalid checkout ID: %v", err))
        return
    }

    resp, err := h.paymentService.ProcessPayment(&req, checkout)
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

    if err := h.createAccountsAndNotify(checkout, &req); err != nil {
        log.Printf("[RequestID: %s] Failed to create account: %v", requestID, err)
        sendErrorResponse(w, http.StatusInternalServerError, fmt.Sprintf("Failed to create account: %v", err))
        return
    }

    log.Printf("[RequestID: %s] Payment processed successfully for checkout ID: %s", requestID, req.CheckoutID)

    defer h.db.ReleaseLock(req.CheckoutID)

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

func (h *PaymentHandler) ResetCheckoutStatus(w http.ResponseWriter, r *http.Request) {
    requestID := generateShortID("RST", 10)
    log.Printf("[RequestID: %s] Resetting checkout status", requestID)

    var req struct {
        CheckoutID string `json:"sid"`
    }

    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        log.Printf("[RequestID: %s] Invalid request body: %v", requestID, err)
        sendErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %v", err))
        return;
    }

    if req.CheckoutID == "" {
        log.Printf("[RequestID: %s] Checkout ID is required", requestID)
        sendErrorResponse(w, http.StatusBadRequest, "Checkout ID is required")
        return;
    }

    log.Printf("[RequestID: %s] Resetting checkout status for ID: %s", requestID, req.CheckoutID)

    query := `UPDATE checkout_historics SET status = 'pending' WHERE checkout_id = ? AND status = 'processing'`
    
    result, err := h.db.GetDB().Exec(query, req.CheckoutID)
    if err != nil {
        log.Printf("[RequestID: %s] Error resetting checkout status: %v", requestID, err)
        sendErrorResponse(w, http.StatusInternalServerError, "Internal server error")
        return;
    }

    rowsAffected, err := result.RowsAffected()
    if err != nil {
        log.Printf("[RequestID: %s] Error getting rows affected: %v", requestID, err)
        sendErrorResponse(w, http.StatusInternalServerError, "Internal server error")
        return;
    }

    h.db.ReleaseLock(req.CheckoutID)

    log.Printf("[RequestID: %s] Successfully reset checkout status for ID: %s. Rows affected: %d", requestID, req.CheckoutID, rowsAffected)

    sendSuccessResponse(w, models.APIResponse{
        Status:  "success",
        Message: "Checkout status reset successfully",
        Data: map[string]interface{}{
            "checkout_id": req.CheckoutID,
            "reset": rowsAffected > 0,
        },
    })
}

func (h *PaymentHandler) UpdateCheckoutID(w http.ResponseWriter, r *http.Request) {
	requestID := generateShortID("UPD", 10)
	log.Printf("[RequestID: %s] Updating checkout ID", requestID)

	var req struct {
		OldCheckoutID string `json:"old_checkout_id"`
		NewCheckoutID string `json:"new_checkout_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("[RequestID: %s] Invalid request body: %v", requestID, err)
		sendErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %v", err))
		return
	}

	if req.OldCheckoutID == "" || req.NewCheckoutID == "" {
		log.Printf("[RequestID: %s] Both old and new checkout IDs are required", requestID)
		sendErrorResponse(w, http.StatusBadRequest, "Both old and new checkout IDs are required")
		return
	}

	log.Printf("[RequestID: %s] Updating checkout ID from %s to %s", requestID, req.OldCheckoutID, req.NewCheckoutID)

	// First, check if the old checkout ID exists
	exists, err := h.checkIfCheckoutExists(req.OldCheckoutID)
	if err != nil {
		log.Printf("[RequestID: %s] Error checking if checkout exists: %v", requestID, err)
		sendErrorResponse(w, http.StatusInternalServerError, "Internal server error")
		return
	}

	if !exists {
		log.Printf("[RequestID: %s] Old checkout ID not found: %s", requestID, req.OldCheckoutID)
		sendErrorResponse(w, http.StatusNotFound, "Old checkout ID not found")
		return
	}

	// Update the checkout ID in the checkout_historics table
	query := `UPDATE checkout_historics SET checkout_id = ? WHERE checkout_id = ?`

	result, err := h.db.GetDB().Exec(query, req.NewCheckoutID, req.OldCheckoutID)
	if err != nil {
		log.Printf("[RequestID: %s] Error updating checkout ID: %v", requestID, err)
		sendErrorResponse(w, http.StatusInternalServerError, "Internal server error")
		return
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		log.Printf("[RequestID: %s] Error getting rows affected: %v", requestID, err)
		sendErrorResponse(w, http.StatusInternalServerError, "Internal server error")
		return
	}

	if rowsAffected == 0 {
		log.Printf("[RequestID: %s] No rows updated for checkout ID: %s", requestID, req.OldCheckoutID)
		sendErrorResponse(w, http.StatusNotFound, "Checkout record not found")
		return
	}

	log.Printf("[RequestID: %s] Successfully updated checkout ID from %s to %s. Rows affected: %d", requestID, req.OldCheckoutID, req.NewCheckoutID, rowsAffected)

	// Make sure any locks on the old checkout ID are released
	h.db.ReleaseLock(req.OldCheckoutID)

	sendSuccessResponse(w, models.APIResponse{
		Status:  "success",
		Message: "Checkout ID updated successfully",
		Data: map[string]interface{}{
			"old_checkout_id": req.OldCheckoutID,
			"new_checkout_id": req.NewCheckoutID,
			"updated":         rowsAffected > 0,
		},
	})
}

// checkIfCheckoutExists checks if a checkout record exists with the given ID
func (h *PaymentHandler) checkIfCheckoutExists(checkoutID string) (bool, error) {
	query := `SELECT COUNT(*) FROM checkout_historics WHERE checkout_id = ?`
	
	var count int
	err := h.db.GetDB().QueryRow(query, checkoutID).Scan(&count)
	if err != nil {
		return false, err
	}
	
	return count > 0, nil
}


func (h *PaymentHandler) createAccountsAndNotify(checkout *models.CheckoutData, payment *models.PaymentRequest) error {
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

    if err := tx.SaveTransaction(masterUUID, checkout.ID, 1.00, "authorized", "INIT_AUTH"); err != nil {
        tx.Rollback()
        return fmt.Errorf("failed to save transaction: %v", err)
    }

    if err := tx.SaveSubscription(masterUUID, "active", futureDate); err != nil {
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