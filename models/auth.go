package models

import "time"

// AuthRequest representa uma requisição de autenticação
type AuthRequest struct {
    Username string `json:"username" binding:"required"`
    Password string `json:"password" binding:"required"`
}

// RefreshTokenRequest representa uma requisição de renovação de token
type RefreshTokenRequest struct {
    RefreshToken string `json:"refresh_token" binding:"required"`
}

// AuthUser representa um usuário autenticado
type AuthUser struct {
    Username    string `json:"username"`
    Email       string `json:"email"`
    IsMaster    bool   `json:"is_master"`
    IsActive    int    `json:"is_active"`
    AccountType string `json:"account_type"` // "master", "normal", "payment_error", "dea", "inactive"
    MfaEnabled  bool   `json:"mfa_enabled"`
}

// AuthResponse representa a resposta de autenticação
type AuthResponse struct {
    Token        string    `json:"token"`
    RefreshToken string    `json:"refresh_token"`
    ExpiresAt    time.Time `json:"expires_at"`
    User         AuthUser  `json:"user"`
}

// TokenValidationResponse representa a resposta de validação de token
type TokenValidationResponse struct {
    Valid bool     `json:"valid"`
    User  AuthUser `json:"user"`
}

// PaymentErrorInfo representa informações de erro de pagamento
type PaymentErrorInfo struct {
    Username      string  `json:"username"`
    Email         string  `json:"email"`
    Name          string  `json:"name"`
    LastName      string  `json:"last_name"`
    TotalPrice    float64 `json:"total_price"`
    ReferenceUUID string  `json:"reference_uuid"`
}

// ChangePasswordRequest representa uma requisição de mudança de senha
type ChangePasswordRequest struct {
    CurrentPassword string `json:"current_password" binding:"required"`
    NewPassword     string `json:"new_password" binding:"required"`
}

// UpdatePaymentRequest representa uma requisição de atualização de método de pagamento
type UpdatePaymentRequest struct {
    CardName   string `json:"card_name" binding:"required"`
    CardNumber string `json:"card_number" binding:"required"`
    Expiry     string `json:"expiry" binding:"required"`
    CVV        string `json:"cvv" binding:"required"`
}

// AddPlanRequest representa uma requisição para adicionar um plano
type AddPlanRequest struct {
    PlanID   int  `json:"plan_id" binding:"required"`
    Annually bool `json:"annually"`
}

// AccountDetailsResponse representa detalhes da conta
type AccountDetailsResponse struct {
    Username          string                 `json:"username"`
    Email             string                 `json:"email"`
    Name              string                 `json:"name"`
    PhoneNumber       string                 `json:"phone_number"`
    TotalPrice        float64                `json:"total_price"`
    IsAnnually        bool                   `json:"is_annually"`
    SimultaneousUsers int                    `json:"simultaneous_users"`
    RenewDate         string                 `json:"renew_date"`
    MaskedCard        string                 `json:"masked_card"`
    NextBilling       *string                `json:"next_billing"`
    Address           map[string]string      `json:"address"`
    Status            string                 `json:"status"`
}

// PaymentHistoryItem representa um item do histórico de pagamentos
type PaymentHistoryItem struct {
    TransactionID string    `json:"transaction_id"`
    Amount        float64   `json:"amount"`
    Status        string    `json:"status"`
    Date          time.Time `json:"date"`
}

// PaymentHistoryResponse representa o histórico de pagamentos
type PaymentHistoryResponse struct {
    Transactions []PaymentHistoryItem `json:"transactions"`
    TotalCount   int                  `json:"total_count"`
}