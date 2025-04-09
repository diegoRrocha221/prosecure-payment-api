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
    RequestTimeout = 45 * time.Second // Reduzido para 15 segundos para evitar esperas longas
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
        TLSHandshakeTimeout: 30 * time.Second,
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
    
    // Log das credenciais e ambiente (sem expor a chave de transação completa)
    loginIDMasked := "***"
    if len(c.apiLoginID) > 3 {
        loginIDMasked = c.apiLoginID[:3] + "***"
    }
    log.Printf("Processing payment with API Login ID: %s, Environment: %s, Endpoint: %s", 
        loginIDMasked, c.environment, c.getEndpoint())
    
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
                // Removido: configurações de Silent Post que estavam causando o erro
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

    // Log da requisição (sem dados sensíveis)
    log.Printf("Sending payment request to Authorize.net for checkout: %s, Amount: %s, Order ID: %s", 
        req.CheckoutID, txRequest.Amount, orderID)

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
        log.Printf("HTTP request error: %v", err)
        return nil, fmt.Errorf("error making request: %v", err)
    }
    defer resp.Body.Close()

    respBody, err := io.ReadAll(resp.Body)
    if err != nil {
        log.Printf("Error reading response body: %v", err)
        return nil, fmt.Errorf("error reading response body: %v", err)
    }

    // Log do tempo de resposta e status HTTP
    log.Printf("Authorize.net response received in %v for checkout: %s, HTTP Status: %d", 
        time.Since(startTime), req.CheckoutID, resp.StatusCode)

    cleanBody := strings.TrimPrefix(string(respBody), "\ufeff")
    
    // Log da parte inicial do corpo da resposta (para diagnóstico, sem dados sensíveis)
    if len(cleanBody) > 500 {
        log.Printf("Response body preview (first 500 chars): %s", cleanBody[:500])
    } else {
        log.Printf("Response body: %s", cleanBody)
    }

    var response createTransactionResponse
    if err := json.Unmarshal([]byte(cleanBody), &response); err != nil {
        log.Printf("Error decoding response: %v", err)
        return nil, fmt.Errorf("error decoding response: %v, response body: %s", err, string(respBody))
    }

    // Log dos resultados da resposta
    log.Printf("Response result code: %s", response.Messages.ResultCode)
    if len(response.Messages.Message) > 0 {
        log.Printf("Response message: Code=%s, Text=%s", 
            response.Messages.Message[0].Code, 
            response.Messages.Message[0].Text)
    }

    // Verificar se é um erro de transação duplicada
    if response.Messages.ResultCode == "Error" {
        isDuplicate := false
        duplicateTransID := ""
        
        for _, msg := range response.Messages.Message {
            log.Printf("Error message: Code=%s, Text=%s", msg.Code, msg.Text)
            
            if msg.Code == "E00027" { // Código de erro de duplicação da Authorize.net
                isDuplicate = true
                // Tentar obter o ID da transação original
                if response.TransactionResponse.Errors != nil && len(response.TransactionResponse.Errors) > 0 {
                    for _, err := range response.TransactionResponse.Errors {
                        log.Printf("Transaction error: Code=%s, Text=%s", err.ErrorCode, err.ErrorText)
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

    // Log das informações da transação
    if response.TransactionResponse.TransID != "" {
        log.Printf("Transaction ID: %s, Response Code: %s", 
            response.TransactionResponse.TransID, 
            response.TransactionResponse.ResponseCode)
    }

    // Verificar se há erros na resposta da transação
    if response.TransactionResponse.ResponseCode != "1" {
        message := "Transaction failed"
        
        // Log detalhado dos erros da transação
        if response.TransactionResponse.Errors != nil && len(response.TransactionResponse.Errors) > 0 {
            for i, err := range response.TransactionResponse.Errors {
                log.Printf("Transaction error details [%d]: Code=%s, Text=%s", 
                    i, err.ErrorCode, err.ErrorText)
                // Usar a primeira mensagem de erro como mensagem de retorno
                if i == 0 {
                    message = err.ErrorText
                }
            }
        } else if len(response.TransactionResponse.Messages) > 0 {
            message = response.TransactionResponse.Messages[0].Description
            log.Printf("Transaction message: %s", message)
        }
        
        return &models.TransactionResponse{
            Success: false,
            Message: message,
        }, nil
    }

    log.Printf("Transaction successful with ID: %s", response.TransactionResponse.TransID)
    
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
        log.Printf("HTTP error in void request: %v", err)
        return fmt.Errorf("error making void request: %v", err)
    }
    defer resp.Body.Close()

    respBody, err := io.ReadAll(resp.Body)
    if err != nil {
        log.Printf("Error reading void response: %v", err)
        return fmt.Errorf("error reading void response body: %v", err)
    }

    log.Printf("Void response received in %v for transaction: %s", 
        time.Since(startTime), transactionID)

    // Remove BOM if present
    cleanBody := strings.TrimPrefix(string(respBody), "\ufeff")

    var response createTransactionResponse
    if err := json.Unmarshal([]byte(cleanBody), &response); err != nil {
        log.Printf("Error decoding void response: %v", err)
        return fmt.Errorf("error decoding void response: %v, response body: %s", err, string(respBody))
    }

    if response.Messages.ResultCode == "Error" {
        if len(response.Messages.Message) > 0 {
            log.Printf("Void transaction error: Code=%s, Text=%s", 
                response.Messages.Message[0].Code, 
                response.Messages.Message[0].Text)
            return fmt.Errorf("void transaction failed: %s (Code: %s)", 
                response.Messages.Message[0].Text, 
                response.Messages.Message[0].Code)
        }
        return fmt.Errorf("void transaction failed with unknown error")
    }

    if response.TransactionResponse.ResponseCode != "1" {
        if len(response.TransactionResponse.Messages) > 0 {
            log.Printf("Void transaction message: %s", 
                response.TransactionResponse.Messages[0].Description)
            return fmt.Errorf("void transaction failed: %s", 
                response.TransactionResponse.Messages[0].Description)
        }
        return fmt.Errorf("void transaction failed with unknown error")
    }

    log.Printf("Void transaction successful for transaction ID: %s", transactionID)
    return nil
}