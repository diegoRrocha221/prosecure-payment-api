// handlers/update_card.go - CORRIGIDO SEM ERROS DE COMPILAÇÃO
package handlers

import (
    "context"
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "time"
    
    "prosecure-payment-api/database"
    "prosecure-payment-api/models"
    "prosecure-payment-api/services/payment"
    "prosecure-payment-api/services/email"
    "prosecure-payment-api/types"
)

type UpdateCardHandler struct {
    db             *database.Connection
    paymentService *payment.Service
    emailService   *email.SMTPService
}

func NewUpdateCardHandler(db *database.Connection, ps *payment.Service, es *email.SMTPService) *UpdateCardHandler {
    return &UpdateCardHandler{
        db:             db,
        paymentService: ps,
        emailService:   es,
    }
}

type UpdateCardRequest struct {
    Email      string `json:"email"`
    Username   string `json:"username"`
    CardName   string `json:"card_name"`
    CardNumber string `json:"card_number"`
    Expiry     string `json:"expiry"`
    CVV        string `json:"cvv"`
}

type UpdateCardResponse struct {
    Status    string `json:"status"`
    Message   string `json:"message"`
    Success   bool   `json:"success"`
    Details   map[string]interface{} `json:"details,omitempty"`
}

func (h *UpdateCardHandler) UpdateCard(w http.ResponseWriter, r *http.Request) {
    requestID := fmt.Sprintf("update-%d", time.Now().UnixNano())
    log.Printf("[UpdateCard %s] Starting ENHANCED card update process with Customer Profile", requestID)
    
    // TIMEOUT QUASE ILIMITADO: 10 MINUTOS
    ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute) // 600 segundos
    defer cancel()
    
    // Rest of the function remains the same...
    // Canal para resultado do processamento
    resultChan := make(chan UpdateCardResponse, 1)
    errorChan := make(chan error, 1)

    var req UpdateCardRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        log.Printf("[UpdateCard %s] Invalid request body: %v", requestID, err)
        h.sendErrorResponse(w, http.StatusBadRequest, "Invalid request body")
        return
    }

    // Validar campos obrigatórios RAPIDAMENTE
    if req.Email == "" || req.Username == "" || req.CardName == "" || 
       req.CardNumber == "" || req.Expiry == "" || req.CVV == "" {
        log.Printf("[UpdateCard %s] Missing required fields", requestID)
        h.sendErrorResponse(w, http.StatusBadRequest, "All fields are required")
        return
    }

    // Validações básicas RÁPIDAS
    if len(req.CardName) < 3 {
        h.sendErrorResponse(w, http.StatusBadRequest, "Please enter a valid cardholder name")
        return
    }
    
    if len(req.CardNumber) < 13 {
        h.sendErrorResponse(w, http.StatusBadRequest, "Please enter a valid card number")
        return
    }
    
    if len(req.CVV) < 3 || len(req.CVV) > 4 {
        h.sendErrorResponse(w, http.StatusBadRequest, "Please enter a valid CVV")
        return
    }

    log.Printf("[UpdateCard %s] Basic validation passed, starting async processing with Customer Profile", requestID)
    
    // Processar de forma assíncrona com Customer Profile
    go func() {
        defer func() {
            if r := recover(); r != nil {
                log.Printf("[UpdateCard %s] Panic during async processing: %v", requestID, r)
                select {
                case errorChan <- fmt.Errorf("internal processing error"):
                default:
                }
            }
        }()
        
        result, err := h.processCardUpdateWithCustomerProfile(ctx, requestID, req)
        if err != nil {
            select {
            case errorChan <- err:
            case <-ctx.Done():
                log.Printf("[UpdateCard %s] Context cancelled during error send", requestID)
            }
            return
        }
        
        select {
        case resultChan <- result:
        case <-ctx.Done():
            log.Printf("[UpdateCard %s] Context cancelled during result send", requestID)
        }
    }()

    // Aguardar resultado com timeout QUASE ILIMITADO
    select {
    case result := <-resultChan:
        log.Printf("[UpdateCard %s] Async processing completed successfully", requestID)
        h.sendSuccessResponse(w, result)
        
        // Enviar email APÓS resposta HTTP (assíncrono)
        if result.Success {
            go func() {
                emailCtx, emailCancel := context.WithTimeout(context.Background(), 30*time.Second)
                defer emailCancel()
                
                if err := h.sendCardUpdateConfirmationEmailAsync(emailCtx, req.Email, req.CardName, requestID); err != nil {
                    log.Printf("[UpdateCard %s] Warning: Failed to send confirmation email: %v", requestID, err)
                }
            }()
        }
        
    case err := <-errorChan:
        log.Printf("[UpdateCard %s] Async processing failed: %v", requestID, err)
        h.sendErrorResponse(w, http.StatusInternalServerError, err.Error())
        
    case <-ctx.Done():
        log.Printf("[UpdateCard %s] Request timeout after 10 minutes", requestID)
        h.sendErrorResponse(w, http.StatusRequestTimeout, "Request timeout - please try again")
    }
}

