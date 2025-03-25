package utils

import (
    "encoding/json"
    "math"
    "strconv"
)

type DiscountRule struct {
    Quantity string `json:"qtd"`
    Percent  string `json:"percent"`
}

func Round(value float64) float64 {
    return math.Round(value*100) / 100
}

func CalculateAnnualPrice(monthlyPrice float64) float64 {
    return Round(monthlyPrice * 10) // 10 months for annual discount
}

func ParseDiscountRules(discountJSON string) ([]DiscountRule, error) {
    var rules []DiscountRule
    if err := json.Unmarshal([]byte(discountJSON), &rules); err != nil {
        return nil, err
    }
    return rules, nil
}

func CalculateDiscount(basePrice float64, quantity int, discountRules []DiscountRule) (float64, float64) {
    var discountPercent float64
    for _, rule := range discountRules {
        qtd, _ := strconv.Atoi(rule.Quantity)
        percent, _ := strconv.ParseFloat(rule.Percent, 64)
        if quantity >= qtd && percent > discountPercent {
            discountPercent = percent
        }
    }

    if discountPercent > 0 {
        discount := (basePrice * discountPercent) / 100
        return Round(discount), discountPercent
    }
    return 0, 0
}