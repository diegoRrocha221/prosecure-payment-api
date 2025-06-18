// middleware/rate_limit.go
package middleware

import (
    "context"
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "strconv"
    "strings"
    "time"

    "github.com/go-redis/redis/v8"
    "prosecure-payment-api/models"
)

type RateLimiter struct {
    client *redis.Client
}

// RateLimitConfig representa a configuração de rate limiting
type RateLimitConfig struct {
    Requests int           // Número de requests permitidos
    Window   time.Duration // Janela de tempo
    Message  string        // Mensagem personalizada
}

// Configurações padrão para diferentes endpoints
var defaultConfigs = map[string]RateLimitConfig{
    "/auth/login": {
        Requests: 5,
        Window:   time.Minute * 15, // 5 tentativas por 15 minutos
        Message:  "Too many login attempts. Please try again in 15 minutes.",
    },
    "/auth/refresh": {
        Requests: 10,
        Window:   time.Minute * 5, // 10 refreshes por 5 minutos
        Message:  "Too many token refresh attempts. Please wait 5 minutes.",
    },
    "/protected/update-payment": {
        Requests: 3,
        Window:   time.Minute * 30, // 3 tentativas por 30 minutos
        Message:  "Too many payment update attempts. Please wait 30 minutes.",
    },
    "/internal/generate-token": {
        Requests: 100,
        Window:   time.Minute, // 100 tokens por minuto para integração PHP
        Message:  "Internal API rate limit exceeded.",
    },
    "default": {
        Requests: 60,
        Window:   time.Minute, // 60 requests por minuto como padrão
        Message:  "Rate limit exceeded. Please slow down your requests.",
    },
}

// NewRateLimiter cria um novo rate limiter
func NewRateLimiter(redisURL string) (*RateLimiter, error) {
    opt, err := redis.ParseURL(redisURL)
    if err != nil {
        return nil, fmt.Errorf("invalid Redis URL for rate limiter: %v", err)
    }

    client := redis.NewClient(opt)
    
    // Testar conexão
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    
    if err := client.Ping(ctx).Err(); err != nil {
        return nil, fmt.Errorf("failed to connect to Redis for rate limiting: %v", err)
    }

    return &RateLimiter{client: client}, nil
}

// RateLimitMiddleware retorna middleware de rate limiting
func (rl *RateLimiter) RateLimitMiddleware() func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            // Determinar configuração baseada no endpoint
            config := rl.getConfigForEndpoint(r.URL.Path)
            
            // Determinar chave para rate limiting
            key := rl.getRateLimitKey(r, config)
            
            // Verificar rate limit
            allowed, remaining, resetTime, err := rl.checkRateLimit(r.Context(), key, config)
            if err != nil {
                log.Printf("Rate limit check error: %v", err)
                // Em caso de erro, permitir o request mas logar
                next.ServeHTTP(w, r)
                return
            }

            // Adicionar headers de rate limit
            w.Header().Set("X-RateLimit-Limit", strconv.Itoa(config.Requests))
            w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
            w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetTime.Unix(), 10))

            if !allowed {
                log.Printf("Rate limit exceeded for key: %s, endpoint: %s", key, r.URL.Path)
                
                w.Header().Set("Content-Type", "application/json")
                w.Header().Set("Retry-After", strconv.FormatInt(int64(resetTime.Sub(time.Now()).Seconds()), 10))
                w.WriteHeader(http.StatusTooManyRequests)
                
                response := models.APIResponse{
                    Status:  "error",
                    Message: config.Message,
                }
                
                json.NewEncoder(w).Encode(response)
                return
            }

            next.ServeHTTP(w, r)
        })
    }
}

// getConfigForEndpoint retorna a configuração apropriada para o endpoint
func (rl *RateLimiter) getConfigForEndpoint(path string) RateLimitConfig {
    // Normalizar path removendo parâmetros de query
    if idx := strings.Index(path, "?"); idx != -1 {
        path = path[:idx]
    }

    // Verificar configurações específicas
    if config, exists := defaultConfigs[path]; exists {
        return config
    }

    // Verificar padrões
    if strings.HasPrefix(path, "/auth/") {
        return RateLimitConfig{
            Requests: 20,
            Window:   time.Minute * 5,
            Message:  "Too many authentication requests. Please wait 5 minutes.",
        }
    }

    if strings.HasPrefix(path, "/protected/") {
        return RateLimitConfig{
            Requests: 30,
            Window:   time.Minute * 2,
            Message:  "Too many requests to protected endpoints. Please slow down.",
        }
    }

    if strings.HasPrefix(path, "/internal/") {
        return RateLimitConfig{
            Requests: 200,
            Window:   time.Minute,
            Message:  "Internal API rate limit exceeded.",
        }
    }

    // Configuração padrão
    return defaultConfigs["default"]
}

// getRateLimitKey gera chave única para rate limiting
func (rl *RateLimiter) getRateLimitKey(r *http.Request, config RateLimitConfig) string {
    // Para endpoints críticos, usar IP + User-Agent para maior precisão
    ip := rl.getClientIP(r)
    userAgent := r.Header.Get("User-Agent")
    endpoint := r.URL.Path

    // Para endpoints de autenticação, incluir mais detalhes
    if strings.HasPrefix(endpoint, "/auth/") {
        // Hash do User-Agent para economizar espaço
        userAgentHash := fmt.Sprintf("%x", userAgent)[:8]
        return fmt.Sprintf("rate_limit:auth:%s:%s", ip, userAgentHash)
    }

    // Para endpoints protegidos, usar também o token do usuário se disponível
    if strings.HasPrefix(endpoint, "/protected/") {
        authHeader := r.Header.Get("Authorization")
        if authHeader != "" && len(authHeader) > 20 {
            // Usar parte do token para identificar usuário
            tokenPart := authHeader[len(authHeader)-10:]
            return fmt.Sprintf("rate_limit:protected:%s:%s", ip, tokenPart)
        }
    }

    // Para endpoints internos, usar secret ou IP
    if strings.HasPrefix(endpoint, "/internal/") {
        secret := r.Header.Get("X-Internal-Secret")
        if secret != "" && len(secret) > 10 {
            secretHash := fmt.Sprintf("%x", secret)[:8]
            return fmt.Sprintf("rate_limit:internal:%s", secretHash)
        }
    }

    // Chave padrão baseada em IP e endpoint
    return fmt.Sprintf("rate_limit:default:%s:%s", ip, endpoint)
}