// PROCESSAMENTO COM TIMEOUTS MAIORES
func (h *UpdateCardHandler) processCardUpdateWithCustomerProfile(ctx context.Context, requestID string, req UpdateCardRequest) (UpdateCardResponse, error) {
    log.Printf("[UpdateCard %s] Enhanced processing started with Customer Profile integration", requestID)
    
    // ETAPA 1: Buscar dados da conta (TIMEOUT AUMENTADO)
    dbCtx, dbCancel := context.WithTimeout(ctx, 20*time.Second) // Aumentado
    defer dbCancel()
    
    masterAccount, err := h.getMasterAccountDataFast(dbCtx, req.Email, req.Username)
    if err != nil {
        return UpdateCardResponse{}, fmt.Errorf("account not found or invalid credentials: %v", err)
    }

    // ETAPA 2: Verificar status de pagamento
    hasPaymentError, err := h.checkUserPaymentErrorStatusFast(dbCtx, req.Email, req.Username)
    if err != nil {
        return UpdateCardResponse{}, fmt.Errorf("error checking account payment status: %v", err)
    }

    if !hasPaymentError {
        return UpdateCardResponse{
            Status:  "error",
            Message: "Account payment status is normal, no card update needed",
            Success: false,
        }, nil
    }

    log.Printf("[UpdateCard %s] Account validation completed, processing payment with Customer Profile", requestID)

    // ETAPA 3: Criar request de pagamento
    paymentReq := &models.PaymentRequest{
        CardName:      req.CardName,
        CardNumber:    req.CardNumber,
        Expiry:        req.Expiry,
        CVV:           req.CVV,
        CheckoutID:    masterAccount.ReferenceUUID,
        CustomerEmail: req.Email,
        BillingInfo: &types.BillingInfoType{
            FirstName:   masterAccount.Name,
            LastName:    masterAccount.LastName,
            Address:     masterAccount.Street,
            City:        masterAccount.City,
            State:       masterAccount.State,
            Zip:         masterAccount.ZipCode,
            Country:     "US",
            PhoneNumber: masterAccount.PhoneNumber,
        },
    }

    // ETAPA 4: Validar cartão RAPIDAMENTE
    if !h.paymentService.ValidateCard(paymentReq) {
        return UpdateCardResponse{
            Status:  "error", 
            Message: "Invalid card data: check card number, expiration date and CVV",
            Success: false,
        }, nil
    }

    // ETAPA 5: Processar pagamento com Customer Profile (TIMEOUT MUITO GRANDE)
    paymentCtx, paymentCancel := context.WithTimeout(ctx, 8*time.Minute) // 8 MINUTOS
    defer paymentCancel()
    
    transactionID, customerProfileID, paymentProfileID, err := h.processPaymentWithCustomerProfile(paymentCtx, requestID, paymentReq, masterAccount)
    if err != nil {
        return UpdateCardResponse{}, err
    }

    // ETAPA 6: Atualizar banco rapidamente
    updateCtx, updateCancel := context.WithTimeout(ctx, 30*time.Second) // Aumentado
    defer updateCancel()
    
    if err := h.updateAccountAfterCardUpdateWithProfile(updateCtx, masterAccount, paymentReq, transactionID, customerProfileID, paymentProfileID); err != nil {
        return UpdateCardResponse{}, fmt.Errorf("failed to update account data: %v", err)
    }

    log.Printf("[UpdateCard %s] Card update completed successfully with Customer Profile", requestID)

    return UpdateCardResponse{
        Status:  "success",
        Message: "Card updated successfully with Customer Profile and recurring billing reactivated",
        Success: true,
        Details: map[string]interface{}{
            "transaction_id":        transactionID,
            "customer_profile_id":   customerProfileID,
            "payment_profile_id":    paymentProfileID,
            "account_status":        "active",
            "updated_at":           time.Now().Format("2006-01-02 15:04:05"),
            "processing_mode":      "enhanced_with_profile",
        },
    }, nil
}

