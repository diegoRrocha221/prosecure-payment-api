package types

// BillingInfoType representa informações de cobrança para um pagamento
type BillingInfoType struct {
    FirstName   string `json:"firstName,omitempty"`
    LastName    string `json:"lastName,omitempty"`
    Address     string `json:"address,omitempty"`
    City        string `json:"city,omitempty"`
    State       string `json:"state,omitempty"`
    Zip         string `json:"zip,omitempty"`
    Country     string `json:"country,omitempty"`
    PhoneNumber string `json:"phoneNumber,omitempty"`
}

// ThreeDSData representa dados de 3D Secure para pagamentos
type ThreeDSData struct {
    Enabled bool   `json:"enabled"`
    Browser string `json:"browser"`
    Version string `json:"version"`
}

// ThreeDSResponse representa a resposta de autenticação 3D Secure
type ThreeDSResponse struct {
    AcsUrl   string `json:"acsUrl"`   // URL para autenticação 3DS
    Payload  string `json:"payload"`  // Dados para enviar ao ACS
    TransID  string `json:"transId"`  // ID da transação para referência
}

// ThreeDSCallback representa um callback de 3D Secure
type ThreeDSCallback struct {
    TransID  string `json:"transId"`
    Status   string `json:"status"`
    Payload  string `json:"payload"`
}