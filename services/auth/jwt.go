package auth

import (
    "crypto/sha256"
    "database/sql"
    "encoding/hex"
    "errors"
    "fmt"
    "time"

    "github.com/golang-jwt/jwt/v5"
    "prosecure-payment-api/database"
    "prosecure-payment-api/models"
)

const (
    AccessTokenDuration  = 15 * time.Minute  // Token de acesso expira em 15 minutos
    RefreshTokenDuration = 7 * 24 * time.Hour // Refresh token expira em 7 dias
)

var (
    ErrInvalidCredentials = errors.New("invalid username or password")
    ErrEmailNotConfirmed  = errors.New("email not confirmed")
    ErrUserInactive      = errors.New("user account inactive")
    ErrTokenExpired      = errors.New("token expired")
    ErrInvalidToken      = errors.New("invalid token")
)

type JWTService struct {
    secretKey []byte
    issuer    string
    db        *database.Connection
}

type Claims struct {
    Username    string `json:"username"`
    Email       string `json:"email"`
    IsMaster    bool   `json:"is_master"`
    IsActive    int    `json:"is_active"`
    AccountType string `json:"account_type"`
    MfaEnabled  bool   `json:"mfa_enabled"`
    TokenType   string `json:"token_type"` // "access" or "refresh"
    jwt.RegisteredClaims
}

func NewJWTService(secretKey, issuer string, db *database.Connection) *JWTService {
    return &JWTService{
        secretKey: []byte(secretKey),
        issuer:    issuer,
        db:        db,
    }
}

// CORRIGIDO: Authenticate agora busca payment_status e usa para determinar account_type
func (j *JWTService) Authenticate(username, password string) (*models.AuthResponse, error) {
    // Hash da senha usando SHA256 (compatível com o sistema PHP)
    hasher := sha256.New()
    hasher.Write([]byte(password))
    hashedPassword := hex.EncodeToString(hasher.Sum(nil))

    // CORRIGIDO: Buscar usuário no banco incluindo payment_status
    var emailConfirmed, isActive, isMaster int
    var email string
    var mfaEnabled bool
    var paymentStatus sql.NullInt32 // Usar NullInt32 para tratar casos onde payment_status é NULL

    query := `
        SELECT u.email, u.email_confirmed, u.is_active, u.is_master,
               COALESCE(ma.mfa_is_enable, 0) as mfa_enabled,
               u.payment_status
        FROM users u
        LEFT JOIN master_accounts ma ON u.username = ma.username
        WHERE u.username = ? AND u.passphrase = ?
    `

    err := j.db.GetDB().QueryRow(query, username, hashedPassword).Scan(
        &email, &emailConfirmed, &isActive, &isMaster, &mfaEnabled, &paymentStatus)

    if err != nil {
        if err == sql.ErrNoRows {
            return nil, ErrInvalidCredentials
        }
        return nil, fmt.Errorf("database error: %v", err)
    }

    // Verificar se email foi confirmado
    if emailConfirmed != 1 {
        return nil, ErrEmailNotConfirmed
    }

    // CORRIGIDO: Determinar tipo de conta baseado no payment_status se disponível
    var finalPaymentStatus int
    if paymentStatus.Valid {
        finalPaymentStatus = int(paymentStatus.Int32)
    } else {
        finalPaymentStatus = -1 // Valor padrão quando payment_status é NULL
    }

    accountType := j.determineAccountType(isActive, isMaster == 1, finalPaymentStatus)

    // Verificar se a conta está ativa (exceto para casos de payment_error)
    if accountType == "inactive" || accountType == "dea" {
        return nil, ErrUserInactive
    }

    // Criar usuário autenticado
    authUser := models.AuthUser{
        Username:    username,
        Email:       email,
        IsMaster:    isMaster == 1,
        IsActive:    isActive,
        AccountType: accountType,
        MfaEnabled:  mfaEnabled,
    }

    // Gerar tokens
    accessToken, err := j.GenerateToken(authUser, "access", AccessTokenDuration)
    if err != nil {
        return nil, fmt.Errorf("error generating access token: %v", err)
    }

    refreshToken, err := j.GenerateToken(authUser, "refresh", RefreshTokenDuration)
    if err != nil {
        return nil, fmt.Errorf("error generating refresh token: %v", err)
    }

    return &models.AuthResponse{
        Token:        accessToken,
        RefreshToken: refreshToken,
        ExpiresAt:    time.Now().Add(AccessTokenDuration),
        User:         authUser,
    }, nil
}

// GenerateToken gera um token JWT
func (j *JWTService) GenerateToken(user models.AuthUser, tokenType string, duration time.Duration) (string, error) {
    now := time.Now()
    claims := Claims{
        Username:    user.Username,
        Email:       user.Email,
        IsMaster:    user.IsMaster,
        IsActive:    user.IsActive,
        AccountType: user.AccountType,
        MfaEnabled:  user.MfaEnabled,
        TokenType:   tokenType,
        RegisteredClaims: jwt.RegisteredClaims{
            Subject:   user.Username,
            Issuer:    j.issuer,
            IssuedAt:  jwt.NewNumericDate(now),
            ExpiresAt: jwt.NewNumericDate(now.Add(duration)),
            NotBefore: jwt.NewNumericDate(now),
        },
    }

    token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
    return token.SignedString(j.secretKey)
}

