// handlers/dashboard_update_card.go
package handlers

import (
    "context"
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "time"
    
    "prosecure-payment-api/database"
    "prosecure-payment-api/middleware"
    "prosecure-payment-api/models"
    "prosecure-payment-api/services/payment"
    "prosecure-payment-api/services/email"
    "prosecure-payment-api/utils"
)

type DashboardUpdateCardHandler struct {
    db             *database.Connection
    paymentService *payment.Service
    emailService   *email.SMTPService
}

func NewDashboardUpdateCardHandler(db *database.Connection, ps *payment.Service, es *email.SMTPService) *DashboardUpdateCardHandler {
    return &DashboardUpdateCardHandler{
        db:             db,
        paymentService: ps,
        emailService:   es,
    }
}

// Tipos únicos para o dashboard handler
type DashboardUpdateCardRequest struct {
    CardName   string `json:"card_name" binding:"required"`
    CardNumber string `json:"card_number" binding:"required"`
    Expiry     string `json:"expiry" binding:"required"`
    CVV        string `json:"cvv" binding:"required"`
}

type DashboardUpdateCardResponse struct {
    Success    bool   `json:"success"`
    Message    string `json:"message"`
    UpdatedAt  string `json:"updated_at"`
    MaskedCard string `json:"masked_card"`
}

func (h *DashboardUpdateCardHandler) UpdateCard(w http.ResponseWriter, r *http.Request) {
    user := middleware.GetUserFromContext(r.Context())
    if user == nil {
        utils.SendErrorResponse(w, http.StatusInternalServerError, "User not found")
        return
    }

    // Apenas contas master ou com erro de pagamento podem atualizar cartão
    if !user.IsMaster && user.AccountType != "payment_error" {
        utils.SendErrorResponse(w, http.StatusForbidden, "Only master accounts or accounts with payment errors can update payment methods")
        return
    }

    var req DashboardUpdateCardRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        utils.SendErrorResponse(w, http.StatusBadRequest, "Invalid request body")
        return
    }

    // Validar dados obrigatórios
    if req.CardName == "" || req.CardNumber == "" || req.Expiry == "" || req.CVV == "" {
        utils.SendErrorResponse(w, http.StatusBadRequest, "All card fields are required")
        return
    }

    log.Printf("Processing card update for user: %s (account_type: %s)", user.Username, user.AccountType)

    // Buscar dados da conta
    masterAccount, err := h.getMasterAccountData(user.Username, user.Email)
    if err != nil {
        log.Printf("Error getting master account for %s: %v", user.Username, err)
        utils.SendErrorResponse(w, http.StatusNotFound, "Account not found")
        return
    }

    // Buscar Customer Profile
    customerProfile, err := h.db.GetCustomerProfile(masterAccount.ReferenceUUID)
    if err != nil {
        log.Printf("Error getting customer profile for %s: %v", masterAccount.ReferenceUUID, err)
        utils.SendErrorResponse(w, http.StatusNotFound, "Customer profile not found. Please contact support.")
        return
    }

    // Criar dados de checkout para update
    checkoutData := &models.CheckoutData{
        Name:        fmt.Sprintf("%s %s", masterAccount.Name, masterAccount.LastName),
        Email:       masterAccount.Email,
        PhoneNumber: masterAccount.PhoneNumber,
        Street:      masterAccount.Street,
        City:        masterAccount.City,
        State:       masterAccount.State,
        ZipCode:     masterAccount.ZipCode,
    }

    // Criar request de pagamento
    paymentReq := &models.PaymentRequest{
        CardName:   req.CardName,
        CardNumber: req.CardNumber,
        CVV:        req.CVV,
        Expiry:     req.Expiry,
    }

    // Validar cartão usando o service
    if !h.paymentService.ValidateCard(paymentReq) {
        utils.SendErrorResponse(w, http.StatusBadRequest, "Invalid card data. Please check card number, expiration date, and CVV.")
        return
    }

    // Tentar atualizar Customer Payment Profile na Authorize.net
    log.Printf("Updating customer payment profile: %s/%s", 
        customerProfile.AuthorizeCustomerProfileID, 
        customerProfile.AuthorizePaymentProfileID)
    
    err = h.paymentService.UpdateCustomerPaymentProfile(
        customerProfile.AuthorizeCustomerProfileID,
        customerProfile.AuthorizePaymentProfileID,
        paymentReq,
        checkoutData,
    )
    
    // Se o payment profile é inválido, criar um novo
    if err != nil && (contains(err.Error(), "Payment Profile ID is invalid") || contains(err.Error(), "payment profile not found")) {
        log.Printf("Payment profile invalid, creating new one for customer: %s", customerProfile.AuthorizeCustomerProfileID)
        
        newPaymentProfileID, createErr := h.paymentService.CreateCustomerPaymentProfile(
            customerProfile.AuthorizeCustomerProfileID,
            paymentReq,
            checkoutData,
        )
        
        if createErr != nil {
            log.Printf("Error creating new customer payment profile: %v", createErr)
            utils.SendErrorResponse(w, http.StatusPaymentRequired, fmt.Sprintf("Failed to create payment profile: %v", createErr))
            return
        }
        
        // Atualizar o payment profile ID no banco
        updateErr := h.updateCustomerPaymentProfileID(masterAccount.ReferenceUUID, newPaymentProfileID)
        if updateErr != nil {
            log.Printf("Warning: Failed to update payment profile ID in database: %v", updateErr)
            // Não falhar a operação, mas logar o erro
        }
        
        log.Printf("Successfully created new payment profile: %s", newPaymentProfileID)
        
    } else if err != nil {
        log.Printf("Error updating customer payment profile: %v", err)
        utils.SendErrorResponse(w, http.StatusPaymentRequired, fmt.Sprintf("Payment method update failed: %v", err))
        return
    }

    // Atualizar billing_infos no banco
    maskedCard, err := h.updateBillingInfo(masterAccount.ReferenceUUID, req.CardNumber, req.Expiry)
    if err != nil {
        log.Printf("Error updating billing info: %v", err)
        utils.SendErrorResponse(w, http.StatusInternalServerError, "Failed to update billing information")
        return
    }

    // Se o usuário tinha erro de pagamento, reativar a conta
    if user.AccountType == "payment_error" {
        err = h.reactivateAccount(user.Username, user.Email)
        if err != nil {
            log.Printf("Warning: Failed to reactivate account for %s: %v", user.Username, err)
            // Não falhar a operação, apenas logar
        } else {
            log.Printf("Account reactivated for user: %s", user.Username)
        }
    }

    // Enviar email de confirmação (assíncrono)
    go func() {
        emailErr := h.sendUpdateConfirmationEmail(masterAccount, maskedCard)
        if emailErr != nil {
            log.Printf("Warning: Failed to send confirmation email to %s: %v", masterAccount.Email, emailErr)
        } else {
            log.Printf("Confirmation email sent to: %s", masterAccount.Email)
        }
    }()

    log.Printf("Card update completed successfully for user: %s", user.Username)

    response := DashboardUpdateCardResponse{
        Success:    true,
        Message:    "Payment method updated successfully",
        UpdatedAt:  time.Now().Format("2006-01-02 15:04:05"),
        MaskedCard: maskedCard,
    }

    utils.SendSuccessResponse(w, models.APIResponse{
        Status:  "success",
        Message: "Payment method updated successfully",
        Data:    response,
    })
}