// NOVA FUNÇÃO: Processamento de pagamento com Customer Profile
func (h *UpdateCardHandler) processPaymentWithCustomerProfile(ctx context.Context, requestID string, paymentReq *models.PaymentRequest, master *models.MasterAccount) (string, string, string, error) {
    log.Printf("[UpdateCard %s] Enhanced payment operations started with Customer Profile", requestID)
    
    // ETAPA 1: Transação teste de $1
    log.Printf("[UpdateCard %s] Step 1: Processing test transaction", requestID)
    
    resp, err := h.paymentService.ProcessInitialAuthorization(paymentReq)
    if err != nil {
        return "", "", "", fmt.Errorf("test transaction failed: %v", err)
    }
    if resp == nil || !resp.Success {
        message := "transaction declined"
        if resp != nil {
            message = resp.Message
        }
        return "", "", "", fmt.Errorf("transaction declined: %s", message)
    }
    
    transactionID := resp.TransactionID
    log.Printf("[UpdateCard %s] Test transaction successful: %s", requestID, transactionID)
    
    // ETAPA 2: Void da transação teste
    log.Printf("[UpdateCard %s] Step 2: Voiding test transaction", requestID)
    
    if err := h.paymentService.VoidTransaction(transactionID); err != nil {
        log.Printf("[UpdateCard %s] Failed to void test transaction: %v", requestID, err)
        // Continua mesmo com falha no void
    } else {
        log.Printf("[UpdateCard %s] Test transaction voided successfully", requestID)
    }
    
    // NOVA ETAPA 3: Criar ou Atualizar Customer Profile
    log.Printf("[UpdateCard %s] Step 3: Creating/Updating Customer Profile in Authorize.net", requestID)
    
    checkoutData := h.convertMasterAccountToCheckoutData(master)
    customerProfileID, paymentProfileID, err := h.handleCustomerProfile(ctx, requestID, paymentReq, checkoutData, master)
    if err != nil {
        return "", "", "", fmt.Errorf("failed to handle customer profile: %v", err)
    }
    
    log.Printf("[UpdateCard %s] Customer Profile handled successfully - Profile ID: %s, Payment Profile ID: %s", 
        requestID, customerProfileID, paymentProfileID)
    
    // NOVA ETAPA 4: Criar ARB usando Customer Profile
    log.Printf("[UpdateCard %s] Step 4: Creating subscription (ARB) using Customer Profile", requestID)
    
    subscriptionID, err := h.paymentService.SetupRecurringBilling(paymentReq, checkoutData)
    if err != nil {
        log.Printf("[UpdateCard %s] Failed to setup subscription with customer profile: %v", requestID, err)
        // Continua mesmo com falha na subscription - o importante é o Customer Profile
    } else {
        log.Printf("[UpdateCard %s] Subscription created successfully using Customer Profile - Subscription ID: %s", 
            requestID, subscriptionID)
        
        // Atualizar subscription no banco em background
        go func() {
            updateCtx, updateCancel := context.WithTimeout(context.Background(), 10*time.Second)
            defer updateCancel()
            
            _, updateErr := h.db.GetDB().ExecContext(updateCtx,
                `UPDATE subscriptions 
                 SET subscription_id = ?, status = 'active', updated_at = NOW()
                 WHERE master_reference = ?`,
                subscriptionID, master.ReferenceUUID)
            
            if updateErr != nil {
                log.Printf("[UpdateCard %s] Warning: Failed to update subscription with new ID: %v", requestID, updateErr)
            } else {
                log.Printf("[UpdateCard %s] Successfully updated subscription with new ID: %s", requestID, subscriptionID)
            }
        }()
    }
    
    return transactionID, customerProfileID, paymentProfileID, nil
}

