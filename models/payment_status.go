// models/payment_status.go
package models

type PaymentStatus int

const (
    PaymentStatusProcessing PaymentStatus = 0
    
    PaymentStatusFailed PaymentStatus = 1
    
    PaymentStatusSuccess PaymentStatus = 3
)

func (ps PaymentStatus) String() string {
    switch ps {
    case PaymentStatusProcessing:
        return "processing"
    case PaymentStatusFailed:
        return "failed"
    case PaymentStatusSuccess:
        return "success"
    default:
        return "unknown"
    }
}

func (ps PaymentStatus) IsValid() bool {
    return ps == PaymentStatusProcessing || ps == PaymentStatusFailed || ps == PaymentStatusSuccess
}