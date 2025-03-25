package main

import (
    "context"
    "encoding/gob"
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
    "prosecure-payment-api/models"
    "prosecure-payment-api/services/payment"
    "prosecure-payment-api/services/email"
)
/*
func corsMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Permite qualquer origem
        w.Header().Set("Access-Control-Allow-Origin", "*")
        w.Header().Set("Access-Control-Allow-Methods", "*")
        w.Header().Set("Access-Control-Allow-Headers", "*")
        w.Header().Set("Access-Control-Allow-Credentials", "true")
        
        if r.Method == "OPTIONS" {
            w.WriteHeader(http.StatusOK)
            return
        }
        next.ServeHTTP(w, r)
    })
}
*/
func corsMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        origin := r.Header.Get("Origin")
        allowedOrigins := map[string]bool{
            "https://prosecurelsp.com":     true,
            "https://www.prosecurelsp.com": true,
        }

        if allowedOrigins[origin] {
            w.Header().Set("Access-Control-Allow-Origin", origin)
            w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
            w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization")
            w.Header().Set("Access-Control-Allow-Credentials", "true")
            w.Header().Set("Access-Control-Max-Age", "86400")
        }

        if r.Method == "OPTIONS" {
            w.WriteHeader(http.StatusOK)
            return
        }

        next.ServeHTTP(w, r)
    })
}

func recoveryMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        defer func() {
            if err := recover(); err != nil {
                log.Printf("Panic recovered: %v", err)
                http.Error(w, "Internal server error", http.StatusInternalServerError)
            }
        }()
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

    gob.Register([]models.CartItem{})
    cfg := config.Load()
    log.Printf("Starting server with configuration: %+v", cfg)

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


    paymentService := payment.NewPaymentService(
        cfg.AuthNet.APILoginID,
        cfg.AuthNet.TransactionKey,
        cfg.AuthNet.MerchantID,
        cfg.AuthNet.Environment,
    )
    emailService := email.NewSMTPService(cfg.SMTP)


    var paymentHandler *handlers.PaymentHandler
    for retries := 0; retries < 3; retries++ {
        paymentHandler, err = handlers.NewPaymentHandler(db, paymentService, emailService)
        if err == nil {
            break
        }
        log.Printf("Failed to initialize payment handler (attempt %d/3): %v", retries+1, err)
        time.Sleep(time.Second * time.Duration(retries+1))
    }
    if err != nil {
        log.Fatalf("Failed to initialize payment handler after retries: %v", err)
    }

    router := mux.NewRouter()
    router.Use(recoveryMiddleware)
    router.Use(corsMiddleware)
    router.Use(loggingMiddleware)
    planHandler := handlers.NewPlanHandler(db)
    cartHandler := handlers.NewCartHandler(db, cfg)
    checkoutHandler := handlers.NewCheckoutHandler(db)
    linkAccountHandler := handlers.NewLinkAccountHandler(db, cfg)
    api := router.PathPrefix("/api").Subrouter()
    api.HandleFunc("/process-payment", paymentHandler.ProcessPayment).Methods("POST", "OPTIONS")
    api.HandleFunc("/reset-checkout-status", paymentHandler.ResetCheckoutStatus).Methods("POST", "OPTIONS")
    api.HandleFunc("/generate-checkout-id", paymentHandler.GenerateCheckoutID).Methods("GET")
    api.HandleFunc("/update-checkout-id", paymentHandler.UpdateCheckoutID).Methods("POST")
    api.HandleFunc("/check-checkout-status", paymentHandler.CheckCheckoutStatus).Methods("GET")
    api.HandleFunc("/checkout", checkoutHandler.UpdateCheckout).Methods("POST", "PUT", "OPTIONS")
    api.HandleFunc("/checkout", checkoutHandler.GetCheckout).Methods("GET", "OPTIONS")
    api.HandleFunc("/check-email-availability", checkoutHandler.CheckEmailAvailability).Methods("GET", "OPTIONS")
    api.HandleFunc("/link-account", linkAccountHandler.LinkAccount).Methods("POST", "OPTIONS")
    api.HandleFunc("/plans", planHandler.GetPlans).Methods("GET", "OPTIONS")
    api.HandleFunc("/cart", cartHandler.AddToCart).Methods("POST", "OPTIONS")
    api.HandleFunc("/cart", cartHandler.UpdateCart).Methods("PUT", "OPTIONS")
    api.HandleFunc("/cart", cartHandler.GetCart).Methods("GET", "OPTIONS")
    api.HandleFunc("/cart/remove", cartHandler.RemoveFromCart).Methods("POST", "OPTIONS")
    //api.HandleFunc("/3ds/callback", paymentHandler.Handle3DSCallback).Methods("POST", "OPTIONS")

    api.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
        health := struct {
            Status    string    `json:"status"`
            Time     string    `json:"time"`
            Database string    `json:"database"`
        }{
            Status:    "ok",
            Time:     time.Now().Format(time.RFC3339),
            Database: "connected",
        }

        if err := db.Ping(); err != nil {
            health.Status = "degraded"
            health.Database = "error"
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

    if err := srv.Shutdown(ctx); err != nil {
        log.Printf("Server forced to shutdown: %v", err)
    }

    log.Println("Server exited properly")
}