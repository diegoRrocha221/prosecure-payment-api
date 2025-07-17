// handlers/update_card.go
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
    log.Printf("[UpdateCard %s] Starting FAST card update process", requestID)
    
    // CRÍTICO: Definir timeout de resposta otimizado
    ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second) // 40 segundos MAX
    defer cancel()
    
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

    log.Printf("[UpdateCard %s] Basic validation passed, starting async processing", requestID)
    
    // NOVO: Processar de forma assíncrona com timeout rígido
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
        
        result, err := h.processCardUpdateFast(ctx, requestID, req)
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

    // Aguardar resultado com timeout RÍGIDO
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
        log.Printf("[UpdateCard %s] Request timeout after 40 seconds", requestID)
        h.sendErrorResponse(w, http.StatusRequestTimeout, "Request timeout - please try again")
    }
}

// NOVA FUNÇÃO: Processamento rápido e otimizado
func (h *UpdateCardHandler) processCardUpdateFast(ctx context.Context, requestID string, req UpdateCardRequest) (UpdateCardResponse, error) {
    log.Printf("[UpdateCard %s] Fast processing started", requestID)
    
    // ETAPA 1: Buscar dados da conta (COM TIMEOUT)
    dbCtx, dbCancel := context.WithTimeout(ctx, 5*time.Second)
    defer dbCancel()
    
    masterAccount, err := h.getMasterAccountDataFast(dbCtx, req.Email, req.Username)
    if err != nil {
        return UpdateCardResponse{}, fmt.Errorf("account not found or invalid credentials: %v", err)
    }

    // ETAPA 2: Verificar status inativo (COM TIMEOUT)
    isInactive, err := h.checkUserInactiveStatusFast(dbCtx, req.Email, req.Username)
    if err != nil {
        return UpdateCardResponse{}, fmt.Errorf("error checking account status: %v", err)
    }

    if !isInactive {
        return UpdateCardResponse{
            Status:  "error",
            Message: "Account is already active, no card update needed",
            Success: false,
        }, nil
    }

    log.Printf("[UpdateCard %s] Account validation completed, processing payment", requestID)

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

    // ETAPA 5: Processar pagamento com timeout reduzido
    paymentCtx, paymentCancel := context.WithTimeout(ctx, 25*time.Second) // 25 segundos para pagamento
    defer paymentCancel()
    
    transactionID, err := h.processPaymentOperationsFast(paymentCtx, requestID, paymentReq, masterAccount)
    if err != nil {
        return UpdateCardResponse{}, err
    }

    // ETAPA 6: Atualizar banco rapidamente
    updateCtx, updateCancel := context.WithTimeout(ctx, 10*time.Second)
    defer updateCancel()
    
    if err := h.updateAccountAfterCardUpdateFast(updateCtx, masterAccount, paymentReq, transactionID); err != nil {
        return UpdateCardResponse{}, fmt.Errorf("failed to update account data: %v", err)
    }

    log.Printf("[UpdateCard %s] Card update completed successfully in fast mode", requestID)

    return UpdateCardResponse{
        Status:  "success",
        Message: "Card updated successfully and recurring billing reactivated",
        Success: true,
        Details: map[string]interface{}{
            "transaction_id": transactionID,
            "account_status": "active",
            "updated_at":     time.Now().Format("2006-01-02 15:04:05"),
            "processing_mode": "fast",
        },
    }, nil
}

// OTIMIZADA: Operações de pagamento mais rápidas
func (h *UpdateCardHandler) processPaymentOperationsFast(ctx context.Context, requestID string, paymentReq *models.PaymentRequest, master *models.MasterAccount) (string, error) {
    log.Printf("[UpdateCard %s] Fast payment operations started", requestID)
    
    // Canal para cada operação
    testTxChan := make(chan struct { 
        resp *models.TransactionResponse
        err error 
    }, 1)
    
    // ETAPA 1: Transação teste (com timeout interno)
    go func() {
        resp, err := h.paymentService.ProcessInitialAuthorization(paymentReq)
        testTxChan <- struct { 
            resp *models.TransactionResponse
            err error 
        }{resp, err}
    }()
    
    // Aguardar resultado da transação teste
    select {
    case result := <-testTxChan:
        if result.err != nil {
            return "", fmt.Errorf("test transaction failed: %v", result.err)
        }
        if result.resp == nil || !result.resp.Success {
            message := "transaction declined"
            if result.resp != nil {
                message = result.resp.Message
            }
            return "", fmt.Errorf("transaction declined: %s", message)
        }
        
        transactionID := result.resp.TransactionID
        log.Printf("[UpdateCard %s] Test transaction successful: %s", requestID, transactionID)
        
        // ETAPA 2: Void em background (não bloquear resposta)
        go func() {
            log.Printf("[UpdateCard %s] Starting void transaction in background: %s", requestID, transactionID)
            if err := h.paymentService.VoidTransaction(transactionID); err != nil {
                log.Printf("[UpdateCard %s] Warning: Failed to void test transaction: %v", requestID, err)
            } else {
                log.Printf("[UpdateCard %s] Test transaction voided successfully in background", requestID)
            }
        }()
        
        // ETAPA 3: ARB em background (não bloquear resposta)
        go func() {
            checkoutData, err := h.convertMasterAccountToCheckoutDataWithPrices(master)
            if err != nil {
                log.Printf("[UpdateCard %s] Warning: Failed to convert account data for ARB: %v", requestID, err)
                return
            }
            
            if err := h.paymentService.SetupRecurringBilling(paymentReq, checkoutData); err != nil {
                log.Printf("[UpdateCard %s] Warning: Failed to setup ARB in background: %v", requestID, err)
                // TODO: Poderia retentar ou notificar admin
            } else {
                log.Printf("[UpdateCard %s] ARB setup completed in background", requestID)
            }
        }()
        
        return transactionID, nil
        
    case <-ctx.Done():
        return "", fmt.Errorf("payment operations timeout")
    }
}

