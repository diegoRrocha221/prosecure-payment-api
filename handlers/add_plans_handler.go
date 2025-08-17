package handlers

import (
    "context"
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "time"

    "prosecure-payment-api/database"
    "prosecure-payment-api/middleware"
    "prosecure-payment-api/models"
    "prosecure-payment-api/services/payment"
    "prosecure-payment-api/utils"
)

type AddPlansHandler struct {
    db             *database.Connection
    paymentService *payment.Service
}

type AddPlansRequest struct {
    Cart []CartPlan `json:"cart"`
}

type CartPlan struct {
    PlanID   int `json:"plan_id"`
    Quantity int `json:"quantity"`
}

type PlanCalculation struct {
    PlanID       int     `json:"plan_id"`
    PlanName     string  `json:"plan_name"`
    BasePrice    float64 `json:"base_price"`
    Quantity     int     `json:"quantity"`
    ProRata      float64 `json:"prorata"`
    TotalProRata float64 `json:"total_prorata"`
    MonthlyPrice float64 `json:"monthly_price"`
    TotalMonthly float64 `json:"total_monthly"`
}

type AddPlansResponse struct {
    Success           bool               `json:"success"`
    Message          string             `json:"message"`
    ProRataCharged   float64            `json:"prorata_charged"`
    MonthlyIncrease  float64            `json:"monthly_increase"`
    TransactionID    string             `json:"transaction_id"`
    NewMonthlyTotal  float64            `json:"new_monthly_total"`
    PlanDetails      []PlanCalculation  `json:"plan_details"`
    UserType         string             `json:"user_type"`
}

type PurchasedPlan struct {
    PlanID   int    `json:"plan_id"`
    PlanName string `json:"plan_name"`
    Annually int    `json:"anually"` 
    Username string `json:"username"`
    Email    string `json:"email"`
    IsMaster int    `json:"is_master"`
}

func NewAddPlansHandler(db *database.Connection, ps *payment.Service) *AddPlansHandler {
    return &AddPlansHandler{
        db:             db,
        paymentService: ps,
    }
}

func (h *AddPlansHandler) PreviewAddPlans(w http.ResponseWriter, r *http.Request) {
    user := middleware.GetUserFromContext(r.Context())
    if user == nil {
        utils.SendErrorResponse(w, http.StatusInternalServerError, "User not found")
        return
    }

    if !user.IsMaster {
        utils.SendErrorResponse(w, http.StatusForbidden, "Only master accounts can add plans")
        return
    }

    var req AddPlansRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        utils.SendErrorResponse(w, http.StatusBadRequest, "Invalid request body")
        return
    }

    if len(req.Cart) == 0 {
        utils.SendSuccessResponse(w, models.APIResponse{
            Status: "success",
            Data: AddPlansResponse{
                ProRataCharged:  0,
                MonthlyIncrease: 0,
                PlanDetails:     []PlanCalculation{},
            },
        })
        return
    }

    // Buscar dados da conta master
    masterAccount, err := h.getMasterAccountData(user.Username, user.Email)
    if err != nil {
        utils.SendErrorResponse(w, http.StatusNotFound, "Account not found")
        return
    }

    // Determinar se é usuário anual baseado no purchased_plans JSON
    isAnnualUser, err := h.isAnnualUser(masterAccount.PurchasedPlans)
    if err != nil {
        utils.SendErrorResponse(w, http.StatusInternalServerError, "Error determining billing type")
        return
    }

    // Calcular custos sem processar pagamento
    planCalculations, totalProRata, totalMonthlyIncrease, err := h.calculatePlansFromDatabase(req.Cart, isAnnualUser, masterAccount.RenewDate)
    if err != nil {
        utils.SendErrorResponse(w, http.StatusBadRequest, err.Error())
        return
    }

    userType := "monthly"
    if isAnnualUser {
        userType = "annual"
    }

    response := AddPlansResponse{
        Success:         true,
        ProRataCharged:  totalProRata,
        MonthlyIncrease: totalMonthlyIncrease,
        NewMonthlyTotal: masterAccount.TotalPrice + totalMonthlyIncrease,
        PlanDetails:     planCalculations,
        UserType:        userType,
    }

    utils.SendSuccessResponse(w, models.APIResponse{
        Status: "success",
        Data:   response,
    })
}

