package models

type APIResponse struct {
    Status  string      `json:"status"`
    Message string      `json:"message"`
    Data    interface{} `json:"data,omitempty"`
}

type TransactionResponse struct {
    Success       bool   `json:"success"`
    TransactionID string `json:"transaction_id"`
    Message       string `json:"message"`
    Error         string `json:"error,omitempty"`
    IsDuplicate   bool   `json:"is_duplicate,omitempty"`
}

type SubscriptionResponse struct {
    Success        bool   `json:"success"`
    SubscriptionID string `json:"subscription_id"`
    Message        string `json:"message"`
    Error          string `json:"error,omitempty"`
}