// OTIMIZADA: Busca de dados mais rápida
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

// OTIMIZADA: Verificação de status mais rápida
func (h *UpdateCardHandler) checkUserInactiveStatusFast(ctx context.Context, email, username string) (bool, error) {
    var isActive int
    query := "SELECT is_active FROM users WHERE email = ? AND username = ? LIMIT 1"
    
    err := h.db.GetDB().QueryRowContext(ctx, query, email, username).Scan(&isActive)
    if err != nil {
        return false, fmt.Errorf("user not found: %v", err)
    }
    
    return isActive == 9, nil
}

// OTIMIZADA: Atualização de banco mais rápida
func (h *UpdateCardHandler) updateAccountAfterCardUpdateFast(ctx context.Context, master *models.MasterAccount, payment *models.PaymentRequest, transactionID string) error {
    tx, err := h.db.BeginTransaction()
    if err != nil {
        return fmt.Errorf("failed to begin transaction: %v", err)
    }
    defer func() {
        if err != nil {
            tx.Rollback()
        }
    }()
    
    // 1. Verificar se existe Customer Profile para este usuário
    var customerProfileID, paymentProfileID string
    profileErr := h.db.GetDB().QueryRowContext(ctx,
        `SELECT authorize_customer_profile_id, authorize_payment_profile_id 
         FROM customer_profiles 
         WHERE master_reference = ?`,
        master.ReferenceUUID).Scan(&customerProfileID, &paymentProfileID)
    
    // 2. Se existe Customer Profile, atualizar na Authorize.net
    if profileErr == nil && customerProfileID != "" && paymentProfileID != "" {
        log.Printf("Updating existing Customer Profile: %s/%s for user: %s", 
            customerProfileID, paymentProfileID, master.Username)
        
        // Converter MasterAccount para CheckoutData para compatibilidade
        checkoutData := h.convertMasterAccountToCheckoutData(master)
        
        // Atualizar o Customer Profile na Authorize.net
        updateErr := h.paymentService.UpdateCustomerPaymentProfile(
            customerProfileID, 
            paymentProfileID, 
            payment, 
            checkoutData,
        )
        
        if updateErr != nil {
            log.Printf("Failed to update Customer Profile, will create new one: %v", updateErr)
            
            // Se falhar a atualização, criar um novo Customer Profile
            newCustomerProfileID, newPaymentProfileID, createErr := h.paymentService.CreateCustomerProfile(payment, checkoutData)
            if createErr != nil {
                return fmt.Errorf("failed to create new customer profile after update failure: %v", createErr)
            }
            
            // Atualizar os IDs no banco de dados
            _, err = h.db.GetDB().ExecContext(ctx,
                `UPDATE customer_profiles 
                 SET authorize_customer_profile_id = ?, 
                     authorize_payment_profile_id = ?,
                     updated_at = NOW()
                 WHERE master_reference = ?`,
                newCustomerProfileID, newPaymentProfileID, master.ReferenceUUID)
            
            if err != nil {
                return fmt.Errorf("failed to update customer profile IDs: %v", err)
            }
            
            log.Printf("Created new Customer Profile: %s/%s for user: %s", 
                newCustomerProfileID, newPaymentProfileID, master.Username)
            
        } else {
            log.Printf("Successfully updated Customer Profile: %s/%s for user: %s", 
                customerProfileID, paymentProfileID, master.Username)
        }
        
    } else {
        // 3. Se não existe Customer Profile, criar um novo
        log.Printf("No existing Customer Profile found for user: %s, creating new one", master.Username)
        
        checkoutData := h.convertMasterAccountToCheckoutData(master)
        
        newCustomerProfileID, newPaymentProfileID, createErr := h.paymentService.CreateCustomerProfile(payment, checkoutData)
        if createErr != nil {
            return fmt.Errorf("failed to create customer profile: %v", createErr)
        }
        
        // Inserir os novos IDs no banco de dados
        _, err = h.db.GetDB().ExecContext(ctx,
            `INSERT INTO customer_profiles 
             (master_reference, authorize_customer_profile_id, authorize_payment_profile_id, created_at)
             VALUES (?, ?, ?, NOW())
             ON DUPLICATE KEY UPDATE
             authorize_customer_profile_id = ?,
             authorize_payment_profile_id = ?,
             updated_at = NOW()`,
            master.ReferenceUUID, newCustomerProfileID, newPaymentProfileID,
            newCustomerProfileID, newPaymentProfileID)
        
        if err != nil {
            return fmt.Errorf("failed to save customer profile IDs: %v", err)
        }
        
        log.Printf("Created and saved new Customer Profile: %s/%s for user: %s", 
            newCustomerProfileID, newPaymentProfileID, master.Username)
    }
    
    // 4. Continuar com as operações existentes...
    
    // Atualizar método de pagamento (billing_infos)
    cardData := &models.CardData{
        Number: payment.CardNumber,
        Expiry: payment.Expiry,
    }
    
    if err = tx.SavePaymentMethod(master.ReferenceUUID, cardData); err != nil {
        return fmt.Errorf("failed to update payment method: %v", err)
    }
    
    // Atualizar status do usuário para ativo (is_active = 1)
    _, err = h.db.GetDB().ExecContext(ctx,
        "UPDATE users SET is_active = 1 WHERE email = ? AND username = ?",
        master.Email, master.Username)
    
    if err != nil {
        return fmt.Errorf("failed to reactivate user: %v", err)
    }
    
    // Atualizar status das subscriptions para ativa
    _, err = h.db.GetDB().ExecContext(ctx,
        "UPDATE subscriptions SET status = 'active', updated_at = NOW() WHERE master_reference = ?",
        master.ReferenceUUID)
    
    if err != nil {
        return fmt.Errorf("failed to update subscription status: %v", err)
    }
    
    // Registrar nova transação
    if err = tx.SaveTransaction(master.ReferenceUUID, "CARD_UPDATE", 1.00, "voided", transactionID); err != nil {
        return fmt.Errorf("failed to save transaction: %v", err)
    }
    
    // Commit da transação
    if err = tx.Commit(); err != nil {
        return fmt.Errorf("failed to commit transaction: %v", err)
    }
    
    log.Printf("Successfully updated account data with Customer Profile for user: %s", master.Username)
    return nil
}

