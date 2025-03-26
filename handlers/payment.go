package handlers

import (
    "context"
    "encoding/json"
    "encoding/base64"
    "fmt"
    "log"
    "net/http"
    "strings"
    "sync"
    "time"
    "github.com/google/uuid"
    "prosecure-payment-api/models"
    "prosecure-payment-api/services/payment"
    "prosecure-payment-api/services/email"
    "prosecure-payment-api/database"
    "prosecure-payment-api/utils"
    "prosecure-payment-api/queue"
    "prosecure-payment-api/types"
)

// Estrutura para armazenar em cache dados de checkout
type checkoutCache struct {
    data      *models.CheckoutData
    timestamp time.Time
}

type PaymentHandler struct {
    db             *database.Connection
    paymentService *payment.Service
    emailService   *email.SMTPService
    queue          *queue.Queue
    checkoutCache  sync.Map // Cache para dados de checkout
}

func NewPaymentHandler(db *database.Connection, ps *payment.Service, es *email.SMTPService, q *queue.Queue) (*PaymentHandler, error) {
    if db == nil {
        return nil, fmt.Errorf("database connection is required")
    }
    if ps == nil {
        return nil, fmt.Errorf("payment service is required")
    }
    if es == nil {
        return nil, fmt.Errorf("email service is required")
    }
    if q == nil {
        return nil, fmt.Errorf("queue is required")
    }

    return &PaymentHandler{
        db:             db,
        paymentService: ps,
        emailService:   es,
        queue:          q,
        checkoutCache:  sync.Map{},
    }, nil
}

func sendErrorResponse(w http.ResponseWriter, status int, message string) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    json.NewEncoder(w).Encode(models.APIResponse{
        Status:  "error",
        Message: message,
    })
}

func sendSuccessResponse(w http.ResponseWriter, response models.APIResponse) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(http.StatusOK)
    json.NewEncoder(w).Encode(response)
}

