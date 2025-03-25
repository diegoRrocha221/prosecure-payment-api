// handlers/link_account.go
package handlers

import (
    "encoding/json"
    "log"
    "net/http"

    "github.com/gorilla/sessions"
    "prosecure-payment-api/database"
    "prosecure-payment-api/models"
    "prosecure-payment-api/utils"
    "prosecure-payment-api/config"
)

type LinkAccountHandler struct {
    db    *database.Connection
    store *sessions.CookieStore
}

func NewLinkAccountHandler(db *database.Connection, cfg *config.Config) *LinkAccountHandler {
    store := sessions.NewCookieStore([]byte(cfg.Session.Secret))
    store.Options = &sessions.Options{
        Path:     "/",
        Domain:   cfg.Session.Domain,
        MaxAge:   cfg.Session.MaxAge,
        Secure:   cfg.Session.Secure,
        HttpOnly: cfg.Session.HttpOnly,
        SameSite: http.SameSiteLaxMode,
    }
    log.Printf("Initializing LinkAccountHandler with session options: %+v", store.Options)
    return &LinkAccountHandler{db: db, store: store}
}

type PlanNode struct {
    PlanID    int     `json:"plan_id"`
    PlanName  string  `json:"plan_name"`
    Annually  int     `json:"anually"`
    Username  string  `json:"username"`
    Email     string  `json:"email"`
    IsMaster  int     `json:"is_master"`
}

func (h *LinkAccountHandler) LinkAccount(w http.ResponseWriter, r *http.Request) {
    log.Printf("LinkAccount called with cookies: %+v", r.Cookies())
    log.Printf("Request headers: %+v", r.Header)
    
    // Obter checkout_id da query
    checkoutID := r.URL.Query().Get("checkout_id")
    if checkoutID == "" {
        log.Printf("No checkout_id provided")
        utils.SendErrorResponse(w, http.StatusBadRequest, "Checkout ID is required")
        return
    }
    log.Printf("Using checkout_id: %s", checkoutID)

    // Obter sessão do carrinho
    session, err := h.store.Get(r, "cart-session")
    if err != nil {
        log.Printf("Error getting session: %v", err)
        utils.SendErrorResponse(w, http.StatusInternalServerError, "Error getting session")
        return
    }

    log.Printf("Session values: %+v", session.Values)
    log.Printf("Session options: %+v", session.Options)

    // Verificar carrinho na sessão
    cart, ok := session.Values["cart"].([]models.CartItem)
    if !ok {
        log.Printf("Failed to get cart from session. Session values: %+v", session.Values)
        utils.SendErrorResponse(w, http.StatusBadRequest, "No cart found in session")
        return
    }

    if len(cart) == 0 {
        log.Printf("Cart is empty")
        utils.SendErrorResponse(w, http.StatusBadRequest, "Cart is empty")
        return
    }

    log.Printf("Cart contents: %+v", cart)

    // Buscar dados do master account
    masterData, err := h.getMasterAccountData(checkoutID)
    if err != nil {
        log.Printf("Error getting master account data: %v", err)
        utils.SendErrorResponse(w, http.StatusInternalServerError, "Error getting account data")
        return
    }

    // Buscar detalhes dos planos do banco
    plans, err := h.db.GetPlans()
    if err != nil {
        log.Printf("Error getting plans: %v", err)
        utils.SendErrorResponse(w, http.StatusInternalServerError, "Error getting plans")
        return
    }

    // Converter CartItems para PlanNodes
    planNodes := h.convertCartItemsToPlanNodes(cart, plans, masterData)

    // Verificar se já existe um master associado
    if h.hasMasterAssociated(planNodes) {
        utils.SendSuccessResponse(w, models.APIResponse{
            Status:  "success",
            Message: "Master account already associated",
        })
        return
    }

    // Auto associar seguindo a hierarquia
    var selectedPlanID int
    var updatedPlans []PlanNode

    // Business Plan (ID: 7)
    if h.hasPlanInCart(cart, 7) {
        selectedPlanID = 7
        updatedPlans = h.linkMasterToPlans(planNodes, 7, masterData)
    } else if h.hasPlanInCart(cart, 6) {
        // Home Plan (ID: 6)
        selectedPlanID = 6
        updatedPlans = h.linkMasterToPlans(planNodes, 6, masterData)
    } else if h.hasPlanInCart(cart, 4) {
        // Family Plan (ID: 4)
        selectedPlanID = 4
        updatedPlans = h.linkMasterToPlans(planNodes, 4, masterData)
    } else if h.hasPlanInCart(cart, 5) {
        // Kids Plan (ID: 5)
        selectedPlanID = 5
        updatedPlans = h.linkMasterToPlans(planNodes, 5, masterData)
    } else {
        utils.SendErrorResponse(w, http.StatusBadRequest, "No valid plan found for master association")
        return
    }

    // Atualizar no banco
    updatedJSON, err := json.Marshal(updatedPlans)
    if err != nil {
        log.Printf("Error marshaling updated plans: %v", err)
        utils.SendErrorResponse(w, http.StatusInternalServerError, "Error processing updated plans")
        return
    }

    if err := h.updateCheckoutPlans(checkoutID, string(updatedJSON), selectedPlanID); err != nil {
        log.Printf("Error updating checkout plans: %v", err)
        utils.SendErrorResponse(w, http.StatusInternalServerError, "Error updating plans")
        return
    }

    utils.SendSuccessResponse(w, models.APIResponse{
        Status:  "success",
        Message: "Master account linked successfully",
        Data: map[string]interface{}{
            "selected_plan_id": selectedPlanID,
            "updated_plans":    updatedPlans,
        },
    })
}