// getClientIP extrai o IP real do cliente
func (rl *RateLimiter) getClientIP(r *http.Request) string {
    // Verificar headers de proxy
    if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
        // Pegar o primeiro IP da lista
        ips := strings.Split(ip, ",")
        return strings.TrimSpace(ips[0])
    }

    if ip := r.Header.Get("X-Real-IP"); ip != "" {
        return ip
    }

    if ip := r.Header.Get("CF-Connecting-IP"); ip != "" { // Cloudflare
        return ip
    }

    // Fallback para RemoteAddr
    ip := r.RemoteAddr
    if idx := strings.LastIndex(ip, ":"); idx != -1 {
        ip = ip[:idx]
    }
    return ip
}

// checkRateLimit verifica se o request está dentro do limite
func (rl *RateLimiter) checkRateLimit(ctx context.Context, key string, config RateLimitConfig) (allowed bool, remaining int, resetTime time.Time, err error) {
    now := time.Now()
    windowStart := now.Truncate(config.Window)
    windowEnd := windowStart.Add(config.Window)
    
    // Script Lua para operação atômica
    luaScript := `
        local key = KEYS[1]
        local window_start = ARGV[1]
        local window_end = ARGV[2]
        local limit = tonumber(ARGV[3])
        local current_time = ARGV[4]

        -- Limpar entradas antigas
        redis.call('ZREMRANGEBYSCORE', key, 0, window_start - 1)

        -- Contar requests atuais na janela
        local current_count = redis.call('ZCARD', key)

        if current_count < limit then
            -- Adicionar request atual
            redis.call('ZADD', key, current_time, current_time)
            redis.call('EXPIRE', key, 3600) -- TTL de 1 hora
            return {1, limit - current_count - 1} -- allowed, remaining
        else
            return {0, 0} -- not allowed, no remaining
        end
    `

    windowStartUnix := windowStart.Unix()
    windowEndUnix := windowEnd.Unix()
    nowUnix := now.Unix()

    result, err := rl.client.Eval(ctx, luaScript, []string{key}, 
        windowStartUnix, windowEndUnix, config.Requests, nowUnix).Result()
    
    if err != nil {
        return false, 0, time.Time{}, err
    }

    resultSlice, ok := result.([]interface{})
    if !ok || len(resultSlice) != 2 {
        return false, 0, time.Time{}, fmt.Errorf("unexpected redis result format")
    }

    allowedInt, ok1 := resultSlice[0].(int64)
    remainingInt, ok2 := resultSlice[1].(int64)
    
    if !ok1 || !ok2 {
        return false, 0, time.Time{}, fmt.Errorf("failed to parse redis result")
    }

    return allowedInt == 1, int(remainingInt), windowEnd, nil
}

// IPWhitelistMiddleware cria middleware de whitelist de IPs
func IPWhitelistMiddleware(allowedIPs []string) func(http.Handler) http.Handler {
    ipMap := make(map[string]bool)
    for _, ip := range allowedIPs {
        ipMap[ip] = true
    }

    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            clientIP := getClientIPFromRequest(r)
            
            if !ipMap[clientIP] {
                log.Printf("Access denied for IP: %s, endpoint: %s", clientIP, r.URL.Path)
                
                w.Header().Set("Content-Type", "application/json")
                w.WriteHeader(http.StatusForbidden)
                
                response := models.APIResponse{
                    Status:  "error",
                    Message: "Access denied from your IP address",
                }
                
                json.NewEncoder(w).Encode(response)
                return
            }

            next.ServeHTTP(w, r)
        })
    }
}

// SecurityHeadersMiddleware adiciona headers de segurança
func SecurityHeadersMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Adicionar headers de segurança
        w.Header().Set("X-Content-Type-Options", "nosniff")
        w.Header().Set("X-Frame-Options", "DENY")
        w.Header().Set("X-XSS-Protection", "1; mode=block")
        w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
        w.Header().Set("Content-Security-Policy", "default-src 'self'")
        
        // Para endpoints de API, adicionar headers específicos
        if strings.HasPrefix(r.URL.Path, "/api/") {
            w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
            w.Header().Set("Pragma", "no-cache")
            w.Header().Set("Expires", "0")
        }

        next.ServeHTTP(w, r)
    })
}

// Helper function para extrair IP (reutilizada)
func getClientIPFromRequest(r *http.Request) string {
    if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
        ips := strings.Split(ip, ",")
        return strings.TrimSpace(ips[0])
    }

    if ip := r.Header.Get("X-Real-IP"); ip != "" {
        return ip
    }

    if ip := r.Header.Get("CF-Connecting-IP"); ip != "" {
        return ip
    }

    ip := r.RemoteAddr
    if idx := strings.LastIndex(ip, ":"); idx != -1 {
        ip = ip[:idx]
    }
    return ip
}

// Close fecha a conexão Redis
func (rl *RateLimiter) Close() error {
    return rl.client.Close()
}