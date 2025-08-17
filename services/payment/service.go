package payment

import (
    "errors"
    "fmt"
    "log"
    "sync"
    "time"
    "prosecure-payment-api/models"
    "prosecure-payment-api/services/payment/authorizenet"
)

type Service struct {
    client *authorizenet.Client
    cache  *sync.Map // Para cache de validações de cartão
}

// NewPaymentService cria um novo serviço de pagamento com cache e timeouts otimizados
func NewPaymentService(apiLoginID, transactionKey, merchantID, environment string) *Service {
    client := authorizenet.NewClient(apiLoginID, transactionKey, merchantID, environment)
    return &Service{
        client: client,
        cache:  &sync.Map{},
    }
}

// ProcessInitialAuthorization apenas executa a autorização inicial de $1 sem void ou assinatura
func (s *Service) ProcessInitialAuthorization(payment *models.PaymentRequest) (*models.TransactionResponse, error) {
    log.Printf("Starting initial payment authorization for checkout ID: %s", payment.CheckoutID)
    
    startTime := time.Now()
    defer func() {
        log.Printf("Payment authorization took %v for checkout ID: %s", 
            time.Since(startTime), payment.CheckoutID)
    }()

    if !s.ValidateCard(payment) {
        return nil, errors.New("invalid card data: please check card number, expiration date and CVV")
    }

    // Processo de cobrança inicial com timeout reduzido
    resp, err := s.client.ProcessPayment(payment)
    if err != nil {
        log.Printf("Error processing payment: %v", err)
        return nil, fmt.Errorf("payment processing failed: %v", err)
    }

    if !resp.Success {
        log.Printf("Payment authorization unsuccessful: %s", resp.Message)
        return resp, nil
    }

    log.Printf("Initial payment authorization successful for checkout ID: %s with transaction ID: %s", 
        payment.CheckoutID, resp.TransactionID)
    
    return resp, nil
}

// ProcessPayment executa o fluxo completo de pagamento (autorização, void e assinatura)
// Este é o método original mantido para compatibilidade
func (s *Service) ProcessPayment(payment *models.PaymentRequest, checkout *models.CheckoutData) (*models.TransactionResponse, error) {
    log.Printf("Starting payment processing for checkout ID: %s", payment.CheckoutID)
    
    startTime := time.Now()
    defer func() {
        log.Printf("Full payment processing took %v for checkout ID: %s", 
            time.Since(startTime), payment.CheckoutID)
    }()

    if !s.ValidateCard(payment) {
        return nil, errors.New("invalid card data: please check card number, expiration date and CVV")
    }

    // Processo de cobrança inicial
    resp, err := s.client.ProcessPayment(payment)
    if err != nil {
        log.Printf("Error processing payment: %v", err)
        return nil, fmt.Errorf("payment processing failed: %v", err)
    }

    if !resp.Success {
        log.Printf("Payment unsuccessful: %s", resp.Message)
        return resp, nil
    }

    // Void da transação
    log.Printf("Payment successful, voiding transaction: %s", resp.TransactionID)
    if err := s.client.VoidTransaction(resp.TransactionID); err != nil {
        log.Printf("Error voiding transaction: %v", err)
        return nil, fmt.Errorf("failed to void initial transaction: %v", err)
    }

    // CORRIGIDO: Configurar cobrança recorrente (AGORA COM CUSTOMER PROFILE)
    log.Printf("Setting up recurring billing with customer profile for checkout ID: %s", payment.CheckoutID)
    subscriptionID, err := s.SetupRecurringBilling(payment, checkout)
    if err != nil {
        log.Printf("Error setting up recurring billing: %v", err)
        // Tentar anular a transação novamente para garantir que não esteja pendente
        if voidErr := s.client.VoidTransaction(resp.TransactionID); voidErr != nil {
            log.Printf("Error voiding transaction after recurring billing failure: %v", voidErr)
        }
        return nil, fmt.Errorf("failed to setup recurring billing: %v", err)
    }

    log.Printf("Successfully created subscription with ID: %s for checkout: %s", subscriptionID, payment.CheckoutID)
    return resp, nil
}

// VoidTransaction anula uma transação previamente autorizada
func (s *Service) VoidTransaction(transactionID string) error {
    log.Printf("Voiding transaction: %s", transactionID)
    startTime := time.Now()
    defer func() {
        log.Printf("Void transaction took %v for transaction ID: %s", 
            time.Since(startTime), transactionID)
    }()
    
    return s.client.VoidTransaction(transactionID)
}

