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
    "sync"
    "time"
    
    "prosecure-payment-api/models"
)

const (
    SandboxEndpoint = "https://apitest.authorize.net/xml/v1/request.api"
    ProductionEndpoint = "https://api.authorize.net/xml/v1/request.api"
    DuplicateWindow = 3
    RequestTimeout = 30 * time.Second // Aumentado para 30 segundos para evitar timeouts
    SilentPostURL = "https://api.prosecurelsp.com/api/authorize-net/webhook/silent-post"
)

type Client struct {
    apiLoginID     string
    transactionKey string
    merchantID     string
    environment    string
    client         *http.Client
    transport      *http.Transport
    mutex          sync.Mutex // Para operações concorrentes seguras
}

func NewClient(apiLoginID, transactionKey, merchantID, environment string) *Client {
    // Configuração otimizada do Transport para HTTP
    transport := &http.Transport{
        MaxIdleConns:        100,
        MaxIdleConnsPerHost: 20,
        MaxConnsPerHost:     100,
        IdleConnTimeout:     90 * time.Second,
        DisableKeepAlives:   false, // Manter conexões ativas
        TLSHandshakeTimeout: 10 * time.Second,
    }
    
    return &Client{
        apiLoginID:     apiLoginID,
        transactionKey: transactionKey,
        merchantID:     merchantID,
        environment:    environment,
        transport:      transport,
        client:         &http.Client{
            Timeout:   RequestTimeout,
            Transport: transport,
        },
    }
}

func (c *Client) getEndpoint() string {
    if c.environment == "production" {
        return ProductionEndpoint
    }
    return SandboxEndpoint
}

func (c *Client) getMerchantAuthentication() merchantAuthenticationType {
    return merchantAuthenticationType{
        Name:           c.apiLoginID,
        TransactionKey: c.transactionKey,
    }
}

// Método auxiliar para criar contexto com timeout
func (c *Client) createRequestContext() (context.Context, context.CancelFunc) {
    return context.WithTimeout(context.Background(), RequestTimeout)
}

func (c *Client) ProcessPayment(req *models.PaymentRequest) (*models.TransactionResponse, error) {
    startTime := time.Now()
    
    orderID := fmt.Sprintf("Order-%s-%d", req.CheckoutID, time.Now().UnixNano())
    if len(orderID) > 20 {
        orderID = fmt.Sprintf("Order-%d", time.Now().UnixNano() % 100000)
    }
    
    // Construir a solicitação básica
    txRequest := transactionRequestType{
        TransactionType: "authOnlyTransaction",
        Amount:         "1.00",
        Payment: &PaymentType{
            CreditCard: CreditCardType{
                CardNumber:     req.CardNumber,
                ExpirationDate: req.Expiry,
                CardCode:       req.CVV,
            },
        },
        // Adicionar informações do pedido para controle de duplicação
        Order: &OrderType{
            InvoiceNumber: orderID,
            Description:   "ProSecure Validation Charge",
        },
        // Adicionar configurações de transação, incluindo a janela de duplicação
        TransactionSettings: &TransactionSettingsType{
            Settings: []SettingType{
                {
                    SettingName:  "duplicateWindow",
                    SettingValue: fmt.Sprintf("%d", DuplicateWindow),
                },
                // Adicionar configuração para Silent Post
                {
                    SettingName:  "x_relay_url",
                    SettingValue: SilentPostURL,
                },
                // Habilitar Silent Post
                {
                    SettingName:  "x_silent_post_enabled",
                    SettingValue: "true",
                },
            },
        },
    }
    
    // Adicionar informações do cliente se disponíveis
    if req.CustomerEmail != "" {
        txRequest.Customer = &CustomerType{
            Type:  "individual",
            Email: req.CustomerEmail,
        }
    }
    
    // Adicionar informações de faturamento se disponíveis
    if req.BillingInfo != nil {
        txRequest.BillTo = req.BillingInfo
    }
    
    refId := req.CheckoutID
    if len(refId) > 20 {
        refId = refId[:19]
    }
    wrapper := createTransactionRequestWrapper{
        CreateTransactionRequest: createTransactionRequest{
            MerchantAuthentication: c.getMerchantAuthentication(),
            RefID: refId,
            TransactionRequest: txRequest,
        },
    }

    jsonPayload, err := json.Marshal(wrapper)
    if err != nil {
        return nil, fmt.Errorf("error marshaling request: %v", err)
    }

    // Log com menos informações sensíveis
    log.Printf("Sending payment request to Authorize.net for checkout: %s", req.CheckoutID)

    // Usar timeout específico para esta operação
    ctx, cancel := c.createRequestContext()
    defer cancel()
    
    httpReq, err := http.NewRequestWithContext(ctx, "POST", c.getEndpoint(), bytes.NewBuffer(jsonPayload))
    if err != nil {
        return nil, fmt.Errorf("error creating request: %v", err)
    }

    httpReq.Header.Set("Content-Type", "application/json")
    httpReq.Header.Set("Cache-Control", "no-cache")

    c.mutex.Lock() // Bloqueio para evitar problemas com simultaneidade
    resp, err := c.client.Do(httpReq)
    c.mutex.Unlock()
    
    if err != nil {
        return nil, fmt.Errorf("error making request: %v", err)
    }
    defer resp.Body.Close()

    respBody, err := io.ReadAll(resp.Body)
    if err != nil {
        return nil, fmt.Errorf("error reading response body: %v", err)
    }

    // Log do tempo de resposta
    log.Printf("Authorize.net response received in %v for checkout: %s", 
        time.Since(startTime), req.CheckoutID)

    cleanBody := strings.TrimPrefix(string(respBody), "\ufeff")

    var response createTransactionResponse
    if err := json.Unmarshal([]byte(cleanBody), &response); err != nil {
        return nil, fmt.Errorf("error decoding response: %v, response body: %s", err, string(respBody))
    }

    // Verificar se é um erro de transação duplicada
    if response.Messages.ResultCode == "Error" {
        isDuplicate := false
        duplicateTransID := ""
        
        for _, msg := range response.Messages.Message {
            if msg.Code == "E00027" { // Código de erro de duplicação da Authorize.net
                isDuplicate = true
                // Tentar obter o ID da transação original
                if response.TransactionResponse.Errors != nil && len(response.TransactionResponse.Errors) > 0 {
                    for _, err := range response.TransactionResponse.Errors {
                        if err.ErrorCode == "11" { // Código de erro de duplicação
                            duplicateTransID = err.OriginalTransactionID
                            break
                        }
                    }
                }
                break
            }
        }
        
        if isDuplicate && duplicateTransID != "" {
            log.Printf("Detected duplicate transaction. Original transaction ID: %s", duplicateTransID)
            
            // Retornar a transação original como se fosse uma nova transação bem-sucedida
            return &models.TransactionResponse{
                Success:       true,
                TransactionID: duplicateTransID,
                Message:       "Transaction previously processed",
                IsDuplicate:   true,
            }, nil
        }
        
        // Se não for duplicação ou não tiver ID da transação original, retornar erro normal
        if len(response.Messages.Message) > 0 {
            return &models.TransactionResponse{
                Success: false,
                Message: response.Messages.Message[0].Text,
            }, nil
        }
        return &models.TransactionResponse{
            Success: false,
            Message: "Unknown error occurred",
        }, nil
    }

    if response.TransactionResponse.ResponseCode != "1" {
        message := "Transaction failed"
        if len(response.TransactionResponse.Messages) > 0 {
            message = response.TransactionResponse.Messages[0].Description
        }
        return &models.TransactionResponse{
            Success: false,
            Message: message,
        }, nil
    }

    return &models.TransactionResponse{
        Success:       true,
        TransactionID: response.TransactionResponse.TransID,
        Message:      response.TransactionResponse.Messages[0].Description,
    }, nil
}

