package worker

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"math"
	"strings"
	"time"

	"prosecure-payment-api/config"
	"prosecure-payment-api/database"
	"prosecure-payment-api/models"
	"prosecure-payment-api/queue"
	"prosecure-payment-api/services/email"
	"prosecure-payment-api/services/payment"
	"prosecure-payment-api/types"
	"prosecure-payment-api/utils"
	"github.com/google/uuid"
)

// Worker handles background payment processing tasks
type Worker struct {
	queue          *queue.Queue
	db             *database.Connection
	paymentService *payment.Service
	emailService   *email.SMTPService
	shutdown       chan struct{}
	isRunning      bool
}

// NewWorker creates a new worker
func NewWorker(q *queue.Queue, db *database.Connection, ps *payment.Service, es *email.SMTPService) *Worker {
	return &Worker{
		queue:          q,
		db:             db,
		paymentService: ps,
		emailService:   es,
		shutdown:       make(chan struct{}),
	}
}

// Start begins processing jobs
func (w *Worker) Start(concurrency int) {
	w.isRunning = true
	
	for i := 0; i < concurrency; i++ {
		go w.processJobs(i)
	}
	
	// Start a goroutine to process delayed jobs
	go w.processDelayedJobs()
	
	log.Printf("Started %d worker goroutines and delayed job processor", concurrency)
}

// processDelayedJobs periodically checks for delayed jobs that are ready to be processed
func (w *Worker) processDelayedJobs() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-w.shutdown:
			log.Println("Delayed job processor shutting down")
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := w.queue.ProcessDelayedJobs(ctx)
			cancel()
			
			if err != nil {
				log.Printf("Error processing delayed jobs: %v", err)
			}
		}
	}
}

// Stop signals the worker to stop processing jobs
func (w *Worker) Stop() {
	if !w.isRunning {
		return
	}
	
	log.Println("Stopping worker...")
	close(w.shutdown)
	w.isRunning = false
}

// processJobs continuously processes jobs from the queue
func (w *Worker) processJobs(workerID int) {
	log.Printf("Worker %d starting", workerID)
	
	for {
		select {
		case <-w.shutdown:
			log.Printf("Worker %d shutting down", workerID)
			return
		default:
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			job, err := w.queue.Dequeue(ctx, 3*time.Second)
			cancel()
			
			if err != nil {
				log.Printf("Worker %d: Error dequeuing job: %v", workerID, err)
				time.Sleep(time.Second)
				continue
			}
			
			if job == nil {
				// No jobs available, wait before trying again
				time.Sleep(100 * time.Millisecond)
				continue
			}
			
			log.Printf("Worker %d processing job %s of type %s", workerID, job.ID, job.Type)
			
			// Process the job
			jobErr := w.processJob(job)
			if jobErr != nil {
				log.Printf("Worker %d: Error processing job %s: %v", workerID, job.ID, jobErr)
				
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				failErr := w.queue.FailJob(ctx, job, jobErr)
				cancel()
				
				if failErr != nil {
					log.Printf("Worker %d: Error marking job %s as failed: %v", workerID, job.ID, failErr)
				}
				
				time.Sleep(time.Second)
				continue
			}
			
			// Mark job as complete
			ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
			completeErr := w.queue.CompleteJob(ctx, job)
			cancel()
			
			if completeErr != nil {
				log.Printf("Worker %d: Error marking job %s as complete: %v", workerID, job.ID, completeErr)
			}
		}
	}
}

// processJob processes a single job
func (w *Worker) processJob(job *queue.Job) error {
	switch job.Type {
	case queue.JobTypeVoidTransaction:
		return w.processVoidTransaction(job)
	case queue.JobTypeCreateSubscription:
		return w.processCreateSubscription(job)
	case queue.JobTypeProcessPayment:
		return w.processPaymentJob(job)
	case queue.JobTypeCreateAccount:
		return w.processCreateAccountJob(job)
	case queue.JobTypeDelayedPayment:
		return w.processDelayedPaymentJob(job) // Novo processador para pagamento com delay
	default:
		return fmt.Errorf("unknown job type: %s", job.Type)
	}
}

