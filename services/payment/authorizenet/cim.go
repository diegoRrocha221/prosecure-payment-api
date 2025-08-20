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

// CreateCustomerProfile cria um perfil de cliente na Authorize.net
func (c *Client) CreateCustomerProfile(payment *models.PaymentRequest, checkout *models.CheckoutData) (string, string, error) {
    startTime := time.Now()
    defer func() {
        log.Printf("CreateCustomerProfile completed in %v for checkout: %s", 
            time.Since(startTime), payment.CheckoutID)
    }()

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
        log.Printf("Warning: Could not format phone number %s, omitting from profile", checkout.PhoneNumber)
    }

    // CORREÇÃO: Truncar RefID para máximo de 20 caracteres
    refId := c.normalizeRefID(payment.CheckoutID)
    
    // CORREÇÃO: Truncar merchantCustomerId para máximo de 20 caracteres
    merchantCustomerId := payment.CheckoutID
    if len(merchantCustomerId) > 20 {
        merchantCustomerId = merchantCustomerId[:20]
        log.Printf("MerchantCustomerId truncated from %s to %s", payment.CheckoutID, merchantCustomerId)
    }

    // CORREÇÃO: Truncar description para máximo de 255 caracteres  
    maxDescLength := 255
    description := fmt.Sprintf("ProSecure Customer Profile - %s", checkout.Username)
    if len(description) > maxDescLength {
        // Truncar mantendo a parte mais importante
        prefix := "ProSecure Customer - "
        availableSpace := maxDescLength - len(prefix)
        if availableSpace > 0 && len(checkout.Username) > availableSpace {
            truncatedUsername := checkout.Username[:availableSpace]
            description = prefix + truncatedUsername
        } else if availableSpace > 0 {
            description = prefix + checkout.Username
        } else {
            description = "ProSecure Customer Profile"
        }
        log.Printf("Profile description truncated to: %s", description)
    }

    // Criar o perfil de pagamento
    paymentProfile := CustomerPaymentProfileType{
        CustomerType: "individual",
        BillTo: &CustomerAddressType{
            FirstName: firstName,
            LastName:  lastName,
            Address:   checkout.Street,
            City:     checkout.City,
            State:    checkout.State,
            Zip:      checkout.ZipCode,
            Country:  "US",
        },
        Payment: &PaymentType{
            CreditCard: CreditCardType{
                CardNumber:     payment.CardNumber,
                ExpirationDate: payment.Expiry,
                CardCode:       payment.CVV,
            },
        },
        DefaultPaymentProfile: true,
    }

    // Criar o perfil do cliente
    profile := CustomerProfileType{
        MerchantCustomerID: merchantCustomerId,
        Description:       description,
        Email:            checkout.Email,
        PaymentProfiles:   []CustomerPaymentProfileType{paymentProfile},
    }

    // Construir a requisição
    request := CreateCustomerProfileRequestWrapper{
        CreateCustomerProfileRequest: CreateCustomerProfileRequest{
            MerchantAuthentication: c.getMerchantAuthentication(),
            RefID:                 refId,
            Profile:               profile,
            ValidationMode:        "testMode", // Validar o cartão na criação
        },
    }

    // Serializar para JSON
    jsonPayload, err := json.Marshal(request)
    if err != nil {
        return "", "", fmt.Errorf("error marshaling customer profile request: %v", err)
    }

    log.Printf("Creating customer profile for checkout: %s (RefID: %s)", payment.CheckoutID, refId)

    // Criar contexto com timeout
    ctx, cancel := context.WithTimeout(context.Background(), RequestTimeout)
    defer cancel()
    
    httpReq, err := http.NewRequestWithContext(ctx, "POST", c.getEndpoint(), bytes.NewBuffer(jsonPayload))
    if err != nil {
        return "", "", fmt.Errorf("error creating customer profile request: %v", err)
    }

    httpReq.Header.Set("Content-Type", "application/json")
    httpReq.Header.Set("Cache-Control", "no-cache")

    // Usar mutex para garantir thread safety
    c.mutex.Lock()
    resp, err := c.client.Do(httpReq)
    c.mutex.Unlock()
    
    if err != nil {
        return "", "", fmt.Errorf("error making customer profile request: %v", err)
    }
    defer resp.Body.Close()

    respBody, err := io.ReadAll(resp.Body)
    if err != nil {
        return "", "", fmt.Errorf("error reading customer profile response body: %v", err)
    }

    log.Printf("Customer profile response received in %v for checkout: %s", 
        time.Since(startTime), payment.CheckoutID)

    // Remover BOM se presente
    cleanBody := strings.TrimPrefix(string(respBody), "\ufeff")

    var response CreateCustomerProfileResponse
    if err := json.Unmarshal([]byte(cleanBody), &response); err != nil {
        return "", "", fmt.Errorf("error decoding customer profile response: %v, response body: %s", err, string(respBody))
    }

    if response.Messages.ResultCode == "Error" {
        message := "Customer profile creation failed"
        if len(response.Messages.Message) > 0 {
            message = response.Messages.Message[0].Text
            log.Printf("Customer profile error: %s (Code: %s)", message, response.Messages.Message[0].Code)
            
            // Verificar se é um erro de perfil duplicado
            if response.Messages.Message[0].Code == "E00039" {
                // Erro de duplicação - tentar extrair o customer profile ID existente
                log.Printf("Duplicate customer profile detected, attempting to extract existing profile ID")
                
                // A mensagem de erro contém o ID do perfil existente
                // Formato típico: "A duplicate record with ID 123456789 already exists."
                existingProfileID := extractProfileIDFromDuplicateError(message)
                if existingProfileID != "" {
                    log.Printf("Extracted existing customer profile ID: %s", existingProfileID)
                    
                    // Buscar o payment profile ID do perfil existente - CORRIGIDO
                    paymentProfileID, err := c.getPaymentProfileIDFromExistingProfile(existingProfileID)
                    if err != nil {
                        log.Printf("Failed to get payment profile ID from existing profile: %v", err)
                        // Em caso de erro, retornar o profile ID mesmo sem payment profile ID
                        // A subscription pode tentar usar apenas o customer profile ID
                        return existingProfileID, "", nil
                    }
                    
                    return existingProfileID, paymentProfileID, nil
                }
            }
        }
        
        return "", "", fmt.Errorf("customer profile creation failed: %s", message)
    }

    if response.CustomerProfileID == "" {
        return "", "", fmt.Errorf("no customer profile ID received")
    }

    // Obter o payment profile ID
    var paymentProfileID string
    if len(response.CustomerPaymentProfileIDList) > 0 {
        paymentProfileID = response.CustomerPaymentProfileIDList[0]
    } else {
        return "", "", fmt.Errorf("no payment profile ID received")
    }

    log.Printf("Customer profile created successfully with ID: %s, Payment Profile ID: %s", 
        response.CustomerProfileID, paymentProfileID)
    
    return response.CustomerProfileID, paymentProfileID, nil
}