func (c *Client) VoidTransaction(transactionID string) error {
    startTime := time.Now()
    
    wrapper := createTransactionRequestWrapper{
        CreateTransactionRequest: createTransactionRequest{
            MerchantAuthentication: c.getMerchantAuthentication(),
            TransactionRequest: transactionRequestType{
                TransactionType: "voidTransaction",
                RefTransId:     transactionID,
            },
        },
    }

    jsonPayload, err := json.Marshal(wrapper)
    if err != nil {
        return fmt.Errorf("error marshaling void request: %v", err)
    }

    log.Printf("Sending void request to Authorize.net for transaction: %s", transactionID)

    ctx, cancel := c.createRequestContext()
    defer cancel()
    
    httpReq, err := http.NewRequestWithContext(ctx, "POST", c.getEndpoint(), bytes.NewBuffer(jsonPayload))
    if err != nil {
        return fmt.Errorf("error creating void request: %v", err)
    }

    httpReq.Header.Set("Content-Type", "application/json")
    httpReq.Header.Set("Cache-Control", "no-cache")

    c.mutex.Lock()
    resp, err := c.client.Do(httpReq)
    c.mutex.Unlock()
    
    if err != nil {
        return fmt.Errorf("error making void request: %v", err)
    }
    defer resp.Body.Close()

    respBody, err := io.ReadAll(resp.Body)
    if err != nil {
        return fmt.Errorf("error reading void response body: %v", err)
    }

    log.Printf("Void response received in %v for transaction: %s", 
        time.Since(startTime), transactionID)

    // Remove BOM if present
    cleanBody := strings.TrimPrefix(string(respBody), "\ufeff")

    var response createTransactionResponse
    if err := json.Unmarshal([]byte(cleanBody), &response); err != nil {
        return fmt.Errorf("error decoding void response: %v, response body: %s", err, string(respBody))
    }

    if response.Messages.ResultCode == "Error" {
        if len(response.Messages.Message) > 0 {
            return fmt.Errorf("void transaction failed: %s (Code: %s)", 
                response.Messages.Message[0].Text, 
                response.Messages.Message[0].Code)
        }
        return fmt.Errorf("void transaction failed with unknown error")
    }

    if response.TransactionResponse.ResponseCode != "1" {
        if len(response.TransactionResponse.Messages) > 0 {
            return fmt.Errorf("void transaction failed: %s", response.TransactionResponse.Messages[0].Description)
        }
        return fmt.Errorf("void transaction failed with unknown error")
    }

    return nil
}