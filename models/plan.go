package models

type PlanCart struct {
    ID                  int     `json:"id" db:"id"`
    Image              string  `json:"image" db:"image"`
    Name               string  `json:"name" db:"name"`
    Description        string  `json:"description" db:"description"`
    Price              float64 `json:"price" db:"price"`
    Rules              string  `json:"rules" db:"rules"`
    SingleDiscount     string  `json:"single_discount" db:"single_discount"`
    DiscountRuleApplied int    `json:"discount_rule_applied" db:"discount_rule_applied"`
    DeletedAt          *string `json:"deleted_at" db:"deleted_at"`
}