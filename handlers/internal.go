// handlers/internal.go
package handlers

import (
    "encoding/json"
    "log"
    "net/http"
    "os"
    "time"

    "prosecure-payment-api/models"
    "prosecure-payment-api/services/auth"
    "prosecure-payment-api/utils"
)

type InternalHandler struct {
    jwtService     *auth.JWTService
    internalSecret string
}

// NewInternalHandler cria handler para endpoints internos (PHP integration)
func NewInternalHandler(jwtService *auth.JWTService) *InternalHandler {
    internalSecret := os.Getenv("INTERNAL_API_SECRET")
    if internalSecret == "" {
        internalSecret = "LSP0197O81r73a8Pd57c39ER3fu11cadSec4fb83d91" // Fallback para desenvolvimento
    }
    
    return &InternalHandler{
        jwtService:     jwtService,
        internalSecret: internalSecret,
    }
}

// RequireInternalSecret - Middleware para verificar secret interno (corrigido)
func (h *InternalHandler) RequireInternalSecret(next http.HandlerFunc) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        secret := r.Header.Get("X-Internal-Secret")
        if secret == "" || secret != h.internalSecret {
            log.Printf("Invalid or missing internal secret from %s", r.RemoteAddr)
            utils.SendErrorResponse(w, http.StatusUnauthorized, "Unauthorized")
            return
        }
        next.ServeHTTP(w, r)
    }
}

// GenerateTokenForUser gera token JWT para usuário (chamado pelo sistema PHP)
func (h *InternalHandler) GenerateTokenForUser(w http.ResponseWriter, r *http.Request) {
    var req struct {
        Username    string `json:"username" binding:"required"`
        Email       string `json:"email" binding:"required"`
        IsMaster    bool   `json:"is_master"`
        IsActive    int    `json:"is_active"`
        AccountType string `json:"account_type"`
        MfaEnabled  bool   `json:"mfa_enabled"`
    }

    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        log.Printf("Error decoding internal token generation request: %v", err)
        utils.SendErrorResponse(w, http.StatusBadRequest, "Invalid request body")
        return
    }

    // Validar campos obrigatórios
    if req.Username == "" || req.Email == "" {
        utils.SendErrorResponse(w, http.StatusBadRequest, "Username and email are required")
        return
    }

    // Determinar account type se não fornecido
    if req.AccountType == "" {
        req.AccountType = h.determineAccountType(req.IsMaster, req.IsActive)
    }

    log.Printf("Generating internal token for user: %s (type: %s)", req.Username, req.AccountType)

    // Criar usuário autenticado
    authUser := models.AuthUser{
        Username:    req.Username,
        Email:       req.Email,
        IsMaster:    req.IsMaster,
        IsActive:    req.IsActive,
        AccountType: req.AccountType,
        MfaEnabled:  req.MfaEnabled,
    }

    // Gerar tokens usando o serviço JWT
    accessToken, err := h.jwtService.GenerateToken(authUser, "access", auth.AccessTokenDuration)
    if err != nil {
        log.Printf("Error generating access token: %v", err)
        utils.SendErrorResponse(w, http.StatusInternalServerError, "Failed to generate access token")
        return
    }

    refreshToken, err := h.jwtService.GenerateToken(authUser, "refresh", auth.RefreshTokenDuration)
    if err != nil {
        log.Printf("Error generating refresh token: %v", err)
        utils.SendErrorResponse(w, http.StatusInternalServerError, "Failed to generate refresh token")
        return
    }

    authResponse := &models.AuthResponse{
        Token:        accessToken,
        RefreshToken: refreshToken,
        ExpiresAt:    time.Now().Add(auth.AccessTokenDuration),
        User:         authUser,
    }

    log.Printf("Successfully generated tokens for user: %s", req.Username)

    utils.SendSuccessResponse(w, models.APIResponse{
        Status:  "success",
        Message: "Tokens generated successfully",
        Data:    authResponse,
    })
}

// ValidateTokenInternal valida token JWT (chamado pelo sistema PHP)
func (h *InternalHandler) ValidateTokenInternal(w http.ResponseWriter, r *http.Request) {
    var req struct {
        Token string `json:"token" binding:"required"`
    }

    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        utils.SendErrorResponse(w, http.StatusBadRequest, "Invalid request body")
        return
    }

    if req.Token == "" {
        utils.SendErrorResponse(w, http.StatusBadRequest, "Token is required")
        return
    }

    // Validar token
    user, err := h.jwtService.ValidateToken(req.Token)
    if err != nil {
        var message string
        switch err {
        case auth.ErrTokenExpired:
            message = "Token expired"
        case auth.ErrInvalidToken:
            message = "Invalid token"
        default:
            message = "Token validation failed"
        }
        
        utils.SendErrorResponse(w, http.StatusUnauthorized, message)
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

// GetUserByToken obtém dados do usuário pelo token (chamado pelo sistema PHP)
func (h *InternalHandler) GetUserByToken(w http.ResponseWriter, r *http.Request) {
    token := r.Header.Get("Authorization")
    if token == "" {
        utils.SendErrorResponse(w, http.StatusBadRequest, "Authorization header required")
        return
    }

    // Remover "Bearer " se presente
    if len(token) > 7 && token[:7] == "Bearer " {
        token = token[7:]
    }

    user, err := h.jwtService.ValidateToken(token)
    if err != nil {
        utils.SendErrorResponse(w, http.StatusUnauthorized, "Invalid or expired token")
        return
    }

    utils.SendSuccessResponse(w, models.APIResponse{
        Status:  "success",
        Message: "User data retrieved",
        Data:    user,
    })
}

// RefreshTokenInternal renova token (chamado pelo sistema PHP)
func (h *InternalHandler) RefreshTokenInternal(w http.ResponseWriter, r *http.Request) {
    var req struct {
        RefreshToken string `json:"refresh_token" binding:"required"`
    }

    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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
        utils.SendErrorResponse(w, http.StatusUnauthorized, "Invalid or expired refresh token")
        return
    }

    utils.SendSuccessResponse(w, models.APIResponse{
        Status:  "success",
        Message: "Token refreshed successfully",
        Data:    authResponse,
    })
}

// Helper method para determinar tipo de conta
func (h *InternalHandler) determineAccountType(isMaster bool, isActive int) string {
    switch isActive {
    case 1:
        if isMaster {
            return "master"
        }
        return "normal"
    case 2:
        return "dea"
    case 9:
        return "payment_error"
    default:
        return "inactive"
    }
}

// Health check interno
func (h *InternalHandler) InternalHealthCheck(w http.ResponseWriter, r *http.Request) {
    utils.SendSuccessResponse(w, models.APIResponse{
        Status:  "success",
        Message: "Internal API is healthy",
        Data: map[string]interface{}{
            "timestamp": time.Now().Format(time.RFC3339),
            "service":   "prosecure-payment-api-internal",
        },
    })
}