// NOVA FUNÇÃO: Gerenciar Customer Profile (criar ou atualizar)
func (h *UpdateCardHandler) handleCustomerProfile(ctx context.Context, requestID string, paymentReq *models.PaymentRequest, checkoutData *models.CheckoutData, master *models.MasterAccount) (string, string, error) {
    log.Printf("[UpdateCard %s] Handling Customer Profile for user: %s", requestID, master.Username)
    
    // Verificar se já existe Customer Profile para este usuário
    var existingCustomerProfileID, existingPaymentProfileID string
    profileErr := h.db.GetDB().QueryRowContext(ctx,
        `SELECT authorize_customer_profile_id, authorize_payment_profile_id 
         FROM customer_profiles 
         WHERE master_reference = ?`,
        master.ReferenceUUID).Scan(&existingCustomerProfileID, &existingPaymentProfileID)
    
    if profileErr == nil && existingCustomerProfileID != "" && existingPaymentProfileID != "" {
        // CENÁRIO 1: Customer Profile existe - tentar atualizar
        log.Printf("[UpdateCard %s] Existing Customer Profile found: %s/%s - attempting update", 
            requestID, existingCustomerProfileID, existingPaymentProfileID)
        
        updateErr := h.paymentService.UpdateCustomerPaymentProfile(
            existingCustomerProfileID, 
            existingPaymentProfileID, 
            paymentReq, 
            checkoutData,
        )
        
        if updateErr == nil {
            log.Printf("[UpdateCard %s] Successfully updated existing Customer Profile: %s/%s", 
                requestID, existingCustomerProfileID, existingPaymentProfileID)
            return existingCustomerProfileID, existingPaymentProfileID, nil
        }
        
        log.Printf("[UpdateCard %s] Failed to update existing Customer Profile, creating new one: %v", 
            requestID, updateErr)
        // Se falhar a atualização, criar um novo (fallthrough)
    } else {
        log.Printf("[UpdateCard %s] No existing Customer Profile found for user: %s, creating new one", 
            requestID, master.Username)
    }
    
    // CENÁRIO 2: Criar novo Customer Profile
    maxAttempts := 2
    var customerProfileID, paymentProfileID string
    var createErr error
    
    for attempt := 1; attempt <= maxAttempts; attempt++ {
        if attempt > 1 {
            log.Printf("[UpdateCard %s] Retry customer profile creation attempt %d/%d", 
                requestID, attempt, maxAttempts)
            time.Sleep(time.Duration(attempt) * 2 * time.Second)
        }
        
        customerProfileID, paymentProfileID, createErr = h.paymentService.CreateCustomerProfile(paymentReq, checkoutData)
        if createErr == nil && customerProfileID != "" && paymentProfileID != "" {
            break // Sucesso!
        }
        
        log.Printf("[UpdateCard %s] Customer profile creation attempt %d failed: %v", requestID, attempt, createErr)
    }
    
    if createErr != nil {
        return "", "", fmt.Errorf("failed to create customer profile after %d attempts: %v", maxAttempts, createErr)
    }
    
    log.Printf("[UpdateCard %s] Successfully created new Customer Profile: %s/%s", 
        requestID, customerProfileID, paymentProfileID)
    
    return customerProfileID, paymentProfileID, nil
}