// processDelayedPaymentJob - Processa apenas o PAGAMENTO (conta já foi criada)
func (w *Worker) processDelayedPaymentJob(job *queue.Job) error {
    checkoutID, ok := job.Data["checkout_id"].(string)
    if !ok || checkoutID == "" {
        return fmt.Errorf("invalid checkout_id in job data")
    }
    
    requestID, _ := job.Data["request_id"].(string)
    if requestID == "" {
        requestID = job.ID
    }
    
    log.Printf("[RequestID: %s] Processing delayed PAYMENT ONLY for checkout: %s (account already created)", requestID, checkoutID)
    
    // Verificar se o checkout já foi processado para pagamento
    var paymentProcessed bool
    err := w.db.GetDB().QueryRow(
        `SELECT EXISTS(
            SELECT 1 FROM payment_results 
            WHERE checkout_id = ? AND status = 'success'
        )`,
        checkoutID).Scan(&paymentProcessed)
    
    if err != nil {
        return fmt.Errorf("failed to check if payment is processed: %v", err)
    }
    
    if paymentProcessed {
        log.Printf("[RequestID: %s] Payment for checkout %s already processed, skipping", requestID, checkoutID)
        return nil
    }
    
    // Obter dados do checkout
    checkout, err := w.db.GetCheckoutData(checkoutID)
    if err != nil {
        return fmt.Errorf("failed to get checkout data: %v", err)
    }
    
    // Obter os dados do cartão armazenados temporariamente
    var cardNumber, cardExpiry, cardCVV, cardName string
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    
    err = w.db.GetDB().QueryRowContext(ctx, 
        `SELECT card_number, card_expiry, card_cvv, card_name 
         FROM temp_payment_data 
         WHERE checkout_id = ? 
         AND created_at > NOW() - INTERVAL 2 HOUR`, // 2 horas para dar margem
        checkoutID).Scan(&cardNumber, &cardExpiry, &cardCVV, &cardName)
    
    if err != nil {
        log.Printf("[RequestID: %s] Failed to retrieve payment data: %v", requestID, err)
        return w.handlePaymentFailure(checkout, requestID, "Payment data not found or expired", true) // enviar email
    }
    
    // Criar objeto de requisição de pagamento
    paymentReq := &models.PaymentRequest{
        CardName:      cardName,
        CardNumber:    cardNumber,
        Expiry:        cardExpiry,
        CVV:           cardCVV,
        CheckoutID:    checkoutID,
        CustomerEmail: checkout.Email,
    }
    
    // Adicionar informações de billing
    if checkout.Street != "" {
        paymentReq.BillingInfo = &types.BillingInfoType{
            FirstName:   strings.Split(checkout.Name, " ")[0],
            LastName:    strings.Join(strings.Split(checkout.Name, " ")[1:], " "),
            Address:     checkout.Street,
            City:        checkout.City,
            State:       checkout.State,
            Zip:         checkout.ZipCode,
            Country:     "US",
            PhoneNumber: checkout.PhoneNumber,
        }
    }
    
    // ETAPA 1: Processar transação teste de $1 (com tentativas)
    log.Printf("[RequestID: %s] Step 1: Processing test transaction", requestID)
    
    var resp *models.TransactionResponse
    var transactionErr error
    maxAttempts := 3
    
    for attempt := 1; attempt <= maxAttempts; attempt++ {
        if attempt > 1 {
            log.Printf("[RequestID: %s] Retry test transaction attempt %d/%d", requestID, attempt, maxAttempts)
            time.Sleep(time.Duration(attempt) * time.Second) // 1s, 2s, 3s
        }
        
        resp, transactionErr = w.paymentService.ProcessInitialAuthorization(paymentReq)
        if transactionErr == nil && resp != nil && resp.Success {
            break // Sucesso!
        }
        
        // Log do erro da tentativa
        if transactionErr != nil {
            log.Printf("[RequestID: %s] Test transaction attempt %d failed: %v", requestID, attempt, transactionErr)
        } else if resp != nil {
            log.Printf("[RequestID: %s] Test transaction attempt %d declined: %s", requestID, attempt, resp.Message)
        }
        
        // Se não é a última tentativa, não envia email ainda
        if attempt < maxAttempts {
            continue
        }
    }
    
    // Verificar se todas as tentativas falharam
    if transactionErr != nil || resp == nil || !resp.Success {
        finalError := "Test transaction failed after all attempts"
        if transactionErr != nil {
            finalError = fmt.Sprintf("Test transaction failed: %v", transactionErr)
        } else if resp != nil {
            finalError = fmt.Sprintf("Test transaction declined: %s", resp.Message)
        }
        
        log.Printf("[RequestID: %s] All test transaction attempts failed", requestID)
        return w.handlePaymentFailure(checkout, requestID, finalError, true) // true = enviar email
    }
    
    transactionID := resp.TransactionID
    log.Printf("[RequestID: %s] Test transaction successful: %s", requestID, transactionID)
    
    // ETAPA 2: Fazer VOID da transação teste
    log.Printf("[RequestID: %s] Step 2: Voiding test transaction", requestID)
    
    voidErr := w.paymentService.VoidTransaction(transactionID)
    if voidErr != nil {
        log.Printf("[RequestID: %s] Failed to void test transaction: %v", requestID, voidErr)
        return w.handlePaymentFailure(checkout, requestID, fmt.Sprintf("Failed to void test transaction: %v", voidErr), true) // enviar email
    }
    
    log.Printf("[RequestID: %s] Test transaction voided successfully", requestID)
    
    // ETAPA 3: Criar ARB (assinatura recorrente)
    log.Printf("[RequestID: %s] Step 3: Creating subscription (ARB)", requestID)
    
    subscriptionErr := w.paymentService.SetupRecurringBilling(paymentReq, checkout)
    if subscriptionErr != nil {
        log.Printf("[RequestID: %s] Failed to setup subscription: %v", requestID, subscriptionErr)
        return w.handlePaymentFailure(checkout, requestID, fmt.Sprintf("Failed to setup subscription: %v", subscriptionErr), true) // enviar email
    }
    
    log.Printf("[RequestID: %s] Subscription created successfully", requestID)
    
    // ETAPA 4: Atualizar transação no banco (já que a conta já existe)
    log.Printf("[RequestID: %s] Step 4: Updating transaction record", requestID)
    
    // Buscar o master_reference da conta que já foi criada
    var masterRef string
    masterErr := w.db.GetDB().QueryRowContext(ctx, 
        `SELECT reference_uuid FROM master_accounts 
         WHERE username = ? AND email = ? LIMIT 1`,
        checkout.Username, checkout.Email).Scan(&masterRef)
    
    if masterErr != nil {
        log.Printf("[RequestID: %s] Warning: Could not find master account: %v", requestID, masterErr)
        // Mesmo assim, considera sucesso pois o pagamento funcionou
    } else {
        // Atualizar a transação pendente com o ID real
        _, updateErr := w.db.GetDB().ExecContext(ctx,
            `UPDATE transactions 
             SET transaction_id = ?, status = 'authorized', updated_at = NOW()
             WHERE master_reference = ? AND transaction_id = 'PENDING'`,
            transactionID, masterRef)
        
        if updateErr != nil {
            log.Printf("[RequestID: %s] Warning: Failed to update transaction: %v", requestID, updateErr)
        }
    }
    
    // Atualizar status do pagamento para sucesso
    _, statusErr := w.db.GetDB().ExecContext(ctx,
        `INSERT INTO payment_results 
         (request_id, checkout_id, status, transaction_id, created_at)
         VALUES (?, ?, 'success', ?, NOW())
         ON DUPLICATE KEY UPDATE
         status = 'success',
         transaction_id = ?,
         created_at = NOW()`,
        requestID, checkoutID, transactionID, transactionID)
    
    if statusErr != nil {
        log.Printf("[RequestID: %s] Warning: Failed to update payment status: %v", requestID, statusErr)
    }
    
    // Limpar dados de cartão temporários
    _, cleanupErr := w.db.GetDB().ExecContext(ctx,
        "DELETE FROM temp_payment_data WHERE checkout_id = ?",
        checkoutID)
    
    if cleanupErr != nil {
        log.Printf("[RequestID: %s] Warning: Failed to clean up temporary payment data: %v", requestID, cleanupErr)
    }
    
    // ETAPA 5: ENVIAR EMAIL DE INVOICE (apenas após sucesso completo)
    log.Printf("[RequestID: %s] Step 5: Sending invoice email after successful payment processing", requestID)
    
    invoiceEmailContent := w.generateInvoiceEmail(checkout)
    emailErr := w.emailService.SendEmail(
        checkout.Email,
        "Your invoice has been delivered :)",
        invoiceEmailContent,
    )
    
    if emailErr != nil {
        log.Printf("[RequestID: %s] Warning: Failed to send invoice email: %v", requestID, emailErr)
        // Não falha o processo por causa do email - pagamento já foi processado com sucesso
    } else {
        log.Printf("[RequestID: %s] Invoice email sent successfully to %s", requestID, checkout.Email)
    }
    
    log.Printf("[RequestID: %s] Delayed payment processing completed successfully", requestID)
    return nil
}

