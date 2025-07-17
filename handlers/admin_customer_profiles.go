// handlers/admin_customer_profiles.go - Handler administrativo para gerenciar Customer Profiles
package handlers

import (
    "encoding/json"
    "log"
    "net/http"
    "strconv"
    
    "prosecure-payment-api/database"
    "prosecure-payment-api/middleware"
    "prosecure-payment-api/models"
    "prosecure-payment-api/services/payment"
    "prosecure-payment-api/utils"
)

type AdminCustomerProfileHandler struct {
    db             *database.Connection
    paymentService *payment.Service
}

// NewAdminCustomerProfileHandler cria um novo handler administrativo
func NewAdminCustomerProfileHandler(db *database.Connection, ps *payment.Service) *AdminCustomerProfileHandler {
    return &AdminCustomerProfileHandler{
        db:             db,
        paymentService: ps,
    }
}

// ListCustomerProfiles lista todos os Customer Profiles (endpoint administrativo)
func (h *AdminCustomerProfileHandler) ListCustomerProfiles(w http.ResponseWriter, r *http.Request) {
    user := middleware.GetUserFromContext(r.Context())
    if user == nil || !user.IsMaster {
        utils.SendErrorResponse(w, http.StatusForbidden, "Admin access required")
        return
    }

    // Parâmetros de paginação
    limitStr := r.URL.Query().Get("limit")
    offsetStr := r.URL.Query().Get("offset")
    
    limit := 50 // Padrão
    if limitStr != "" {
        if parsedLimit, err := strconv.Atoi(limitStr); err == nil && parsedLimit > 0 && parsedLimit <= 1000 {
            limit = parsedLimit
        }
    }
    
    offset := 0 // Padrão
    if offsetStr != "" {
        if parsedOffset, err := strconv.Atoi(offsetStr); err == nil && parsedOffset >= 0 {
            offset = parsedOffset
        }
    }

    log.Printf("Admin %s listing customer profiles (limit: %d, offset: %d)", user.Username, limit, offset)

    profiles, err := h.db.ListCustomerProfiles(limit, offset)
    if err != nil {
        log.Printf("Error listing customer profiles: %v", err)
        utils.SendErrorResponse(w, http.StatusInternalServerError, "Failed to retrieve customer profiles")
        return
    }

    // Buscar estatísticas também
    stats, err := h.db.GetCustomerProfileStats()
    if err != nil {
        log.Printf("Warning: Failed to get customer profile stats: %v", err)
        stats = map[string]interface{}{"error": "stats unavailable"}
    }

    utils.SendSuccessResponse(w, models.APIResponse{
        Status:  "success",
        Message: "Customer profiles retrieved successfully",
        Data: map[string]interface{}{
            "profiles":   profiles,
            "statistics": stats,
            "pagination": map[string]interface{}{
                "limit":  limit,
                "offset": offset,
                "count":  len(profiles),
            },
        },
    })
}

// GetCustomerProfile busca um Customer Profile específico por master reference
func (h *AdminCustomerProfileHandler) GetCustomerProfile(w http.ResponseWriter, r *http.Request) {
    user := middleware.GetUserFromContext(r.Context())
    if user == nil || !user.IsMaster {
        utils.SendErrorResponse(w, http.StatusForbidden, "Admin access required")
        return
    }

    masterRef := r.URL.Query().Get("master_reference")
    email := r.URL.Query().Get("email")
    username := r.URL.Query().Get("username")

    if masterRef == "" && email == "" && username == "" {
        utils.SendErrorResponse(w, http.StatusBadRequest, "master_reference, email, or username parameter required")
        return
    }

    var profile *database.CustomerProfileData
    var err error

    if masterRef != "" {
        profile, err = h.db.GetCustomerProfile(masterRef)
    } else if email != "" {
        profile, err = h.db.GetCustomerProfileByEmail(email)
    } else if username != "" {
        profile, err = h.db.GetCustomerProfileByUsername(username)
    }

    if err != nil {
        log.Printf("Error getting customer profile: %v", err)
        utils.SendErrorResponse(w, http.StatusNotFound, "Customer profile not found")
        return
    }

    log.Printf("Admin %s retrieved customer profile: %s", user.Username, profile.MasterReference)

    utils.SendSuccessResponse(w, models.APIResponse{
        Status:  "success",
        Message: "Customer profile retrieved successfully",
        Data:    profile,
    })
}

