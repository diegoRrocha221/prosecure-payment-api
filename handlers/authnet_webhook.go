package handlers

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"time"
	
	"prosecure-payment-api/database"
	"prosecure-payment-api/models"
	"prosecure-payment-api/queue"
	"prosecure-payment-api/services/payment"
)

type WebhookHandler struct {
	db             *database.Connection
	queue          *queue.Queue
	paymentService *payment.Service
}

func NewWebhookHandler(db *database.Connection, q *queue.Queue, ps *payment.Service) *WebhookHandler {
	return &WebhookHandler{
		db:             db,
		queue:          q,
		paymentService: ps,
	}
}

// HandleSilentPost processa as notificações da Authorize.net via Silent Post
func (h *WebhookHandler) HandleSilentPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		log.Printf("Error parsing silent post form: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Log da notificação recebida
	transactionID := r.FormValue("x_trans_id")
	responseCode := r.FormValue("x_response_code")
	responseReasonCode := r.FormValue("x_response_reason_code")
	responseReasonText := r.FormValue("x_response_reason_text")
	
	log.Printf("Received Silent Post notification for transaction %s: code=%s, reason=%s, text=%s",
		transactionID, responseCode, responseReasonCode, responseReasonText)

	// Enviar uma resposta 200 OK imediatamente
	w.WriteHeader(http.StatusOK)
	
	// Processar o resultado em background
	go h.processNotification(transactionID, responseCode)
}

// HandleRelayResponse processa os redirecionamentos da Authorize.net via Relay Response
func (h *WebhookHandler) HandleRelayResponse(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		log.Printf("Error parsing relay response form: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Log do redirecionamento recebido
	transactionID := r.FormValue("x_trans_id")
	responseCode := r.FormValue("x_response_code")
	
	log.Printf("Received Relay Response for transaction %s: code=%s", transactionID, responseCode)

	// Responder com HTML que será exibido ao cliente (pode redirecionar para sua página de sucesso/erro)
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(`
		<!DOCTYPE html>
		<html>
		<head>
			<title>Transaction Processing</title>
			<meta http-equiv="refresh" content="0;url=https://prosecurelsp.com/checkout/result?tx_id=` + transactionID + `">
		</head>
		<body>
			<p>Processing your transaction. Redirecting...</p>
		</body>
		</html>
	`))
	
	// Processar o resultado em background
	go h.processNotification(transactionID, responseCode)
}

