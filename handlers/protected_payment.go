// handlers/protected_payment.go
package handlers

import (
    "context"
    "database/sql"
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "time"

    "prosecure-payment-api/database"
    "prosecure-payment-api/middleware"
    "prosecure-payment-api/models"
    "prosecure-payment-api/services/email"
    "prosecure-payment-api/services/payment"
    "prosecure-payment-api/types"
    "prosecure-payment-api/utils"
)

type ProtectedPaymentHandler struct {
    db             *database.Connection
    paymentService *payment.Service
    emailService   *email.SMTPService
}

func NewProtectedPaymentHandler(db *database.Connection, ps *payment.Service, es *email.SMTPService) *ProtectedPaymentHandler {
    return &ProtectedPaymentHandler{
        db:             db,
        paymentService: ps,
        emailService:   es,
    }
}

// UpdatePaymentMethod - Atualiza método de pagamento (versão autenticada)
func (h *ProtectedPaymentHandler) UpdatePaymentMethod(w http.ResponseWriter, r *http.Request) {
    user := middleware.GetUserFromContext(r.Context())
    if user == nil {
        utils.SendErrorResponse(w, http.StatusInternalServerError, "User not found in context")
        return
    }

    var req struct {
        CardName   string `json:"card_name" binding:"required"`
        CardNumber string `json:"card_number" binding:"required"`
        Expiry     string `json:"expiry" binding:"required"`
        CVV        string `json:"cvv" binding:"required"`
    }

    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        log.Printf("Error decoding update payment request: %v", err)
        utils.SendErrorResponse(w, http.StatusBadRequest, "Invalid request body")
        return
    }

    // Validar campos obrigatórios
    if req.CardName == "" || req.CardNumber == "" || req.Expiry == "" || req.CVV == "" {
        utils.SendErrorResponse(w, http.StatusBadRequest, "All card fields are required")
        return
    }

    log.Printf("Processing payment method update for user: %s", user.Username)

    // Buscar dados da conta master
    masterAccount, err := h.getMasterAccountByUser(user.Username, user.Email)
    if err != nil {
        log.Printf("Error getting master account for user %s: %v", user.Username, err)
        utils.SendErrorResponse(w, http.StatusNotFound, "Account not found")
        return
    }

    // Criar request de pagamento
    paymentReq := &models.PaymentRequest{
        CardName:      req.CardName,
        CardNumber:    req.CardNumber,
        Expiry:        req.Expiry,
        CVV:           req.CVV,
        CheckoutID:    masterAccount.ReferenceUUID,
        CustomerEmail: user.Email,
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

    // Validar cartão
    if !h.paymentService.ValidateCard(paymentReq) {
        utils.SendErrorResponse(w, http.StatusBadRequest, "Invalid card data")
        return
    }

    // Processar transação teste
    resp, err := h.paymentService.ProcessInitialAuthorization(paymentReq)
    if err != nil {
        log.Printf("Payment authorization failed for user %s: %v", user.Username, err)
        utils.SendErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("Payment authorization failed: %v", err))
        return
    }

    if !resp.Success {
        log.Printf("Payment declined for user %s: %s", user.Username, resp.Message)
        utils.SendErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("Payment declined: %s", resp.Message))
        return
    }

    transactionID := resp.TransactionID
    log.Printf("Payment authorized for user %s, transaction: %s", user.Username, transactionID)

    // Fazer void da transação teste
    if err := h.paymentService.VoidTransaction(transactionID); err != nil {
        log.Printf("Warning: Failed to void test transaction %s: %v", transactionID, err)
    }

    // Atualizar dados no banco
    err = h.updateAccountAfterCardUpdate(masterAccount, paymentReq, transactionID)
    if err != nil {
        log.Printf("Error updating account after card update: %v", err)
        utils.SendErrorResponse(w, http.StatusInternalServerError, "Failed to update account data")
        return
    }

    log.Printf("Payment method updated successfully for user: %s", user.Username)

    // Enviar email de confirmação
    go h.sendCardUpdateConfirmationEmail(user.Email, req.CardName)

    utils.SendSuccessResponse(w, models.APIResponse{
        Status:  "success",
        Message: "Payment method updated successfully",
        Data: map[string]interface{}{
            "transaction_id": transactionID,
            "updated_at":     time.Now().Format("2006-01-02 15:04:05"),
        },
    })
}