// handlePaymentFailure - Trata falhas de pagamento, enviando email apenas se sendEmail = true
func (w *Worker) handlePaymentFailure(checkout *models.CheckoutData, requestID, errorMsg string, sendEmail bool) error {
    log.Printf("[RequestID: %s] Handling payment failure for %s: %s (sendEmail: %v)", requestID, checkout.Email, errorMsg, sendEmail)
    
    // Atualizar status do pagamento para falha
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    
    _, dbErr := w.db.GetDB().ExecContext(ctx,
        `INSERT INTO payment_results 
         (request_id, checkout_id, status, error_message, created_at)
         VALUES (?, ?, 'failed', ?, NOW())
         ON DUPLICATE KEY UPDATE
         status = 'failed',
         error_message = ?,
         created_at = NOW()`,
        requestID, checkout.ID, errorMsg, errorMsg)
    
    if dbErr != nil {
        log.Printf("[RequestID: %s] Warning: Failed to update payment failure status: %v", requestID, dbErr)
    }
    
    // Marcar usuário como inativo (is_active = 9) se existir
    markErr := w.db.MarkUserInactive(checkout.Email, checkout.Username)
    if markErr != nil {
        log.Printf("[RequestID: %s] Warning: Failed to mark user inactive: %v", requestID, markErr)
    }
    
    // Enviar email de notificação de falha APENAS se solicitado
    if sendEmail {
        failureEmailContent := w.generatePaymentFailureEmail(checkout.Name, errorMsg)
        
        emailErr := w.emailService.SendEmail(
            checkout.Email,
            "Payment Processing Issue - Action Required",
            failureEmailContent,
        )
        
        if emailErr != nil {
            log.Printf("[RequestID: %s] Warning: Failed to send failure email: %v", requestID, emailErr)
            // Não retorna erro pois o importante é registrar a falha
        } else {
            log.Printf("[RequestID: %s] Payment failure email sent to %s", requestID, checkout.Email)
        }
    } else {
        log.Printf("[RequestID: %s] Skipping failure email (not final attempt)", requestID)
    }
    
    // Limpar dados de cartão temporários
    _, cleanupErr := w.db.GetDB().ExecContext(ctx,
        "DELETE FROM temp_payment_data WHERE checkout_id = ?",
        checkout.ID)
    
    if cleanupErr != nil {
        log.Printf("[RequestID: %s] Warning: Failed to clean up temporary payment data: %v", requestID, cleanupErr)
    }
    
    // Retorna o erro original para que seja registrado no job
    return fmt.Errorf("payment processing failed: %s", errorMsg)
}

