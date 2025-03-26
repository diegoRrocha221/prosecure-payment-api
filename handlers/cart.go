// handlers/cart.go
package handlers


import (
    "encoding/gob"
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "sort"
    "strconv"
    
    "github.com/gorilla/sessions"
    "prosecure-payment-api/database"
    "prosecure-payment-api/models"
    "prosecure-payment-api/config"
)

func init() {
    gob.Register([]models.CartItem{})
}

type CartHandler struct {
    db    *database.Connection
    store *sessions.CookieStore
}

func NewCartHandler(db *database.Connection, cfg *config.Config) *CartHandler {
    store := sessions.NewCookieStore([]byte(cfg.Session.Secret))
    store.Options = &sessions.Options{
        Path:     "/",
        Domain:   cfg.Session.Domain,    
        MaxAge:   cfg.Session.MaxAge, 
        Secure:   true,                 
        HttpOnly: true,                  
        SameSite: http.SameSiteLaxMode, 
    }
    return &CartHandler{db: db, store: store}
}

func (h *CartHandler) AddToCart(w http.ResponseWriter, r *http.Request) {
    session, err := h.store.Get(r, "cart-session")
    if err != nil {
        log.Printf("Error getting session: %v", err)
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    
    var item models.CartItem
    if err := json.NewDecoder(r.Body).Decode(&item); err != nil {
        log.Printf("Error decoding request body: %v", err)
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }
 
    // Verifica se o plano existe
    _, err = h.db.GetPlanByID(item.PlanID)
    if err != nil {
        log.Printf("Plan not found: %v", err)
        http.Error(w, "Plan not found", http.StatusNotFound)
        return
    }
 
    cart, ok := session.Values["cart"].([]models.CartItem)
    if !ok {
        cart = []models.CartItem{}
    }

    if len(cart) > 0 {
        if cart[0].IsAnnual != item.IsAnnual {
            if item.IsAnnual {
                http.Error(w, "Cannot add annual plan when monthly plans exist in cart", http.StatusBadRequest)
            } else {
                http.Error(w, "Cannot add monthly plan when annual plans exist in cart", http.StatusBadRequest)
            }
            return
        }
    }
 
    // Atualiza quantidade se o item já existe ou adiciona novo item
    found := false
    for i, cartItem := range cart {
        if cartItem.PlanID == item.PlanID {
            cart[i].Quantity += item.Quantity
            found = true
            break
        }
    }
    if !found {
        cart = append(cart, item)
    }
 
    session.Values["cart"] = cart
    if err := session.Save(r, w); err != nil {
        log.Printf("Error saving session: %v", err)
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
 
    log.Printf("Cart after add: %+v", cart)
 
    // Retorna 201 Created
    w.WriteHeader(http.StatusCreated)
 }

func (h *CartHandler) UpdateCart(w http.ResponseWriter, r *http.Request) {
    session, _ := h.store.Get(r, "cart-session")
    
    var update models.CartUpdate
    if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }

    cart, ok := session.Values["cart"].([]models.CartItem)
    if !ok {
        http.Error(w, "Cart not found", http.StatusNotFound)
        return
    }

    for i, item := range cart {
        if item.PlanID == update.PlanID {
            if update.Action == "more" {
                cart[i].Quantity++
            } else if update.Action == "remove" && cart[i].Quantity > 0 {
                cart[i].Quantity--
                if cart[i].Quantity == 0 {
                    cart = append(cart[:i], cart[i+1:]...)
                }
            }
            break
        }
    }

    session.Values["cart"] = cart
    session.Save(r, w)

    w.WriteHeader(http.StatusOK)
}

func (h *CartHandler) RemoveFromCart(w http.ResponseWriter, r *http.Request) {
    session, err := h.store.Get(r, "cart-session")
    if err != nil {
        log.Printf("Error getting session: %v", err)
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    var request struct {
        PlanID int `json:"plan_id"`
    }
    if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
        log.Printf("Error decoding request body: %v", err)
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }

    cart, ok := session.Values["cart"].([]models.CartItem)
    if !ok {
        cart = []models.CartItem{}
    }

    for i, item := range cart {
        if item.PlanID == request.PlanID {
            cart = append(cart[:i], cart[i+1:]...)
            break
        }
    }

    session.Values["cart"] = cart
    if err := session.Save(r, w); err != nil {
        log.Printf("Error saving session: %v", err)
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    w.WriteHeader(http.StatusOK)
}

func (h *CartHandler) GetCart(w http.ResponseWriter, r *http.Request) {
    session, err := h.store.Get(r, "cart-session")
    if err != nil {
        log.Printf("Error getting session: %v", err)
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    
    cart, ok := session.Values["cart"].([]models.CartItem)
    if !ok {
        log.Printf("Cart is empty or invalid. Session values: %+v", session.Values)
        cart = []models.CartItem{}
    }

    log.Printf("Cart contents: %+v", cart)

    plans, err := h.db.GetPlans()
    if err != nil {
        log.Printf("Error getting plans: %v", err)
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    response := h.calculateCartDetails(cart, plans)
    
    w.Header().Set("Content-Type", "application/json")
    responseJSON, _ := json.Marshal(response)
    log.Printf("Response being sent: %s", string(responseJSON))
    w.Write(responseJSON)
}

func (h *CartHandler) calculateCartDetails(cart []models.CartItem, plans []models.PlanCart) models.CartResponse {
    var response models.CartResponse
    var subtotal float64
    
    planMap := make(map[int]models.PlanCart)
    for _, plan := range plans {
        planMap[plan.ID] = plan
    }


    for _, item := range cart {
        if plan, exists := planMap[item.PlanID]; exists {
            price := plan.Price
            if item.IsAnnual {
                price = plan.Price * 10
            }
            subtotal += float64(item.Quantity) * price
            response.Items = append(response.Items, models.CartItemResponse{
                PlanID:          plan.ID,
                PlanImage:       plan.Image,
                PlanName:        plan.Name,
                PlanDescription: plan.Description,
                PlanQuantity:    item.Quantity,
                Price:           price,
                IsAnnual:        item.IsAnnual,
            })
        }
    }

    response.CartSubtotal = subtotal
    discount, shortfall := h.calculateDiscount(cart, planMap)
    response.CartDiscount = discount
    response.ShortfallForDiscount = shortfall
    response.CartTotal = subtotal - discount

    return response
}

func (h *CartHandler) calculateDiscount(cart []models.CartItem, planMap map[int]models.PlanCart) (float64, string) {
    planQuantities := make(map[int]int)
    totalQuantity := 0
    totalValue := float64(0)
    
    for _, item := range cart {
        planQuantities[item.PlanID] += item.Quantity
        totalQuantity += item.Quantity
        if plan, exists := planMap[item.PlanID]; exists {
            price := plan.Price
            if item.IsAnnual {
                price = plan.Price * 10  // Convert to annual price
            }
            totalValue += float64(item.Quantity) * price
        }
    }

    if totalQuantity == 0 {
        return 0, ""
    }

    var currentDiscount float64
    var shortfallMsg string

    // Prioriza o desconto global (tipo 2)
    for _, plan := range planMap {
        if plan.DiscountRuleApplied == 2 {
            var rules []models.DiscountRule
            if err := json.Unmarshal([]byte(plan.SingleDiscount), &rules); err != nil {
                log.Printf("Error unmarshaling single discount rules: %v", err)
                continue
            }

            // Ordenar regras por quantidade crescente
            sort.Slice(rules, func(i, j int) bool {
                qi, _ := strconv.Atoi(rules[i].Qtd)
                qj, _ := strconv.Atoi(rules[j].Qtd)
                return qi < qj
            })

            // Encontrar regra atual e próxima
            var currentRule *models.DiscountRule
            var nextRule *models.DiscountRule

            // Encontrar a regra aplicável atual
            for i := len(rules) - 1; i >= 0; i-- {
                qtd, _ := strconv.Atoi(rules[i].Qtd)
                if totalQuantity >= qtd {
                    currentRule = &rules[i]
                    break
                }
            }

            // Encontrar a próxima regra
            if totalQuantity > 0 {
                for _, rule := range rules {
                    qtd, _ := strconv.Atoi(rule.Qtd)
                    if totalQuantity < qtd {
                        nextRule = &rule
                        break
                    }
                }
            }

            // Calcular desconto atual
            if currentRule != nil {
                percent, _ := strconv.ParseFloat(currentRule.Percent, 64)
                currentDiscount = totalValue * percent / 100
            }

            // Gerar mensagem para próximo desconto
            if nextRule != nil {
                nextQtd, _ := strconv.Atoi(nextRule.Qtd)
                nextPercent, _ := strconv.ParseFloat(nextRule.Percent, 64)
                remainingItems := nextQtd - totalQuantity
                if remainingItems > 0 {
                    shortfallMsg = fmt.Sprintf("Add more %d plans to get %.0f%% discount",
                        remainingItems,
                        nextPercent)
                }
            }

            break 
        }
    }

    return currentDiscount, shortfallMsg
}

func (h *CartHandler) findNextDiscountRule(rules []models.DiscountRule, quantity int) *models.DiscountRule {
    var nextRule *models.DiscountRule

    for i := len(rules) - 1; i >= 0; i-- {
        ruleQtd, _ := strconv.Atoi(rules[i].Qtd)
        if quantity >= ruleQtd {
            if i < len(rules)-1 {
                return &rules[i+1]
            }
            return nil
        }
        nextRule = &rules[i]
    }

    ruleQtd, _ := strconv.Atoi(rules[0].Qtd)
    if quantity < ruleQtd {
        return &rules[0]
    }

    return nextRule
}