package models

import "prosecure-payment-api/types"

type PaymentRequest struct {
    CardName      string               `json:"cardname"`
    CardNumber    string               `json:"cardnumber"`
    CVV           string               `json:"cvv"`
    Expiry        string               `json:"expiry"`
    CheckoutID    string               `json:"sid"`
    CustomerEmail string               `json:"email,omitempty"`
    BillingInfo   *types.BillingInfoType `json:"-"` // Alterado de authorizenet.BillingInfoType
    ThreeDSData   *types.ThreeDSData     `json:"threeDSData,omitempty"`
}

type CardData struct {
    Number string
    Expiry string
}

// Use tipos do novo pacote types