// ValidateToken valida um token JWT e retorna as informações do usuário
func (j *JWTService) ValidateToken(tokenString string) (*models.AuthUser, error) {
    token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
        if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
            return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
        }
        return j.secretKey, nil
    })

    if err != nil {
        if errors.Is(err, jwt.ErrTokenExpired) {
            return nil, ErrTokenExpired
        }
        return nil, ErrInvalidToken
    }

    claims, ok := token.Claims.(*Claims)
    if !ok || !token.Valid {
        return nil, ErrInvalidToken
    }

    // Verificar se é um access token
    if claims.TokenType != "access" {
        return nil, ErrInvalidToken
    }

    return &models.AuthUser{
        Username:    claims.Username,
        Email:       claims.Email,
        IsMaster:    claims.IsMaster,
        IsActive:    claims.IsActive,
        AccountType: claims.AccountType,
        MfaEnabled:  claims.MfaEnabled,
    }, nil
}

// CORRIGIDO: RefreshToken agora busca payment_status atual
func (j *JWTService) RefreshToken(refreshTokenString string) (*models.AuthResponse, error) {
    token, err := jwt.ParseWithClaims(refreshTokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
        if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
            return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
        }
        return j.secretKey, nil
    })

    if err != nil {
        if errors.Is(err, jwt.ErrTokenExpired) {
            return nil, ErrTokenExpired
        }
        return nil, ErrInvalidToken
    }

    claims, ok := token.Claims.(*Claims)
    if !ok || !token.Valid {
        return nil, ErrInvalidToken
    }

    // Verificar se é um refresh token
    if claims.TokenType != "refresh" {
        return nil, ErrInvalidToken
    }

    // CORRIGIDO: Verificar se o usuário ainda existe e buscar payment_status atual
    var isActive, isMaster int
    var paymentStatus sql.NullInt32
    
    query := `SELECT is_active, is_master, payment_status FROM users WHERE username = ?`
    err = j.db.GetDB().QueryRow(query, claims.Username).Scan(&isActive, &isMaster, &paymentStatus)
    if err != nil {
        return nil, ErrInvalidCredentials
    }

    // Determinar account_type atualizado
    var finalPaymentStatus int
    if paymentStatus.Valid {
        finalPaymentStatus = int(paymentStatus.Int32)
    } else {
        finalPaymentStatus = -1
    }

    accountType := j.determineAccountType(isActive, isMaster == 1, finalPaymentStatus)

    // Atualizar informações do usuário
    user := models.AuthUser{
        Username:    claims.Username,
        Email:       claims.Email,
        IsMaster:    isMaster == 1,
        IsActive:    isActive,
        AccountType: accountType,
        MfaEnabled:  claims.MfaEnabled,
    }

    // Gerar novo access token
    accessToken, err := j.GenerateToken(user, "access", AccessTokenDuration)
    if err != nil {
        return nil, fmt.Errorf("error generating new access token: %v", err)
    }

    // Gerar novo refresh token
    newRefreshToken, err := j.GenerateToken(user, "refresh", RefreshTokenDuration)
    if err != nil {
        return nil, fmt.Errorf("error generating new refresh token: %v", err)
    }

    return &models.AuthResponse{
        Token:        accessToken,
        RefreshToken: newRefreshToken,
        ExpiresAt:    time.Now().Add(AccessTokenDuration),
        User:         user,
    }, nil
}

// CORRIGIDO: determineAccountType agora usa payment_status quando disponível
func (j *JWTService) determineAccountType(isActive int, isMaster bool, paymentStatus int) string {
    // Se payment_status está disponível e indica falha, usar payment_error
    if paymentStatus == int(models.PaymentStatusFailed) {
        return "payment_error"
    }
    
    // Caso contrário, usar is_active como antes
    switch isActive {
    case 1:
        if isMaster {
            return "master"
        }
        return "normal"
    case 2:
        return "dea"
    case 9:
        // Manter compatibilidade: se payment_status não está disponível mas is_active = 9
        if paymentStatus == -1 {
            return "payment_error"
        }
        // Se payment_status está disponível mas não é falha, usar is_active
        if isMaster {
            return "master"
        }
        return "normal"
    default:
        return "inactive"
    }
}

// GetPaymentErrorInfo obtém informações de erro de pagamento
func (j *JWTService) GetPaymentErrorInfo(username string) (*models.PaymentErrorInfo, error) {
    query := `
        SELECT u.username, u.email, ma.name, ma.lname, ma.total_price, ma.reference_uuid
        FROM users u
        JOIN master_accounts ma ON u.username = ma.username
        WHERE u.username = ? AND (u.payment_status = ? OR u.is_active = 9)
    `

    var info models.PaymentErrorInfo
    err := j.db.GetDB().QueryRow(query, username, models.PaymentStatusFailed).Scan(
        &info.Username, &info.Email, &info.Name, &info.LastName,
        &info.TotalPrice, &info.ReferenceUUID,
    )

    if err != nil {
        if err == sql.ErrNoRows {
            return nil, fmt.Errorf("no payment error info found for user: %s", username)
        }
        return nil, fmt.Errorf("database error: %v", err)
    }

    return &info, nil
}