// HandleSubscriptionNotification processa notificações relacionadas a assinaturas
func (h *WebhookHandler) HandleSubscriptionNotification(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		log.Printf("Error parsing subscription notification form: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Extrair informações relevantes
	subscriptionID := r.FormValue("x_subscription_id")
	eventType := r.FormValue("x_event_type")
	
	log.Printf("Received subscription notification for subscription %s: event=%s", 
		subscriptionID, eventType)
	
	// Responder imediatamente com 200 OK
	w.WriteHeader(http.StatusOK)
	
	// Processar a notificação de assinatura em background
	go h.processSubscriptionNotification(subscriptionID, eventType)
}

// processNotification processa as notificações e enfileira jobs conforme necessário
func (h *WebhookHandler) processNotification(transactionID, responseCode string) {
	// Apenas processa se a transação foi aprovada (código 1)
	if responseCode != "1" {
		log.Printf("Transaction %s not approved (response code %s). No background jobs will be queued.", 
			transactionID, responseCode)
		return
	}

	// Buscar informações da transação no banco de dados
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Verificar se a transação já foi processada
	var status string
	err := h.db.GetDB().QueryRowContext(ctx, 
		"SELECT status FROM transactions WHERE transaction_id = ?", 
		transactionID).Scan(&status)
	
	if err != nil {
		if err == sql.ErrNoRows {
			log.Printf("Transaction %s not found in database", transactionID)
		} else {
			log.Printf("Error checking transaction status: %v", err)
		}
		return
	}
	
	// Se a transação já foi processada (void ou falha), não processa novamente
	if status != "authorized" {
		log.Printf("Transaction %s has already been processed (status=%s). Skipping.", 
			transactionID, status)
		return
	}

	// Buscar o checkout_id associado à transação
	var checkoutID string
	err = h.db.GetDB().QueryRowContext(ctx, 
		"SELECT checkout_id FROM transactions WHERE transaction_id = ?", 
		transactionID).Scan(&checkoutID)
	
	if err != nil {
		log.Printf("Error finding checkout ID for transaction %s: %v", transactionID, err)
		return
	}

	// Enfileirar job para anular a transação (void)
	err = h.queue.Enqueue(ctx, queue.JobTypeVoidTransaction, map[string]interface{}{
		"transaction_id": transactionID,
		"checkout_id":    checkoutID,
	})
	
	if err != nil {
		log.Printf("Error enqueueing void transaction job: %v", err)
	} else {
		log.Printf("Successfully queued void job for transaction %s", transactionID)
	}

	// Buscar os dados necessários para configurar a assinatura recorrente
	checkout, err := h.db.GetCheckoutData(checkoutID)
	if err != nil {
		log.Printf("Error retrieving checkout data for ID %s: %v", checkoutID, err)
		return
	}
	
	// Buscar informações de pagamento salvas temporariamente
	var cardNumber, cardExpiry, cardCVV, cardName string
	err = h.db.GetDB().QueryRowContext(ctx, 
		`SELECT card_number, card_expiry, card_cvv, card_name 
		 FROM temp_payment_data 
		 WHERE checkout_id = ? 
		 AND created_at > NOW() - INTERVAL 1 HOUR`,
		checkoutID).Scan(&cardNumber, &cardExpiry, &cardCVV, &cardName)
	
	if err != nil {
		log.Printf("Error retrieving payment data for checkout %s: %v", checkoutID, err)
		return
	}
	
	// Enfileirar job para configurar assinatura recorrente com todos os dados necessários
	err = h.queue.Enqueue(ctx, queue.JobTypeCreateSubscription, map[string]interface{}{
		"checkout_id":    checkoutID,
		"transaction_id": transactionID,
		"card_number":    cardNumber,
		"card_expiry":    cardExpiry,
		"card_cvv":       cardCVV,
		"card_name":      cardName,
		"email":          checkout.Email,
	})
	
	if err != nil {
		log.Printf("Error enqueueing subscription job: %v", err)
	} else {
		log.Printf("Successfully queued subscription job for checkout %s", checkoutID)
	}
	
	// Limpar dados de pagamento temporários após processamento
	_, err = h.db.GetDB().ExecContext(ctx,
		"DELETE FROM temp_payment_data WHERE checkout_id = ?",
		checkoutID)
	
	if err != nil {
		log.Printf("Warning: Failed to clean up temporary payment data: %v", err)
	}
}

// processSubscriptionNotification processa notificações relacionadas a assinaturas
func (h *WebhookHandler) processSubscriptionNotification(subscriptionID, eventType string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	log.Printf("Processing subscription notification for %s: %s", subscriptionID, eventType)
	
	// Atualizar o status da assinatura no banco de dados
	var status string
	switch eventType {
	case "subscription_cancelled":
		status = "cancelled"
	case "subscription_suspended":
		status = "suspended"
	case "subscription_terminated":
		status = "terminated"
	case "subscription_expired":
		status = "expired"
	case "subscription_failed":
		status = "failed"
	case "subscription_successful":
		status = "active"
	default:
		log.Printf("Unknown subscription event type: %s", eventType)
		return
	}
	
	// Atualizar o status no banco de dados
	_, err := h.db.GetDB().ExecContext(ctx,
		`UPDATE subscriptions 
		 SET status = ?, updated_at = NOW() 
		 WHERE subscription_id = ?`,
		status, subscriptionID)
	
	if err != nil {
		log.Printf("Error updating subscription status: %v", err)
		return
	}
	
	log.Printf("Successfully updated subscription %s status to %s", subscriptionID, status)
	
	// Para falhas de pagamento, podemos registrar e possivelmente notificar o cliente
	if eventType == "subscription_failed" {
		log.Printf("Subscription payment failed for subscription %s. Consider sending a notification.", subscriptionID)
		
		// Código para enviar notificação de falha por email pode ser adicionado aqui
	}
}

// Função para armazenar temporariamente os dados do cartão de forma segura
// Nota: Em produção, considere usar criptografia ou tokenização
func (h *WebhookHandler) StoreTemporaryPaymentData(w http.ResponseWriter, r *http.Request) {
	var req models.PaymentDataStorage
	if err := r.ParseForm(); err != nil {
		log.Printf("Error parsing payment storage form: %v", err)
		sendErrorResponse(w, http.StatusBadRequest, "Invalid request format")
		return
	}
	
	checkoutID := r.FormValue("checkout_id")
	cardNumber := r.FormValue("card_number")
	cardExpiry := r.FormValue("card_expiry")
	cardCVV := r.FormValue("card_cvv")
	cardName := r.FormValue("card_name")
	
	if checkoutID == "" || cardNumber == "" || cardExpiry == "" || cardCVV == "" || cardName == "" {
		sendErrorResponse(w, http.StatusBadRequest, "Missing required payment information")
		return
	}
	
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	// Inserir ou atualizar os dados de pagamento temporários
	_, err := h.db.GetDB().ExecContext(ctx,
		`INSERT INTO temp_payment_data
		 (checkout_id, card_number, card_expiry, card_cvv, card_name, created_at)
		 VALUES (?, ?, ?, ?, ?, NOW())
		 ON DUPLICATE KEY UPDATE
		 card_number = VALUES(card_number),
		 card_expiry = VALUES(card_expiry),
		 card_cvv = VALUES(card_cvv),
		 card_name = VALUES(card_name),
		 created_at = NOW()`,
		checkoutID, cardNumber, cardExpiry, cardCVV, cardName)
	
	if err != nil {
		log.Printf("Error storing temporary payment data: %v", err)
		sendErrorResponse(w, http.StatusInternalServerError, "Failed to store payment data")
		return
	}
	
	// Configurar uma limpeza programada após algumas horas por segurança
	go func() {
		time.Sleep(1 * time.Hour)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		
		_, err := h.db.GetDB().ExecContext(ctx,
			"DELETE FROM temp_payment_data WHERE checkout_id = ?",
			checkoutID)
		
		if err != nil {
			log.Printf("Error cleaning up temporary payment data: %v", err)
		}
	}()
	
	sendSuccessResponse(w, models.APIResponse{
		Status:  "success",
		Message: "Payment data stored temporarily",
	})
}