// generatePaymentFailureEmail - Gera email de falha de pagamento
func (w *Worker) generatePaymentFailureEmail(customerName, errorMessage string) string {
    footer := "If you need assistance, please don't hesitate to contact our support team. We're here to help you get your ProSecureLSP account set up successfully."
    
    return fmt.Sprintf(
        email.PaymentFailureEmailTemplate,
        customerName,    // %s - Hi customerName!
        errorMessage,    // %s - Descrição do erro
        footer,          // %s - Footer message
    )
}

// processVoidTransaction voids an authorized transaction
func (w *Worker) processVoidTransaction(job *queue.Job) error {
	transactionID, ok := job.Data["transaction_id"].(string)
	if !ok || transactionID == "" {
		return fmt.Errorf("invalid transaction_id in job data")
	}
	
	log.Printf("Voiding transaction %s", transactionID)
	
	return w.paymentService.VoidTransaction(transactionID)
}

// processPaymentJob processa o pagamento de forma assíncrona (método legado mantido para compatibilidade)
func (w *Worker) processPaymentJob(job *queue.Job) error {
    checkoutID, ok := job.Data["checkout_id"].(string)
    if !ok || checkoutID == "" {
        return fmt.Errorf("invalid checkout_id in job data")
    }
    
    requestID, _ := job.Data["request_id"].(string)
    if requestID == "" {
        requestID = job.ID
    }
    
    log.Printf("[RequestID: %s] Processing payment job for checkout: %s", requestID, checkoutID)
    
    // Obter dados do checkout
    checkout, err := w.db.GetCheckoutData(checkoutID)
    if err != nil {
        return fmt.Errorf("failed to get checkout data: %v", err)
    }
    
    // Obter os dados do cartão armazenados temporariamente
    var cardNumber, cardExpiry, cardCVV, cardName string
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    
    err = w.db.GetDB().QueryRowContext(ctx, 
        `SELECT card_number, card_expiry, card_cvv, card_name 
         FROM temp_payment_data 
         WHERE checkout_id = ? 
         AND created_at > NOW() - INTERVAL 1 HOUR`,
        checkoutID).Scan(&cardNumber, &cardExpiry, &cardCVV, &cardName)
    
    if err != nil {
        return fmt.Errorf("failed to retrieve payment data: %v", err)
    }
    
    // Criar objeto de requisição de pagamento
    paymentReq := &models.PaymentRequest{
        CardName:      cardName,
        CardNumber:    cardNumber,
        Expiry:        cardExpiry,
        CVV:           cardCVV,
        CheckoutID:    checkoutID,
        CustomerEmail: checkout.Email,
    }
    
    // Adicionar informações de billing se disponíveis
    if checkout.Street != "" {
        paymentReq.BillingInfo = &types.BillingInfoType{
            FirstName:   strings.Split(checkout.Name, " ")[0],
            LastName:    strings.Join(strings.Split(checkout.Name, " ")[1:], " "),
            Address:     checkout.Street,
            City:        checkout.City,
            State:       checkout.State,
            Zip:         checkout.ZipCode,
            Country:     "US",
            PhoneNumber: checkout.PhoneNumber,
        }
    }
    
    // Processar o pagamento (com retries)
    var resp *models.TransactionResponse
    var processErr error
    
    for attempt := 0; attempt < 3; attempt++ {
        if attempt > 0 {
            log.Printf("[RequestID: %s] Retry payment attempt %d", requestID, attempt+1)
            time.Sleep(time.Duration(math.Pow(2, float64(attempt))) * time.Second)
        }
        
        resp, processErr = w.paymentService.ProcessInitialAuthorization(paymentReq)
        if processErr == nil && (resp == nil || !resp.Success) {
            processErr = fmt.Errorf("payment unsuccessful: %s", resp.Message)
        }
        
        if processErr == nil {
            break
        }
        
        // Se não for um erro de timeout, não faz mais tentativas
        if !strings.Contains(processErr.Error(), "deadline exceeded") && 
           !strings.Contains(processErr.Error(), "timeout") {
            break
        }
    }
    
    // Atualizar o resultado no banco de dados 
    status := "failed"
    transactionID := ""
    errorMsg := ""
    
    if processErr != nil {
        errorMsg = processErr.Error()
        log.Printf("[RequestID: %s] Payment processing error: %v", requestID, processErr)
    } else if resp.Success {
        status = "success"
        transactionID = resp.TransactionID
        log.Printf("[RequestID: %s] Payment processed successfully: %s", requestID, transactionID)
        
        // Enfileirar jobs subsequentes (void, subscription, account creation)
        ctxJobs := context.Background()
        
        // Void transaction job
        err := w.queue.Enqueue(ctxJobs, queue.JobTypeVoidTransaction, map[string]interface{}{
            "transaction_id": transactionID,
            "checkout_id":    checkoutID,
            "request_id":     requestID,
        })
        if err != nil {
            log.Printf("[RequestID: %s] Failed to enqueue void transaction job: %v", requestID, err)
        }
        
        // Subscription job
        err = w.queue.Enqueue(ctxJobs, queue.JobTypeCreateSubscription, map[string]interface{}{
            "checkout_id":    checkoutID,
            "transaction_id": transactionID,
            "card_number":    cardNumber,
            "card_expiry":    cardExpiry, 
            "card_cvv":       cardCVV,
            "card_name":      cardName,
            "email":          checkout.Email,
            "request_id":     requestID,
        })
        if err != nil {
            log.Printf("[RequestID: %s] Failed to enqueue subscription job: %v", requestID, err)
        }
        
        // Account creation job - SOMENTE após pagamento bem-sucedido
        err = w.queue.Enqueue(ctxJobs, queue.JobTypeCreateAccount, map[string]interface{}{
            "checkout_id":    checkoutID,
            "transaction_id": transactionID,
            "card_number":    cardNumber,
            "card_expiry":    cardExpiry, 
            "card_cvv":       cardCVV,
            "card_name":      cardName,
            "request_id":     requestID,
        })
        if err != nil {
            log.Printf("[RequestID: %s] Failed to enqueue account creation job: %v", requestID, err)
        }
    } else {
        errorMsg = resp.Message
        log.Printf("[RequestID: %s] Payment declined: %s", requestID, errorMsg)
    }
    
    // Armazenar o resultado no banco de dados para consulta
    _, err = w.db.GetDB().ExecContext(ctx,
        `INSERT INTO payment_results 
         (request_id, checkout_id, status, transaction_id, error_message, created_at)
         VALUES (?, ?, ?, ?, ?, NOW())
         ON DUPLICATE KEY UPDATE
         status = ?,
         transaction_id = ?,
         error_message = ?,
         created_at = NOW()`,
        requestID, checkoutID, status, transactionID, errorMsg,
        status, transactionID, errorMsg)
    
    if err != nil {
        log.Printf("[RequestID: %s] Failed to store payment result: %v", requestID, err)
    }
    
    // Se o pagamento falhou, limpar os dados temporários
    if status == "failed" {
        _, err = w.db.GetDB().ExecContext(ctx,
            "DELETE FROM temp_payment_data WHERE checkout_id = ?",
            checkoutID)
        
        if err != nil {
            log.Printf("[RequestID: %s] Warning: Failed to clean up temporary payment data: %v", requestID, err)
        }
    }
    
    return nil
}

