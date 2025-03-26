package payment

import (
    "errors"
    "fmt"
    "log"
    "time"
    "prosecure-payment-api/models"
    "prosecure-payment-api/services/payment/authorizenet"
)

type Service struct {
    client *authorizenet.Client
}

func NewPaymentService(apiLoginID, transactionKey, merchantID, environment string) *Service {
    client := authorizenet.NewClient(apiLoginID, transactionKey, merchantID, environment)
    return &Service{
        client: client,
    }
}

// ProcessInitialAuthorization only performs the initial $1 authorization without void or subscription
func (s *Service) ProcessInitialAuthorization(payment *models.PaymentRequest) (*models.TransactionResponse, error) {
    log.Printf("Starting initial payment authorization for checkout ID: %s", payment.CheckoutID)

    if !s.ValidateCard(payment) {
        return nil, errors.New("invalid card data: please check card number, expiration date and CVV")
    }

    // Process initial charge
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

// ProcessPayment performs the complete payment flow (authorization, void, and subscription)
// This is the original method kept for compatibility
func (s *Service) ProcessPayment(payment *models.PaymentRequest, checkout *models.CheckoutData) (*models.TransactionResponse, error) {
    log.Printf("Starting payment processing for checkout ID: %s", payment.CheckoutID)

    if !s.ValidateCard(payment) {
        return nil, errors.New("invalid card data: please check card number, expiration date and CVV")
    }

    // Process initial charge
    resp, err := s.client.ProcessPayment(payment)
    if err != nil {
        log.Printf("Error processing payment: %v", err)
        return nil, fmt.Errorf("payment processing failed: %v", err)
    }

    if !resp.Success {
        log.Printf("Payment unsuccessful: %s", resp.Message)
        return resp, nil
    }

    // Void the transaction
    log.Printf("Payment successful, voiding transaction: %s", resp.TransactionID)
    if err := s.client.VoidTransaction(resp.TransactionID); err != nil {
        log.Printf("Error voiding transaction: %v", err)
        return nil, fmt.Errorf("failed to void initial transaction: %v", err)
    }

    // Setup recurring billing
    log.Printf("Setting up recurring billing for checkout ID: %s", payment.CheckoutID)
    if err := s.SetupRecurringBilling(payment, checkout); err != nil {
        log.Printf("Error setting up recurring billing: %v", err)
        // Try to void the transaction again to ensure it's not hanging
        if voidErr := s.client.VoidTransaction(resp.TransactionID); voidErr != nil {
            log.Printf("Error voiding transaction after recurring billing failure: %v", voidErr)
        }
        return nil, fmt.Errorf("failed to setup recurring billing: %v", err)
    }

    return resp, nil
}

// VoidTransaction voids a previously authorized transaction
func (s *Service) VoidTransaction(transactionID string) error {
    log.Printf("Voiding transaction: %s", transactionID)
    return s.client.VoidTransaction(transactionID)
}

func (s *Service) ValidateCard(payment *models.PaymentRequest) bool {
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

    return true
}

func (s *Service) SetupRecurringBilling(payment *models.PaymentRequest, checkout *models.CheckoutData) error {
    if !s.ValidateCard(payment) {
        return errors.New("invalid card data for recurring billing setup")
    }

    // CreateSubscription will return error if the operation fails
    subscription, err := s.client.CreateSubscription(payment, checkout)
    if err != nil {
        return fmt.Errorf("failed to setup recurring billing: %v", err)
    }

    if subscription == nil || subscription.SubscriptionID == "" {
        return errors.New("subscription creation failed: no subscription ID returned")
    }

    log.Printf("Successfully created subscription with ID: %s", subscription.SubscriptionID)
    return nil
}

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