// main.go (versão atualizada com autenticação)
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "os"
    "os/signal"
    "runtime"
    "syscall"
    "time"
    
    _ "github.com/go-sql-driver/mysql"
    "github.com/gorilla/mux"
    
    "prosecure-payment-api/config"
    "prosecure-payment-api/database"
    "prosecure-payment-api/handlers"
    "prosecure-payment-api/middleware"
    "prosecure-payment-api/queue"
    "prosecure-payment-api/services/auth"
    "prosecure-payment-api/services/email"
    "prosecure-payment-api/services/payment"
    "prosecure-payment-api/worker"
)

func timeoutMiddleware(timeout time.Duration) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            ctx, cancel := context.WithTimeout(r.Context(), timeout)
            defer cancel()
            
            r = r.WithContext(ctx)
            
            done := make(chan struct{})
            go func() {
                defer close(done)
                next.ServeHTTP(w, r)
            }()
            
            select {
            case <-done:
                // Request completed normally
            case <-ctx.Done():
                // Request timed out
                log.Printf("Request timeout: %s %s", r.Method, r.URL.Path)
                if ctx.Err() == context.DeadlineExceeded {
                    http.Error(w, "Request timeout", http.StatusRequestTimeout)
                }
            }
        })
    }
}

func corsMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        origin := r.Header.Get("Origin")
        
        // Lista de origens permitidas (incluindo domínios de autenticação)
        allowedOrigins := []string{
            "https://prosecurelsp.com",
            "https://www.prosecurelsp.com",
            "http://localhost:3000", // Para desenvolvimento
        }
        
        originAllowed := false
        for _, allowedOrigin := range allowedOrigins {
            if origin == allowedOrigin {
                originAllowed = true
                break
            }
        }
        
        if originAllowed {
            w.Header().Set("Access-Control-Allow-Origin", origin)
        } else {
            w.Header().Set("Access-Control-Allow-Origin", "*")
        }
        
        w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE")
        w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, Authorization, h-captcha-response")
        w.Header().Set("Access-Control-Allow-Credentials", "true")
        
        if r.Method == "OPTIONS" {
            w.WriteHeader(http.StatusOK)
            return
        }
        next.ServeHTTP(w, r)
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

func loggingMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()

        wrapper := &responseWriter{ResponseWriter: w, status: http.StatusOK}
        next.ServeHTTP(wrapper, r)

        elapsed := time.Since(start)
        
        // Log mais detalhado para operações de autenticação e pagamento
        if r.URL.Path == "/api/auth/login" || r.URL.Path == "/api/protected/update-payment" || 
           elapsed > 500*time.Millisecond || wrapper.status >= 400 {
            log.Printf(
                "%s %s %s %d %v %s",
                r.Method,
                r.RequestURI,
                r.RemoteAddr,
                wrapper.status,
                elapsed,
                r.UserAgent(),
            )
        }
    })
}