// OTIMIZADA: Email assíncrono
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
        
        <p>Great news! Your payment method has been updated successfully and your ProSecureLSP account is now active again.</p>
        
        <div style="background-color: #dcfdf7; padding: 15px; border-radius: 5px; margin: 20px 0;">
            <p style="margin: 0; color: #065f46;"><strong>✓ Payment method updated</strong></p>
            <p style="margin: 0; color: #065f46;"><strong>✓ Recurring billing reactivated</strong></p>
            <p style="margin: 0; color: #065f46;"><strong>✓ Account fully restored</strong></p>
        </div>
        
        <p>Your subscription will continue as normal from your next billing cycle.</p>
        
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

// CORREÇÃO: Conversão otimizada (mantida da versão anterior)
func (h *UpdateCardHandler) convertMasterAccountToCheckoutDataWithPrices(master *models.MasterAccount) (*models.CheckoutData, error) {
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
    return checkoutData, nil
}

// Helper methods para responses (mantidos)
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

// Métodos de status (mantidos mas com timeouts otimizados)
func (h *UpdateCardHandler) CheckAccountStatus(w http.ResponseWriter, r *http.Request) {
    // Implementação mantida com timeout de 10 segundos
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

    var isActive int
    err = h.db.GetDB().QueryRowContext(ctx,
        "SELECT is_active FROM users WHERE email = ? AND username = ? LIMIT 1",
        email, username).Scan(&isActive)
    
    if err != nil {
        h.sendErrorResponse(w, http.StatusNotFound, "User not found")
        return
    }

    // Resposta rápida
    response := map[string]interface{}{
        "status":              "success",
        "account_status":      func() string {
            switch isActive {
            case 0: return "pending_activation"
            case 1: return "active" 
            case 9: return "payment_error"
            default: return "unknown"
            }
        }(),
        "needs_card_update":   isActive == 9,
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

    response := map[string]interface{}{
        "status": "success",
        "account_info": map[string]interface{}{
            "email":    masterAccount.Email,
            "username": masterAccount.Username,
            "name":     fmt.Sprintf("%s %s", masterAccount.Name, masterAccount.LastName),
        },
        "update_history": updates,
        "total_updates":  len(updates),
    }

    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(http.StatusOK)
    json.NewEncoder(w).Encode(response)
}