// processCreateAccountJob processa a criação de conta após pagamento bem-sucedido
func (w *Worker) processCreateAccountJob(job *queue.Job) error {
    checkoutID, ok := job.Data["checkout_id"].(string)
    if !ok || checkoutID == "" {
        return fmt.Errorf("invalid checkout_id in job data")
    }
    
    transactionID, ok := job.Data["transaction_id"].(string)
    if !ok || transactionID == "" {
        return fmt.Errorf("invalid transaction_id in job data")
    }
    
    requestID, _ := job.Data["request_id"].(string)
    if requestID == "" {
        requestID = job.ID
    }
    
    log.Printf("[RequestID: %s] Processing account creation for checkout: %s", requestID, checkoutID)
    
    // Verificar se o pagamento foi realmente bem-sucedido
    var paymentStatus string
    err := w.db.GetDB().QueryRow(
        `SELECT status 
         FROM payment_results 
         WHERE checkout_id = ? AND transaction_id = ?
         ORDER BY created_at DESC 
         LIMIT 1`,
        checkoutID, transactionID).Scan(&paymentStatus)
    
    if err != nil || paymentStatus != "success" {
        return fmt.Errorf("cannot create account - payment not successful: %v", err)
    }
    
    // Verificar se a conta já foi criada
    var accountExists bool
    err = w.db.GetDB().QueryRow(
        `SELECT EXISTS(
            SELECT 1 FROM master_accounts ma 
            JOIN transactions t ON t.master_reference = ma.reference_uuid 
            WHERE t.checkout_id = ?
        )`,
        checkoutID).Scan(&accountExists)
    
    if err != nil {
        return fmt.Errorf("error checking if account exists: %v", err)
    }
    
    if accountExists {
        log.Printf("[RequestID: %s] Account already exists for checkout %s", requestID, checkoutID)
        return nil
    }
    
    // Obter dados do checkout
    checkout, err := w.db.GetCheckoutData(checkoutID)
    if err != nil {
        return fmt.Errorf("failed to get checkout data: %v", err)
    }
    
    // Extrair dados do cartão do job
    cardNumber, _ := job.Data["card_number"].(string)
    cardExpiry, _ := job.Data["card_expiry"].(string)
    
    cardData := &models.CardData{
        Number: cardNumber,
        Expiry: cardExpiry,
    }
    
    log.Printf("[RequestID: %s] Creating account for checkout %s", requestID, checkoutID)
    
    // Criar a conta do usuário utilizando a mesma lógica do PaymentHandler
    err = w.createAccountsAndNotify(checkout, cardData, transactionID)
    if err != nil {
        return fmt.Errorf("error creating account: %v", err)
    }
    
    // Limpar dados de cartão temporários depois de criar a conta
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    
    _, err = w.db.GetDB().ExecContext(ctx,
        "DELETE FROM temp_payment_data WHERE checkout_id = ?",
        checkoutID)
    
    if err != nil {
        log.Printf("[RequestID: %s] Warning: Failed to clean up temporary payment data: %v", requestID, err)
    }
    
    log.Printf("[RequestID: %s] Successfully created account for checkout %s", requestID, checkoutID)
    return nil
}