func (h *LinkAccountHandler) getMasterAccountData(checkoutID string) (map[string]string, error) {
    query := "SELECT username, email FROM checkout_historics WHERE checkout_id = ?"
    var username, email string
    err := h.db.GetDB().QueryRow(query, checkoutID).Scan(&username, &email)
    if err != nil {
        return nil, err
    }
    return map[string]string{
        "username": username,
        "email":   email,
    }, nil
}

func (h *LinkAccountHandler) hasPlanInCart(cart []models.CartItem, planID int) bool {
    for _, item := range cart {
        if item.PlanID == planID {
            return true
        }
    }
    return false
}

func (h *LinkAccountHandler) hasMasterAssociated(plans []PlanNode) bool {
    for _, plan := range plans {
        if plan.IsMaster == 1 {
            return true
        }
    }
    return false
}

func (h *LinkAccountHandler) convertCartItemsToPlanNodes(cart []models.CartItem, plans []models.PlanCart, masterData map[string]string) []PlanNode {
    var planNodes []PlanNode
    
    // Criar mapa de planos para fácil acesso
    planMap := make(map[int]models.PlanCart)
    for _, plan := range plans {
        planMap[plan.ID] = plan
    }

    // Converter cada item do carrinho para PlanNode
    for _, item := range cart {
        if plan, exists := planMap[item.PlanID]; exists {
            // Criar um nó para cada quantidade
            for i := 0; i < item.Quantity; i++ {
                planNode := PlanNode{
                    PlanID:    plan.ID,
                    PlanName:  plan.Name,
                    Annually:  0, // Converter bool para int
                    Username:  "none",
                    Email:     "none",
                    IsMaster:  0,
                }
                if item.IsAnnual {
                    planNode.Annually = 1
                }
                planNodes = append(planNodes, planNode)
            }
        }
    }

    return planNodes
}

func (h *LinkAccountHandler) linkMasterToPlans(plans []PlanNode, masterPlanID int, masterData map[string]string) []PlanNode {
    updated := false
    newPlans := make([]PlanNode, len(plans))
    copy(newPlans, plans)

    for i := range newPlans {
        if newPlans[i].PlanID == masterPlanID && !updated {
            newPlans[i].Username = masterData["username"]
            newPlans[i].Email = masterData["email"]
            newPlans[i].IsMaster = 1
            updated = true
        }
    }
    return newPlans
}

func (h *LinkAccountHandler) updateCheckoutPlans(checkoutID string, plansJSON string, planID int) error {
    query := "UPDATE checkout_historics SET plans_json = ?, plan = ? WHERE checkout_id = ?"
    _, err := h.db.GetDB().Exec(query, plansJSON, planID, checkoutID)
    return err
}