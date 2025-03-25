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
	
	log.Printf("Started %d worker goroutines", concurrency)
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
				time.Sleep(100 * time.Millisecond)
				continue
			}
			
			log.Printf("Worker %d processing job %s of type %s", workerID, job.ID, job.Type)
			
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
			
			ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
			completeErr := w.queue.CompleteJob(ctx, job)
			cancel()
			
			if completeErr != nil {
				log.Printf("Worker %d: Error marking job %s as complete: %v", workerID, job.ID, completeErr)
			}
		}
	}
}

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

func (w *Worker) processVoidTransaction(job *queue.Job) error {
	transactionID, ok := job.Data["transaction_id"].(string)
	if !ok || transactionID == "" {
		return fmt.Errorf("invalid transaction_id in job data")
	}
	
	log.Printf("Voiding transaction %s", transactionID)
	
	return w.paymentService.VoidTransaction(transactionID)
}

func (w *Worker) processCreateSubscription(job *queue.Job) error {

	checkoutID, ok := job.Data["checkout_id"].(string)
	if !ok || checkoutID == "" {
		return fmt.Errorf("invalid checkout_id in job data")
	}
	
	checkout, err := w.db.GetCheckoutData(checkoutID)
	if err != nil {
		return fmt.Errorf("failed to get checkout data: %v", err)
	}
	
	paymentRequest := &models.PaymentRequest{
		CardName:   job.Data["card_name"].(string),
		CardNumber: job.Data["card_number"].(string),
		Expiry:     job.Data["expiry"].(string),
		CVV:        job.Data["cvv"].(string),
		CheckoutID: checkoutID,
	}
	
	log.Printf("Setting up subscription for checkout %s", checkoutID)
	
	err = w.paymentService.SetupRecurringBilling(paymentRequest, checkout)
	if err != nil {
		return fmt.Errorf("failed to setup recurring billing: %v", err)
	}
	
	return nil
}

func StartWorker(cfg *config.Config, concurrency int) (*Worker, error) {
	db, err := database.NewConnection(cfg.Database)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %v", err)
	}
	
	paymentService := payment.NewPaymentService(
		cfg.AuthNet.APILoginID,
		cfg.AuthNet.TransactionKey,
		cfg.AuthNet.MerchantID,
		cfg.AuthNet.Environment,
	)
	
	queue, err := queue.NewQueue(cfg.Redis.URL, "payment_jobs")
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %v", err)
	}
	
	worker := NewWorker(queue, db, paymentService)
	worker.Start(concurrency)
	
	return worker, nil
}