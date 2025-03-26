package models

import "time"

type Invoice struct {
    ID              string    `json:"id"`
    MasterReference string    `json:"master_reference"`
    IsTrial         int       `json:"is_trial"`
    Total           float64   `json:"total"`
    DueDate         time.Time `json:"due_date"`
    IsPaid          int       `json:"is_paid"`
}