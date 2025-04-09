// config/config.go
package config

import (
    "log"
    "os"
    "strconv"
    "github.com/joho/godotenv"
    "prosecure-payment-api/database"
    "prosecure-payment-api/services/email"
)

type Config struct {
    Database database.DatabaseConfig
    AuthNet  AuthNetConfig
    SMTP     email.SMTPConfig
    Server   ServerConfig
    Session  SessionConfig
    Redis    RedisConfig
}

type AuthNetConfig struct {
    APILoginID      string
    TransactionKey  string
    MerchantID      string
    SignatureKey    string
    Environment     string
}

type ServerConfig struct {
    Port string
}

type SessionConfig struct {
    Secret   string
    MaxAge   int
    Domain   string
    Secure   bool
    HttpOnly bool
}

type RedisConfig struct {
    URL              string
    WorkerConcurrency int
}

func Load() *Config {
    if err := godotenv.Load(); err != nil {
        log.Printf("Warning: Error loading .env file: %v", err)
    }

    dir, err := os.Getwd()
    if err != nil {
        log.Printf("Error getting current directory: %v", err)
    }
    log.Printf("Current directory: %s", dir)

    // Parse boolean values from environment
    secure, _ := strconv.ParseBool(os.Getenv("SESSION_SECURE"))
    httpOnly, _ := strconv.ParseBool(os.Getenv("SESSION_HTTP_ONLY"))
    maxAge, _ := strconv.Atoi(os.Getenv("SESSION_MAX_AGE"))
    if maxAge == 0 {
        maxAge = 2400 // Default to 2400 if not set
    }
    workerConcurrency := 4
    cfg := &Config{
        Database: database.DatabaseConfig{
            Host:     os.Getenv("DB_HOST"),
            User:     os.Getenv("DB_USER"),
            Password: os.Getenv("DB_PASSWORD"),
            DBName:   os.Getenv("DB_NAME"),
        },
        Session: SessionConfig{
            Secret:   os.Getenv("SESSION_SECRET"),
            MaxAge:   maxAge,
            Domain:   os.Getenv("SESSION_DOMAIN"),
            Secure:   secure,
            HttpOnly: httpOnly,
        },
        AuthNet: AuthNetConfig{
            APILoginID:     os.Getenv("AUTHNET_API_LOGIN_ID"),
            TransactionKey: os.Getenv("AUTHNET_TRANSACTION_KEY"),
            MerchantID:     os.Getenv("AUTHNET_MERCHANT_ID"),
            SignatureKey:   os.Getenv("AUTHNET_SIGNATURE_KEY"),
            Environment:    os.Getenv("AUTHNET_ENVIRONMENT"),
        },
        SMTP: email.SMTPConfig{
            Host:     os.Getenv("SMTP_HOST"),
            Port:     os.Getenv("SMTP_PORT"),
            Username: os.Getenv("SMTP_USER"),
            Password: os.Getenv("SMTP_PASSWORD"),
        },
        Server: ServerConfig{
            Port: os.Getenv("SERVER_PORT"),
        },
        Redis: RedisConfig{
            URL: os.Getenv("REDIS_URL"),
            WorkerConcurrency: workerConcurrency,
        },
    }
    if cfg.Redis.URL == "" {
        cfg.Redis.URL = "redis://localhost:6379/0"
        log.Printf("Warning: REDIS_URL not set, using default: %s", cfg.Redis.URL)
    }
    log.Printf("Session config loaded: %+v", cfg.Session)
    return cfg
}