// AddPlan - Adiciona plano à conta (versão autenticada)
func (h *ProtectedPaymentHandler) AddPlan(w http.ResponseWriter, r *http.Request) {
    user := middleware.GetUserFromContext(r.Context())
    if user == nil {
        utils.SendErrorResponse(w, http.StatusInternalServerError, "User not found in context")
        return
    }

    // Verificar se é master account
    if !user.IsMaster {
        utils.SendErrorResponse(w, http.StatusForbidden, "Only master accounts can add plans")
        return
    }

    var req struct {
        PlanID   int  `json:"plan_id" binding:"required"`
        Annually bool `json:"annually"`
    }

    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        utils.SendErrorResponse(w, http.StatusBadRequest, "Invalid request body")
        return
    }

    if req.PlanID <= 0 {
        utils.SendErrorResponse(w, http.StatusBadRequest, "Valid plan ID is required")
        return
    }

    log.Printf("Processing plan addition for user: %s, plan: %d", user.Username, req.PlanID)

    // Verificar se o plano existe
    plan, err := h.db.GetPlanByID(req.PlanID)
    if err != nil {
        log.Printf("Plan not found: %v", err)
        utils.SendErrorResponse(w, http.StatusNotFound, "Plan not found")
        return
    }



    // Calcular preço
    planPrice := plan.Price
    if req.Annually {
        planPrice = plan.Price * 10 // Desconto anual
    }

    // TODO: Implementar lógica de cobrança do plano adicional
    // Por enquanto, simula sucesso
    log.Printf("Plan %d (%s) would be added for user %s at price $%.2f", 
        req.PlanID, plan.Name, user.Username, planPrice)

    utils.SendSuccessResponse(w, models.APIResponse{
        Status:  "success",
        Message: "Plan added successfully",
        Data: map[string]interface{}{
            "plan_id":    req.PlanID,
            "plan_name":  plan.Name,
            "price":      planPrice,
            "annually":   req.Annually,
            "added_at":   time.Now().Format("2006-01-02 15:04:05"),
        },
    })
}

func (h *ProtectedPaymentHandler) GetAccountDetails(w http.ResponseWriter, r *http.Request) {
    user := middleware.GetUserFromContext(r.Context())
    if user == nil {
        utils.SendErrorResponse(w, http.StatusInternalServerError, "User not found in context")
        return
    }

    // Buscar conta master se for master account
    if user.IsMaster {
        // CORREÇÃO: Removida declaração desnecessária de masterAccount que não estava sendo usada
        masterAccountData, err := h.getMasterAccountByUser(user.Username, user.Email)
        if err != nil {
            log.Printf("Error getting master account details: %v", err)
            utils.SendErrorResponse(w, http.StatusInternalServerError, "Error retrieving account details")
            return
        }

        // Buscar informações de pagamento (sem dados sensíveis)
        var maskedCard string
        var nextBillingDate sql.NullTime
        
        err = h.db.GetDB().QueryRow(`
            SELECT COALESCE(bi.card, 'No card on file') as card,
                   s.next_billing_date
            FROM master_accounts ma
            LEFT JOIN billing_infos bi ON ma.reference_uuid = bi.master_reference
            LEFT JOIN subscriptions s ON ma.reference_uuid = s.master_reference
            WHERE ma.username = ? AND ma.email = ?
        `, user.Username, user.Email).Scan(&maskedCard, &nextBillingDate)

        if err != nil && err != sql.ErrNoRows {
            log.Printf("Error getting payment details: %v", err)
        }

        accountDetails := map[string]interface{}{
            "username":          masterAccountData.Username,
            "email":             masterAccountData.Email,
            "name":              fmt.Sprintf("%s %s", masterAccountData.Name, masterAccountData.LastName),
            "phone_number":      masterAccountData.PhoneNumber,
            "total_price":       masterAccountData.TotalPrice,
            "is_annually":       masterAccountData.IsAnnually == 1,
            "simultaneous_users": masterAccountData.SimultaneousUsers,
            "renew_date":        masterAccountData.RenewDate.Format("2006-01-02"),
            "masked_card":       maskedCard,
            "next_billing":      nil,
            "address": map[string]string{
                "street":     masterAccountData.Street,
                "city":       masterAccountData.City,
                "state":      masterAccountData.State,
                "zip_code":   masterAccountData.ZipCode,
                "additional": masterAccountData.AdditionalInfo,
            },
        }

        if nextBillingDate.Valid {
            accountDetails["next_billing"] = nextBillingDate.Time.Format("2006-01-02")
        }

        utils.SendSuccessResponse(w, models.APIResponse{
            Status:  "success",
            Message: "Account details retrieved",
            Data:    accountDetails,
        })
    } else {
        // Para contas normais, retornar informações básicas
        utils.SendSuccessResponse(w, models.APIResponse{
            Status:  "success",
            Message: "Account details retrieved",
            Data: map[string]interface{}{
                "username":     user.Username,
                "email":        user.Email,
                "account_type": user.AccountType,
                "is_master":    user.IsMaster,
            },
        })
    }
}
// GetPaymentHistory - Retorna histórico de pagamentos
func (h *ProtectedPaymentHandler) GetPaymentHistory(w http.ResponseWriter, r *http.Request) {
    user := middleware.GetUserFromContext(r.Context())
    if user == nil {
        utils.SendErrorResponse(w, http.StatusInternalServerError, "User not found in context")
        return
    }

    // Buscar master reference
    var masterRef string
    err := h.db.GetDB().QueryRow(`
        SELECT reference_uuid FROM master_accounts 
        WHERE username = ? AND email = ?
    `, user.Username, user.Email).Scan(&masterRef)

    if err != nil {
        if err == sql.ErrNoRows {
            utils.SendErrorResponse(w, http.StatusNotFound, "Master account not found")
        } else {
            utils.SendErrorResponse(w, http.StatusInternalServerError, "Error retrieving account")
        }
        return
    }

    // Buscar histórico de transações
    rows, err := h.db.GetDB().Query(`
        SELECT transaction_id, amount, status, created_at
        FROM transactions 
        WHERE master_reference = ?
        ORDER BY created_at DESC 
        LIMIT 50
    `, masterRef)

    if err != nil {
        log.Printf("Error getting payment history: %v", err)
        utils.SendErrorResponse(w, http.StatusInternalServerError, "Error retrieving payment history")
        return
    }
    defer rows.Close()

    var transactions []map[string]interface{}
    for rows.Next() {
        var transactionID, status string
        var amount float64
        var createdAt time.Time

        err := rows.Scan(&transactionID, &amount, &status, &createdAt)
        if err != nil {
            continue
        }

        transactions = append(transactions, map[string]interface{}{
            "transaction_id": transactionID,
            "amount":        amount,
            "status":        status,
            "date":          createdAt.Format("2006-01-02 15:04:05"),
        })
    }

    utils.SendSuccessResponse(w, models.APIResponse{
        Status:  "success",
        Message: "Payment history retrieved",
        Data: map[string]interface{}{
            "transactions": transactions,
            "total_count":  len(transactions),
        },
    })
}

