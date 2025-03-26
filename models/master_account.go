package models

import "time"

type MasterAccount struct {
    ID               int       `json:"id"`
    Name             string    `json:"name"`
    LastName         string    `json:"last_name"`
    IsAnnually       int       `json:"is_annually"`
    IsTrial          int       `json:"is_trial"`
    Email            string    `json:"email"`
    Username         string    `json:"username"`
    Plan             int       `json:"plan"`
    PurchasedPlans   string    `json:"purchased_plans"`
    SimultaneousUsers int      `json:"simultaneus_users"`
    PhoneNumber      string    `json:"phone_number"`
    RenewDate        time.Time `json:"renew_date"`
    TotalPrice       float64   `json:"total_price"`
    ReferenceUUID    string    `json:"reference_uuid"`
    State            string    `json:"state"`
    City             string    `json:"city"`
    Street           string    `json:"street"`
    ZipCode          string    `json:"zip_code"`
    AdditionalInfo   string    `json:"additional_info"`
}