package authorizenet

import (
    "bytes"
    "encoding/json"
    "fmt"
    "io"
    "log"
    "net/http"
    "strings"
    "prosecure-payment-api/models"
    "time"
)

func formatPhoneNumber(phone string) string {
    clean := strings.Map(func(r rune) rune {
        if r >= '0' && r <= '9' {
            return r
        }
        return -1
    }, phone)

    if len(clean) == 10 {
        return fmt.Sprintf("(%s) %s-%s", clean[0:3], clean[3:6], clean[6:])
    } else if len(clean) == 11 && clean[0] == '1' {
        return fmt.Sprintf("(%s) %s-%s", clean[1:4], clean[4:7], clean[7:])
    }

    return ""
}

func (c *Client) CreateSubscription(payment *models.PaymentRequest, checkout *models.CheckoutData) (*models.SubscriptionResponse, error) {
    var total float64
    interval := IntervalType{
        Length: 1,
        Unit:   "months",
    }

    for _, plan := range checkout.Plans {
        if plan.Annually == 1 {
            interval = IntervalType{
                Length: 12,
                Unit:   "months",
            }
            total += plan.Price * 10 // 10 months for annual
        } else {
            total += plan.Price
        }
    }

    log.Printf("Creating subscription with total amount: %.2f", total)

    names := strings.Fields(checkout.Name)
    firstName := names[0]
    lastName := ""
    if len(names) > 1 {
        lastName = strings.Join(names[1:], " ")
    }

    formattedPhone := formatPhoneNumber(checkout.PhoneNumber)
    if formattedPhone == "" {
        log.Printf("Warning: Could not format phone number %s, omitting from request", checkout.PhoneNumber)
    }

    startDate := time.Now().AddDate(0, 1, 0).Format("2006-01-02") 
    subscription := ARBSubscriptionRequest{
        MerchantAuthentication: c.getMerchantAuthentication(),
        RefID: payment.CheckoutID,
        Subscription: ARBSubscriptionType{
            Name: fmt.Sprintf("ProSecure Subscription - %s", checkout.Username),
            PaymentSchedule: PaymentScheduleType{
                Interval:         interval,
                StartDate:       startDate,
                TotalOccurrences: "9999", // Ongoing subscription
            },
            Amount: fmt.Sprintf("%.2f", total),
            Payment: PaymentType{
                CreditCard: CreditCardType{
                    CardNumber:     payment.CardNumber,
                    ExpirationDate: payment.Expiry,
                    CardCode:       payment.CVV,
                },
            },
            Order: OrderType{
                InvoiceNumber: fmt.Sprintf("INV-%s", time.Now().Format("20060102150405")),
                Description:   "ProSecure Security Services Subscription",
            },
            Customer: CustomerType{
                Type:        "individual",
                Email:       checkout.Email,
                PhoneNumber: formattedPhone,
            },
            BillTo: CustomerAddressType{
                FirstName: firstName,
                LastName:  lastName,
                Address:   checkout.Street,
                City:     checkout.City,
                State:    checkout.State,
                Zip:      checkout.ZipCode,
                Country:  "US",
            },
        },
    }

    jsonPayload, err := json.Marshal(map[string]interface{}{
        "ARBCreateSubscriptionRequest": subscription,
    })
    if err != nil {
        return nil, fmt.Errorf("error marshaling subscription request: %v", err)
    }

    log.Printf("ARB request to Authorize.net: %s", string(jsonPayload))

    httpReq, err := http.NewRequest("POST", c.getEndpoint(), bytes.NewBuffer(jsonPayload))
    if err != nil {
        return nil, fmt.Errorf("error creating ARB request: %v", err)
    }

    httpReq.Header.Set("Content-Type", "application/json")

    resp, err := c.client.Do(httpReq)
    if err != nil {
        return nil, fmt.Errorf("error making ARB request: %v", err)
    }
    defer resp.Body.Close()

    respBody, err := io.ReadAll(resp.Body)
    if err != nil {
        return nil, fmt.Errorf("error reading ARB response body: %v", err)
    }

    log.Printf("ARB response from Authorize.net: %s", string(respBody))

    // Remove BOM if present
    cleanBody := strings.TrimPrefix(string(respBody), "\ufeff")

    var response ARBResponse
    if err := json.Unmarshal([]byte(cleanBody), &response); err != nil {
        return nil, fmt.Errorf("error decoding ARB response: %v, response body: %s", err, string(respBody))
    }

    if response.Messages.ResultCode == "Error" {
        message := "Subscription creation failed"
        if len(response.Messages.Message) > 0 {
            message = response.Messages.Message[0].Text
        }
        return &models.SubscriptionResponse{
            Success: false,
            Message: message,
        }, nil
    }

    if response.SubscriptionID == "" {
        return &models.SubscriptionResponse{
            Success: false,
            Message: "No subscription ID received",
        }, nil
    }

    return &models.SubscriptionResponse{
        Success:        true,
        SubscriptionID: response.SubscriptionID,
        Message:       "Subscription created successfully",
    }, nil
}