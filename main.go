package main

import (
    "context"
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "os"
    "os/signal"
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

func corsMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Access-Control-Allow-Origin", "*")
        w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT")
        w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, Authorization, h-captcha-response")
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

        log.Printf(
            "%s %s %s %d %v",
            r.Method,
            r.RequestURI,
            r.RemoteAddr,
            wrapper.status,
            time.Since(start),
        )
    })
}

func main() {
    log.SetFlags(log.LstdFlags | log.Lshortfile | log.LUTC)

    cfg := config.Load()
    log.Printf("Starting server with configuration: %+v", cfg)

    // Connect to database
    var db *database.Connection
    var err error
    for retries := 0; retries < 3; retries++ {
        db, err = database.NewConnection(cfg.Database)
        if err == nil {
            break
        }
        log.Printf("Failed to connect to database (attempt %d/3): %v", retries+1, err)
        time.Sleep(time.Second * time.Duration(retries+1))
    }
    if err != nil {
        log.Fatalf("Failed to connect to database after retries: %v", err)
    }
    defer db.Close()

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    
    if err := db.GetDB().PingContext(ctx); err != nil {
        log.Fatalf("Failed to ping database: %v", err)
    }
    log.Println("Successfully connected to database")

    // Initialize Redis queue
    jobQueue, err := queue.NewQueue(cfg.Redis.URL, "payment_jobs")
    if err != nil {
        log.Fatalf("Failed to connect to Redis: %v", err)
    }
    defer jobQueue.Close()
    log.Println("Successfully connected to Redis")

    // Initialize services
    paymentService := payment.NewPaymentService(
        cfg.AuthNet.APILoginID,
        cfg.AuthNet.TransactionKey,
        cfg.AuthNet.MerchantID,
        cfg.AuthNet.Environment,
    )
    emailService := email.NewSMTPService(cfg.SMTP)

    // Start the worker
    paymentWorker := worker.NewWorker(jobQueue, db, paymentService)
    paymentWorker.Start(cfg.Redis.WorkerConcurrency)
    defer paymentWorker.Stop()
    log.Printf("Started payment worker with %d threads", cfg.Redis.WorkerConcurrency)

    // Initialize handlers
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

    // Initialize webhook handler
    webhookHandler := handlers.NewWebhookHandler(db, jobQueue, paymentService)

    // Initialize other handlers
    planHandler := handlers.NewPlanHandler(db)
    cartHandler := handlers.NewCartHandler(db, cfg)
    checkoutHandler := handlers.NewCheckoutHandler(db)
    linkAccountHandler := handlers.NewLinkAccountHandler(db, cfg)

    // Set up router
    router := mux.NewRouter()
    router.Use(corsMiddleware)
    router.Use(loggingMiddleware)

    api := router.PathPrefix("/api").Subrouter()
    
    // Payment processing endpoints
    api.HandleFunc("/process-payment", paymentHandler.ProcessPayment).Methods("POST", "OPTIONS")
    api.HandleFunc("/reset-checkout-status", paymentHandler.ResetCheckoutStatus).Methods("POST", "OPTIONS")
    api.HandleFunc("/generate-checkout-id", paymentHandler.GenerateCheckoutID).Methods("GET")
    api.HandleFunc("/update-checkout-id", paymentHandler.UpdateCheckoutID).Methods("POST")
    api.HandleFunc("/check-checkout-status", paymentHandler.CheckCheckoutStatus).Methods("GET")
    
    // Webhook endpoints
    webhookRouter := api.PathPrefix("/authorize-net/webhook").Subrouter()
    webhookRouter.HandleFunc("/silent-post", webhookHandler.HandleSilentPost).Methods("POST")
    webhookRouter.HandleFunc("/relay-response", webhookHandler.HandleRelayResponse).Methods("POST")
    webhookRouter.HandleFunc("/subscription-notification", webhookHandler.HandleSubscriptionNotification).Methods("POST")
    webhookRouter.HandleFunc("/store-payment-data", webhookHandler.StoreTemporaryPaymentData).Methods("POST")
    
    // Other existing endpoints
    api.HandleFunc("/checkout", checkoutHandler.UpdateCheckout).Methods("POST", "PUT", "OPTIONS")
    api.HandleFunc("/checkout", checkoutHandler.GetCheckout).Methods("GET", "OPTIONS")
    api.HandleFunc("/check-email-availability", checkoutHandler.CheckEmailAvailability).Methods("GET", "OPTIONS")
    api.HandleFunc("/link-account", linkAccountHandler.LinkAccount).Methods("POST", "OPTIONS")
    api.HandleFunc("/plans", planHandler.GetPlans).Methods("GET", "OPTIONS")
    api.HandleFunc("/cart", cartHandler.AddToCart).Methods("POST", "OPTIONS")
    api.HandleFunc("/cart", cartHandler.UpdateCart).Methods("PUT", "OPTIONS")
    api.HandleFunc("/cart", cartHandler.GetCart).Methods("GET", "OPTIONS")
    api.HandleFunc("/cart/remove", cartHandler.RemoveFromCart).Methods("POST", "OPTIONS")

    // Health check endpoint
    api.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
        health := struct {
            Status    string    `json:"status"`
            Time      string    `json:"time"`
            Database  string    `json:"database"`
            Redis     string    `json:"redis"`
        }{
            Status:   "ok",
            Time:     time.Now().Format(time.RFC3339),
            Database: "connected",
            Redis:    "connected",
        }

        if err := db.Ping(); err != nil {
            health.Status = "degraded"
            health.Database = "error"
        }

        ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
        defer cancel()
        
        if err := jobQueue.Client().Ping(ctx).Err(); err != nil {
            health.Status = "degraded"
            health.Redis = "error"
        }

        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(health)
    }).Methods("GET")

    srv := &http.Server{
        Addr:         fmt.Sprintf(":%s", cfg.Server.Port),
        Handler:      router,
        ReadTimeout:  15 * time.Second,
        WriteTimeout: 15 * time.Second,
        IdleTimeout:  60 * time.Second,
    }

    stop := make(chan os.Signal, 1)
    signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

    go func() {
        log.Printf("Server starting on port %s", cfg.Server.Port)
        if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            log.Fatalf("Server error: %v", err)
        }
    }()

    <-stop
    log.Println("Shutting down server...")

    ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    // First shutdown the HTTP server
    if err := srv.Shutdown(ctx); err != nil {
        log.Printf("Server forced to shutdown: %v", err)
    }

    // Then stop the worker
    paymentWorker.Stop()
    log.Println("Payment worker stopped")

    log.Println("Server exited properly")
}