// ProcessPayment processa um pagamento de forma otimizada
func (h *PaymentHandler) ProcessPayment(w http.ResponseWriter, r *http.Request) {
    requestID := uuid.New().String()
    startTime := time.Now()
    log.Printf("[RequestID: %s] Starting payment processing", requestID)

    // Decodificar a requisição
    var req models.PaymentRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        log.Printf("[RequestID: %s] Invalid request body: %v", requestID, err)
        sendErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %v", err))
        return
    }

    log.Printf("[RequestID: %s] Processing payment for checkout ID: %s", requestID, req.CheckoutID)

    // Verificar se o checkout já foi processado (com timeout reduzido)
    processed, err := h.db.IsCheckoutProcessed(req.CheckoutID)
    
    if err != nil {
        log.Printf("[RequestID: %s] Error checking checkout status: %v", requestID, err)
        sendErrorResponse(w, http.StatusInternalServerError, "Internal server error")
        return
    }

    if processed {
        log.Printf("[RequestID: %s] Checkout already processed: %s", requestID, req.CheckoutID)
        sendSuccessResponse(w, models.APIResponse{
            Status:  "success",
            Message: "Payment has been processed successfully",
        })
        return
    }

    // Tentar adquirir lock para processamento
    acquired, err := h.db.LockCheckout(req.CheckoutID)
    if err != nil {
        log.Printf("[RequestID: %s] Error acquiring lock: %v", requestID, err)
        sendErrorResponse(w, http.StatusInternalServerError, "Internal server error")
        return
    }
    if !acquired {
        log.Printf("[RequestID: %s] Checkout is being processed: %s", requestID, req.CheckoutID)
        sendErrorResponse(w, http.StatusConflict, "Este checkout já está sendo processado")
        return
    }
    
    // Liberar o lock no final da função
    defer h.db.ReleaseLock(req.CheckoutID)
    
    // Obter dados do checkout - primeiro verifica no cache
    var checkout *models.CheckoutData
    if cachedData, found := h.checkoutCache.Load(req.CheckoutID); found {
        cache := cachedData.(checkoutCache)
        // Usar cache se tiver menos de 5 minutos
        if time.Since(cache.timestamp) < 5*time.Minute {
            checkout = cache.data
            log.Printf("[RequestID: %s] Using cached checkout data", requestID)
        }
    }
    
    // Se não encontrou no cache, buscar do banco
    if checkout == nil {
        
        var checkoutErr error
        checkout, checkoutErr = h.db.GetCheckoutData(req.CheckoutID)

        
        if checkoutErr != nil {
            log.Printf("[RequestID: %s] Invalid checkout ID: %v", requestID, checkoutErr)
            sendErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("Invalid checkout ID: %v", checkoutErr))
            return
        }
        
        // Armazenar no cache
        h.checkoutCache.Store(req.CheckoutID, checkoutCache{
            data:      checkout,
            timestamp: time.Now(),
        })
    }

    // Preencher informações adicionais do cliente
    req.CustomerEmail = checkout.Email
    req.BillingInfo = &types.BillingInfoType{
        FirstName:   strings.Split(checkout.Name, " ")[0],
        LastName:    strings.Join(strings.Split(checkout.Name, " ")[1:], " "),
        Address:     checkout.Street,
        City:        checkout.City,
        State:       checkout.State,
        Zip:         checkout.ZipCode,
        Country:     "US",
        PhoneNumber: checkout.PhoneNumber,
    }

    // Validar dados do cartão antes de processar
    if !h.paymentService.ValidateCard(&req) {
        log.Printf("[RequestID: %s] Invalid card data", requestID)
        sendErrorResponse(w, http.StatusBadRequest, "Dados do cartão inválidos: verifique o número, data de validade e código de segurança")
        return
    }

    // Processar apenas a autorização inicial
    resp, err := h.paymentService.ProcessInitialAuthorization(&req)
    if err != nil {
        log.Printf("[RequestID: %s] Payment processing failed: %v", requestID, err)
        sendErrorResponse(w, http.StatusInternalServerError, fmt.Sprintf("Falha no processamento do pagamento: %v", err))
        return
    }

    if !resp.Success {
        log.Printf("[RequestID: %s] Payment unsuccessful: %s", requestID, resp.Message)
        sendErrorResponse(w, http.StatusBadRequest, resp.Message)
        return
    }

    // Armazenar temporariamente os dados do cartão para uso pelo worker
    // Este processo é executado de forma assíncrona para não atrasar a resposta
    go func() {
        ctxTemp, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        
        _, err := h.db.GetDB().ExecContext(ctxTemp,
            `INSERT INTO temp_payment_data
             (checkout_id, card_number, card_expiry, card_cvv, card_name, created_at)
             VALUES (?, ?, ?, ?, ?, NOW())
             ON DUPLICATE KEY UPDATE
             card_number = VALUES(card_number),
             card_expiry = VALUES(card_expiry),
             card_cvv = VALUES(card_cvv),
             card_name = VALUES(card_name),
             created_at = NOW()`,
            checkout.ID, req.CardNumber, req.Expiry, req.CVV, req.CardName)
        
        if err != nil {
            log.Printf("[RequestID: %s] Warning: Failed to store temporary payment data: %v", requestID, err)
        }
    }()

    // Criar contas e enviar emails em background
    go func() {
        if err := h.createAccountsAndNotify(checkout, &req, resp.TransactionID); err != nil {
            log.Printf("[RequestID: %s] Failed to create account: %v", requestID, err)
        }
    }()

    // Enfileirar tarefas em background se a transação foi aprovada
    if resp.Success {
        go h.enqueueBackgroundTasks(requestID, &req, resp.TransactionID)
    } else {
        log.Printf("[RequestID: %s] Transaction not approved. No background tasks will be queued.", requestID)
    }

    log.Printf("[RequestID: %s] Payment processing completed in %v", requestID, time.Since(startTime))

    // Responder sucesso para o cliente rapidamente
    sendSuccessResponse(w, models.APIResponse{
        Status:  "success",
        Message: "Pagamento processado com sucesso",
        Data: map[string]interface{}{
            "transaction_id": resp.TransactionID,
            "checkout_id":   req.CheckoutID,
            "amount":       checkout.Total,
        },
    })
}