// extractProfileIDFromDuplicateError extrai o ID do perfil de uma mensagem de erro de duplicação
func extractProfileIDFromDuplicateError(errorMessage string) string {
    // Padrões comuns de mensagem de erro da Authorize.net para duplicação
    // "A duplicate record with ID 123456789 already exists."
    // "Duplicate customer profile ID 123456789."
    
    // Buscar por padrões de ID numérico
    words := strings.Fields(errorMessage)
    for i, word := range words {
        // Verificar se a palavra atual é "ID" e a próxima é numérica
        if strings.ToUpper(word) == "ID" && i+1 < len(words) {
            potentialID := strings.TrimRight(words[i+1], ".")
            if isNumeric(potentialID) && len(potentialID) >= 8 { // IDs da Authorize.net são tipicamente longos
                return potentialID
            }
        }
        
        // Verificar se a palavra atual é um ID numérico longo
        cleanWord := strings.TrimRight(word, ".")
        if isNumeric(cleanWord) && len(cleanWord) >= 8 {
            return cleanWord
        }
    }
    
    return ""
}

// isNumeric verifica se uma string contém apenas dígitos
func isNumeric(s string) bool {
    if s == "" {
        return false
    }
    for _, char := range s {
        if char < '0' || char > '9' {
            return false
        }
    }
    return true
}

