package utils

import (
    "encoding/json"
    "net/http"
    "prosecure-payment-api/models"
)

func SendErrorResponse(w http.ResponseWriter, status int, message string) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    json.NewEncoder(w).Encode(models.APIResponse{
        Status:  "error",
        Message: message,
    })
}

func SendSuccessResponse(w http.ResponseWriter, response models.APIResponse) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(http.StatusOK)
    json.NewEncoder(w).Encode(response)
}