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
    log.Printf("[UpdateCard %s] Starting card update process", requestID)

    var req UpdateCardRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        log.Printf("[UpdateCard %s] Invalid request body: %v", requestID, err)
        h.sendErrorResponse(w, http.StatusBadRequest, "Invalid request body")
        return
    }

    // Validar campos obrigatórios
    if req.Email == "" || req.Username == "" || req.CardName == "" || 
       req.CardNumber == "" || req.Expiry == "" || req.CVV == "" {
        log.Printf("[UpdateCard %s] Missing required fields", requestID)
        h.sendErrorResponse(w, http.StatusBadRequest, "All fields are required")
        return
    }

    // Buscar dados da conta master
    masterAccount, err := h.getMasterAccountData(req.Email, req.Username)
    if err != nil {
        log.Printf("[UpdateCard %s] Error getting master account: %v", requestID, err)
        h.sendErrorResponse(w, http.StatusNotFound, "Account not found or invalid credentials")
        return
    }

    // Verificar se a conta tem erro de pagamento (is_active = 9)
    isInactive, err := h.checkUserInactiveStatus(req.Email, req.Username)
    if err != nil {
        log.Printf("[UpdateCard %s] Error checking user status: %v", requestID, err)
        h.sendErrorResponse(w, http.StatusInternalServerError, "Error checking account status")
        return
    }

    if !isInactive {
        log.Printf("[UpdateCard %s] Account is not inactive, update not needed", requestID)
        h.sendErrorResponse(w, http.StatusBadRequest, "Account is already active, no card update needed")
        return
    }

    log.Printf("[UpdateCard %s] Processing card update for account: %s", requestID, masterAccount.ReferenceUUID)

    // Criar request de pagamento
    paymentReq := &models.PaymentRequest{
        CardName:      req.CardName,
        CardNumber:    req.CardNumber,
        Expiry:        req.Expiry,
        CVV:           req.CVV,
        CheckoutID:    masterAccount.ReferenceUUID, // Usar reference como ID
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

    // Validar dados do cartão
    if !h.paymentService.ValidateCard(paymentReq) {
        log.Printf("[UpdateCard %s] Invalid card data", requestID)
        h.sendErrorResponse(w, http.StatusBadRequest, "Invalid card data: check card number, expiration date and CVV")
        return
    }

    // ETAPA 1: Processar transação teste de $1.00
    log.Printf("[UpdateCard %s] Step 1: Processing test transaction", requestID)
    
    resp, err := h.paymentService.ProcessInitialAuthorization(paymentReq)
    if err != nil {
        log.Printf("[UpdateCard %s] Test transaction failed: %v", requestID, err)
        h.sendErrorResponse(w, http.StatusPaymentRequired, fmt.Sprintf("Payment processing failed: %v", err))
        return
    }

    if !resp.Success {
        log.Printf("[UpdateCard %s] Test transaction declined: %s", requestID, resp.Message)
        h.sendErrorResponse(w, http.StatusPaymentRequired, fmt.Sprintf("Transaction declined: %s", resp.Message))
        return
    }

    transactionID := resp.TransactionID
    log.Printf("[UpdateCard %s] Test transaction successful: %s", requestID, transactionID)

    // ETAPA 2: Fazer VOID da transação teste
    log.Printf("[UpdateCard %s] Step 2: Voiding test transaction", requestID)
    
    if err := h.paymentService.VoidTransaction(transactionID); err != nil {
        log.Printf("[UpdateCard %s] Failed to void test transaction: %v", requestID, err)
        h.sendErrorResponse(w, http.StatusInternalServerError, fmt.Sprintf("Failed to void test transaction: %v", err))
        return
    }

    log.Printf("[UpdateCard %s] Test transaction voided successfully", requestID)

    // ETAPA 3: Criar nova subscription usando dados da master account
    log.Printf("[UpdateCard %s] Step 3: Creating new subscription", requestID)
    
    // CORREÇÃO: Converter master account para checkout data COM preços corretos
    checkoutData, err := h.convertMasterAccountToCheckoutDataWithPrices(masterAccount)
    if err != nil {
        log.Printf("[UpdateCard %s] Failed to convert master account data: %v", requestID, err)
        h.sendErrorResponse(w, http.StatusInternalServerError, fmt.Sprintf("Failed to process account data: %v", err))
        return
    }
    
    log.Printf("[UpdateCard %s] Checkout data total calculated: $%.2f", requestID, checkoutData.Total)
    
    if checkoutData.Total <= 0 {
        log.Printf("[UpdateCard %s] Invalid subscription amount: $%.2f", requestID, checkoutData.Total)
        h.sendErrorResponse(w, http.StatusInternalServerError, "Invalid subscription amount calculated")
        return
    }
    
    if err := h.paymentService.SetupRecurringBilling(paymentReq, checkoutData); err != nil {
        log.Printf("[UpdateCard %s] Failed to setup subscription: %v", requestID, err)
        h.sendErrorResponse(w, http.StatusInternalServerError, fmt.Sprintf("Failed to setup recurring billing: %v", err))
        return
    }

    log.Printf("[UpdateCard %s] Subscription created successfully", requestID)

    // ETAPA 4: Atualizar dados no banco de dados
    log.Printf("[UpdateCard %s] Step 4: Updating database records", requestID)
    
    if err := h.updateAccountAfterCardUpdate(masterAccount, paymentReq, transactionID); err != nil {
        log.Printf("[UpdateCard %s] Failed to update database: %v", requestID, err)
        h.sendErrorResponse(w, http.StatusInternalServerError, fmt.Sprintf("Failed to update account data: %v", err))
        return
    }

    // ETAPA 5: Enviar email de confirmação
    log.Printf("[UpdateCard %s] Step 5: Sending confirmation email", requestID)
    
    if err := h.sendCardUpdateConfirmationEmail(req.Email, masterAccount.Name); err != nil {
        log.Printf("[UpdateCard %s] Warning: Failed to send confirmation email: %v", requestID, err)
        // Não falha o processo por causa do email
    }

    log.Printf("[UpdateCard %s] Card update process completed successfully", requestID)

    // Resposta de sucesso
    h.sendSuccessResponse(w, UpdateCardResponse{
        Status:  "success",
        Message: "Card updated successfully and recurring billing reactivated",
        Success: true,
        Details: map[string]interface{}{
            "transaction_id": transactionID,
            "account_status": "active",
            "updated_at":     time.Now().Format("2006-01-02 15:04:05"),
        },
    })
}

