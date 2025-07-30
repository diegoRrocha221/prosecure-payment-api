// services/payment/authorizenet/arb.go - VERSÃO CORRIGIDA PARA PRODUÇÃO
package authorizenet

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "log"
    "net/http"
    "strings"
    "time"
    
    "prosecure-payment-api/models"
)

// CreateSubscription cria uma assinatura usando Customer Profile (método atualizado)
func (c *Client) CreateSubscription(payment *models.PaymentRequest, checkout *models.CheckoutData) (*models.SubscriptionResponse, error) {
    startTime := time.Now()
    defer func() {
        log.Printf("CreateSubscription completed in %v for checkout: %s", 
            time.Since(startTime), payment.CheckoutID)
    }()
    
    log.Printf("Starting subscription creation process with Customer Profile for checkout: %s", payment.CheckoutID)
    
    // ETAPA 1: Criar Customer Profile primeiro
    log.Printf("Step 1: Creating customer profile for checkout: %s", payment.CheckoutID)
    customerProfileID, paymentProfileID, err := c.CreateCustomerProfile(payment, checkout)
    if err != nil {
        log.Printf("Customer profile creation failed for checkout %s: %v", payment.CheckoutID, err)
        return &models.SubscriptionResponse{
            Success: false,
            Message: fmt.Sprintf("Failed to create customer profile: %v", err),
        }, nil
    }
    
    log.Printf("Customer profile created successfully - Profile ID: %s, Payment Profile ID: %s", 
        customerProfileID, paymentProfileID)
    
    // ETAPA 2: Criar subscription usando o Customer Profile
    log.Printf("Step 2: Creating subscription using customer profile")
    return c.createSubscriptionWithProfile(payment, checkout, customerProfileID, paymentProfileID)
}

// createSubscriptionWithProfile cria uma subscription usando Customer Profile ID
func (c *Client) createSubscriptionWithProfile(payment *models.PaymentRequest, checkout *models.CheckoutData, customerProfileID, paymentProfileID string) (*models.SubscriptionResponse, error) {
    log.Printf("Creating ARB subscription with Customer Profile ID: %s, Payment Profile ID: %s", 
        customerProfileID, paymentProfileID)
    
    if customerProfileID == "" || paymentProfileID == "" {
        return &models.SubscriptionResponse{
            Success: false,
            Message: "Invalid customer profile or payment profile ID",
        }, nil
    }
    
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
            total += plan.Price * 10
        } else {
            total += plan.Price
        }
    }

    startDate := time.Now().AddDate(0, 1, 0).Format("2006-01-02")
    refId := c.normalizeRefID(payment.CheckoutID)
    
    // ESTRUTURA CORRETA PARA ARB COM CUSTOMER PROFILE
    request := ARBSubscriptionRequestWithProfile{
        MerchantAuthentication: c.getMerchantAuthentication(),
        RefID: refId,
        Subscription: ARBSubscriptionTypeWithProfile{
            PaymentSchedule: PaymentScheduleType{
                Interval:         interval,
                StartDate:       startDate,
                TotalOccurrences: "9999",
            },
            Amount: fmt.Sprintf("%.2f", total),
            Profile: ProfileType{
                CustomerProfileID:       customerProfileID,
                CustomerPaymentProfileID: paymentProfileID,
            },
            // NÃO incluir Name, Customer, BillTo quando usar Profile
        },
    }

    jsonPayload, err := json.Marshal(map[string]interface{}{
        "ARBCreateSubscriptionRequest": request,
    })
    if err != nil {
        return &models.SubscriptionResponse{
            Success: false,
            Message: fmt.Sprintf("Error marshaling subscription request: %v", err),
        }, nil
    }

    log.Printf("ARB Request with Customer Profile - RefID: %s, Amount: %.2f, ProfileID: %s", 
        refId, total, customerProfileID)

    ctx, cancel := context.WithTimeout(context.Background(), RequestTimeout)
    defer cancel()
    
    httpReq, err := http.NewRequestWithContext(ctx, "POST", c.getEndpoint(), bytes.NewBuffer(jsonPayload))
    if err != nil {
        return &models.SubscriptionResponse{
            Success: false,
            Message: fmt.Sprintf("Error creating ARB request: %v", err),
        }, nil
    }

    httpReq.Header.Set("Content-Type", "application/json")
    httpReq.Header.Set("Cache-Control", "no-cache")

    c.mutex.Lock()
    requestStart := time.Now()
    resp, err := c.client.Do(httpReq)
    requestDuration := time.Since(requestStart)
    c.mutex.Unlock()
    
    if err != nil {
        log.Printf("Error making ARB HTTP request (took %v): %v", requestDuration, err)
        return &models.SubscriptionResponse{
            Success: false,
            Message: fmt.Sprintf("Error making ARB request: %v", err),
        }, nil
    }
    defer resp.Body.Close()

    respBody, err := io.ReadAll(resp.Body)
    if err != nil {
        return &models.SubscriptionResponse{
            Success: false,
            Message: fmt.Sprintf("Error reading ARB response body: %v", err),
        }, nil
    }

    cleanBody := strings.TrimPrefix(string(respBody), "\ufeff")

    var response ARBResponse
    if err := json.Unmarshal([]byte(cleanBody), &response); err != nil {
        log.Printf("Error decoding ARB response JSON: %v", err)
        log.Printf("Raw response body: %s", string(respBody))
        return &models.SubscriptionResponse{
            Success: false,
            Message: fmt.Sprintf("Error decoding ARB response: %v", err),
        }, nil
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

    log.Printf("ARB subscription created successfully with ID: %s (using customer profile: %s)", 
        response.SubscriptionID, customerProfileID)
    
    return &models.SubscriptionResponse{
        Success:        true,
        SubscriptionID: response.SubscriptionID,
        Message:       "Subscription created successfully with customer profile",
    }, nil
}

