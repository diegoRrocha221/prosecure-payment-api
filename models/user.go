package models

type User struct {
    ID              int    `json:"id"`
    MasterReference string `json:"master_reference"`
    Username        string `json:"username"`
    Email           string `json:"email"`
    Passphrase      string `json:"passphrase"`
    IsMaster        int    `json:"is_master"`
    PlanID          int    `json:"plan_id"`
}