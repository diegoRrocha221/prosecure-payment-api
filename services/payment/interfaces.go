package payment

import (
    "prosecure-payment-api/models"
)
type PaymentProcessor interface {
    ProcessPayment(models.PaymentRequest, models.CheckoutData) (models.TransactionResponse, error)
    ValidateCard(models.PaymentRequest) bool
    SetupRecurringBilling(models.PaymentRequest, models.CheckoutData) error
}
type PaymentGateway interface {
    ProcessInitialCharge(models.PaymentRequest) (models.TransactionResponse, error)
    VoidTransaction(string) error
    SetupRecurringBilling(models.PaymentRequest, models.CheckoutData) error
}