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
    "prosecure-payment-api/queue"
    "prosecure-payment-api/services/payment"
    "prosecure-payment-api/services/email"
    "prosecure-payment-api/worker"
)

// NOVO: Middleware de timeout para todas as requests
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
        w.Header().Set("Access-Control-Allow-Origin", "*")
        w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT")
        w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, Authorization, h-captcha-response")
        
        // OTIMIZADO: Responder imediatamente para OPTIONS
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
        
        // OTIMIZADO: Log mais detalhado para operações de update-card
        if r.URL.Path == "/api/update-card" || elapsed > 500*time.Millisecond || wrapper.status >= 400 {
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

// NOVO: Middleware para detectar conexões fechadas
func connectionStateMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Verificar se a conexão ainda está ativa antes de processar
        select {
        case <-r.Context().Done():
            log.Printf("Client disconnected before processing: %s %s", r.Method, r.URL.Path)
            return
        default:
        }
        
        next.ServeHTTP(w, r)
    })
}

func main() {
    // Configurar logging com timestamp preciso
    log.SetFlags(log.LstdFlags | log.Lshortfile | log.Lmicroseconds | log.LUTC)
    
    // Otimizar o número de CPUs que Go pode usar
    numCPU := runtime.NumCPU()
    runtime.GOMAXPROCS(numCPU)
    log.Printf("Server starting with %d CPUs available", numCPU)

    // Carregar configurações
    cfg := config.Load()
    log.Printf("Configuration loaded successfully")

    // Conectar ao banco de dados com retry
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

    // Verificar a conexão com o banco de dados
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

    // Iniciar o worker com quantidade otimizada de threads
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

    // Inicializar outros handlers
    webhookHandler := handlers.NewWebhookHandler(db, jobQueue, paymentService)
    planHandler := handlers.NewPlanHandler(db)
    cartHandler := handlers.NewCartHandler(db, cfg)
    checkoutHandler := handlers.NewCheckoutHandler(db)
    linkAccountHandler := handlers.NewLinkAccountHandler(db, cfg)
    updateCardHandler := handlers.NewUpdateCardHandler(db, paymentService, emailService)

    // Configurar o router com middleware otimizados
    router := mux.NewRouter()
    
    // ORDEM IMPORTANTE dos middlewares:
    router.Use(corsMiddleware)
    router.Use(connectionStateMiddleware) // NOVO: Detectar conexões fechadas
    router.Use(loggingMiddleware)
    
    api := router.PathPrefix("/api").Subrouter()
    
    // NOVO: Aplicar timeout específico para diferentes endpoints
    // Update card precisa de mais tempo
    updateCardRouter := api.PathPrefix("/update-card").Subrouter()
    updateCardRouter.Use(timeoutMiddleware(45 * time.Second)) // 45 segundos para update-card (reduzido de 90)
    updateCardRouter.HandleFunc("", updateCardHandler.UpdateCard).Methods("POST", "OPTIONS")
    
    // Account status e history podem ser mais rápidos
    statusRouter := api.PathPrefix("").Subrouter()
    statusRouter.Use(timeoutMiddleware(15 * time.Second)) // 15 segundos para status
    statusRouter.HandleFunc("/check-account-status", updateCardHandler.CheckAccountStatus).Methods("GET", "OPTIONS")
    statusRouter.HandleFunc("/card-update-history", updateCardHandler.GetCardUpdateHistory).Methods("GET", "OPTIONS")
    
    // Payment processing endpoints com timeout médio
    paymentRouter := api.PathPrefix("").Subrouter()
    paymentRouter.Use(timeoutMiddleware(60 * time.Second)) // 60 segundos para payment
    paymentRouter.HandleFunc("/process-payment", paymentHandler.ProcessPayment).Methods("POST", "OPTIONS")
    paymentRouter.HandleFunc("/check-payment-status", paymentHandler.CheckPaymentStatus).Methods("GET", "OPTIONS")
    paymentRouter.HandleFunc("/reset-checkout-status", paymentHandler.ResetCheckoutStatus).Methods("POST", "OPTIONS")
    paymentRouter.HandleFunc("/generate-checkout-id", paymentHandler.GenerateCheckoutID).Methods("GET")
    paymentRouter.HandleFunc("/update-checkout-id", paymentHandler.UpdateCheckoutID).Methods("POST")
    paymentRouter.HandleFunc("/check-checkout-status", paymentHandler.CheckCheckoutStatus).Methods("GET")
    
    // Webhook endpoints com timeout longo (podem receber dados grandes)
    webhookRouter := api.PathPrefix("/authorize-net/webhook").Subrouter()
    webhookRouter.Use(timeoutMiddleware(30 * time.Second)) // 30 segundos para webhooks
    webhookRouter.HandleFunc("/silent-post", webhookHandler.HandleSilentPost).Methods("POST")
    webhookRouter.HandleFunc("/relay-response", webhookHandler.HandleRelayResponse).Methods("POST")
    webhookRouter.HandleFunc("/subscription-notification", webhookHandler.HandleSubscriptionNotification).Methods("POST")
    webhookRouter.HandleFunc("/store-payment-data", webhookHandler.StoreTemporaryPaymentData).Methods("POST")
    
    // Other endpoints com timeout padrão
    generalRouter := api.PathPrefix("").Subrouter()
    generalRouter.Use(timeoutMiddleware(30 * time.Second)) // 30 segundos para endpoints gerais
    generalRouter.HandleFunc("/checkout", checkoutHandler.UpdateCheckout).Methods("POST", "PUT", "OPTIONS")
    generalRouter.HandleFunc("/checkout", checkoutHandler.GetCheckout).Methods("GET", "OPTIONS")
    generalRouter.HandleFunc("/check-email-availability", checkoutHandler.CheckEmailAvailability).Methods("GET", "OPTIONS")
    generalRouter.HandleFunc("/link-account", linkAccountHandler.LinkAccount).Methods("POST", "OPTIONS")
    generalRouter.HandleFunc("/plans", planHandler.GetPlans).Methods("GET", "OPTIONS")
    generalRouter.HandleFunc("/cart", cartHandler.AddToCart).Methods("POST", "OPTIONS")
    generalRouter.HandleFunc("/cart", cartHandler.UpdateCart).Methods("PUT", "OPTIONS")
    generalRouter.HandleFunc("/cart", cartHandler.GetCart).Methods("GET", "OPTIONS")
    generalRouter.HandleFunc("/cart/remove", cartHandler.RemoveFromCart).Methods("POST", "OPTIONS")

    // Registrar hora de início para cálculo de uptime
    startTime := time.Now()
    
    // Endpoint de health check (sem timeout - deve ser rápido)
    api.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
        ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
        defer cancel()
        
        health := struct {
            Status    string    `json:"status"`
            Time      string    `json:"time"`
            Database  string    `json:"database"`
            Redis     string    `json:"redis"`
            Uptime    string    `json:"uptime"`
            GoVersion string    `json:"go_version"`
        }{
            Status:    "ok",
            Time:      time.Now().Format(time.RFC3339),
            Database:  "connected",
            Redis:     "connected",
            Uptime:    fmt.Sprintf("%v", time.Since(startTime)),
            GoVersion: runtime.Version(),
        }

        // Verificar conexão com banco de dados
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

    // OTIMIZADO: Configurar servidor HTTP com timeouts mais agressivos
    srv := &http.Server{
        Addr:         fmt.Sprintf(":%s", cfg.Server.Port),
        Handler:      router,
        ReadTimeout:  30 * time.Second,   // REDUZIDO: 30 segundos para ler request
        WriteTimeout: 120 * time.Second,  // AUMENTADO: 120 segundos para escrever response
        IdleTimeout:  300 * time.Second,  // AUMENTADO: 5 minutos para conexões idle
        
        // NOVO: Configurações adicionais para otimização
        ReadHeaderTimeout: 10 * time.Second,  // 10 segundos para ler headers
        MaxHeaderBytes:    1 << 20,           // 1MB para headers
        
        // NOVO: Configurar error log customizado
        ErrorLog: log.New(os.Stderr, "HTTP Server Error: ", log.LstdFlags),
    }

    // Iniciar servidor em goroutine separada
    go func() {
        log.Printf("Server starting on port %s with optimized timeouts", cfg.Server.Port)
        log.Printf("Timeouts configured: Read=%v, Write=%v, Idle=%v", 
            srv.ReadTimeout, srv.WriteTimeout, srv.IdleTimeout)
        
        if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            log.Fatalf("Server error: %v", err)
        }
    }()

    // Configurar canal para capturar sinais de encerramento
    stop := make(chan os.Signal, 1)
    signal.Notify(stop, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)

    // Aguardar o sinal de parada
    <-stop
    log.Println("Shutdown signal received, gracefully shutting down...")

    // Criar contexto com timeout para encerramento
    shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer shutdownCancel()

    // Primeiro encerrar o HTTP server
    log.Println("Shutting down HTTP server...")
    if err := srv.Shutdown(shutdownCtx); err != nil {
        log.Printf("Server forced to shutdown: %v", err)
    }

    // Depois parar o worker
    log.Println("Stopping payment worker...")
    paymentWorker.Stop()
    
    // Tempo para workers finalizarem
    time.Sleep(2 * time.Second)
    
    // Fechar conexões de banco de dados
    log.Println("Closing database connections...")
    db.Close()
    
    // Fechar conexões com Redis
    log.Println("Closing Redis connections...")
    jobQueue.Close()

    log.Println("Server exited properly")
}