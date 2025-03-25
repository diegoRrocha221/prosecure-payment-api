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

const (
    SandboxEndpoint = "https://apitest.authorize.net/xml/v1/request.api"
    ProductionEndpoint = "https://api.authorize.net/xml/v1/request.api"
    DuplicateWindow = 120 // Janela de duplicação em segundos (2 minutos)
)

type Client struct {
    apiLoginID     string
    transactionKey string
    merchantID     string
    environment    string
    client         *http.Client
}

func NewClient(apiLoginID, transactionKey, merchantID, environment string) *Client {
    return &Client{
        apiLoginID:     apiLoginID,
        transactionKey: transactionKey,
        merchantID:     merchantID,
        environment:    environment,
        client:         &http.Client{Timeout: 30 * time.Second},
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

func (c *Client) ProcessPayment(req *models.PaymentRequest) (*models.TransactionResponse, error) {
    // Criar um ID de pedido único para evitar duplicações
    orderID := fmt.Sprintf("Order-%s-%d", req.CheckoutID, time.Now().UnixNano())
    
    wrapper := createTransactionRequestWrapper{
        CreateTransactionRequest: createTransactionRequest{
            MerchantAuthentication: c.getMerchantAuthentication(),
            RefID: req.CheckoutID,
            TransactionRequest: transactionRequestType{
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
                // Adicionar configuração de janela de duplicação
                DuplicateWindow: DuplicateWindow,
                // Adicionar informações do cliente para melhorar a detecção de fraudes
                Customer: &CustomerType{
                    Type:  "individual",
                    Email: req.CustomerEmail,
                },
                // Adicionar informações de faturamento para melhorar a autorização
                BillTo: req.BillingInfo,
            },
        },
    }

    jsonPayload, err := json.Marshal(wrapper)
    if err != nil {
        return nil, fmt.Errorf("error marshaling request: %v", err)
    }

    log.Printf("Request to Authorize.net: %s", string(jsonPayload))

    httpReq, err := http.NewRequest("POST", c.getEndpoint(), bytes.NewBuffer(jsonPayload))
    if err != nil {
        return nil, fmt.Errorf("error creating request: %v", err)
    }

    httpReq.Header.Set("Content-Type", "application/json")

    resp, err := c.client.Do(httpReq)
    if err != nil {
        return nil, fmt.Errorf("error making request: %v", err)
    }
    defer resp.Body.Close()

    respBody, err := io.ReadAll(resp.Body)
    if err != nil {
        return nil, fmt.Errorf("error reading response body: %v", err)
    }

    log.Printf("Response from Authorize.net: %s", string(respBody))

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

    log.Printf("Void request to Authorize.net: %s", string(jsonPayload))

    httpReq, err := http.NewRequest("POST", c.getEndpoint(), bytes.NewBuffer(jsonPayload))
    if err != nil {
        return fmt.Errorf("error creating void request: %v", err)
    }

    httpReq.Header.Set("Content-Type", "application/json")

    resp, err := c.client.Do(httpReq)
    if err != nil {
        return fmt.Errorf("error making void request: %v", err)
    }
    defer resp.Body.Close()

    respBody, err := io.ReadAll(resp.Body)
    if err != nil {
        return fmt.Errorf("error reading void response body: %v", err)
    }

    log.Printf("Void response from Authorize.net: %s", string(respBody))

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