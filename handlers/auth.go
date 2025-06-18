package handlers

import (
    "encoding/json"
    "log"
    "net/http"

    "prosecure-payment-api/middleware"
    "prosecure-payment-api/models"
    "prosecure-payment-api/services/auth"
    "prosecure-payment-api/utils"
)

type AuthHandler struct {
    jwtService *auth.JWTService
}

// NewAuthHandler cria um novo handler de autenticação
func NewAuthHandler(jwtService *auth.JWTService) *AuthHandler {
    return &AuthHandler{
        jwtService: jwtService,
    }
}

// Login autentica um usuário e retorna tokens JWT
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
    var req models.AuthRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        log.Printf("Error decoding login request: %v", err)
        utils.SendErrorResponse(w, http.StatusBadRequest, "Invalid request body")
        return
    }

    // Validar campos obrigatórios
    if req.Username == "" || req.Password == "" {
        utils.SendErrorResponse(w, http.StatusBadRequest, "Username and password are required")
        return
    }

    log.Printf("Login attempt for user: %s", req.Username)

    // Autenticar usuário
    authResponse, err := h.jwtService.Authenticate(req.Username, req.Password)
    if err != nil {
        log.Printf("Authentication failed for user %s: %v", req.Username, err)
        
        var message string
        var statusCode int
        
        switch err {
        case auth.ErrInvalidCredentials:
            message = "Invalid username or password"
            statusCode = http.StatusUnauthorized
        case auth.ErrEmailNotConfirmed:
            message = "Please confirm your email address before logging in"
            statusCode = http.StatusForbidden
        case auth.ErrUserInactive:
            message = "Account is inactive"
            statusCode = http.StatusForbidden
        default:
            message = "Authentication failed"
            statusCode = http.StatusInternalServerError
        }
        
        utils.SendErrorResponse(w, statusCode, message)
        return
    }

    log.Printf("Login successful for user: %s (type: %s)", req.Username, authResponse.User.AccountType)

    // Resposta de sucesso
    utils.SendSuccessResponse(w, models.APIResponse{
        Status:  "success",
        Message: "Authentication successful",
        Data:    authResponse,
    })
}

// RefreshToken renova um access token
func (h *AuthHandler) RefreshToken(w http.ResponseWriter, r *http.Request) {
    var req models.RefreshTokenRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        log.Printf("Error decoding refresh token request: %v", err)
        utils.SendErrorResponse(w, http.StatusBadRequest, "Invalid request body")
        return
    }

    if req.RefreshToken == "" {
        utils.SendErrorResponse(w, http.StatusBadRequest, "Refresh token is required")
        return
    }

    // Renovar token
    authResponse, err := h.jwtService.RefreshToken(req.RefreshToken)
    if err != nil {
        log.Printf("Token refresh failed: %v", err)
        utils.SendErrorResponse(w, http.StatusUnauthorized, "Invalid or expired refresh token")
        return
    }

    log.Printf("Token refreshed for user: %s", authResponse.User.Username)

    utils.SendSuccessResponse(w, models.APIResponse{
        Status:  "success",
        Message: "Token refreshed successfully",
        Data:    authResponse,
    })
}

// ValidateToken verifica se um token é válido
func (h *AuthHandler) ValidateToken(w http.ResponseWriter, r *http.Request) {
    // O middleware já validou o token, então apenas retornamos as informações do usuário
    user := middleware.GetUserFromContext(r.Context())
    if user == nil {
        utils.SendErrorResponse(w, http.StatusInternalServerError, "User not found in context")
        return
    }

    response := models.TokenValidationResponse{
        Valid: true,
        User:  *user,
    }

    utils.SendSuccessResponse(w, models.APIResponse{
        Status:  "success",
        Message: "Token is valid",
        Data:    response,
    })
}

// GetUserInfo retorna informações do usuário autenticado
func (h *AuthHandler) GetUserInfo(w http.ResponseWriter, r *http.Request) {
    user := middleware.GetUserFromContext(r.Context())
    if user == nil {
        utils.SendErrorResponse(w, http.StatusInternalServerError, "User not found in context")
        return
    }

    utils.SendSuccessResponse(w, models.APIResponse{
        Status:  "success",
        Message: "User information retrieved",
        Data:    user,
    })
}

// Logout invalida o token (lado cliente deve remover o token)
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
    user := middleware.GetUserFromContext(r.Context())
    if user != nil {
        log.Printf("User logged out: %s", user.Username)
    }

    utils.SendSuccessResponse(w, models.APIResponse{
        Status:  "success",
        Message: "Logged out successfully",
    })
}

// ChangePassword permite alterar a senha do usuário
func (h *AuthHandler) ChangePassword(w http.ResponseWriter, r *http.Request) {
    user := middleware.GetUserFromContext(r.Context())
    if user == nil {
        utils.SendErrorResponse(w, http.StatusInternalServerError, "User not found in context")
        return
    }

    var req struct {
        CurrentPassword string `json:"current_password" binding:"required"`
        NewPassword     string `json:"new_password" binding:"required"`
    }

    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        utils.SendErrorResponse(w, http.StatusBadRequest, "Invalid request body")
        return
    }

    if req.CurrentPassword == "" || req.NewPassword == "" {
        utils.SendErrorResponse(w, http.StatusBadRequest, "Current password and new password are required")
        return
    }

    if len(req.NewPassword) < 8 {
        utils.SendErrorResponse(w, http.StatusBadRequest, "New password must be at least 8 characters long")
        return
    }

    // Verificar senha atual
    _, err := h.jwtService.Authenticate(user.Username, req.CurrentPassword)
    if err != nil {
        utils.SendErrorResponse(w, http.StatusUnauthorized, "Current password is incorrect")
        return
    }

    // TODO: Implementar atualização de senha no banco de dados
    // Por enquanto, retorna sucesso simulado
    log.Printf("Password change requested for user: %s", user.Username)

    utils.SendSuccessResponse(w, models.APIResponse{
        Status:  "success",
        Message: "Password changed successfully",
    })
}

// GetAccountStatus retorna status detalhado da conta
func (h *AuthHandler) GetAccountStatus(w http.ResponseWriter, r *http.Request) {
    user := middleware.GetUserFromContext(r.Context())
    if user == nil {
        utils.SendErrorResponse(w, http.StatusInternalServerError, "User not found in context")
        return
    }

    // Buscar informações adicionais da conta se necessário
    accountInfo := map[string]interface{}{
        "username":     user.Username,
        "email":        user.Email,
        "account_type": user.AccountType,
        "is_master":    user.IsMaster,
        "is_active":    user.IsActive,
        "mfa_enabled":  user.MfaEnabled,
        "status":       getStatusDescription(user.AccountType, user.IsActive),
    }

    utils.SendSuccessResponse(w, models.APIResponse{
        Status:  "success",
        Message: "Account status retrieved",
        Data:    accountInfo,
    })
}

// getStatusDescription retorna descrição amigável do status
func getStatusDescription(accountType string, isActive int) string {
    switch accountType {
    case "master":
        return "Master Account - Active"
    case "normal":
        return "Normal Account - Active"
    case "payment_error":
        return "Payment Issue - Update Payment Method Required"
    case "dea":
        return "Account Deactivated"
    case "inactive":
        return "Account Inactive"
    default:
        return "Unknown Status"
    }
}