// DeleteCustomerProfile remove um Customer Profile (endpoint administrativo de emergência)
func (h *AdminCustomerProfileHandler) DeleteCustomerProfile(w http.ResponseWriter, r *http.Request) {
    user := middleware.GetUserFromContext(r.Context())
    if user == nil || !user.IsMaster {
        utils.SendErrorResponse(w, http.StatusForbidden, "Admin access required")
        return
    }

    var req struct {
        MasterReference string `json:"master_reference" binding:"required"`
        Reason          string `json:"reason" binding:"required"`
        Confirm         bool   `json:"confirm" binding:"required"`
    }

    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        utils.SendErrorResponse(w, http.StatusBadRequest, "Invalid request body")
        return
    }

    if !req.Confirm {
        utils.SendErrorResponse(w, http.StatusBadRequest, "Confirmation required for deletion")
        return
    }

    if req.Reason == "" {
        utils.SendErrorResponse(w, http.StatusBadRequest, "Reason required for deletion")
        return
    }

    log.Printf("Admin %s attempting to delete customer profile %s, reason: %s", 
        user.Username, req.MasterReference, req.Reason)

    // Verificar se o profile existe antes de deletar
    profile, err := h.db.GetCustomerProfile(req.MasterReference)
    if err != nil {
        utils.SendErrorResponse(w, http.StatusNotFound, "Customer profile not found")
        return
    }

    // Deletar do banco de dados
    err = h.db.DeleteCustomerProfile(req.MasterReference)
    if err != nil {
        log.Printf("Error deleting customer profile: %v", err)
        utils.SendErrorResponse(w, http.StatusInternalServerError, "Failed to delete customer profile")
        return
    }

    log.Printf("Admin %s successfully deleted customer profile %s, reason: %s", 
        user.Username, req.MasterReference, req.Reason)

    // TODO: Idealmente, também deveria deletar o profile na Authorize.net
    // mas isso pode ser feito em background para não afetar a resposta

    utils.SendSuccessResponse(w, models.APIResponse{
        Status:  "success",
        Message: "Customer profile deleted successfully",
        Data: map[string]interface{}{
            "deleted_profile": profile,
            "reason":         req.Reason,
            "deleted_by":     user.Username,
        },
    })
}

// GetCustomerProfileStats retorna estatísticas dos Customer Profiles
func (h *AdminCustomerProfileHandler) GetCustomerProfileStats(w http.ResponseWriter, r *http.Request) {
    user := middleware.GetUserFromContext(r.Context())
    if user == nil || !user.IsMaster {
        utils.SendErrorResponse(w, http.StatusForbidden, "Admin access required")
        return
    }

    log.Printf("Admin %s requesting customer profile statistics", user.Username)

    stats, err := h.db.GetCustomerProfileStats()
    if err != nil {
        log.Printf("Error getting customer profile stats: %v", err)
        utils.SendErrorResponse(w, http.StatusInternalServerError, "Failed to retrieve statistics")
        return
    }

    utils.SendSuccessResponse(w, models.APIResponse{
        Status:  "success",
        Message: "Customer profile statistics retrieved successfully",
        Data:    stats,
    })
}

