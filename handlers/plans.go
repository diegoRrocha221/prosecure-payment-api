package handlers

import (
    "encoding/json"
    "net/http"
    "prosecure-payment-api/database"
)

type PlanHandler struct {
    db *database.Connection
}

func NewPlanHandler(db *database.Connection) *PlanHandler {
    return &PlanHandler{db: db}
}

func (h *PlanHandler) GetPlans(w http.ResponseWriter, r *http.Request) {
    plans, err := h.db.GetPlans()
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(plans)
}