func (h *AddPlansHandler) AddPlans(w http.ResponseWriter, r *http.Request) {
    user := middleware.GetUserFromContext(r.Context())
    if user == nil {
        utils.SendErrorResponse(w, http.StatusInternalServerError, "User not found")
        return
    }

    if !user.IsMaster {
        utils.SendErrorResponse(w, http.StatusForbidden, "Only master accounts can add plans")
        return
    }

    var req AddPlansRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        utils.SendErrorResponse(w, http.StatusBadRequest, "Invalid request body")
        return
    }

    if len(req.Cart) == 0 {
        utils.SendErrorResponse(w, http.StatusBadRequest, "Cart is empty")
        return
    }

    log.Printf("Processing add plans for user: %s, cart items: %d", user.Username, len(req.Cart))

    // 1. Buscar dados da conta master
    masterAccount, err := h.getMasterAccountData(user.Username, user.Email)
    if err != nil {
        log.Printf("Error getting master account: %v", err)
        utils.SendErrorResponse(w, http.StatusNotFound, "Account not found")
        return
    }

    // 2. Determinar se é usuário anual baseado no purchased_plans JSON
    isAnnualUser, err := h.isAnnualUser(masterAccount.PurchasedPlans)
    if err != nil {
        log.Printf("Error determining billing type: %v", err)
        utils.SendErrorResponse(w, http.StatusInternalServerError, "Error determining billing type")
        return
    }

    // 3. Buscar Customer Profile
    customerProfile, err := h.db.GetCustomerProfile(masterAccount.ReferenceUUID)
    if err != nil {
        log.Printf("Error getting customer profile: %v", err)
        utils.SendErrorResponse(w, http.StatusNotFound, "Customer profile not found")
        return
    }

    // 4. Buscar planos do banco de dados e calcular custos
    planCalculations, totalProRata, totalMonthlyIncrease, err := h.calculatePlansFromDatabase(req.Cart, isAnnualUser, masterAccount.RenewDate)
    if err != nil {
        log.Printf("Error calculating plans: %v", err)
        utils.SendErrorResponse(w, http.StatusBadRequest, err.Error())
        return
    }

    if totalProRata <= 0 {
        utils.SendErrorResponse(w, http.StatusBadRequest, "No charges calculated")
        return
    }

    // 5. Fazer cobrança pro-rata usando Customer Profile
    transactionID, err := h.chargeCustomerProfile(customerProfile.AuthorizeCustomerProfileID, 
        customerProfile.AuthorizePaymentProfileID, totalProRata, masterAccount)
    if err != nil {
        log.Printf("Error charging customer profile: %v", err)
        utils.SendErrorResponse(w, http.StatusPaymentRequired, fmt.Sprintf("Payment failed: %v", err))
        return
    }

    // 6. Atualizar dados no banco de dados
    err = h.updateAccountWithNewPlans(masterAccount, req.Cart, planCalculations, totalMonthlyIncrease, transactionID, isAnnualUser)
    if err != nil {
        log.Printf("Error updating account: %v", err)
        utils.SendErrorResponse(w, http.StatusInternalServerError, "Failed to update account")
        return
    }

    // 7. Atualizar ARB subscription com novo valor
    newMonthlyTotal := masterAccount.TotalPrice + totalMonthlyIncrease
    err = h.updateARBSubscription(masterAccount.ReferenceUUID, newMonthlyTotal)
    if err != nil {
        log.Printf("Warning: Failed to update ARB subscription: %v", err)
    }

    userType := "monthly"
    if isAnnualUser {
        userType = "annual"
    }

    response := AddPlansResponse{
        Success:           true,
        Message:          "Plans added successfully",
        ProRataCharged:   totalProRata,
        MonthlyIncrease:  totalMonthlyIncrease,
        TransactionID:    transactionID,
        NewMonthlyTotal:  newMonthlyTotal,
        PlanDetails:      planCalculations,
        UserType:         userType,
    }

    utils.SendSuccessResponse(w, models.APIResponse{
        Status:  "success",
        Message: "Plans added successfully",
        Data:    response,
    })
}

func (h *AddPlansHandler) isAnnualUser(purchasedPlansJSON string) (bool, error) {
    if purchasedPlansJSON == "" {
        return false, nil // Default para mensal se não houver planos
    }

    var plans []PurchasedPlan
    if err := json.Unmarshal([]byte(purchasedPlansJSON), &plans); err != nil {
        return false, fmt.Errorf("error parsing purchased_plans JSON: %v", err)
    }

    if len(plans) == 0 {
        return false, nil // Default para mensal se não houver planos
    }

    // Verificar o primeiro plano para determinar o tipo de billing
    // Como explicado, todos os planos de um usuário têm o mesmo tipo (anual ou mensal)
    isAnnual := plans[0].Annually == 1
    
    log.Printf("User billing type determined: %s (based on %d plans)", 
        map[bool]string{true: "annual", false: "monthly"}[isAnnual], len(plans))
    
    return isAnnual, nil
}

