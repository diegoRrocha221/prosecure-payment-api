package models

type Plan struct {
    PlanID      int     `json:"plan_id"`
    PlanName    string  `json:"plan_name"`
    BasePrice   float64 `json:"base_price"`
    Price       float64 `json:"price"`
    Discount    float64 `json:"discount"`
    Annually    int     `json:"anually"`
    Username    string  `json:"username"`
    Email       string  `json:"email"`
    IsMaster    int     `json:"is_master"`
    Quantity    int     `json:"quantity"`
}

type CheckoutData struct {
    ID          string  `json:"checkout_id"`
    Name        string  `json:"name"`
    Email       string  `json:"email"`
    PhoneNumber string  `json:"phoneNumber"`
    Username    string  `json:"username"`
    Passphrase  string  `json:"passphrase"`
    PlansJSON   string  `json:"plans_json"`
    Plans       []Plan  `json:"-"`
    PlanID      int     `json:"plan"`
    ZipCode     string  `json:"zipcode"`
    State       string  `json:"state"`
    City        string  `json:"city"`
    Street      string  `json:"street"`
    Additional  string  `json:"additional"`
    Status      string  `json:"status"`
    Subtotal    float64 `json:"subtotal"`
    Discount    float64 `json:"discount"`
    Total       float64 `json:"total"`
}