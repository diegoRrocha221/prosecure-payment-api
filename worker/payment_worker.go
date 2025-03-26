package worker

import (
	"context"
	"fmt"
	"log"
	"time"

	"prosecure-payment-api/config"
	"prosecure-payment-api/database"
	"prosecure-payment-api/models"
	"prosecure-payment-api/queue"
	"prosecure-payment-api/services/payment"
)

// Worker handles background payment processing tasks
type Worker struct {
	queue          *queue.Queue
	db             *database.Connection
	paymentService *payment.Service
	shutdown       chan struct{}
	isRunning      bool
}

// NewWorker creates a new worker
func NewWorker(q *queue.Queue, db *database.Connection, ps *payment.Service) *Worker {
	return &Worker{
		queue:          q,
		db:             db,
		paymentService: ps,
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
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			job, err := w.queue.Dequeue(ctx, 5*time.Second)
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
				
				// Wait a bit after an error
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
	default:
		return fmt.Errorf("unknown job type: %s", job.Type)
	}
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
			"SELECT card_name, card_number, card_expiry, card_cvv FROM temp_payment_data WHERE checkout_id = ?", 
			checkoutID).Scan(&tempCardName, &tempCardNumber, &tempExpiry, &tempCVV)
		
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
		return fmt.Errorf("insufficient payment data for subscription creation. Fields missing: %s%s%s%s", 
			cardName == "" ? "cardName " : "", 
			cardNumber == "" ? "cardNumber " : "", 
			expiry == "" ? "expiry " : "", 
			cvv == "" ? "cvv" : "")
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
	
	// Connect to Redis queue
	queue, err := queue.NewQueue(cfg.Redis.URL, "payment_jobs")
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %v", err)
	}
	
	// Create and start worker
	worker := NewWorker(queue, db, paymentService)
	worker.Start(concurrency)
	
	return worker, nil
}