// CORRIGIDO: getPaymentProfileIDFromExistingProfile busca o payment profile ID de um customer profile existente
func (c *Client) getPaymentProfileIDFromExistingProfile(customerProfileID string) (string, error) {
    log.Printf("Getting payment profile ID from existing customer profile: %s", customerProfileID)
    
    request := GetCustomerProfileRequestWrapper{
        GetCustomerProfileRequest: GetCustomerProfileRequest{
            MerchantAuthentication: c.getMerchantAuthentication(),
            CustomerProfileID:     customerProfileID,
        },
    }

    jsonPayload, err := json.Marshal(request)
    if err != nil {
        return "", fmt.Errorf("error marshaling get profile request: %v", err)
    }

    ctx, cancel := context.WithTimeout(context.Background(), RequestTimeout)
    defer cancel()
    
    httpReq, err := http.NewRequestWithContext(ctx, "POST", c.getEndpoint(), bytes.NewBuffer(jsonPayload))
    if err != nil {
        return "", fmt.Errorf("error creating get profile request: %v", err)
    }

    httpReq.Header.Set("Content-Type", "application/json")

    c.mutex.Lock()
    resp, err := c.client.Do(httpReq)
    c.mutex.Unlock()
    
    if err != nil {
        return "", fmt.Errorf("error making get profile request: %v", err)
    }
    defer resp.Body.Close()

    respBody, err := io.ReadAll(resp.Body)
    if err != nil {
        return "", fmt.Errorf("error reading get profile response: %v", err)
    }

    cleanBody := strings.TrimPrefix(string(respBody), "\ufeff")

    var response GetCustomerProfileResponse
    if err := json.Unmarshal([]byte(cleanBody), &response); err != nil {
        return "", fmt.Errorf("error decoding get profile response: %v", err)
    }

    if response.Messages.ResultCode == "Error" {
        message := "Failed to get customer profile"
        if len(response.Messages.Message) > 0 {
            message = response.Messages.Message[0].Text
        }
        return "", fmt.Errorf("get customer profile failed: %s", message)
    }

    // CORRIGIDO: Extrair o payment profile ID diretamente da resposta
    if len(response.Profile.PaymentProfiles) > 0 {
        // Assumir que queremos o primeiro payment profile
        // Na verdade, o payment profile ID está no campo CustomerPaymentProfileID da resposta
        // Vamos verificar se o profile tem payment profiles e extrair o ID
        
        // Como o PaymentProfiles não tem o ID diretamente no tipo atual,
        // vamos usar uma abordagem diferente - parsear a resposta JSON diretamente
        var rawResponse map[string]interface{}
        if err := json.Unmarshal([]byte(cleanBody), &rawResponse); err == nil {
            if profile, ok := rawResponse["profile"].(map[string]interface{}); ok {
                if paymentProfiles, ok := profile["paymentProfiles"].([]interface{}); ok && len(paymentProfiles) > 0 {
                    if firstProfile, ok := paymentProfiles[0].(map[string]interface{}); ok {
                        if customerPaymentProfileID, ok := firstProfile["customerPaymentProfileId"].(string); ok {
                            log.Printf("Successfully extracted payment profile ID: %s from customer profile: %s", 
                                customerPaymentProfileID, customerProfileID)
                            return customerPaymentProfileID, nil
                        }
                    }
                }
            }
        }
        
        log.Printf("Customer profile %s has payment profiles but could not extract payment profile ID", customerProfileID)
        return "", fmt.Errorf("could not extract payment profile ID from customer profile response")
    }

    return "", fmt.Errorf("no payment profiles found in customer profile %s", customerProfileID)
}

// getFirstPaymentProfileID - DEPRECATED: Use getPaymentProfileIDFromExistingProfile instead
func (c *Client) getFirstPaymentProfileID(customerProfileID string) (string, error) {
    return c.getPaymentProfileIDFromExistingProfile(customerProfileID)
}

// UpdateCustomerPaymentProfile atualiza o método de pagamento de um customer profile existente
func (c *Client) UpdateCustomerPaymentProfile(customerProfileID, paymentProfileID string, payment *models.PaymentRequest, checkout *models.CheckoutData) error {
    startTime := time.Now()
    defer func() {
        log.Printf("UpdateCustomerPaymentProfile completed in %v", time.Since(startTime))
    }()

    // CORRIGIDO: Estrutura MÍNIMA - apenas payment profile ID + payment
    paymentProfile := UpdateCustomerPaymentProfileType{
        CustomerPaymentProfileID: paymentProfileID, // OBRIGATÓRIO
        Payment: &PaymentType{                       // APENAS o novo método de pagamento
            CreditCard: CreditCardType{
                CardNumber:     payment.CardNumber,
                ExpirationDate: payment.Expiry,
                CardCode:       payment.CVV,
            },
        },
        // NÃO incluir billTo, customerType, etc.
    }

    // Construir a requisição
    request := UpdateCustomerPaymentProfileRequestWrapper{
        UpdateCustomerPaymentProfileRequest: UpdateCustomerPaymentProfileRequest{
            MerchantAuthentication: c.getMerchantAuthentication(),
            CustomerProfileID:     customerProfileID,
            PaymentProfile:        paymentProfile,
            ValidationMode:        "testMode",
        },
    }

    // Serializar para JSON
    jsonPayload, err := json.Marshal(request)
    if err != nil {
        return fmt.Errorf("error marshaling update payment profile request: %v", err)
    }

    log.Printf("Updating customer payment profile: %s/%s", customerProfileID, paymentProfileID)

    ctx, cancel := context.WithTimeout(context.Background(), RequestTimeout)
    defer cancel()
    
    httpReq, err := http.NewRequestWithContext(ctx, "POST", c.getEndpoint(), bytes.NewBuffer(jsonPayload))
    if err != nil {
        return fmt.Errorf("error creating update payment profile request: %v", err)
    }

    httpReq.Header.Set("Content-Type", "application/json")

    c.mutex.Lock()
    resp, err := c.client.Do(httpReq)
    c.mutex.Unlock()
    
    if err != nil {
        return fmt.Errorf("error making update payment profile request: %v", err)
    }
    defer resp.Body.Close()

    respBody, err := io.ReadAll(resp.Body)
    if err != nil {
        return fmt.Errorf("error reading update payment profile response: %v", err)
    }

    cleanBody := strings.TrimPrefix(string(respBody), "\ufeff")

    var response UpdateCustomerPaymentProfileResponse
    if err := json.Unmarshal([]byte(cleanBody), &response); err != nil {
        return fmt.Errorf("error decoding update payment profile response: %v", err)
    }

    if response.Messages.ResultCode == "Error" {
        message := "Update payment profile failed"
        if len(response.Messages.Message) > 0 {
            message = response.Messages.Message[0].Text
        }
        return fmt.Errorf("update payment profile failed: %s", message)
    }

    log.Printf("Customer payment profile updated successfully: %s/%s", customerProfileID, paymentProfileID)
    return nil
}