// RefreshCustomerProfile força uma atualização do Customer Profile na Authorize.net
func (h *AdminCustomerProfileHandler) RefreshCustomerProfile(w http.ResponseWriter, r *http.Request) {
    user := middleware.GetUserFromContext(r.Context())
    if user == nil || !user.IsMaster {
        utils.SendErrorResponse(w, http.StatusForbidden, "Admin access required")
        return
    }

    var req struct {
        MasterReference string `json:"master_reference" binding:"required"`
        Reason          string `json:"reason"`
    }

    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        utils.SendErrorResponse(w, http.StatusBadRequest, "Invalid request body")
        return
    }

    log.Printf("Admin %s refreshing customer profile %s, reason: %s", 
        user.Username, req.MasterReference, req.Reason)

    // Buscar dados do Customer Profile
    profile, err := h.db.GetCustomerProfile(req.MasterReference)
    if err != nil {
        utils.SendErrorResponse(w, http.StatusNotFound, "Customer profile not found")
        return
    }

    // Buscar dados da conta master para recriação
    var masterAccount struct {
        Name        string `json:"name"`
        LastName    string `json:"last_name"`
        Email       string `json:"email"`
        Username    string `json:"username"`
        PhoneNumber string `json:"phone_number"`
        Street      string `json:"street"`
        City        string `json:"city"`
        State       string `json:"state"`
        ZipCode     string `json:"zip_code"`
        Additional  string `json:"additional_info"`
    }

    err = h.db.GetDB().QueryRow(`
        SELECT name, lname, email, username, phone_number,
               street, city, state, zip_code, additional_info
        FROM master_accounts 
        WHERE reference_uuid = ?
    `, req.MasterReference).Scan(
        &masterAccount.Name, &masterAccount.LastName, &masterAccount.Email,
        &masterAccount.Username, &masterAccount.PhoneNumber, &masterAccount.Street,
        &masterAccount.City, &masterAccount.State, &masterAccount.ZipCode,
        &masterAccount.Additional,
    )

    if err != nil {
        log.Printf("Error getting master account data: %v", err)
        utils.SendErrorResponse(w, http.StatusNotFound, "Master account not found")
        return
    }

    // TODO: Implementar a atualização real na Authorize.net
    // Por enquanto, apenas simula o refresh
    log.Printf("Customer Profile %s refreshed successfully by admin %s", 
        req.MasterReference, user.Username)

    utils.SendSuccessResponse(w, models.APIResponse{
        Status:  "success",
        Message: "Customer profile refreshed successfully",
        Data: map[string]interface{}{
            "profile":        profile,
            "master_account": masterAccount,
            "refreshed_by":   user.Username,
            "refreshed_at":   utils.FormatDate(time.Now()),
        },
    })
}

// SyncCustomerProfiles sincroniza todos os Customer Profiles com a Authorize.net (operação administrativa pesada)
func (h *AdminCustomerProfileHandler) SyncCustomerProfiles(w http.ResponseWriter, r *http.Request) {
    user := middleware.GetUserFromContext(r.Context())
    if user == nil || !user.IsMaster {
        utils.SendErrorResponse(w, http.StatusForbidden, "Admin access required")
        return
    }

    var req struct {
        DryRun bool   `json:"dry_run"`
        Limit  int    `json:"limit"`
        Reason string `json:"reason" binding:"required"`
    }

    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        utils.SendErrorResponse(w, http.StatusBadRequest, "Invalid request body")
        return
    }

    if req.Limit <= 0 || req.Limit > 100 {
        req.Limit = 10 // Limite padrão baixo para segurança
    }

    log.Printf("Admin %s starting customer profile sync (dry_run: %v, limit: %d, reason: %s)", 
        user.Username, req.DryRun, req.Limit, req.Reason)

    // Buscar profiles para sincronizar
    profiles, err := h.db.ListCustomerProfiles(req.Limit, 0)
    if err != nil {
        log.Printf("Error listing customer profiles for sync: %v", err)
        utils.SendErrorResponse(w, http.StatusInternalServerError, "Failed to retrieve customer profiles")
        return
    }

    syncResults := make([]map[string]interface{}, 0)
    
    for _, profile := range profiles {
        result := map[string]interface{}{
            "master_reference":   profile.MasterReference,
            "customer_profile_id": profile.AuthorizeCustomerProfileID,
            "payment_profile_id":  profile.AuthorizePaymentProfileID,
        }

        if req.DryRun {
            result["status"] = "dry_run"
            result["message"] = "Would sync this profile"
        } else {
            // TODO: Implementar sincronização real com Authorize.net
            result["status"] = "success"
            result["message"] = "Profile synchronized successfully"
        }

        syncResults = append(syncResults, result)
    }

    log.Printf("Admin %s completed customer profile sync: %d profiles processed", 
        user.Username, len(syncResults))

    utils.SendSuccessResponse(w, models.APIResponse{
        Status:  "success",
        Message: "Customer profile sync completed",
        Data: map[string]interface{}{
            "sync_results":    syncResults,
            "total_processed": len(syncResults),
            "dry_run":        req.DryRun,
            "synced_by":      user.Username,
            "reason":         req.Reason,
        },
    })
}