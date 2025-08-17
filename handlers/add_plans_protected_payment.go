package handlers

import (
    "log"
    "net/http"

    "prosecure-payment-api/database"
    "prosecure-payment-api/middleware"
    "prosecure-payment-api/models"
    "prosecure-payment-api/utils"
)

type AddPlansProtectedPaymentHandler struct {
    db *database.Connection
}

func NewAddPlansProtectedPaymentHandler(db *database.Connection) *AddPlansProtectedPaymentHandler {
    return &AddPlansProtectedPaymentHandler{
        db: db,
    }
}

// GetCardInfo retorna informações mascaradas do cartão do usuário para add plans
func (h *AddPlansProtectedPaymentHandler) GetCardInfo(w http.ResponseWriter, r *http.Request) {
    user := middleware.GetUserFromContext(r.Context())
    if user == nil {
        utils.SendErrorResponse(w, http.StatusInternalServerError, "User not found")
        return
    }

    if !user.IsMaster {
        utils.SendErrorResponse(w, http.StatusForbidden, "Only master accounts can access card information")
        return
    }

    // Buscar dados da conta master e informações do cartão
    var maskedCard, expiryDate string
    query := `
        SELECT COALESCE(bi.card, ''), COALESCE(bi.expiry, '')
        FROM master_accounts ma
        LEFT JOIN billing_infos bi ON ma.reference_uuid = bi.master_reference
        WHERE ma.username = ? AND ma.email = ?
    `

    err := h.db.GetDB().QueryRow(query, user.Username, user.Email).Scan(&maskedCard, &expiryDate)
    if err != nil {
        log.Printf("Error getting card info for user %s: %v", user.Username, err)
        utils.SendErrorResponse(w, http.StatusNotFound, "Card information not found")
        return
    }

    // Se não há informações do cartão
    if maskedCard == "" {
        utils.SendErrorResponse(w, http.StatusNotFound, "No payment method on file")
        return
    }

    log.Printf("Card info retrieved for user: %s", user.Username)

    utils.SendSuccessResponse(w, models.APIResponse{
        Status:  "success",
        Message: "Card information retrieved",
        Data: map[string]interface{}{
            "masked_card": maskedCard,
            "expiry":     expiryDate,
            "has_card":   true,
        },
    })
}