func (c *Client) CreateCustomerPaymentProfile(customerProfileID string, paymentReq *models.PaymentRequest, checkoutData *models.CheckoutData) (string, error) {
    log.Printf("Creating customer payment profile for customer: %s", customerProfileID)
    
    startTime := time.Now()
    defer func() {
        log.Printf("CreateCustomerPaymentProfile completed in %v", time.Since(startTime))
    }()

    // Extrair nome e sobrenome
    names := strings.Fields(checkoutData.Name)
    firstName := names[0]
    lastName := ""
    if len(names) > 1 {
        lastName = strings.Join(names[1:], " ")
    }

    // Create payment profile request
    paymentProfile := &CustomerPaymentProfileType{
        CustomerType: "individual",
        BillTo: &CustomerAddressType{
            FirstName: firstName,
            LastName:  lastName,
            Address:   checkoutData.Street,
            City:      checkoutData.City,
            State:     checkoutData.State,
            Zip:       checkoutData.ZipCode,
            Country:   "US",
        },
        Payment: &PaymentType{
            CreditCard: CreditCardType{
                CardNumber:     paymentReq.CardNumber,
                ExpirationDate: paymentReq.Expiry,
                CardCode:       paymentReq.CVV,
            },
        },
    }

    // Create the request
    request := CreateCustomerPaymentProfileRequestWrapper{
        CreateCustomerPaymentProfileRequest: CreateCustomerPaymentProfileRequest{
            MerchantAuthentication: c.getMerchantAuthentication(),
            CustomerProfileId:      customerProfileID,
            PaymentProfile:         paymentProfile,
            ValidationMode:         "testMode",
        },
    }

    // Convert to JSON
    jsonData, err := json.Marshal(request)
    if err != nil {
        return "", fmt.Errorf("failed to marshal create payment profile request: %v", err)
    }

    // Make API call
    ctx, cancel := context.WithTimeout(context.Background(), RequestTimeout)
    defer cancel()
    
    httpReq, err := http.NewRequestWithContext(ctx, "POST", c.getEndpoint(), bytes.NewBuffer(jsonData))
    if err != nil {
        return "", fmt.Errorf("failed to create HTTP request: %v", err)
    }

    httpReq.Header.Set("Content-Type", "application/json")

    c.mutex.Lock()
    resp, err := c.client.Do(httpReq)
    c.mutex.Unlock()
    
    if err != nil {
        return "", fmt.Errorf("failed to make API call: %v", err)
    }
    defer resp.Body.Close()

    // Read response
    body, err := io.ReadAll(resp.Body)
    if err != nil {
        return "", fmt.Errorf("failed to read response: %v", err)
    }

    cleanBody := strings.TrimPrefix(string(body), "\ufeff")

    // Parse response
    var response CreateCustomerPaymentProfileResponse
    if err := json.Unmarshal([]byte(cleanBody), &response); err != nil {
        return "", fmt.Errorf("failed to unmarshal response: %v", err)
    }

    // Check for errors
    if response.Messages.ResultCode != "Ok" {
        errorMsg := "Unknown error"
        if len(response.Messages.Message) > 0 {
            errorMsg = response.Messages.Message[0].Text
        }
        return "", fmt.Errorf("create payment profile failed: %s", errorMsg)
    }

    log.Printf("Successfully created payment profile: %s for customer: %s", response.CustomerPaymentProfileId, customerProfileID)
    return response.CustomerPaymentProfileId, nil
}