// Helper methods

func (h *ProtectedPaymentHandler) getMasterAccountByUser(username, email string) (*models.MasterAccount, error) {
    query := `
        SELECT reference_uuid, name, lname, email, username, phone_number,
               state, city, street, zip_code, additional_info, total_price,
               is_annually, plan, purchased_plans, simultaneus_users, renew_date
        FROM master_accounts 
        WHERE username = ? AND email = ?
    `

    var account models.MasterAccount
    err := h.db.GetDB().QueryRow(query, username, email).Scan(
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
        &account.SimultaneousUsers,
        &account.RenewDate,
    )

    return &account, err
}

func (h *ProtectedPaymentHandler) updateAccountAfterCardUpdate(master *models.MasterAccount, payment *models.PaymentRequest, transactionID string) error {
    tx, err := h.db.BeginTransaction()
    if err != nil {
        return fmt.Errorf("failed to begin transaction: %v", err)
    }
    defer func() {
        if err != nil {
            tx.Rollback()
        }
    }()

    // Atualizar método de pagamento
    cardData := &models.CardData{
        Number: payment.CardNumber,
        Expiry: payment.Expiry,
    }

    if err = tx.SavePaymentMethod(master.ReferenceUUID, cardData); err != nil {
        return fmt.Errorf("failed to update payment method: %v", err)
    }

    // Reativar usuário se estava inativo por pagamento
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()

    _, err = h.db.GetDB().ExecContext(ctx,
        "UPDATE users SET is_active = 1 WHERE email = ? AND username = ? AND is_active = 9",
        master.Email, master.Username)

    if err != nil {
        return fmt.Errorf("failed to reactivate user: %v", err)
    }

    // Registrar transação
    if err = tx.SaveTransaction(master.ReferenceUUID, "CARD_UPDATE", 1.00, "voided", transactionID); err != nil {
        return fmt.Errorf("failed to save transaction: %v", err)
    }

    return tx.Commit()
}

func (h *ProtectedPaymentHandler) sendCardUpdateConfirmationEmail(email, cardName string) {
    subject := "Payment Method Updated Successfully"
    content := fmt.Sprintf(`
        <h2>Payment Method Updated</h2>
        <p>Your payment method has been updated successfully.</p>
        <p>Cardholder: %s</p>
        <p>Your recurring billing will continue with the new payment method.</p>
    `, cardName)

    err := h.emailService.SendEmail(email, subject, content)
    if err != nil {
        log.Printf("Failed to send card update confirmation email: %v", err)
    }
}