// createAccountsAndNotify - Método copiado do PaymentHandler para criar contas
func (w *Worker) createAccountsAndNotify(checkout *models.CheckoutData, cardData *models.CardData, transactionID string) error {
    startTime := time.Now()
    defer func() {
        log.Printf("Account creation and notifications completed in %v for checkout ID: %s", 
            time.Since(startTime), checkout.ID)
    }()
    
    tx, err := w.db.BeginTransaction()
    if err != nil {
        return fmt.Errorf("failed to begin transaction: %v", err)
    }

    masterUUID := uuid.New().String()
    log.Printf("Generated master UUID: %s", masterUUID)

    names := strings.Fields(checkout.Name)
    firstName := names[0]
    lastName := strings.Join(names[1:], " ")

    // Preparar a conta master
    masterAccount := &models.MasterAccount{
        Name:             firstName,
        LastName:         lastName,
        ReferenceUUID:    masterUUID,
        Email:            checkout.Email,
        Username:         checkout.Username,
        PhoneNumber:      checkout.PhoneNumber,
        IsAnnually:      0,
        IsTrial:         1,
        State:           checkout.State,
        City:            checkout.City,
        Street:          checkout.Street,
        ZipCode:         checkout.ZipCode,
        AdditionalInfo:  checkout.Additional,
        Plan:            checkout.PlanID,
        PurchasedPlans:  checkout.PlansJSON,
        SimultaneousUsers: len(checkout.Plans),
        RenewDate:       time.Now().AddDate(0, 1, 0),
    }

    // Calcular preço total
    var total float64
    for _, plan := range checkout.Plans {
        if plan.Annually == 1 {
            masterAccount.IsAnnually = 1
            total += plan.Price * 10 
        } else {
            total += plan.Price
        }
    }
    masterAccount.TotalPrice = total

    // Salvar a conta master
    if err := tx.SaveMasterAccount(masterAccount); err != nil {
        tx.Rollback()
        return fmt.Errorf("failed to create master account: %v", err)
    }

    // Criar usuário
    user := &models.User{
        MasterReference: masterUUID,
        Username:        checkout.Username,
        Email:          checkout.Email,
        Passphrase:     checkout.Passphrase,
        IsMaster:       1,
        PlanID:         checkout.Plans[0].PlanID,
    }

    if err := tx.SaveUser(user); err != nil {
        tx.Rollback()
        return fmt.Errorf("failed to create user: %v", err)
    }

    // Salvar método de pagamento
    if err := tx.SavePaymentMethod(masterUUID, cardData); err != nil {
        tx.Rollback()
        return fmt.Errorf("failed to save payment method: %v", err)
    }

    // Criar faturas
    trialInvoice := &models.Invoice{
        MasterReference: masterUUID,
        IsTrial:        1,
        Total:          0,
        DueDate:        time.Now(),
        IsPaid:         1,
    }

    if err := tx.SaveInvoice(trialInvoice); err != nil {
        tx.Rollback()
        return fmt.Errorf("failed to create trial invoice: %v", err)
    }

    futureDate := time.Now().AddDate(0, 1, 0)
    futureInvoice := &models.Invoice{
        MasterReference: masterUUID,
        IsTrial:        0,
        Total:          total,
        DueDate:        futureDate,
        IsPaid:         0,
    }

    if err := tx.SaveInvoice(futureInvoice); err != nil {
        tx.Rollback()
        return fmt.Errorf("failed to create future invoice: %v", err)
    }

    // Salvar transação
    if err := tx.SaveTransaction(masterUUID, checkout.ID, 1.00, "authorized", transactionID); err != nil {
        tx.Rollback()
        return fmt.Errorf("failed to save transaction: %v", err)
    }

    // Salvar assinatura (inicialmente vazia)
    if err := tx.SaveSubscription(masterUUID, "pending", futureDate, ""); err != nil {
        tx.Rollback()
        return fmt.Errorf("failed to save subscription: %v", err)
    }

    // Commit da transação
    if err := tx.Commit(); err != nil {
        return fmt.Errorf("failed to commit transaction: %v", err)
    }

    // Gerar código de ativação e preparar URLs
    code := utils.GenerateActivationCode()
    encodedUser := base64.StdEncoding.EncodeToString([]byte(checkout.Username))
    encodedEmail := base64.StdEncoding.EncodeToString([]byte(checkout.Email))
    encodedCode := base64.StdEncoding.EncodeToString([]byte(code))

    // Atualizar código de ativação no DB
    updateErr := w.db.UpdateUserActivationCode(checkout.Email, checkout.Username, code)
    if updateErr != nil {
        log.Printf("Warning: Failed to update activation code: %v", updateErr)
        // Continue mesmo com erro - o usuário ainda poderá ativar de outras formas
    }

    // Criar URL de ativação
    activationURL := fmt.Sprintf(
        "https://prosecurelsp.com/users/active/activation.php?act=%s&emp=%s&cct=%s",
        encodedUser, encodedEmail, encodedCode,
    )

    // Preparar e enviar APENAS email de ativação
    activationEmailContent := w.generateActivationEmail(checkout.Name, activationURL)

    // Enviar email de ativação
    activationErr := w.emailService.SendEmail(
        checkout.Email,
        "Please Confirm Your Email Address",
        activationEmailContent,
    )
    
    if activationErr != nil {
        log.Printf("Warning: Failed to send activation email: %v", activationErr)
        // Continua mesmo com erro no email - conta já foi criada
    } else {
        log.Printf("Activation email sent successfully to %s", checkout.Email)
    }
    
    // NOTA: Email de invoice será enviado apenas após sucesso completo do pagamento no processDelayedPaymentJob

    log.Printf("Successfully created accounts and sent notifications for master reference: %s", masterUUID)
    return nil
}