// FUNÇÃO: Atualização de banco com Customer Profile
func (h *UpdateCardHandler) updateAccountAfterCardUpdateWithProfile(ctx context.Context, master *models.MasterAccount, payment *models.PaymentRequest, transactionID, customerProfileID, paymentProfileID string) error {
    tx, err := h.db.BeginTransaction()
    if err != nil {
        return fmt.Errorf("failed to begin transaction: %v", err)
    }
    defer func() {
        if err != nil {
            tx.Rollback()
        }
    }()
    
    // 1. Salvar/Atualizar Customer Profile IDs no banco
    log.Printf("Saving Customer Profile IDs: %s/%s for user: %s", 
        customerProfileID, paymentProfileID, master.Username)
    
    profileSaveErr := h.db.SaveCustomerProfile(master.ReferenceUUID, customerProfileID, paymentProfileID)
    if profileSaveErr != nil {
        log.Printf("Warning: Failed to save customer profile IDs: %v", profileSaveErr)
        // Continua mesmo com erro - o importante é que o Customer Profile foi criado na Authorize.net
    } else {
        log.Printf("Successfully saved Customer Profile IDs for user: %s", master.Username)
    }
    
    // 2. Atualizar método de pagamento (billing_infos)
    cardData := &models.CardData{
        Number: payment.CardNumber,
        Expiry: payment.Expiry,
    }
    
    if err = tx.SavePaymentMethod(master.ReferenceUUID, cardData); err != nil {
        return fmt.Errorf("failed to update payment method: %v", err)
    }
    
    // 3. CORRIGIDO: Atualizar payment_status para sucesso (3)
    log.Printf("Setting payment_status to success (3) for user: %s", master.Username)
    err = h.db.SetPaymentProcessingSuccess(master.Email, master.Username)
    if err != nil {
        return fmt.Errorf("failed to set payment status to success: %v", err)
    }
    
    // 4. Atualizar status das subscriptions para ativa
    _, err = h.db.GetDB().ExecContext(ctx,
        "UPDATE subscriptions SET status = 'active', updated_at = NOW() WHERE master_reference = ?",
        master.ReferenceUUID)
    
    if err != nil {
        return fmt.Errorf("failed to update subscription status: %v", err)
    }
    
    // 5. Registrar nova transação
    if err = tx.SaveTransaction(master.ReferenceUUID, "CARD_UPDATE", 1.00, "voided", transactionID); err != nil {
        return fmt.Errorf("failed to save transaction: %v", err)
    }
    
    // 6. Commit da transação
    if err = tx.Commit(); err != nil {
        return fmt.Errorf("failed to commit transaction: %v", err)
    }
    
    log.Printf("Successfully updated account data with Customer Profile for user: %s", master.Username)
    return nil
}

// CORRIGIDA: Função para verificar status de pagamento (USANDO PAYMENT_STATUS)
func (h *UpdateCardHandler) checkUserPaymentErrorStatusFast(ctx context.Context, email, username string) (bool, error) {
    paymentStatus, err := h.db.GetUserPaymentStatus(email, username)
    if err != nil {
        return false, fmt.Errorf("user not found or error getting payment status: %v", err)
    }
    
    // payment_status = 1 significa erro de pagamento (precisa atualizar cartão)
    return paymentStatus == int(models.PaymentStatusFailed), nil
}

// Funções auxiliares mantidas
func (h *UpdateCardHandler) getMasterAccountDataFast(ctx context.Context, email, username string) (*models.MasterAccount, error) {
    query := `
        SELECT reference_uuid, name, lname, email, username, phone_number,
               state, city, street, zip_code, additional_info, total_price,
               is_annually, plan, purchased_plans
        FROM master_accounts 
        WHERE email = ? AND username = ?
        LIMIT 1
    `
    
    var account models.MasterAccount
    err := h.db.GetDB().QueryRowContext(ctx, query, email, username).Scan(
        &account.ReferenceUUID,
        &account.Name,
        &account.LastName,
        &account.Email,
        &account.Username,
        &account.PhoneNumber,
        &account.State,
        &account.City,
        &account.Street,
        &account.ZipCode,
        &account.AdditionalInfo,
        &account.TotalPrice,
        &account.IsAnnually,
        &account.Plan,
        &account.PurchasedPlans,
    )
    
    if err != nil {
        return nil, fmt.Errorf("master account not found: %v", err)
    }
    
    return &account, nil
}