func (h *AddPlansHandler) calculatePlansFromDatabase(cart []CartPlan, isAnnualUser bool, renewDate time.Time) ([]PlanCalculation, float64, float64, error) {
    var planCalculations []PlanCalculation
    var totalProRata, totalMonthlyIncrease float64

    currentDate := time.Now()
    
    // Calcular pro-rata baseado no tipo de usuário
    var proRataFactor float64
    
    if isAnnualUser {
        // Para usuários anuais: pro-rata baseado na data de renovação
        if renewDate.After(currentDate) {
            daysUntilRenewal := int(renewDate.Sub(currentDate).Hours() / 24)
            daysInYear := 365
            proRataFactor = float64(daysUntilRenewal) / float64(daysInYear)
            log.Printf("Annual user - days until renewal: %d, prorata factor: %.3f", daysUntilRenewal, proRataFactor)
        } else {
            // Se a data de renovação já passou, considerar como vencida
            proRataFactor = 1.0
        }
    } else {
        // Para usuários mensais: pro-rata pelos dias restantes no mês atual
        year, month, _ := currentDate.Date()
        firstOfNextMonth := time.Date(year, month+1, 1, 0, 0, 0, 0, currentDate.Location())
        totalDaysInMonth := firstOfNextMonth.AddDate(0, 0, -1).Day()
        dayOfMonth := currentDate.Day()
        remainingDays := totalDaysInMonth - dayOfMonth + 1
        
        proRataFactor = float64(remainingDays) / float64(totalDaysInMonth)
        log.Printf("Monthly user - total days: %d, current day: %d, remaining days: %d, prorata factor: %.3f", 
            totalDaysInMonth, dayOfMonth, remainingDays, proRataFactor)
    }

    for _, item := range cart {
        if item.Quantity <= 0 {
            return nil, 0, 0, fmt.Errorf("invalid quantity for plan %d", item.PlanID)
        }

        // Buscar plano do banco de dados
        plan, err := h.db.GetPlanByID(item.PlanID)
        if err != nil {
            return nil, 0, 0, fmt.Errorf("plan %d not found", item.PlanID)
        }

        basePrice := plan.Price
        var monthlyPrice, proRataPerUnit float64

        if isAnnualUser {
            // Para usuários anuais, todos os planos são cobrados anualmente
            monthlyPrice = basePrice * 10 // Preço anual (10 meses)
            proRataPerUnit = monthlyPrice * proRataFactor
        } else {
            // Para usuários mensais, todos os planos são cobrados mensalmente
            monthlyPrice = basePrice
            proRataPerUnit = monthlyPrice * proRataFactor
        }

        totalProRataForPlan := proRataPerUnit * float64(item.Quantity)
        totalMonthlyForPlan := monthlyPrice * float64(item.Quantity)

        planCalc := PlanCalculation{
            PlanID:       item.PlanID,
            PlanName:     plan.Name,
            BasePrice:    basePrice,
            Quantity:     item.Quantity,
            ProRata:      proRataPerUnit,
            TotalProRata: totalProRataForPlan,
            MonthlyPrice: monthlyPrice,
            TotalMonthly: totalMonthlyForPlan,
        }

        planCalculations = append(planCalculations, planCalc)
        totalProRata += totalProRataForPlan
        totalMonthlyIncrease += totalMonthlyForPlan

        log.Printf("Plan %d (%s): base=%.2f, monthly=%.2f, prorata=%.2f, qty=%d, total_prorata=%.2f, total_monthly=%.2f", 
            item.PlanID, plan.Name, basePrice, monthlyPrice, proRataPerUnit, item.Quantity, totalProRataForPlan, totalMonthlyForPlan)
    }

    return planCalculations, utils.Round(totalProRata), utils.Round(totalMonthlyIncrease), nil
}

func (h *AddPlansHandler) getMasterAccountData(username, email string) (*models.MasterAccount, error) {
    query := `
        SELECT reference_uuid, name, lname, email, username, phone_number,
               state, city, street, zip_code, additional_info, total_price,
               is_annually, plan, purchased_plans, simultaneus_users, renew_date
        FROM master_accounts 
        WHERE username = ? AND email = ?
    `

    var account models.MasterAccount
    err := h.db.GetDB().QueryRow(query, username, email).Scan(
        &account.ReferenceUUID, &account.Name, &account.LastName,
        &account.Email, &account.Username, &account.PhoneNumber,
        &account.State, &account.City, &account.Street,
        &account.ZipCode, &account.AdditionalInfo, &account.TotalPrice,
        &account.IsAnnually, &account.Plan, &account.PurchasedPlans,
        &account.SimultaneousUsers, &account.RenewDate,
    )

    return &account, err
}