// ValidateCard verifica se os dados do cartão são válidos
// Implementa caching para evitar revalidações idênticas
func (s *Service) ValidateCard(payment *models.PaymentRequest) bool {
    // Gerar uma chave de cache (usando apenas 4 últimos dígitos do cartão para evitar armazenar PCI)
    var lastFour string
    if len(payment.CardNumber) > 4 {
        lastFour = payment.CardNumber[len(payment.CardNumber)-4:]
    }
    
    cacheKey := fmt.Sprintf("%s-%s-%s", lastFour, payment.Expiry, payment.CardName)
    
    // Verificar cache
    if _, found := s.cache.Load(cacheKey); found {
        return true
    }
    
    // Validações básicas
    if len(payment.CardNumber) < 13 || len(payment.CardNumber) > 19 {
        log.Printf("Invalid card number length: %d", len(payment.CardNumber))
        return false
    }
   
    if len(payment.CVV) < 3 || len(payment.CVV) > 4 {
        log.Printf("Invalid CVV length: %d", len(payment.CVV))
        return false
    }

    if !validateExpiry(payment.Expiry) {
        log.Printf("Invalid expiry date: %s", payment.Expiry)
        return false
    }

    if len(payment.CardName) < 3 {
        log.Printf("Invalid card name length: %d", len(payment.CardName))
        return false
    }
   
    if !validateLuhn(payment.CardNumber) {
        log.Printf("Failed Luhn check for card number")
        return false
    }

    // Salvar no cache (com expiração implícita pela duração do programa)
    s.cache.Store(cacheKey, true)
    return true
}

// SetupRecurringBilling configura cobrança recorrente USANDO CUSTOMER PROFILE
func (s *Service) SetupRecurringBilling(payment *models.PaymentRequest, checkout *models.CheckoutData) (string, error) {
    if !s.ValidateCard(payment) {
        return "", errors.New("invalid card data for recurring billing setup")
    }

    startTime := time.Now()
    defer func() {
        log.Printf("Subscription setup (with customer profile) took %v for checkout ID: %s", 
            time.Since(startTime), payment.CheckoutID)
    }()

    log.Printf("Setting up recurring billing with customer profile for checkout ID: %s", payment.CheckoutID)

    // NOVO: Usar o método que cria customer profile + subscription
    subscriptionResp, err := s.client.CreateSubscription(payment, checkout)
    if err != nil {
        return "", fmt.Errorf("failed to setup recurring billing with customer profile: %v", err)
    }

    if !subscriptionResp.Success {
        return "", fmt.Errorf("subscription creation failed: %s", subscriptionResp.Message)
    }

    log.Printf("Successfully created subscription with customer profile - Subscription ID: %s", subscriptionResp.SubscriptionID)
    return subscriptionResp.SubscriptionID, nil // CORRIGIDO: Retorna o subscription ID
}

// SetupRecurringBillingDirect configura cobrança recorrente SEM CUSTOMER PROFILE (método legado)
func (s *Service) SetupRecurringBillingDirect(payment *models.PaymentRequest, checkout *models.CheckoutData) (string, error) {
    if !s.ValidateCard(payment) {
        return "", errors.New("invalid card data for recurring billing setup")
    }

    startTime := time.Now()
    defer func() {
        log.Printf("Subscription setup (direct method) took %v for checkout ID: %s", 
            time.Since(startTime), payment.CheckoutID)
    }()

    log.Printf("Setting up recurring billing with direct card data for checkout ID: %s", payment.CheckoutID)

    // Usar o método legado que usa dados de cartão diretos
    subscriptionResp, err := s.client.CreateSubscriptionDirect(payment, checkout)
    if err != nil {
        return "", fmt.Errorf("failed to setup recurring billing (direct method): %v", err)
    }

    if !subscriptionResp.Success {
        return "", fmt.Errorf("subscription creation failed (direct method): %s", subscriptionResp.Message)
    }

    log.Printf("Successfully created subscription (direct method) with ID: %s", subscriptionResp.SubscriptionID)
    return subscriptionResp.SubscriptionID, nil // CORRIGIDO: Retorna o subscription ID
}

