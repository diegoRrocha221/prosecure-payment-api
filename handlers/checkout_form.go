package handlers

import (
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "log"
    "net/http"
    "database/sql"
    
    "prosecure-payment-api/database"
    "prosecure-payment-api/models"
    "prosecure-payment-api/utils"
)

type CheckoutHandler struct {
    db *database.Connection
}

func NewCheckoutHandler(db *database.Connection) *CheckoutHandler {
    return &CheckoutHandler{db: db}
}

// UpdateCheckout handles updates to the checkout data
func (h *CheckoutHandler) UpdateCheckout(w http.ResponseWriter, r *http.Request) {
    var req struct {
        CheckoutID   string  `json:"checkout_id"`
        Name         string  `json:"name"`
        Email        string  `json:"email"`
        PhoneNumber  string  `json:"phoneNumber"`
        ZipCode      string  `json:"zipcode"`
        State        string  `json:"state"`
        City         string  `json:"city"`
        Street       string  `json:"street"`
        Additional   *string `json:"additional"` // Tornando optional com ponteiro
        Username     string  `json:"username"`
        Passphrase   string  `json:"passphrase"`
    }

    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        log.Printf("Error decoding request body: %v", err)
        utils.SendErrorResponse(w, http.StatusBadRequest, "Invalid request body")
        return
    }

    // Validação dos campos obrigatórios
    if req.CheckoutID == "" || req.Email == "" || req.Username == "" {
        utils.SendErrorResponse(w, http.StatusBadRequest, "Missing required fields")
        return
    }

    // Hash da passphrase usando SHA256 se ela estiver presente
    var hashedPassphrase string
    if req.Passphrase != "" {
        hash := sha256.New()
        hash.Write([]byte(req.Passphrase))
        hashedPassphrase = hex.EncodeToString(hash.Sum(nil))
    }

    // Verifica se o checkout existe
    var exists bool
    err := h.db.GetDB().QueryRow("SELECT EXISTS(SELECT 1 FROM checkout_historics WHERE checkout_id = ?)", 
        req.CheckoutID).Scan(&exists)
    if err != nil {
        log.Printf("Error checking checkout existence: %v", err)
        utils.SendErrorResponse(w, http.StatusInternalServerError, "Database error")
        return
    }

    var query string
    var args []interface{}

    // Tratamento do campo additional
    var additional sql.NullString
    if req.Additional != nil {
        additional = sql.NullString{
            String: *req.Additional,
            Valid: true,
        }
    }

    if exists {
        // Update existing record
        query = `
            UPDATE checkout_historics 
            SET name = ?, email = ?, phoneNumber = ?, zipcode = ?, 
                state = ?, city = ?, street = ?, additional = ?,
                username = ?
        `
        args = []interface{}{
            req.Name, req.Email, req.PhoneNumber, req.ZipCode,
            req.State, req.City, req.Street, additional,
            req.Username,
        }

        // Adiciona passphrase ao update apenas se fornecida
        if req.Passphrase != "" {
            query += `, passphrase = ?`
            args = append(args, hashedPassphrase)
        }

        query += ` WHERE checkout_id = ?`
        args = append(args, req.CheckoutID)
    } else {
        // Insert new record
        query = `
            INSERT INTO checkout_historics (
                checkout_id, name, email, phoneNumber, zipcode,
                state, city, street, additional, username,
                passphrase, status, created_at
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', NOW())
        `
        args = []interface{}{
            req.CheckoutID, req.Name, req.Email, req.PhoneNumber, req.ZipCode,
            req.State, req.City, req.Street, additional, req.Username,
            hashedPassphrase,
        }
    }

    _, err = h.db.GetDB().Exec(query, args...)
    if err != nil {
        log.Printf("Error %s checkout: %v", map[bool]string{true: "updating", false: "creating"}[exists], err)
        utils.SendErrorResponse(w, http.StatusInternalServerError, "Database error")
        return
    }

    utils.SendSuccessResponse(w, models.APIResponse{
        Status:  "success",
        Message: "Checkout data saved successfully",
        Data: map[string]string{
            "checkout_id": req.CheckoutID,
        },
    })
}

// GetCheckout retrieves checkout data
func (h *CheckoutHandler) GetCheckout(w http.ResponseWriter, r *http.Request) {
    checkoutID := r.URL.Query().Get("checkout_id")
    if checkoutID == "" {
        utils.SendErrorResponse(w, http.StatusBadRequest, "Checkout ID is required")
        return
    }

    query := `
        SELECT name, email, phoneNumber, zipcode, state, city, street, 
               additional, username, passphrase, COALESCE(status, 'pending') as status
        FROM checkout_historics 
        WHERE checkout_id = ?
    `

    var checkout models.CheckoutData
    var additional sql.NullString
    err := h.db.GetDB().QueryRow(query, checkoutID).Scan(
        &checkout.Name, 
        &checkout.Email, 
        &checkout.PhoneNumber,
        &checkout.ZipCode, 
        &checkout.State, 
        &checkout.City,
        &checkout.Street, 
        &additional, 
        &checkout.Username,
        &checkout.Passphrase,
        &checkout.Status,
    )

    if err == sql.ErrNoRows {
        utils.SendErrorResponse(w, http.StatusNotFound, "Checkout not found")
        return
    }
    if err != nil {
        log.Printf("Error retrieving checkout: %v", err)
        utils.SendErrorResponse(w, http.StatusInternalServerError, "Database error")
        return
    }

    // Converte NullString para string vazia se for null
    if additional.Valid {
        checkout.Additional = additional.String
    } else {
        checkout.Additional = ""
    }

    checkout.ID = checkoutID
    utils.SendSuccessResponse(w, models.APIResponse{
        Status:  "success",
        Message: "Checkout retrieved successfully",
        Data:    checkout,
    })
}
func (h *CheckoutHandler) CheckEmailAvailability(w http.ResponseWriter, r *http.Request) {
    email := r.URL.Query().Get("email")
    if email == "" {
        utils.SendErrorResponse(w, http.StatusBadRequest, "Email is required")
        return
    }

    var exists bool
    query := "SELECT EXISTS(SELECT 1 FROM users WHERE username = ?)"
    err := h.db.GetDB().QueryRow(query, email).Scan(&exists)
    if err != nil {
        log.Printf("Error checking email availability: %v", err)
        utils.SendErrorResponse(w, http.StatusInternalServerError, "Database error")
        return
    }

    utils.SendSuccessResponse(w, models.APIResponse{
        Status:  "success",
        Message: "Email availability checked",
        Data: map[string]bool{
            "available": !exists,
        },
    })
}

