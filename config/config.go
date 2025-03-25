package config

import (
    "log"
    "os"
    "github.com/joho/godotenv"
    "prosecure-payment-api/database"
    "prosecure-payment-api/services/email"
)

type Config struct {
    Database database.DatabaseConfig
    AuthNet  AuthNetConfig
    SMTP     email.SMTPConfig
    Server   ServerConfig
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

    // Default worker concurrency
    workerConcurrency := 2

    cfg := &Config{
        Database: database.DatabaseConfig{
            Host:     os.Getenv("DB_HOST"),
            User:     os.Getenv("DB_USER"),
            Password: os.Getenv("DB_PASSWORD"),
            DBName:   os.Getenv("DB_NAME"),
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

    // Use default Redis URL if not set
    if cfg.Redis.URL == "" {
        cfg.Redis.URL = "redis://localhost:6379/0"
        log.Printf("Warning: REDIS_URL not set, using default: %s", cfg.Redis.URL)
    }

    log.Printf("Config loaded: %+v", cfg)

    return cfg
}