// enqueueBackgroundTasks enfileira tarefas em background para void e criação de assinatura
func (h *PaymentHandler) enqueueBackgroundTasks(requestID string, payment *models.PaymentRequest, transactionID string) {
    // Usar um contexto com timeout para evitar bloqueios
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    
    // Criar um grupo de espera para acompanhar as goroutines
    var wg sync.WaitGroup
    
    // Enfileirar job para anular a transação (void)
    wg.Add(1)
    go func() {
        defer wg.Done()
        voidErr := h.queue.Enqueue(ctx, queue.JobTypeVoidTransaction, map[string]interface{}{
            "transaction_id": transactionID,
            "checkout_id":    payment.CheckoutID,
        })
        
        if voidErr != nil {
            log.Printf("[RequestID: %s] Error enqueueing void transaction job: %v", requestID, voidErr)
        } else {
            log.Printf("[RequestID: %s] Successfully queued void job for transaction %s", requestID, transactionID)
        }
    }()
    
    // Enfileirar job para configurar assinatura recorrente
    wg.Add(1)
    go func() {
        defer wg.Done()
        
        subscriptionErr := h.queue.Enqueue(ctx, queue.JobTypeCreateSubscription, map[string]interface{}{
            "checkout_id":    payment.CheckoutID,
            "card_name":      payment.CardName,
            "card_number":    payment.CardNumber,
            "expiry":         payment.Expiry,
            "cvv":            payment.CVV,
            "email":          payment.CustomerEmail,
        })
        
        if subscriptionErr != nil {
            log.Printf("[RequestID: %s] Error enqueueing subscription creation job: %v", requestID, subscriptionErr)
        } else {
            log.Printf("[RequestID: %s] Successfully queued subscription job for checkout %s", requestID, payment.CheckoutID)
        }
    }()
    
    // Aguardar todas as goroutines terminarem
    wg.Wait()
}

// createAccountsAndNotify cria contas de usuário e envia notificações por email
func (h *PaymentHandler) createAccountsAndNotify(checkout *models.CheckoutData, payment *models.PaymentRequest, transactionID string) error {
    startTime := time.Now()
    defer func() {
        log.Printf("Account creation and notifications completed in %v for checkout ID: %s", 
            time.Since(startTime), checkout.ID)
    }()
    
    tx, err := h.db.BeginTransaction()
    if err != nil {
        return fmt.Errorf("failed to begin transaction: %v", err)
    }

    masterUUID := uuid.New().String()
    log.Printf("Generated master UUID: %s", masterUUID)

    names := strings.Fields(checkout.Name)
    firstName := names[0]
    lastName := strings.Join(names[1:], " ")

    // Preparar a conta master
    masterAccount := &models.MasterAccount{
        Name:             firstName,
        LastName:         lastName,
        ReferenceUUID:    masterUUID,
        Email:            checkout.Email,
        Username:         checkout.Username,
        PhoneNumber:      checkout.PhoneNumber,
        IsAnnually:      0,
        IsTrial:         1,
        State:           checkout.State,
        City:            checkout.City,
        Street:          checkout.Street,
        ZipCode:         checkout.ZipCode,
        AdditionalInfo:  checkout.Additional,
        Plan:            checkout.PlanID,
        PurchasedPlans:  checkout.PlansJSON,
        SimultaneousUsers: len(checkout.Plans),
        RenewDate:       time.Now().AddDate(0, 1, 0),
    }

    // Calcular preço total
    var total float64
    for _, plan := range checkout.Plans {
        if plan.Annually == 1 {
            masterAccount.IsAnnually = 1
            total += plan.Price * 10 
        } else {
            total += plan.Price
        }
    }
    masterAccount.TotalPrice = total

    // Salvar a conta master
    if err := tx.SaveMasterAccount(masterAccount); err != nil {
        tx.Rollback()
        return fmt.Errorf("failed to create master account: %v", err)
    }

    // Criar usuário
    user := &models.User{
        MasterReference: masterUUID,
        Username:        checkout.Username,
        Email:          checkout.Email,
        Passphrase:     checkout.Passphrase,
        IsMaster:       1,
        PlanID:         checkout.Plans[0].PlanID,
    }

    if err := tx.SaveUser(user); err != nil {
        tx.Rollback()
        return fmt.Errorf("failed to create user: %v", err)
    }

    // Salvar método de pagamento
    cardData := &models.CardData{
        Number: payment.CardNumber,
        Expiry: payment.Expiry,
    }

    if err := tx.SavePaymentMethod(masterUUID, cardData); err != nil {
        tx.Rollback()
        return fmt.Errorf("failed to save payment method: %v", err)
    }

    // Criar faturas
    trialInvoice := &models.Invoice{
        MasterReference: masterUUID,
        IsTrial:        1,
        Total:          0,
        DueDate:        time.Now(),
        IsPaid:         1,
    }

    if err := tx.SaveInvoice(trialInvoice); err != nil {
        tx.Rollback()
        return fmt.Errorf("failed to create trial invoice: %v", err)
    }

    futureDate := time.Now().AddDate(0, 1, 0)
    futureInvoice := &models.Invoice{
        MasterReference: masterUUID,
        IsTrial:        0,
        Total:          total,
        DueDate:        futureDate,
        IsPaid:         0,
    }

    if err := tx.SaveInvoice(futureInvoice); err != nil {
        tx.Rollback()
        return fmt.Errorf("failed to create future invoice: %v", err)
    }

    // Salvar transação
    if err := tx.SaveTransaction(masterUUID, checkout.ID, 1.00, "authorized", transactionID); err != nil {
        tx.Rollback()
        return fmt.Errorf("failed to save transaction: %v", err)
    }

    // Salvar assinatura (inicialmente vazia)
    if err := tx.SaveSubscription(masterUUID, "pending", futureDate, ""); err != nil {
        tx.Rollback()
        return fmt.Errorf("failed to save subscription: %v", err)
    }

    // Commit da transação
    if err := tx.Commit(); err != nil {
        return fmt.Errorf("failed to commit transaction: %v", err)
    }

    // Gerar código de ativação e preparar URLs
    code := utils.GenerateActivationCode()
    encodedUser := base64.StdEncoding.EncodeToString([]byte(checkout.Username))
    encodedEmail := base64.StdEncoding.EncodeToString([]byte(checkout.Email))
    encodedCode := base64.StdEncoding.EncodeToString([]byte(code))

    // Atualizar código de ativação no DB
    // Esta operação é executada em uma goroutine para não bloquear o fluxo principal
    go func() {
        
        if err := h.db.UpdateUserActivationCode(checkout.Email, checkout.Username, code); err != nil {
            log.Printf("Warning: Failed to update activation code: %v", err)
        }
    }()

    // Criar URL de ativação
    activationURL := fmt.Sprintf(
        "https://prosecurelsp.com/users/active/activation.php?act=%s&emp=%s&cct=%s",
        encodedUser, encodedEmail, encodedCode,
    )

    // Preparar conteúdo dos emails
    activationEmailContent := h.generateActivationEmail(checkout.Username, activationURL)
    invoiceEmailContent := h.generateInvoiceEmail(checkout)

    // Enviar emails em paralelo
    var wg sync.WaitGroup
    var emailErrors []error

    // Enviar email de ativação
    wg.Add(1)
    go func() {
        defer wg.Done()
        if err := h.emailService.SendEmail(
            checkout.Email,
            "Please Confirm Your Email Address",
            activationEmailContent,
        ); err != nil {
            log.Printf("Warning: Failed to send activation email: %v", err)
            emailErrors = append(emailErrors, err)
        }
    }()

    // Enviar email de fatura
    wg.Add(1)
    go func() {
        defer wg.Done()
        if err := h.emailService.SendEmail(
            checkout.Email,
            "Your invoice has been delivered :)",
            invoiceEmailContent,
        ); err != nil {
            log.Printf("Warning: Failed to send invoice email: %v", err)
            emailErrors = append(emailErrors, err)
        }
    }()

    // Aguardar todos os emails serem enviados
    wg.Wait()

    // Se houver erros nos emails, retornar o primeiro
    if len(emailErrors) > 0 {
        return fmt.Errorf("email notification error: %v", emailErrors[0])
    }

    log.Printf("Successfully created accounts and sent notifications for master reference: %s", masterUUID)
    return nil
}

