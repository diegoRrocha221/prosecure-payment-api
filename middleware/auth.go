package middleware

import (
    "context"
    "log"
    "net/http"
    "strings"
    "time"

    "prosecure-payment-api/models"
    "prosecure-payment-api/services/auth"
    "prosecure-payment-api/utils"
)

type contextKey string

const UserContextKey contextKey = "user"

// AuthMiddleware verifica se o usuário está autenticado
func AuthMiddleware(jwtService *auth.JWTService) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            // Extrair token do header Authorization
            authHeader := r.Header.Get("Authorization")
            if authHeader == "" {
                log.Printf("Missing Authorization header from %s", r.RemoteAddr)
                utils.SendErrorResponse(w, http.StatusUnauthorized, "Missing authorization header")
                return
            }

            // Verificar formato "Bearer <token>"
            parts := strings.Split(authHeader, " ")
            if len(parts) != 2 || parts[0] != "Bearer" {
                log.Printf("Invalid Authorization header format from %s", r.RemoteAddr)
                utils.SendErrorResponse(w, http.StatusUnauthorized, "Invalid authorization header format")
                return
            }

            token := parts[1]

            // Validar token
            user, err := jwtService.ValidateToken(token)
            if err != nil {
                log.Printf("Token validation failed from %s: %v", r.RemoteAddr, err)
                
                var message string
                switch err {
                case auth.ErrTokenExpired:
                    message = "Token expired"
                case auth.ErrInvalidToken:
                    message = "Invalid token"
                default:
                    message = "Authentication failed"
                }
                
                utils.SendErrorResponse(w, http.StatusUnauthorized, message)
                return
            }

            // Adicionar usuário ao contexto
            ctx := context.WithValue(r.Context(), UserContextKey, user)
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}

// RequireMaster verifica se o usuário é uma conta master
func RequireMaster() func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            user := GetUserFromContext(r.Context())
            if user == nil {
                utils.SendErrorResponse(w, http.StatusInternalServerError, "User not found in context")
                return
            }

            if !user.IsMaster {
                log.Printf("Non-master user attempted to access master-only endpoint: %s", user.Username)
                utils.SendErrorResponse(w, http.StatusForbidden, "This endpoint requires a master account")
                return
            }

            next.ServeHTTP(w, r)
        })
    }
}

// RequireActiveAccount verifica se a conta está ativa
func RequireActiveAccount() func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            user := GetUserFromContext(r.Context())
            if user == nil {
                utils.SendErrorResponse(w, http.StatusInternalServerError, "User not found in context")
                return
            }

            if user.AccountType == "inactive" || user.AccountType == "dea" {
                log.Printf("Inactive user attempted to access protected endpoint: %s (type: %s)", 
                    user.Username, user.AccountType)
                utils.SendErrorResponse(w, http.StatusForbidden, "Account is inactive")
                return
            }

            next.ServeHTTP(w, r)
        })
    }
}

// AllowPaymentError permite acesso para contas com erro de pagamento (is_active = 9)
// Usado especificamente para endpoints de atualização de cartão
func AllowPaymentError() func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            user := GetUserFromContext(r.Context())
            if user == nil {
                utils.SendErrorResponse(w, http.StatusInternalServerError, "User not found in context")
                return
            }

            // Permitir acesso para contas normais, master e com erro de pagamento
            allowedTypes := []string{"normal", "master", "payment_error"}
            isAllowed := false
            for _, allowedType := range allowedTypes {
                if user.AccountType == allowedType {
                    isAllowed = true
                    break
                }
            }

            if !isAllowed {
                log.Printf("User with account type '%s' attempted to access protected endpoint: %s", 
                    user.AccountType, user.Username)
                utils.SendErrorResponse(w, http.StatusForbidden, "Account access denied")
                return
            }

            next.ServeHTTP(w, r)
        })
    }
}

// RequirePaymentError permite acesso APENAS para contas com erro de pagamento
func RequirePaymentError() func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            user := GetUserFromContext(r.Context())
            if user == nil {
                utils.SendErrorResponse(w, http.StatusInternalServerError, "User not found in context")
                return
            }

            if user.AccountType != "payment_error" {
                log.Printf("User without payment error attempted to access payment-error-only endpoint: %s (type: %s)", 
                    user.Username, user.AccountType)
                utils.SendErrorResponse(w, http.StatusForbidden, "This endpoint is only for accounts with payment issues")
                return
            }

            next.ServeHTTP(w, r)
        })
    }
}

// OptionalAuth middleware que permite acesso com ou sem autenticação
// Se autenticado, adiciona usuário ao contexto, senão continua sem usuário
func OptionalAuth(jwtService *auth.JWTService) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            authHeader := r.Header.Get("Authorization")
            if authHeader == "" {
                // Sem autenticação, continua sem usuário no contexto
                next.ServeHTTP(w, r)
                return
            }

            parts := strings.Split(authHeader, " ")
            if len(parts) != 2 || parts[0] != "Bearer" {
                // Token malformado, continua sem usuário
                next.ServeHTTP(w, r)
                return
            }

            token := parts[1]
            user, err := jwtService.ValidateToken(token)
            if err != nil {
                // Token inválido, continua sem usuário
                next.ServeHTTP(w, r)
                return
            }

            // Token válido, adiciona usuário ao contexto
            ctx := context.WithValue(r.Context(), UserContextKey, user)
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}

// GetUserFromContext extrai o usuário do contexto da requisição
func GetUserFromContext(ctx context.Context) *models.AuthUser {
    user, ok := ctx.Value(UserContextKey).(*models.AuthUser)
    if !ok {
        return nil
    }
    return user
}

// IsAuthenticated verifica se há um usuário no contexto
func IsAuthenticated(ctx context.Context) bool {
    return GetUserFromContext(ctx) != nil
}

// HasRole verifica se o usuário tem um determinado tipo de conta
func HasRole(ctx context.Context, accountType string) bool {
    user := GetUserFromContext(ctx)
    if user == nil {
        return false
    }
    return user.AccountType == accountType
}

// IsMaster verifica se o usuário é uma conta master
func IsMaster(ctx context.Context) bool {
    user := GetUserFromContext(ctx)
    if user == nil {
        return false
    }
    return user.IsMaster
}

// LoggingMiddleware para endpoints de autenticação
func AuthLoggingMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()
        
        // Criar wrapper para capturar status code
        wrapper := &responseWriter{ResponseWriter: w, status: http.StatusOK}
        
        next.ServeHTTP(wrapper, r)
        
        duration := time.Since(start)
        user := GetUserFromContext(r.Context())
        
        var username string
        if user != nil {
            username = user.Username
        } else {
            username = "anonymous"
        }
        
        log.Printf("AUTH %s %s %s %d %v %s", 
            r.Method, r.RequestURI, username, wrapper.status, duration, r.UserAgent())
    })
}

type responseWriter struct {
    http.ResponseWriter
    status int
}

func (rw *responseWriter) WriteHeader(code int) {
    rw.status = code
    rw.ResponseWriter.WriteHeader(code)
}