func (h *AddPlansHandler) chargeCustomerProfile(customerProfileID, paymentProfileID string, amount float64, account *models.MasterAccount) (string, error) {
    log.Printf("Charging customer profile %s/%s amount: $%.2f", customerProfileID, paymentProfileID, amount)

    billingInfo := &models.BillingInfo{
        FirstName: account.Name,
        LastName:  account.LastName,
        Address:   account.Street,
        City:      account.City,
        State:     account.State,
        Zip:       account.ZipCode,
        Country:   "US",
    }

    return h.paymentService.ChargeCustomerProfile(customerProfileID, paymentProfileID, amount, billingInfo)
}

func (h *AddPlansHandler) updateAccountWithNewPlans(account *models.MasterAccount, cart []CartPlan, planCalculations []PlanCalculation, monthlyIncrease float64, transactionID string, isAnnualUser bool) error {
    tx, err := h.db.BeginTransaction()
    if err != nil {
        return fmt.Errorf("failed to begin transaction: %v", err)
    }
    defer func() {
        if err != nil {
            tx.Rollback()
        }
    }()

    // Parse purchased plans existentes
    var existingPlans []PurchasedPlan
    if account.PurchasedPlans != "" {
        if err := json.Unmarshal([]byte(account.PurchasedPlans), &existingPlans); err != nil {
            return fmt.Errorf("failed to parse existing plans: %v", err)
        }
    }

    // Adicionar novos planos
    simultaneousUsersIncrease := 0
    annuallyFlag := 0
    if isAnnualUser {
        annuallyFlag = 1
    }

    for _, item := range cart {
        // Buscar dados do plano calculado
        var planCalc *PlanCalculation
        for i := range planCalculations {
            if planCalculations[i].PlanID == item.PlanID {
                planCalc = &planCalculations[i]
                break
            }
        }
        
        if planCalc == nil {
            return fmt.Errorf("plan calculation not found for plan %d", item.PlanID)
        }

        for i := 0; i < item.Quantity; i++ {
            newPlan := PurchasedPlan{
                PlanID:   planCalc.PlanID,
                PlanName: planCalc.PlanName,
                Annually: annuallyFlag, // Usar o tipo de billing do usuário
                Username: "none",
                Email:    "none",
                IsMaster: 0,
            }
            existingPlans = append(existingPlans, newPlan)
            simultaneousUsersIncrease++
        }
    }

    updatedPlansJSON, err := json.Marshal(existingPlans)
    if err != nil {
        return fmt.Errorf("failed to marshal updated plans: %v", err)
    }

    // Atualizar master account
    newTotalPrice := account.TotalPrice + monthlyIncrease
    _, err = h.db.GetDB().Exec(`
        UPDATE master_accounts 
        SET purchased_plans = ?, total_price = ?, simultaneus_users = simultaneus_users + ?
        WHERE reference_uuid = ?`,
        string(updatedPlansJSON), newTotalPrice, simultaneousUsersIncrease, account.ReferenceUUID)
    
    if err != nil {
        return fmt.Errorf("failed to update master account: %v", err)
    }

    // Registrar transação
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    
    _, err = h.db.GetDB().ExecContext(ctx, `
        INSERT INTO transactions (id, master_reference, checkout_id, amount, status, transaction_id, created_at)
        VALUES (UUID(), ?, 'ADD_PLANS', ?, 'captured', ?, NOW())`,
        account.ReferenceUUID, utils.Round(monthlyIncrease), transactionID)
    
    if err != nil {
        return fmt.Errorf("failed to save transaction: %v", err)
    }

    // Criar nova invoice para os planos adicionados
    dueDate := time.Now().AddDate(0, 1, 0)
    if isAnnualUser {
        dueDate = time.Now().AddDate(1, 0, 0) // 1 ano para usuários anuais
    }

    _, err = h.db.GetDB().ExecContext(ctx, `
        INSERT INTO invoices (master_reference, is_trial, total, due_date, is_paid, created_at)
        VALUES (?, 0, ?, ?, 0, NOW())`,
        account.ReferenceUUID, utils.Round(monthlyIncrease), dueDate)
    
    if err != nil {
        return fmt.Errorf("failed to create invoice: %v", err)
    }

    return tx.Commit()
}

func (h *AddPlansHandler) updateARBSubscription(masterReference string, newMonthlyTotal float64) error {
    // Buscar subscription ID
    var subscriptionID string
    err := h.db.GetDB().QueryRow(
        "SELECT subscription_id FROM subscriptions WHERE master_reference = ? AND status = 'active'",
        masterReference).Scan(&subscriptionID)
    
    if err != nil {
        return fmt.Errorf("subscription not found: %v", err)
    }

    // Atualizar subscription na Authorize.net
    return h.paymentService.UpdateSubscriptionAmount(subscriptionID, newMonthlyTotal)
}