func (w *Worker) generateActivationEmail(username, activationURL string) string {
    content := "In order to activate your account, we need to confirm your email address. Once we do, " +
        "you will be able to log into your Administrator Portal and begin setting up your devices " +
        "on the most advanced security service on the planet."
    
    footer := "Thank you so much,\nThe ProSecureLSP Team"
    
    return fmt.Sprintf(
        email.ActivationEmailTemplate,
        username,        // %s - Hi username!
        content,         // %s - Conteúdo da mensagem
        activationURL,   // %s - Link do botão
        activationURL,   // %s - URL fallback
        footer,          // %s - Footer message
    )
}

func (w *Worker) generateInvoiceEmail(checkout *models.CheckoutData) string {
    var total float64
    
    // Gerar tabela de planos de forma mais simples e robusta
    plansTable := `<table class="plans-table">
        <thead>
            <tr>
                <th>Plan Name</th>
                <th>Price</th>
            </tr>
        </thead>
        <tbody>`
    
    for _, plan := range checkout.Plans {
        planPrice := plan.Price
        if plan.Annually == 1 {
            total += plan.Price * 10
            planPrice = plan.Price * 10
        } else {
            total += plan.Price
        }
        
        plansTable += fmt.Sprintf(`
            <tr>
                <td>%s</td>
                <td>$%.2f</td>
            </tr>`, 
            plan.PlanName, 
            planPrice,
        )
    }
    plansTable += `
        </tbody>
    </table>`

    // Seção de totais mais clara
    totalsSection := fmt.Sprintf(`
        <p><strong>Subtotal:</strong> $%.2f</p>
        <p><strong>Discount:</strong> $%.2f</p>
        <p><strong>Validation charge (refunded):</strong> $0.01</p>
        <p style="font-size: 18px; font-weight: bold; color: #28a745;"><strong>Total Paid:</strong> $0.01</p>
    `, total, total-0.01)

    footer := fmt.Sprintf(
        "Thank you %s for choosing our services. If you have any questions, please contact our support team.",
        checkout.Name,
    )

    // Gerar número da invoice único
    invoiceNumber := fmt.Sprintf("INV-%s", time.Now().Format("20060102-150405"))

    return fmt.Sprintf(
        email.InvoiceEmailTemplate,
        invoiceNumber,   // %s - Invoice number
        plansTable,      // %s - Plans table HTML
        totalsSection,   // %s - Totals section HTML
        "Paid",          // %s - Status
        footer,          // %s - Footer message
    )
}

