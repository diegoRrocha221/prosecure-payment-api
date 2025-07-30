package models

// BillingInfo representa informações de cobrança
type BillingInfo struct {
    FirstName string `json:"firstName"`
    LastName  string `json:"lastName"`
    Address   string `json:"address"`
    City      string `json:"city"`
    State     string `json:"state"`
    Zip       string `json:"zip"`
    Country   string `json:"country"`
}