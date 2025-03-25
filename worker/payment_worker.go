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
	// Extract data from job
	checkoutID, ok := job.Data["checkout_id"].(string)
	if !ok || checkoutID == "" {
		return fmt.Errorf("invalid checkout_id in job data")
	}
	
	// Get checkout data
	checkout, err := w.db.GetCheckoutData(checkoutID)
	if err != nil {
		return fmt.Errorf("failed to get checkout data: %v", err)
	}
	
	// Create payment request from stored data
	paymentRequest := &models.PaymentRequest{
		CardName:   job.Data["card_name"].(string),
		CardNumber: job.Data["card_number"].(string),
		Expiry:     job.Data["expiry"].(string),
		CVV:        job.Data["cvv"].(string),
		CheckoutID: checkoutID,
	}
	
	log.Printf("Setting up subscription for checkout %s", checkoutID)
	
	// Setup the subscription
	err = w.paymentService.SetupRecurringBilling(paymentRequest, checkout)
	if err != nil {
		return fmt.Errorf("failed to setup recurring billing: %v", err)
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