// processCreateSubscription sets up a recurring billing subscription
func (w *Worker) processCreateSubscription(job *queue.Job) error {
	log.Printf("Processing subscription creation job %s", job.ID)
	
	// Extrair dados do job com verificações de tipo
	checkoutID, ok := job.Data["checkout_id"].(string)
	if !ok || checkoutID == "" {
		return fmt.Errorf("invalid checkout_id in job data")
	}
	
	// Obter dados do checkout
	checkout, err := w.db.GetCheckoutData(checkoutID)
	if err != nil {
		return fmt.Errorf("failed to get checkout data: %v", err)
	}
	
	// Extrair os dados do cartão com verificações de segurança
	var cardName, cardNumber, expiry, cvv, email string
	
	// Verificar cada campo individualmente com fallbacks seguros
	if cardNameVal, ok := job.Data["card_name"]; ok && cardNameVal != nil {
		if cardNameStr, ok := cardNameVal.(string); ok {
			cardName = cardNameStr
		}
	}
	
	if cardNumberVal, ok := job.Data["card_number"]; ok && cardNumberVal != nil {
		if cardNumberStr, ok := cardNumberVal.(string); ok {
			cardNumber = cardNumberStr
		}
	}
	
	if expiryVal, ok := job.Data["expiry"]; ok && expiryVal != nil {
		if expiryStr, ok := expiryVal.(string); ok {
			expiry = expiryStr
		}
	}
	
	if cvvVal, ok := job.Data["cvv"]; ok && cvvVal != nil {
		if cvvStr, ok := cvvVal.(string); ok {
			cvv = cvvStr
		}
	}
	
	if emailVal, ok := job.Data["email"]; ok && emailVal != nil {
		if emailStr, ok := emailVal.(string); ok {
			email = emailStr
		} else {
			// Se não conseguir extrair o email do job, use o do checkout
			email = checkout.Email
		}
	} else {
		// Fallback para o email do checkout
		email = checkout.Email
	}
	
	// Verificar se temos todos os dados necessários
	if cardName == "" || cardNumber == "" || expiry == "" || cvv == "" {
		log.Printf("Missing card information in job data. Attempting to retrieve from database.")
		
		// Verificar se a tabela existe
		var tableExists int
		tableCheckCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		
		// Esta consulta funciona em MySQL/MariaDB
		err := w.db.GetDB().QueryRowContext(tableCheckCtx, 
			"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'temp_payment_data'").Scan(&tableExists)
		
		if err != nil || tableExists == 0 {
			log.Printf("Error checking temp_payment_data table existence: %v", err)
			
			// Tentar criar a tabela
			_, err = w.db.GetDB().ExecContext(tableCheckCtx, `
				CREATE TABLE IF NOT EXISTS temp_payment_data (
					checkout_id VARCHAR(36) PRIMARY KEY,
					card_number VARCHAR(19) NOT NULL,
					card_expiry VARCHAR(5) NOT NULL,
					card_cvv VARCHAR(4) NOT NULL,
					card_name VARCHAR(255) NOT NULL,
					created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
				)
			`)
			
			if err != nil {
				log.Printf("Failed to create temp_payment_data table: %v", err)
				return fmt.Errorf("insufficient payment data and could not create table: %v", err)
			}
			
			log.Printf("Created temp_payment_data table")
			return fmt.Errorf("insufficient payment data and temp_payment_data table was just created - please try again")
		}
		
		// A tabela existe, tentar recuperar os dados
		dataCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		
		var tempCardName, tempCardNumber, tempExpiry, tempCVV string
		err = w.db.GetDB().QueryRowContext(dataCtx, 
			"SELECT card_name, card_number, card_expiry, card_cvv FROM temp_payment_data WHERE checkout_id = ?",checkoutID).Scan(&tempCardName, &tempCardNumber, &tempExpiry, &tempCVV)
		
			if err != nil {
				log.Printf("Error retrieving payment data from temp_payment_data: %v", err)
				return fmt.Errorf("failed to retrieve payment data from database: %v", err)
			}
			
			// Usar os dados recuperados
			cardName = tempCardName
			cardNumber = tempCardNumber
			expiry = tempExpiry
			cvv = tempCVV
		}
		
		// Verificar se ainda temos dados insuficientes
		if cardName == "" || cardNumber == "" || expiry == "" || cvv == "" {
			missingFields := ""
			if cardName == "" {
				missingFields += "cardName "
			}
			if cardNumber == "" {
				missingFields += "cardNumber "
			}
			if expiry == "" {
				missingFields += "expiry "
			}
			if cvv == "" {
				missingFields += "cvv"
			}
			return fmt.Errorf("insufficient payment data for subscription creation. Fields missing: %s", missingFields)
		}
		
		// Criar objeto de requisição de pagamento com os dados obtidos
		paymentRequest := &models.PaymentRequest{
			CardName:      cardName,
			CardNumber:    cardNumber,
			Expiry:        expiry,
			CVV:           cvv,
			CheckoutID:    checkoutID,
			CustomerEmail: email,
		}
		
		log.Printf("Setting up subscription for checkout %s", checkoutID)
		
		// Configurar a assinatura recorrente
		err = w.paymentService.SetupRecurringBilling(paymentRequest, checkout)
		if err != nil {
			return fmt.Errorf("failed to setup recurring billing: %v", err)
		}
		
		// Atualizar o status da assinatura no banco de dados
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		
		// Buscar o master_reference associado ao checkout
		var masterRef string
		err = w.db.GetDB().QueryRowContext(ctx, 
			"SELECT master_reference FROM transactions WHERE checkout_id = ? LIMIT 1", 
			checkoutID).Scan(&masterRef)
		
		if err != nil {
			return fmt.Errorf("failed to get master reference: %v", err)
		}
		
		// Atualizar a assinatura com o ID da assinatura da Authorize.net
		_, err = w.db.GetDB().ExecContext(ctx, 
			"UPDATE subscriptions SET status = 'active' WHERE master_reference = ?", 
			masterRef)
		
		if err != nil {
			return fmt.Errorf("failed to update subscription status: %v", err)
		}
		
		log.Printf("Successfully set up subscription for checkout %s", checkoutID)
		
		// Limpar os dados temporários do cartão depois de processar com sucesso
		cleanCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		
		_, err = w.db.GetDB().ExecContext(cleanCtx,
			"DELETE FROM temp_payment_data WHERE checkout_id = ?",
			checkoutID)
		
		if err != nil {
			log.Printf("Warning: Failed to clean up temporary payment data: %v", err)
		}
		
		return nil
	}
	
	// Start a worker with configuration
	func StartWorker(cfg *config.Config, concurrency int) (*Worker, error) {
		// Connect to database
		db, err := database.NewConnection(cfg.Database)
		if err != nil {
			return nil, fmt.Errorf("failed to connect to database: %v", err)
		}
		
		// Create payment service
		paymentService := payment.NewPaymentService(
			cfg.AuthNet.APILoginID,
			cfg.AuthNet.TransactionKey,
			cfg.AuthNet.MerchantID,
			cfg.AuthNet.Environment,
		)
		
		// Create email service
		emailService := email.NewSMTPService(cfg.SMTP)
		
		// Connect to Redis queue
		queue, err := queue.NewQueue(cfg.Redis.URL, "payment_jobs")
		if err != nil {
			return nil, fmt.Errorf("failed to connect to Redis: %v", err)
		}
		
		// Create and start worker
		worker := NewWorker(queue, db, paymentService, emailService)
		worker.Start(concurrency)
		
		return worker, nil
	}