// CreateCustomerProfile cria um customer profile na Authorize.net
func (s *Service) CreateCustomerProfile(payment *models.PaymentRequest, checkout *models.CheckoutData) (string, string, error) {
    if !s.ValidateCard(payment) {
        return "", "", errors.New("invalid card data for customer profile creation")
    }

    startTime := time.Now()
    defer func() {
        log.Printf("Customer profile creation took %v for checkout ID: %s", 
            time.Since(startTime), payment.CheckoutID)
    }()

    log.Printf("Creating customer profile for checkout ID: %s", payment.CheckoutID)

    customerProfileID, paymentProfileID, err := s.client.CreateCustomerProfile(payment, checkout)
    if err != nil {
        return "", "", fmt.Errorf("failed to create customer profile: %v", err)
    }

    log.Printf("Successfully created customer profile - Profile ID: %s, Payment Profile ID: %s", 
        customerProfileID, paymentProfileID)
    
    return customerProfileID, paymentProfileID, nil
}

// UpdateCustomerPaymentProfile atualiza método de pagamento em um customer profile existente
func (s *Service) UpdateCustomerPaymentProfile(customerProfileID, paymentProfileID string, payment *models.PaymentRequest, checkout *models.CheckoutData) error {
    if !s.ValidateCard(payment) {
        return errors.New("invalid card data for payment profile update")
    }

    startTime := time.Now()
    defer func() {
        log.Printf("Customer payment profile update took %v for profile: %s/%s", 
            time.Since(startTime), customerProfileID, paymentProfileID)
    }()

    log.Printf("Updating customer payment profile: %s/%s", customerProfileID, paymentProfileID)

    err := s.client.UpdateCustomerPaymentProfile(customerProfileID, paymentProfileID, payment, checkout)
    if err != nil {
        return fmt.Errorf("failed to update customer payment profile: %v", err)
    }

    log.Printf("Successfully updated customer payment profile: %s/%s", customerProfileID, paymentProfileID)
    return nil
}

// CORRIGIDO: ChargeCustomerProfile - Agora aceita CVV e valida
func (s *Service) ChargeCustomerProfile(customerProfileID, paymentProfileID string, amount float64, cvv string) (string, error) {
    log.Printf("Charging customer profile %s/%s amount: $%.2f with CVV validation", customerProfileID, paymentProfileID, amount)
    
    if amount <= 0 {
        return "", fmt.Errorf("invalid amount: %.2f", amount)
    }
    
    // CRÍTICO: Validar CVV
    if cvv == "" || len(cvv) < 3 || len(cvv) > 4 {
        return "", fmt.Errorf("invalid CVV provided: CVV must be 3-4 digits")
    }
    
    startTime := time.Now()
    defer func() {
        log.Printf("Customer profile charge took %v", time.Since(startTime))
    }()
    
    // Enviar CVV para Authorize.net para validação
    return s.client.ChargeCustomerProfile(customerProfileID, paymentProfileID, amount, cvv)
}

// UpdateSubscriptionAmount atualiza o valor de uma subscription ARB
func (s *Service) UpdateSubscriptionAmount(subscriptionID string, newAmount float64) error {
    log.Printf("Updating subscription %s to new amount: $%.2f", subscriptionID, newAmount)
    
    if newAmount <= 0 {
        return fmt.Errorf("invalid amount: %.2f", newAmount)
    }
    
    if subscriptionID == "" {
        return fmt.Errorf("subscription ID is required")
    }
    
    startTime := time.Now()
    defer func() {
        log.Printf("Subscription update took %v", time.Since(startTime))
    }()
    
    return s.client.UpdateSubscription(subscriptionID, newAmount)
}

// Função helper para validar o algoritmo de Luhn para números de cartão
func validateLuhn(cardNumber string) bool {
    sum := 0
    isEven := len(cardNumber)%2 == 0

    for i, r := range cardNumber {
        digit := int(r - '0')
       
        if digit < 0 || digit > 9 {
            return false
        }

        if isEven == (i%2 == 0) {
            digit *= 2
            if digit > 9 {
                digit -= 9
            }
        }
        sum += digit
    }

    return sum%10 == 0
}

// Função helper para validar data de expiração
func validateExpiry(expiry string) bool {
    currentTime := time.Now()
    expiryTime, err := time.Parse("01/06", expiry)
    if err != nil {
        return false
    }
   
    expiryTime = time.Date(
        expiryTime.Year(),
        expiryTime.Month()+1,
        0,
        23,
        59,
        59,
        0,
        time.UTC,
    )
   
    return expiryTime.After(currentTime)
}