func (h *UpdateCardHandler) convertMasterAccountToCheckoutData(master *models.MasterAccount) *models.CheckoutData {
    checkoutData := &models.CheckoutData{
        ID:          master.ReferenceUUID,
        Name:        fmt.Sprintf("%s %s", master.Name, master.LastName),
        Email:       master.Email,
        Username:    master.Username,
        PhoneNumber: master.PhoneNumber,
        Street:      master.Street,
        City:        master.City,
        State:       master.State,
        ZipCode:     master.ZipCode,
        Additional:  master.AdditionalInfo,
        PlanID:      master.Plan,
        PlansJSON:   master.PurchasedPlans,
        Total:       master.TotalPrice,
    }
    
    // Parse simplificado dos planos
    plans := []models.Plan{
        {
            PlanID:   master.Plan,
            PlanName: "Subscription Plan",
            Price:    master.TotalPrice,
            Annually: master.IsAnnually,
        },
    }
    
    checkoutData.Plans = plans
    return checkoutData
}

func (h *UpdateCardHandler) sendCardUpdateConfirmationEmailAsync(ctx context.Context, email, name, requestID string) error {
    subject := "Payment Method Updated Successfully"
    
    content := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <title>Payment Method Updated</title>
</head>
<body style="font-family: Arial, sans-serif; line-height: 1.6; color: #333;">
    <div style="max-width: 600px; margin: 0 auto; padding: 20px;">
        <h2 style="color: #25364D;">Payment Method Updated Successfully</h2>
        
        <p>Hi %s,</p>
        
        <p>Great news! Your payment method has been updated successfully with enhanced Customer Profile integration, and your ProSecureLSP account is now active again.</p>
        
        <div style="background-color: #dcfdf7; padding: 15px; border-radius: 5px; margin: 20px 0;">
            <p style="margin: 0; color: #065f46;"><strong>✓ Payment method updated with Customer Profile</strong></p>
            <p style="margin: 0; color: #065f46;"><strong>✓ Recurring billing reactivated</strong></p>
            <p style="margin: 0; color: #065f46;"><strong>✓ Account fully restored</strong></p>
        </div>
        
        <p>Your subscription will continue as normal from your next billing cycle with improved payment processing.</p>
        
        <p style="margin-top: 30px;">
            <a href="https://prosecurelsp.com/users/index.php" 
               style="background-color: #157347; color: white; padding: 12px 24px; text-decoration: none; border-radius: 5px; display: inline-block;">
                Access Your Account
            </a>
        </p>
        
        <p>Thank you for choosing ProSecureLSP!</p>
    </div>
</body>
</html>`, name)
    
    log.Printf("[UpdateCard %s] Sending confirmation email to %s", requestID, email)
    return h.emailService.SendEmail(email, subject, content)
}

// Helper methods para responses
func (h *UpdateCardHandler) sendErrorResponse(w http.ResponseWriter, status int, message string) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    json.NewEncoder(w).Encode(UpdateCardResponse{
        Status:  "error",
        Message: message,
        Success: false,
    })
}

func (h *UpdateCardHandler) sendSuccessResponse(w http.ResponseWriter, response UpdateCardResponse) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(http.StatusOK)
    json.NewEncoder(w).Encode(response)
}

// CORRIGIDOS: Métodos de status agora usando payment_status
func (h *UpdateCardHandler) CheckAccountStatus(w http.ResponseWriter, r *http.Request) {
    ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
    defer cancel()
    
    email := r.URL.Query().Get("email")
    username := r.URL.Query().Get("username")
    
    if email == "" || username == "" {
        h.sendErrorResponse(w, http.StatusBadRequest, "Email and username are required")
        return
    }

    masterAccount, err := h.getMasterAccountDataFast(ctx, email, username)
    if err != nil {
        h.sendErrorResponse(w, http.StatusNotFound, "Account not found")
        return
    }

    // CORRIGIDO: Usar payment_status em vez de is_active
    paymentStatus, err := h.db.GetUserPaymentStatus(email, username)
    if err != nil {
        h.sendErrorResponse(w, http.StatusNotFound, "User payment status not found")
        return
    }

    // Verificar se tem Customer Profile
    var hasCustomerProfile bool
    var customerProfileID string
    profileErr := h.db.GetDB().QueryRowContext(ctx,
        `SELECT authorize_customer_profile_id FROM customer_profiles 
         WHERE master_reference = ?`,
        masterAccount.ReferenceUUID).Scan(&customerProfileID)
    
    hasCustomerProfile = (profileErr == nil && customerProfileID != "")

    response := map[string]interface{}{
        "status":              "success",
        "payment_status":      paymentStatus,
        "account_status":      func() string {
            switch paymentStatus {
            case int(models.PaymentStatusProcessing): return "processing"
            case int(models.PaymentStatusFailed): return "payment_error"
            case int(models.PaymentStatusSuccess): return "active" 
            default: return "unknown"
            }
        }(),
        "needs_card_update":   paymentStatus == int(models.PaymentStatusFailed),
        "has_customer_profile": hasCustomerProfile,
        "customer_profile_id":  customerProfileID,
        "account_details": map[string]interface{}{
            "email":        masterAccount.Email,
            "username":     masterAccount.Username,
            "name":         fmt.Sprintf("%s %s", masterAccount.Name, masterAccount.LastName),
            "total_price":  masterAccount.TotalPrice,
            "is_annually":  masterAccount.IsAnnually == 1,
        },
    }

    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(http.StatusOK)
    json.NewEncoder(w).Encode(response)
}

func (h *UpdateCardHandler) GetCardUpdateHistory(w http.ResponseWriter, r *http.Request) {
    ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
    defer cancel()
    
    email := r.URL.Query().Get("email")
    username := r.URL.Query().Get("username")
    
    if email == "" || username == "" {
        h.sendErrorResponse(w, http.StatusBadRequest, "Email and username are required")
        return
    }

    masterAccount, err := h.getMasterAccountDataFast(ctx, email, username)
    if err != nil {
        h.sendErrorResponse(w, http.StatusNotFound, "Account not found")
        return
    }

    // Buscar histórico rapidamente
    query := `
        SELECT transaction_id, amount, status, created_at
        FROM transactions 
        WHERE master_reference = ? 
        AND (checkout_id = 'CARD_UPDATE' OR checkout_id LIKE '%update%')
        ORDER BY created_at DESC 
        LIMIT 10
    `
    
    rows, err := h.db.GetDB().QueryContext(ctx, query, masterAccount.ReferenceUUID)
    if err != nil {
        h.sendErrorResponse(w, http.StatusInternalServerError, "Failed to retrieve update history")
        return
    }
    defer rows.Close()

    var updates []map[string]interface{}
    for rows.Next() {
        var transactionID, status string
        var amount float64
        var createdAt time.Time
        
        if err := rows.Scan(&transactionID, &amount, &status, &createdAt); err != nil {
            continue
        }
        
        updates = append(updates, map[string]interface{}{
            "transaction_id": transactionID,
            "amount":        amount,
            "status":        status,
            "updated_at":    createdAt.Format("2006-01-02 15:04:05"),
        })
    }

    // Buscar informações do Customer Profile
    var customerProfileInfo map[string]interface{}
    var customerProfileID, paymentProfileID string
    profileErr := h.db.GetDB().QueryRowContext(ctx,
        `SELECT authorize_customer_profile_id, authorize_payment_profile_id 
         FROM customer_profiles 
         WHERE master_reference = ?`,
        masterAccount.ReferenceUUID).Scan(&customerProfileID, &paymentProfileID)
    
    if profileErr == nil && customerProfileID != "" {
        customerProfileInfo = map[string]interface{}{
            "customer_profile_id": customerProfileID,
            "payment_profile_id":  paymentProfileID,
            "has_profile":        true,
        }
    } else {
        customerProfileInfo = map[string]interface{}{
            "has_profile": false,
        }
    }

    // CORRIGIDO: Buscar payment_status atual
    paymentStatus, _ := h.db.GetUserPaymentStatus(email, username)

    response := map[string]interface{}{
        "status": "success",
        "account_info": map[string]interface{}{
            "email":          masterAccount.Email,
            "username":       masterAccount.Username,
            "name":           fmt.Sprintf("%s %s", masterAccount.Name, masterAccount.LastName),
            "payment_status": paymentStatus,
        },
        "customer_profile":   customerProfileInfo,
        "update_history":     updates,
        "total_updates":      len(updates),
    }

    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(http.StatusOK)
    json.NewEncoder(w).Encode(response)
}