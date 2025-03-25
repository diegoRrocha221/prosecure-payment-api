package models

type CartItem struct {
    PlanID   int  `json:"plan_id"`
    Quantity int  `json:"quantity"`
    IsAnnual bool `json:"is_annual"`
}   

type CartUpdate struct {
    PlanID int    `json:"plan_id"`
    Action string `json:"action"`
}

type CartResponse struct {
    Items               []CartItemResponse `json:"items"`
    CartSubtotal        float64           `json:"cart_subtotal"`
    CartDiscount        float64           `json:"cart_discount"`
    ShortfallForDiscount string           `json:"shortfall_for_discount"`
    CartTotal           float64           `json:"cart_total"`
}

type CartItemResponse struct {
    PlanID          int     `json:"plan_id"`
    PlanImage       string  `json:"plan_image"`
    PlanName        string  `json:"plan_name"`
    PlanDescription string  `json:"plan_description"`
    PlanQuantity    int     `json:"plan_quantity"`
    Price           float64 `json:"price"`
    IsAnnual        bool    `json:"is_annual"`  
}

type DiscountRule struct {
    Qtd     string `json:"qtd"`     
    Percent string `json:"percent"` 
}