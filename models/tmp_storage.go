package models

import "time"

type PaymentDataStorage struct {
	CheckoutID string    `json:"checkout_id"`
	CardNumber string    `json:"card_number"`
	CardExpiry string    `json:"card_expiry"`
	CardCVV    string    `json:"card_cvv"`
	CardName   string    `json:"card_name"`
	CreatedAt  time.Time `json:"created_at"`
}