func (h *PaymentHandler) generateActivationEmail(username, activationURL string) string {
    content := fmt.Sprintf(
        "In order to activate your account, we need to confirm your email address. Once we do, "+
            "you will be able to log into your Administrator Portal and begin setting up your devices "+
            "on the most advanced security service on the planet.",
    )
    
    footer := "Thank you so much,\nThe ProSecureLSP Team"
    
    return fmt.Sprintf(
        email.ActivationEmailTemplate,
        username,
        content,
        activationURL,
        footer,
    )
}

func (h *PaymentHandler) generateInvoiceEmail(checkout *models.CheckoutData) string {
    var total float64
    plansTable := "<table><thead><tr><th>Plans</th><th>Price</th></tr></thead><tbody>"
    
    for _, plan := range checkout.Plans {
        if plan.Annually == 1 {
            total += plan.Price * 10
        } else {
            total += plan.Price
        }
        plansTable += fmt.Sprintf("<tr><td>%s</td><td>$%.2f</td></tr>", plan.PlanName, plan.Price)
    }
    plansTable += "</tbody></table>"

    totalsSection := fmt.Sprintf(`
        <p><strong>Subtotal:</strong> $%.2f</p>
        <p><strong>Discount:</strong> $%.2f</p>
        <p><strong>Tax validation card (refunded):</strong> $0.01</p>
        <p><strong>Total:</strong> $0.01</p>
    `, total, total-0.01)

    footer := fmt.Sprintf(
        "Thank you %s for choosing our services. If you have any questions, please contact our support team.",
        checkout.Name,
    )

    return fmt.Sprintf(
        email.InvoiceEmailTemplate,
        time.Now().Format("20060102150405"),
        plansTable,
        totalsSection,
        "Paid",
        footer,
    )
}

