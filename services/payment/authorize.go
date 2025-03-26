package payment

import (
    "fmt"
    "time"
    "database/sql"
    "github.com/google/uuid"
    "prosecure-payment-api/models"
    "prosecure-payment-api/services/payment/authorizenet"
)

type AuthorizeNetService struct {
    client *authorizenet.Client
    db     *sql.DB
}

func NewAuthorizeNetService(apiLoginID, transactionKey, merchantID, environment string, db *sql.DB) *AuthorizeNetService {
    client := authorizenet.NewClient(apiLoginID, transactionKey, merchantID, environment)
    return &AuthorizeNetService{
        client: client,
        db:     db,
    }
}

func (s *AuthorizeNetService) VoidTransaction(transactionID string) error {
    err := s.client.VoidTransaction(transactionID)
    if err != nil {
        return fmt.Errorf("failed to void transaction: %v", err)
    }
   
    _, err = s.db.Exec(`
        UPDATE transactions
        SET status = 'voided',
            updated_at = NOW()
        WHERE transaction_id = ?`,
        transactionID,
    )
    if err != nil {
        return fmt.Errorf("failed to update transaction status: %v", err)
    }
    return nil
}

func (s *AuthorizeNetService) ProcessInitialCharge(payment *models.PaymentRequest) (*models.TransactionResponse, error) {
    resp, err := s.client.ProcessPayment(payment)
    if err != nil {
        return nil, fmt.Errorf("failed to process payment: %v", err)
    }
   
    _, err = s.db.Exec(`
        INSERT INTO transactions (
            id,
            master_reference,
            amount,
            status,
            transaction_id,
            created_at
        ) VALUES (?, ?, ?, ?, ?, NOW())`,
        uuid.New().String(),
        payment.CheckoutID,
        1.00,
        "authorized",
        resp.TransactionID,
    )
    if err != nil {
        return nil, fmt.Errorf("failed to record transaction: %v", err)
    }
    return resp, nil
}

func (s *AuthorizeNetService) SetupRecurringBilling(payment *models.PaymentRequest, checkout *models.CheckoutData) error {
    response, err := s.client.CreateSubscription(payment, checkout)
    if err != nil {
        return fmt.Errorf("failed to setup recurring billing: %v", err)
    }
   
    nextBillingDate := time.Now().AddDate(0, 1, 0)
    _, err = s.db.Exec(`
        INSERT INTO subscriptions (
            id,
            master_reference,
            subscription_id,
            status,
            next_billing_date,
            created_at
        ) VALUES (?, ?, ?, ?, ?, NOW())`,
        uuid.New().String(),
        checkout.ID,
        response.SubscriptionID,
        "active",
        nextBillingDate,
    )
    if err != nil {
        return fmt.Errorf("failed to record subscription: %v", err)
    }
   
    _, err = s.db.Exec(`
        UPDATE billing_infos
        SET subscription_id = ?
        WHERE master_reference = ?`,
        response.SubscriptionID,
        checkout.ID,
    )
    if err != nil {
        return fmt.Errorf("failed to update billing info: %v", err)
    }
    return nil
}