// getMasterAccountData busca os dados da conta master
func (h *UpdateCardHandler) getMasterAccountData(email, username string) (*models.MasterAccount, error) {
    query := `
        SELECT reference_uuid, name, lname, email, username, phone_number,
               state, city, street, zip_code, additional_info, total_price,
               is_annually, plan, purchased_plans
        FROM master_accounts 
        WHERE email = ? AND username = ?
        LIMIT 1
    `
    
    var account models.MasterAccount
    err := h.db.GetDB().QueryRow(query, email, username).Scan(
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

// checkUserInactiveStatus verifica se o usuário está inativo (is_active = 9)
func (h *UpdateCardHandler) checkUserInactiveStatus(email, username string) (bool, error) {
    var isActive int
    query := "SELECT is_active FROM users WHERE email = ? AND username = ? LIMIT 1"
    
    err := h.db.GetDB().QueryRow(query, email, username).Scan(&isActive)
    if err != nil {
        return false, fmt.Errorf("user not found: %v", err)
    }
    
    return isActive == 9, nil
}

// CORREÇÃO: Nova função que converte master account para checkout data COM preços corretos
func (h *UpdateCardHandler) convertMasterAccountToCheckoutDataWithPrices(master *models.MasterAccount) (*models.CheckoutData, error) {
    log.Printf("Converting master account to checkout data with prices. Total price from DB: $%.2f", master.TotalPrice)
    
    // Usar o total price que já está salvo na master account
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
        Total:       master.TotalPrice, // CORREÇÃO: Usar o preço total salvo
    }
    
    // Parse dos planos com preços corretos do banco de dados
    plans, err := h.parsePlansFromJSONWithPrices(master.PurchasedPlans, master.IsAnnually)
    if err != nil {
        log.Printf("Warning: Failed to parse plans JSON, using total from master account: %v", err)
        // Se falhar o parse, ainda assim usar o total salvo na master account
        plans = []models.Plan{
            {
                PlanID:   master.Plan,
                PlanName: "Subscription Plan",
                Price:    master.TotalPrice,
                Annually: master.IsAnnually,
            },
        }
    }
    
    checkoutData.Plans = plans
    
    log.Printf("Checkout data converted successfully. Final total: $%.2f, Plans count: %d", checkoutData.Total, len(checkoutData.Plans))
    return checkoutData, nil
}

// CORREÇÃO: Nova função que faz parse dos planos buscando preços atuais do banco de dados
func (h *UpdateCardHandler) parsePlansFromJSONWithPrices(plansJSON string, isAnnually int) ([]models.Plan, error) {
    var plans []models.Plan
    
    // Tentar fazer parse do JSON
    var rawPlans []map[string]interface{}
    if err := json.Unmarshal([]byte(plansJSON), &rawPlans); err != nil {
        return nil, fmt.Errorf("failed to unmarshal plans JSON: %v", err)
    }
    
    log.Printf("Parsing %d plans from JSON", len(rawPlans))
    
    for i, rawPlan := range rawPlans {
        plan := models.Plan{
            Annually: isAnnually,
        }
        
        // Extrair ID do plano
        if planID, ok := rawPlan["plan_id"].(float64); ok {
            plan.PlanID = int(planID)
        } else {
            log.Printf("Warning: Plan %d missing plan_id", i)
            continue
        }
        
        // Buscar preço atual do banco de dados
        var currentPrice float64
        var planName string
        err := h.db.GetDB().QueryRow(
            "SELECT name, price FROM plans WHERE id = ?", 
            plan.PlanID).Scan(&planName, &currentPrice)
        
        if err != nil {
            log.Printf("Warning: Failed to get current price for plan %d: %v", plan.PlanID, err)
            // Fallback: usar preço do JSON se disponível
            if price, ok := rawPlan["price"].(float64); ok {
                currentPrice = price
            } else {
                log.Printf("Warning: No price available for plan %d, skipping", plan.PlanID)
                continue
            }
        }
        
        plan.PlanName = planName
        plan.Price = currentPrice
        
        // Aplicar preço anual se necessário
        if isAnnually == 1 {
            plan.Price = currentPrice * 10 // 10 meses = desconto anual
        }
        
        plans = append(plans, plan)
        log.Printf("Plan %d: %s - $%.2f (annually: %d)", plan.PlanID, plan.PlanName, plan.Price, isAnnually)
    }
    
    if len(plans) == 0 {
        return nil, fmt.Errorf("no valid plans found in JSON")
    }
    
    return plans, nil
}

// updateAccountAfterCardUpdate atualiza os dados no banco após atualização do cartão
func (h *UpdateCardHandler) updateAccountAfterCardUpdate(master *models.MasterAccount, payment *models.PaymentRequest, transactionID string) error {
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    
    tx, err := h.db.BeginTransaction()
    if err != nil {
        return fmt.Errorf("failed to begin transaction: %v", err)
    }
    defer func() {
        if err != nil {
            tx.Rollback()
        }
    }()
    
    // 1. Atualizar método de pagamento
    cardData := &models.CardData{
        Number: payment.CardNumber,
        Expiry: payment.Expiry,
    }
    
    if err = tx.SavePaymentMethod(master.ReferenceUUID, cardData); err != nil {
        return fmt.Errorf("failed to update payment method: %v", err)
    }
    
    // 2. Atualizar status do usuário para ativo (is_active = 1)
    _, err = h.db.GetDB().ExecContext(ctx,
        "UPDATE users SET is_active = 1 WHERE email = ? AND username = ?",
        master.Email, master.Username)
    
    if err != nil {
        return fmt.Errorf("failed to reactivate user: %v", err)
    }
    
    // 3. Atualizar status das subscriptions para ativa
    _, err = h.db.GetDB().ExecContext(ctx,
        "UPDATE subscriptions SET status = 'active', updated_at = NOW() WHERE master_reference = ?",
        master.ReferenceUUID)
    
    if err != nil {
        return fmt.Errorf("failed to update subscription status: %v", err)
    }
    
    // 4. Registrar nova transação
    if err = tx.SaveTransaction(master.ReferenceUUID, "CARD_UPDATE", 1.00, "voided", transactionID); err != nil {
        return fmt.Errorf("failed to save transaction: %v", err)
    }
    
    // Commit da transação
    if err = tx.Commit(); err != nil {
        return fmt.Errorf("failed to commit transaction: %v", err)
    }
    
    log.Printf("Successfully updated account data for user: %s", master.Username)
    return nil
}

// sendCardUpdateConfirmationEmail envia email de confirmação da atualização
func (h *UpdateCardHandler) sendCardUpdateConfirmationEmail(email, name string) error {
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
        
        <p>Your subscription will continue as normal from your next billing cycle. You can now access all your ProSecureLSP services without interruption.</p>
        
        <p style="margin-top: 30px;">
            <a href="https://prosecurelsp.com/users/index.php" 
               style="background-color: #157347; color: white; padding: 12px 24px; text-decoration: none; border-radius: 5px; display: inline-block;">
                Access Your Account
            </a>
        </p>
        
        <p>If you have any questions or need assistance, please don't hesitate to contact our support team.</p>
        
        <p>Thank you for choosing ProSecureLSP!</p>
        
        <hr style="margin: 30px 0; border: none; border-top: 1px solid #eee;">
        <p style="font-size: 12px; color: #666;">
            © 2025 ProSecureLSP. All rights reserved.
        </p>
    </div>
</body>
</html>`, name)
    
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

func (h *UpdateCardHandler) CheckAccountStatus(w http.ResponseWriter, r *http.Request) {
	email := r.URL.Query().Get("email")
	username := r.URL.Query().Get("username")
	
	if email == "" || username == "" {
			h.sendErrorResponse(w, http.StatusBadRequest, "Email and username are required")
			return
	}

	// Verificar se a conta existe
	masterAccount, err := h.getMasterAccountData(email, username)
	if err != nil {
			h.sendErrorResponse(w, http.StatusNotFound, "Account not found")
			return
	}

	// Verificar status do usuário
	var isActive int
	var confirmationCode, lastLogin string
	var createdAt time.Time
	
	query := `
			SELECT is_active, COALESCE(confirmation_code, ''), 
						 COALESCE(last_login, ''), created_at
			FROM users 
			WHERE email = ? AND username = ? 
			LIMIT 1
	`
	
	err = h.db.GetDB().QueryRow(query, email, username).Scan(
			&isActive, &confirmationCode, &lastLogin, &createdAt)
	
	if err != nil {
			h.sendErrorResponse(w, http.StatusNotFound, "User not found")
			return
	}

	// Verificar status das subscriptions
	var subscriptionStatus string
	var nextBillingDate time.Time
	subscriptionQuery := `
			SELECT status, COALESCE(next_billing_date, '1970-01-01')
			FROM subscriptions 
			WHERE master_reference = ? 
			ORDER BY created_at DESC 
			LIMIT 1
	`
	
	err = h.db.GetDB().QueryRow(subscriptionQuery, masterAccount.ReferenceUUID).Scan(
			&subscriptionStatus, &nextBillingDate)
	
	if err != nil {
			// Se não houver subscription, ainda pode precisar de atualização
			subscriptionStatus = "none"
			nextBillingDate = time.Time{}
	}

	// Determinar o status da conta
	accountStatus := "active"
	needsCardUpdate := false
	statusMessage := "Account is active and working normally"
	
	switch isActive {
	case 0:
			accountStatus = "pending_activation"
			statusMessage = "Account created but email not confirmed yet"
	case 1:
			accountStatus = "active"
			statusMessage = "Account is active and working normally"
	case 9:
			accountStatus = "payment_error"
			needsCardUpdate = true
			statusMessage = "Payment method failed - card update required"
	default:
			accountStatus = "unknown"
			statusMessage = "Account status unknown"
	}

	// Se subscription está failed/cancelled, também pode precisar de atualização
	if subscriptionStatus == "failed" || subscriptionStatus == "cancelled" || subscriptionStatus == "suspended" {
			needsCardUpdate = true
			if accountStatus == "active" {
					accountStatus = "subscription_error"
					statusMessage = "Subscription payment failed - card update required"
			}
	}

	response := map[string]interface{}{
			"status":              "success",
			"account_status":      accountStatus,
			"needs_card_update":   needsCardUpdate,
			"message":            statusMessage,
			"account_details": map[string]interface{}{
					"email":             masterAccount.Email,
					"username":          masterAccount.Username,
					"name":              fmt.Sprintf("%s %s", masterAccount.Name, masterAccount.LastName),
					"is_active":         isActive,
					"subscription_status": subscriptionStatus,
					"next_billing_date": nextBillingDate.Format("2006-01-02"),
					"total_price":       masterAccount.TotalPrice,
					"is_annually":       masterAccount.IsAnnually == 1,
					"created_at":        createdAt.Format("2006-01-02 15:04:05"),
			},
	}

	// Adicionar informações sobre última tentativa de pagamento se houver erro
	if needsCardUpdate {
			var lastError string
			var errorDate time.Time
			
			errorQuery := `
					SELECT error_message, created_at 
					FROM payment_results 
					WHERE checkout_id = ? OR request_id LIKE CONCAT('%', ?, '%')
					ORDER BY created_at DESC 
					LIMIT 1
			`
			
			err = h.db.GetDB().QueryRow(errorQuery, masterAccount.ReferenceUUID, masterAccount.ReferenceUUID).Scan(&lastError, &errorDate)
			if err == nil {
					response["last_payment_error"] = map[string]interface{}{
							"error_message": lastError,
							"error_date":   errorDate.Format("2006-01-02 15:04:05"),
					}
			}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// GetCardUpdateHistory retorna o histórico de atualizações de cartão
func (h *UpdateCardHandler) GetCardUpdateHistory(w http.ResponseWriter, r *http.Request) {
	email := r.URL.Query().Get("email")
	username := r.URL.Query().Get("username")
	
	if email == "" || username == "" {
			h.sendErrorResponse(w, http.StatusBadRequest, "Email and username are required")
			return
	}

	// Verificar se a conta existe
	masterAccount, err := h.getMasterAccountData(email, username)
	if err != nil {
			h.sendErrorResponse(w, http.StatusNotFound, "Account not found")
			return
	}

	// Buscar histórico de transações relacionadas a updates de cartão
	query := `
			SELECT transaction_id, amount, status, created_at
			FROM transactions 
			WHERE master_reference = ? 
			AND (checkout_id = 'CARD_UPDATE' OR checkout_id LIKE '%update%')
			ORDER BY created_at DESC 
			LIMIT 10
	`
	
	rows, err := h.db.GetDB().Query(query, masterAccount.ReferenceUUID)
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

	// Buscar informações do cartão atual (mascarado)
	var currentCard string
	var cardExpiry string
	cardQuery := `
			SELECT card, expiry 
			FROM billing_infos 
			WHERE master_reference = ? 
			ORDER BY id DESC 
			LIMIT 1
	`
	
	err = h.db.GetDB().QueryRow(cardQuery, masterAccount.ReferenceUUID).Scan(&currentCard, &cardExpiry)
	if err != nil {
			// Se não houver cartão, definir valores vazios
			currentCard = "No card on file"
			cardExpiry = ""
	}

	response := map[string]interface{}{
			"status": "success",
			"account_info": map[string]interface{}{
					"email":    masterAccount.Email,
					"username": masterAccount.Username,
					"name":     fmt.Sprintf("%s %s", masterAccount.Name, masterAccount.LastName),
			},
			"current_card": map[string]interface{}{
					"masked_number": currentCard,
					"expiry":       cardExpiry,
			},
			"update_history": updates,
			"total_updates":  len(updates),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}