// formatPhoneNumber - função helper mantida (sem alterações)
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

// MÉTODO LEGADO MANTIDO PARA COMPATIBILIDADE (sem alterações significativas)
func (c *Client) CreateSubscriptionDirect(payment *models.PaymentRequest, checkout *models.CheckoutData) (*models.SubscriptionResponse, error) {
    startTime := time.Now()
    defer func() {
        log.Printf("CreateSubscriptionDirect completed in %v for checkout: %s", 
            time.Since(startTime), payment.CheckoutID)
    }()
    
    log.Printf("Creating subscription with direct card data (legacy method) for checkout: %s", payment.CheckoutID)
    
    var total float64
    interval := IntervalType{
        Length: 1,
        Unit:   "months",
    }

    // Calcular o total e definir o intervalo de cobrança
    for _, plan := range checkout.Plans {
        if plan.Annually == 1 {
            interval = IntervalType{
                Length: 12,
                Unit:   "months",
            }
            total += plan.Price * 10 // 10 meses para anual (desconto de 2 meses)
        } else {
            total += plan.Price
        }
    }

    log.Printf("Creating subscription with total amount: %.2f", total)

    // Extrair nome e sobrenome
    names := strings.Fields(checkout.Name)
    firstName := names[0]
    lastName := ""
    if len(names) > 1 {
        lastName = strings.Join(names[1:], " ")
    }

    // Formatar número de telefone
    formattedPhone := formatPhoneNumber(checkout.PhoneNumber)
    if formattedPhone == "" {
        log.Printf("Warning: Could not format phone number %s, omitting from request", checkout.PhoneNumber)
    }

    // Definir data de início para um mês após a data atual
    startDate := time.Now().AddDate(0, 1, 0).Format("2006-01-02") 
    
    // CORREÇÃO: Truncar RefID para máximo de 20 caracteres
    refId := payment.CheckoutID
    if len(refId) > 20 {
        refId = refId[:20]
        log.Printf("RefID truncated from %s to %s for ARB request", payment.CheckoutID, refId)
    }
    
    // CORREÇÃO: Truncar nome da subscription para máximo de 50 caracteres
    maxNameLength := 50
    subscriptionName := fmt.Sprintf("ProSecure Subscription - %s", checkout.Username)
    if len(subscriptionName) > maxNameLength {
        prefix := "ProSecure - "
        availableSpace := maxNameLength - len(prefix)
        if availableSpace > 0 && len(checkout.Username) > availableSpace {
            truncatedUsername := checkout.Username[:availableSpace]
            subscriptionName = prefix + truncatedUsername
        } else if availableSpace > 0 {
            subscriptionName = prefix + checkout.Username
        } else {
            subscriptionName = "ProSecure Subscription"
        }
        log.Printf("Subscription name truncated from '%s' to '%s' (max %d chars)", 
            fmt.Sprintf("ProSecure Subscription - %s", checkout.Username), subscriptionName, maxNameLength)
    }
    
    // Construir a requisição de assinatura (método original)
    subscription := ARBSubscriptionRequest{
        MerchantAuthentication: c.getMerchantAuthentication(),
        RefID: refId,
        Subscription: ARBSubscriptionType{
            Name: subscriptionName,
            PaymentSchedule: PaymentScheduleType{
                Interval:         interval,
                StartDate:       startDate,
                TotalOccurrences: "9999", // Assinatura contínua
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

    // Serializar para JSON
    jsonPayload, err := json.Marshal(map[string]interface{}{
        "ARBCreateSubscriptionRequest": subscription,
    })
    if err != nil {
        return nil, fmt.Errorf("error marshaling subscription request: %v", err)
    }

    log.Printf("Sending ARB request (direct method) to Authorize.net for checkout: %s (RefID: %s)", payment.CheckoutID, refId)

    // Criar contexto com timeout para controle de tempo da requisição
    ctx, cancel := context.WithTimeout(context.Background(), RequestTimeout) 
    defer cancel()
    
    httpReq, err := http.NewRequestWithContext(ctx, "POST", c.getEndpoint(), bytes.NewBuffer(jsonPayload))
    if err != nil {
        return nil, fmt.Errorf("error creating ARB request: %v", err)
    }

    httpReq.Header.Set("Content-Type", "application/json")
    httpReq.Header.Set("Cache-Control", "no-cache")

    // Usar mutex para garantir thread safety
    c.mutex.Lock()
    resp, err := c.client.Do(httpReq)
    c.mutex.Unlock()
    
    if err != nil {
        return nil, fmt.Errorf("error making ARB request: %v", err)
    }
    defer resp.Body.Close()

    respBody, err := io.ReadAll(resp.Body)
    if err != nil {
        return nil, fmt.Errorf("error reading ARB response body: %v", err)
    }

    log.Printf("ARB response received in %v for checkout: %s", 
        time.Since(startTime), payment.CheckoutID)

    // Remover BOM se presente
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
        log.Printf("ARB Error: %s", message)
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

    log.Printf("ARB subscription created successfully with ID: %s", response.SubscriptionID)
    return &models.SubscriptionResponse{
        Success:        true,
        SubscriptionID: response.SubscriptionID,
        Message:       "Subscription created successfully",
    }, nil
}