func main() {
    log.SetFlags(log.LstdFlags | log.Lshortfile | log.Lmicroseconds | log.LUTC)
    
    numCPU := runtime.NumCPU()
    runtime.GOMAXPROCS(numCPU)
    log.Printf("Server starting with %d CPUs available", numCPU)

    // Carregar configurações
    cfg := config.Load()
    log.Printf("Configuration loaded successfully")

    // Conectar ao banco de dados
    var db *database.Connection
    var err error
    for retries := 0; retries < 5; retries++ {
        db, err = database.NewConnection(cfg.Database)
        if err == nil {
            break
        }
        retryDelay := time.Duration(retries+1) * time.Second
        log.Printf("Failed to connect to database (attempt %d/5): %v. Retrying in %v...", 
            retries+1, err, retryDelay)
        time.Sleep(retryDelay)
    }
    
    if err != nil {
        log.Fatalf("Failed to connect to database after retries: %v", err)
    }
    defer db.Close()

    // Verificar conexão com o banco
    ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
    defer cancel()
    
    if err := db.GetDB().PingContext(ctx); err != nil {
        log.Fatalf("Failed to ping database: %v", err)
    }
    log.Println("Successfully connected to database")

    // Inicializar fila Redis
    jobQueue, err := queue.NewQueue(cfg.Redis.URL, "payment_jobs")
    if err != nil {
        log.Fatalf("Failed to connect to Redis: %v", err)
    }
    defer jobQueue.Close()
    log.Println("Successfully connected to Redis")

    // Inicializar serviços
    paymentService := payment.NewPaymentService(
        cfg.AuthNet.APILoginID,
        cfg.AuthNet.TransactionKey,
        cfg.AuthNet.MerchantID,
        cfg.AuthNet.Environment,
    )
    emailService := email.NewSMTPService(cfg.SMTP)

    // NOVO: Inicializar serviço JWT
    jwtSecret := os.Getenv("JWT_SECRET")
    if jwtSecret == "" {
        log.Fatal("JWT_SECRET environment variable is required")
    }
    
    jwtService := auth.NewJWTService(jwtSecret, "prosecure-payment-api", db)
    log.Println("JWT service initialized")

    // Iniciar worker
    workerConcurrency := cfg.Redis.WorkerConcurrency
    if workerConcurrency < 2 {
        workerConcurrency = 2
    } else if workerConcurrency > 8 {
        workerConcurrency = 8
    }
    
    paymentWorker := worker.NewWorker(jobQueue, db, paymentService, emailService)
    paymentWorker.Start(workerConcurrency)
    defer paymentWorker.Stop()
    log.Printf("Started payment worker with %d threads", workerConcurrency)

    // Inicializar handlers
    var paymentHandler *handlers.PaymentHandler
    for retries := 0; retries < 3; retries++ {
        paymentHandler, err = handlers.NewPaymentHandler(db, paymentService, emailService, jobQueue)
        if err == nil {
            break
        }
        log.Printf("Failed to initialize payment handler (attempt %d/3): %v", retries+1, err)
        time.Sleep(time.Second * time.Duration(retries+1))
    }
    if err != nil {
        log.Fatalf("Failed to initialize payment handler after retries: %v", err)
    }

    // Inicializar handlers
    webhookHandler := handlers.NewWebhookHandler(db, jobQueue, paymentService)
    planHandler := handlers.NewPlanHandler(db)
    cartHandler := handlers.NewCartHandler(db, cfg)
    checkoutHandler := handlers.NewCheckoutHandler(db)
    linkAccountHandler := handlers.NewLinkAccountHandler(db, cfg)
    updateCardHandler := handlers.NewUpdateCardHandler(db, paymentService, emailService)
    
    // NOVO: Handlers de autenticação
    authHandler := handlers.NewAuthHandler(jwtService)
    protectedPaymentHandler := handlers.NewProtectedPaymentHandler(db, paymentService, emailService)
    internalHandler := handlers.NewInternalHandler(jwtService)

    // Configurar router
    router := mux.NewRouter()
    
    // Middlewares globais
    router.Use(corsMiddleware)
    router.Use(loggingMiddleware)
    
    api := router.PathPrefix("/api").Subrouter()

    // ===========================================
    // ROTAS DE AUTENTICAÇÃO (SEM PROTEÇÃO)
    // ===========================================
    authRouter := api.PathPrefix("/auth").Subrouter()
    authRouter.Use(timeoutMiddleware(30 * time.Second))
    
    authRouter.HandleFunc("/login", authHandler.Login).Methods("POST", "OPTIONS")
    authRouter.HandleFunc("/refresh", authHandler.RefreshToken).Methods("POST", "OPTIONS")
    
    // Rotas de validação (com autenticação)
    authProtectedRouter := authRouter.PathPrefix("").Subrouter()
    authProtectedRouter.Use(middleware.AuthMiddleware(jwtService))
    authProtectedRouter.HandleFunc("/validate", authHandler.ValidateToken).Methods("GET", "OPTIONS")
    authProtectedRouter.HandleFunc("/user", authHandler.GetUserInfo).Methods("GET", "OPTIONS")
    authProtectedRouter.HandleFunc("/logout", authHandler.Logout).Methods("POST", "OPTIONS")
    authProtectedRouter.HandleFunc("/status", authHandler.GetAccountStatus).Methods("GET", "OPTIONS")
    authProtectedRouter.HandleFunc("/change-password", authHandler.ChangePassword).Methods("POST", "OPTIONS")


    // ===========================================
    // ROTAS INTERNAS (PARA INTEGRAÇÃO PHP)
    // ===========================================
    internalRouter := api.PathPrefix("/internal").Subrouter()
    internalRouter.Use(timeoutMiddleware(15 * time.Second))
    
    // Endpoints internos (requer secret) - CORRIGIDO
    internalRouter.HandleFunc("/generate-token", internalHandler.RequireInternalSecret(internalHandler.GenerateTokenForUser)).Methods("POST", "OPTIONS")
    internalRouter.HandleFunc("/validate-token", internalHandler.RequireInternalSecret(internalHandler.ValidateTokenInternal)).Methods("POST", "OPTIONS")
    internalRouter.HandleFunc("/refresh-token", internalHandler.RequireInternalSecret(internalHandler.RefreshTokenInternal)).Methods("POST", "OPTIONS")
    internalRouter.HandleFunc("/user-by-token", internalHandler.RequireInternalSecret(internalHandler.GetUserByToken)).Methods("GET", "OPTIONS")
    internalRouter.HandleFunc("/health", internalHandler.RequireInternalSecret(internalHandler.InternalHealthCheck)).Methods("GET", "OPTIONS")
    // ===========================================
    // ROTAS PROTEGIDAS (COM AUTENTICAÇÃO)
    // ===========================================
    protectedRouter := api.PathPrefix("/protected").Subrouter()
    protectedRouter.Use(timeoutMiddleware(60 * time.Second))
    protectedRouter.Use(middleware.AuthMiddleware(jwtService))
    protectedRouter.Use(middleware.AllowPaymentError()) // Permite payment_error para update de cartão

    addPlansHandler := handlers.NewAddPlansHandler(db, paymentService)
    addPlansProtectedPaymentHandler := handlers.NewAddPlansProtectedPaymentHandler(db)
    
    protectedRouter.HandleFunc("/add-plans", addPlansHandler.AddPlans).Methods("POST", "OPTIONS")
    protectedRouter.HandleFunc("/preview-add-plans", addPlansHandler.PreviewAddPlans).Methods("POST", "OPTIONS")
    protectedRouter.HandleFunc("/card-info", addPlansProtectedPaymentHandler.GetCardInfo).Methods("GET", "OPTIONS") // NOVA ROTA
    protectedRouter.HandleFunc("/update-payment", protectedPaymentHandler.UpdatePaymentMethod).Methods("POST", "OPTIONS")
    protectedRouter.HandleFunc("/account", protectedPaymentHandler.GetAccountDetails).Methods("GET", "OPTIONS")
    protectedRouter.HandleFunc("/payment-history", protectedPaymentHandler.GetPaymentHistory).Methods("GET", "OPTIONS")
    
    // Endpoints que requerem conta master
    masterOnlyRouter := protectedRouter.PathPrefix("").Subrouter()
    masterOnlyRouter.Use(middleware.RequireMaster())
    masterOnlyRouter.HandleFunc("/add-plan", protectedPaymentHandler.AddPlan).Methods("POST", "OPTIONS")

    // ===========================================
    // ROTAS PÚBLICAS (PARA CHECKOUT E WEBHOOKS)
    // ===========================================
    
    // Update card público (mantido para compatibilidade)
    publicUpdateCardRouter := api.PathPrefix("/update-card").Subrouter()
    publicUpdateCardRouter.Use(timeoutMiddleware(45 * time.Second))
    publicUpdateCardRouter.HandleFunc("", updateCardHandler.UpdateCard).Methods("POST", "OPTIONS")
    
    // Status endpoints públicos
    statusRouter := api.PathPrefix("").Subrouter()
    statusRouter.Use(timeoutMiddleware(15 * time.Second))
    statusRouter.HandleFunc("/check-account-status", updateCardHandler.CheckAccountStatus).Methods("GET", "OPTIONS")
    statusRouter.HandleFunc("/card-update-history", updateCardHandler.GetCardUpdateHistory).Methods("GET", "OPTIONS")
    
    // Payment processing endpoints
    paymentRouter := api.PathPrefix("").Subrouter()
    paymentRouter.Use(timeoutMiddleware(60 * time.Second))
    paymentRouter.HandleFunc("/process-payment", paymentHandler.ProcessPayment).Methods("POST", "OPTIONS")
    paymentRouter.HandleFunc("/check-payment-status", paymentHandler.CheckPaymentStatus).Methods("GET", "OPTIONS")
    paymentRouter.HandleFunc("/reset-checkout-status", paymentHandler.ResetCheckoutStatus).Methods("POST", "OPTIONS")
    paymentRouter.HandleFunc("/generate-checkout-id", paymentHandler.GenerateCheckoutID).Methods("GET")
    paymentRouter.HandleFunc("/update-checkout-id", paymentHandler.UpdateCheckoutID).Methods("POST")
    paymentRouter.HandleFunc("/check-checkout-status", paymentHandler.CheckCheckoutStatus).Methods("GET")
    
    // Webhook endpoints
    webhookRouter := api.PathPrefix("/authorize-net/webhook").Subrouter()
    webhookRouter.Use(timeoutMiddleware(30 * time.Second))
    webhookRouter.HandleFunc("/silent-post", webhookHandler.HandleSilentPost).Methods("POST")
    webhookRouter.HandleFunc("/relay-response", webhookHandler.HandleRelayResponse).Methods("POST")
    webhookRouter.HandleFunc("/subscription-notification", webhookHandler.HandleSubscriptionNotification).Methods("POST")
    webhookRouter.HandleFunc("/store-payment-data", webhookHandler.StoreTemporaryPaymentData).Methods("POST")
    
    // Other public endpoints
    generalRouter := api.PathPrefix("").Subrouter()
    generalRouter.Use(timeoutMiddleware(30 * time.Second))
    generalRouter.HandleFunc("/checkout", checkoutHandler.UpdateCheckout).Methods("POST", "PUT", "OPTIONS")
    generalRouter.HandleFunc("/checkout", checkoutHandler.GetCheckout).Methods("GET", "OPTIONS")
    generalRouter.HandleFunc("/check-email-availability", checkoutHandler.CheckEmailAvailability).Methods("GET", "OPTIONS")
    generalRouter.HandleFunc("/link-account", linkAccountHandler.LinkAccount).Methods("POST", "OPTIONS")
    generalRouter.HandleFunc("/plans", planHandler.GetPlans).Methods("GET", "OPTIONS")
    generalRouter.HandleFunc("/cart", cartHandler.AddToCart).Methods("POST", "OPTIONS")
    generalRouter.HandleFunc("/cart", cartHandler.UpdateCart).Methods("PUT", "OPTIONS")
    generalRouter.HandleFunc("/cart", cartHandler.GetCart).Methods("GET", "OPTIONS")
    generalRouter.HandleFunc("/cart/remove", cartHandler.RemoveFromCart).Methods("POST", "OPTIONS")

    // Health check endpoint
    startTime := time.Now()
    api.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
        ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
        defer cancel()
        
        health := struct {
            Status    string    `json:"status"`
            Time      string    `json:"time"`
            Database  string    `json:"database"`
            Redis     string    `json:"redis"`
            Auth      string    `json:"auth"`
            Uptime    string    `json:"uptime"`
            GoVersion string    `json:"go_version"`
        }{
            Status:    "ok",
            Time:      time.Now().Format(time.RFC3339),
            Database:  "connected",
            Redis:     "connected",
            Auth:      "enabled",
            Uptime:    fmt.Sprintf("%v", time.Since(startTime)),
            GoVersion: runtime.Version(),
        }

        // Verificar conexão com banco
        dbCtx, dbCancel := context.WithTimeout(ctx, 500*time.Millisecond)
        defer dbCancel()
        
        if err := db.GetDB().PingContext(dbCtx); err != nil {
            health.Status = "degraded"
            health.Database = "error"
        }

        // Verificar conexão com Redis
        redisCtx, redisCancel := context.WithTimeout(ctx, 500*time.Millisecond)
        defer redisCancel()
        
        if err := jobQueue.Client().Ping(redisCtx).Err(); err != nil {
            health.Status = "degraded"
            health.Redis = "error"
        }

        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(health)
    }).Methods("GET")

    // Configurar servidor HTTP
    srv := &http.Server{
        Addr:         fmt.Sprintf(":%s", cfg.Server.Port),
        Handler:      router,
        ReadTimeout:  30 * time.Second,
        WriteTimeout: 120 * time.Second,
        IdleTimeout:  300 * time.Second,
        ReadHeaderTimeout: 10 * time.Second,
        MaxHeaderBytes:    1 << 20,
        ErrorLog: log.New(os.Stderr, "HTTP Server Error: ", log.LstdFlags),
    }

    // Iniciar servidor
    go func() {
        log.Printf("Server starting on port %s with authentication enabled", cfg.Server.Port)
        log.Printf("Authentication endpoints available at: /api/auth/*")
        log.Printf("Protected endpoints available at: /api/protected/*")
        
        if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            log.Fatalf("Server error: %v", err)
        }
    }()

    // Graceful shutdown
    stop := make(chan os.Signal, 1)
    signal.Notify(stop, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)

    <-stop
    log.Println("Shutdown signal received, gracefully shutting down...")

    shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer shutdownCancel()

    log.Println("Shutting down HTTP server...")
    if err := srv.Shutdown(shutdownCtx); err != nil {
        log.Printf("Server forced to shutdown: %v", err)
    }

    log.Println("Stopping payment worker...")
    paymentWorker.Stop()
    time.Sleep(2 * time.Second)
    
    log.Println("Closing database connections...")
    db.Close()
    
    log.Println("Closing Redis connections...")
    jobQueue.Close()

    log.Println("Server exited properly")
}