func (h *DashboardUpdateCardHandler) getMasterAccountData(username, email string) (*models.MasterAccount, error) {
    query := `
        SELECT reference_uuid, name, lname, email, username, phone_number,
               state, city, street, zip_code, additional_info
        FROM master_accounts 
        WHERE username = ? AND email = ?
    `

    var account models.MasterAccount
    err := h.db.GetDB().QueryRow(query, username, email).Scan(
        &account.ReferenceUUID, &account.Name, &account.LastName,
        &account.Email, &account.Username, &account.PhoneNumber,
        &account.State, &account.City, &account.Street,
        &account.ZipCode, &account.AdditionalInfo,
    )

    return &account, err
}

func (h *DashboardUpdateCardHandler) updateBillingInfo(masterRef, cardNumber, expiry string) (string, error) {
    // Mascarar cartão
    maskedCard := "XXXX XXXX XXXX " + cardNumber[len(cardNumber)-4:]
    
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    
    query := `
        UPDATE billing_infos 
        SET card = ?, expiry = ?, updated_at = NOW()
        WHERE master_reference = ?
    `
    
    _, err := h.db.GetDB().ExecContext(ctx, query, maskedCard, expiry, masterRef)
    return maskedCard, err
}

func (h *DashboardUpdateCardHandler) updateCustomerPaymentProfileID(masterRef, newPaymentProfileID string) error {
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    
    query := `
        UPDATE customer_profiles 
        SET authorize_payment_profile_id = ?, updated_at = NOW()
        WHERE master_reference = ?
    `
    
    _, err := h.db.GetDB().ExecContext(ctx, query, newPaymentProfileID, masterRef)
    return err
}

func (h *DashboardUpdateCardHandler) reactivateAccount(username, email string) error {
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    
    query := `
        UPDATE users 
        SET is_active = 1
        WHERE username = ? AND email = ?
    `
    
    result, err := h.db.GetDB().ExecContext(ctx, query, username, email)
    if err != nil {
        return fmt.Errorf("failed to reactivate account: %v", err)
    }
    
    rowsAffected, err := result.RowsAffected()
    if err != nil {
        return fmt.Errorf("failed to get rows affected: %v", err)
    }
    
    if rowsAffected == 0 {
        return fmt.Errorf("no user found to reactivate")
    }
    
    return nil
}

func (h *DashboardUpdateCardHandler) sendUpdateConfirmationEmail(account *models.MasterAccount, maskedCard string) error {
    subject := "Payment Method Updated - ProSecureLSP"
    body := fmt.Sprintf(`
        <div style="text-align: center; background-color: #2C3E50; padding: 50px;">
            <img src="https://www.prosecurelsp.com/images/logo.png" style="padding-bottom: 30px"/>
            <h1 style="color:#fff">Payment Method Updated Successfully</h1>
            <div style="color:#fff; font-size: 16px; line-height: 1.6; margin: 20px 0;">
                Hi %s,<br><br>
                Your payment method has been successfully updated in your ProSecureLSP account.<br><br>
                <strong>New Card:</strong> %s<br><br>
                If you did not make this change, please contact our support team immediately.
            </div>
            <a style="color:#fff; padding: 15px 30px; background-color: #28a745; text-decoration: none; border-radius: 5px; display: inline-block; margin-top: 20px;" 
               href="https://prosecurelsp.com/users">
                <strong>Login to Your Account</strong>
            </a>
        </div>
    `, account.Name, maskedCard)

    return h.emailService.SendEmail(account.Email, subject, body)
}

// Helper function to check if string contains substring
func contains(s, substr string) bool {
    return len(s) >= len(substr) && 
           (s == substr || 
            s[:len(substr)] == substr || 
            s[len(s)-len(substr):] == substr ||
            findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
    for i := 0; i <= len(s)-len(substr); i++ {
        if s[i:i+len(substr)] == substr {
            return true
        }
    }
    return false
}