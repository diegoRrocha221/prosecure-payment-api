package models

type PaymentRequest struct {
    CardName    string       `json:"cardname"`
    CardNumber  string       `json:"cardnumber"`
    CVV         string       `json:"cvv"`
    Expiry      string       `json:"expiry"`
    CheckoutID  string       `json:"sid"`
    ThreeDSData *ThreeDSData `json:"threeDSData,omitempty"`
}

type CardData struct {
    Number string
    Expiry string
}

type ThreeDSResponse struct {
    AcsUrl   string `json:"acsUrl"`   // URL for 3DS authentication
    Payload  string `json:"payload"`  // Data to send to ACS
    TransID  string `json:"transId"`  // Transaction ID for reference
}

type ThreeDSCallback struct {
    TransID  string `json:"transId"`
    Status   string `json:"status"`
    Payload  string `json:"payload"`
}

type ThreeDSData struct {
    Enabled bool   `json:"enabled"`
    Browser string `json:"browser"`
    Version string `json:"version"`
}