// ResetCheckoutStatus redefine o status de um checkout
func (h *PaymentHandler) ResetCheckoutStatus(w http.ResponseWriter, r *http.Request) {
    var req struct {
        CheckoutID string `json:"checkout_id"`
    }

    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        sendErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %v", err))
        return
    }

    if req.CheckoutID == "" {
        sendErrorResponse(w, http.StatusBadRequest, "Missing checkout ID")
        return
    }

    // Liberar qualquer lock neste checkout
    if err := h.db.ReleaseLock(req.CheckoutID); err != nil {
        sendErrorResponse(w, http.StatusInternalServerError, fmt.Sprintf("Failed to reset checkout status: %v", err))
        return
    }

    // Remover do cache se existir
    h.checkoutCache.Delete(req.CheckoutID)

    sendSuccessResponse(w, models.APIResponse{
        Status:  "success",
        Message: "Checkout status reset successfully",
    })
}

// GenerateCheckoutID gera um novo ID de checkout
func (h *PaymentHandler) GenerateCheckoutID(w http.ResponseWriter, r *http.Request) {
    checkoutID := uuid.New().String()

    sendSuccessResponse(w, models.APIResponse{
        Status: "success",
        Data: map[string]interface{}{
            "checkout_id": checkoutID,
        },
    })
}

// UpdateCheckoutID atualiza um checkout com um novo ID
func (h *PaymentHandler) UpdateCheckoutID(w http.ResponseWriter, r *http.Request) {
    var req struct {
        OldID string `json:"old_id"`
        NewID string `json:"new_id"`
    }

    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        sendErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %v", err))
        return
    }
    
    if req.OldID == "" || req.NewID == "" {
        sendErrorResponse(w, http.StatusBadRequest, "Missing old_id or new_id parameter")
        return
    }

    // Em uma implementação real, você atualizaria o ID do checkout no banco de dados
    // Para este exemplo, apenas retornaremos sucesso
    sendSuccessResponse(w, models.APIResponse{
        Status:  "success",
        Message: fmt.Sprintf("Checkout ID updated from %s to %s", req.OldID, req.NewID),
    })
}

// CheckCheckoutStatus verifica o status de um checkout
func (h *PaymentHandler) CheckCheckoutStatus(w http.ResponseWriter, r *http.Request) {
    checkoutID := r.URL.Query().Get("id")
    if checkoutID == "" {
        sendErrorResponse(w, http.StatusBadRequest, "Missing checkout ID")
        return
    }

    // Criar contexto com timeout para evitar esperas longas

    // Verificar se o checkout foi processado
    processed, err := h.db.IsCheckoutProcessed(checkoutID)
    if err != nil {
        sendErrorResponse(w, http.StatusInternalServerError, fmt.Sprintf("Error checking checkout status: %v", err))
        return
    }

    // Verificar se o checkout está bloqueado
    locked, err := h.isCheckoutLocked(checkoutID)
    if err != nil {
        sendErrorResponse(w, http.StatusInternalServerError, fmt.Sprintf("Error checking checkout lock: %v", err))
        return
    }

    sendSuccessResponse(w, models.APIResponse{
        Status: "success",
        Data: map[string]interface{}{
            "checkout_id": checkoutID,
            "processed":   processed,
            "locked":      locked,
        },
    })
}

// Helper para verificar se um checkout está bloqueado
func (h *PaymentHandler) isCheckoutLocked(checkoutID string) (bool, error) {
    // Tentar adquirir o lock - se não conseguirmos, está bloqueado
    acquired, err := h.db.LockCheckout(checkoutID)
    if err != nil {
        return false, err
    }
    
    // Se adquirimos o lock, libere-o e retorne false (não bloqueado)
    if acquired {
        if err := h.db.ReleaseLock(checkoutID); err != nil {
            return false, err
        }
        return false, nil
    }
    
    // Não conseguimos adquirir o lock, então está bloqueado
    return true, nil
}