package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/go-redis/redis/v8"
)

type JobType string

const (
	JobTypeVoidTransaction    JobType = "void_transaction"
	JobTypeCreateSubscription JobType = "create_subscription"
	JobTypeProcessPayment     JobType = "process_payment"     
	JobTypeCreateAccount      JobType = "create_account"
	JobTypeDelayedPayment     JobType = "delayed_payment"     // Novo tipo para pagamento com delay
)

type Job struct {
	ID         string                 `json:"id"`
	Type       JobType               `json:"type"`
	Data       map[string]interface{} `json:"data"`
	CreatedAt  time.Time             `json:"created_at"`
	RetryCount int                   `json:"retry_count"`
}

type Queue struct {
	client     *redis.Client
	queueName  string
	processing string
	failed     string
}

func NewQueue(redisURL, queueName string) (*Queue, error) {
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("invalid Redis URL: %v", err)
	}

	client := redis.NewClient(opt)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %v", err)
	}

	return &Queue{
		client:     client,
		queueName:  queueName,
		processing: queueName + ":processing",
		failed:     queueName + ":failed",
	}, nil
}

func (q *Queue) Enqueue(ctx context.Context, jobType JobType, data map[string]interface{}) error {
	job := Job{
		ID:        fmt.Sprintf("%d", time.Now().UnixNano()),
		Type:      jobType,
		Data:      data,
		CreatedAt: time.Now(),
	}

	jobJSON, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("failed to marshal job: %v", err)
	}

	err = q.client.RPush(ctx, q.queueName, jobJSON).Err()
	if err != nil {
		return fmt.Errorf("failed to push job to queue: %v", err)
	}

	log.Printf("Enqueued job %s of type %s", job.ID, job.Type)
	return nil
}

// EnqueueDelayed adiciona um job para ser processado após um delay específico
func (q *Queue) EnqueueDelayed(ctx context.Context, jobType JobType, data map[string]interface{}, delay time.Duration) error {
	job := Job{
		ID:        fmt.Sprintf("%d", time.Now().UnixNano()),
		Type:      jobType,
		Data:      data,
		CreatedAt: time.Now(),
	}

	jobJSON, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("failed to marshal job: %v", err)
	}

	// Calcular quando o job deve ser processado
	executeAt := time.Now().Add(delay)
	score := float64(executeAt.Unix())

	// Adicionar ao sorted set com score sendo o timestamp de execução
	delayedQueueName := q.queueName + ":delayed"
	err = q.client.ZAdd(ctx, delayedQueueName, &redis.Z{
		Score:  score,
		Member: jobJSON,
	}).Err()

	if err != nil {
		return fmt.Errorf("failed to push delayed job to queue: %v", err)
	}

	log.Printf("Enqueued delayed job %s of type %s to execute at %s", 
		job.ID, job.Type, executeAt.Format("2006-01-02 15:04:05"))
	return nil
}

func (q *Queue) Dequeue(ctx context.Context, timeout time.Duration) (*Job, error) {
	result, err := q.client.BLPop(ctx, timeout, q.queueName).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, nil 
		}
		return nil, fmt.Errorf("failed to get job from queue: %v", err)
	}

	if len(result) < 2 {
		return nil, fmt.Errorf("unexpected BLPOP result format")
	}

	var job Job
	if err := json.Unmarshal([]byte(result[1]), &job); err != nil {
		return nil, fmt.Errorf("failed to unmarshal job: %v", err)
	}

	err = q.client.RPush(ctx, q.processing, result[1]).Err()
	if err != nil {
		log.Printf("Warning: Failed to move job %s to processing queue: %v", job.ID, err)
	}

	return &job, nil
}

func (q *Queue) CompleteJob(ctx context.Context, job *Job) error {
	jobJSON, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("failed to marshal job: %v", err)
	}

	err = q.client.LRem(ctx, q.processing, 1, jobJSON).Err()
	if err != nil {
		return fmt.Errorf("failed to remove job from processing queue: %v", err)
	}

	log.Printf("Completed job %s of type %s", job.ID, job.Type)
	return nil
}

// CORREÇÃO: Melhorar lógica de retry e controle de email
func (q *Queue) FailJob(ctx context.Context, job *Job, err error) error {
	job.RetryCount++
	
	job.Data["last_error"] = err.Error()
	job.Data["failed_at"] = time.Now()

	const maxRetries = 5
	
	jobJSON, marshalErr := json.Marshal(job)
	if marshalErr != nil {
		return fmt.Errorf("failed to marshal job: %v", marshalErr)
	}

	if err := q.client.LRem(ctx, q.processing, 1, jobJSON).Err(); err != nil {
		log.Printf("Warning: Failed to remove job %s from processing queue: %v", job.ID, err)
	}
	
	// CORREÇÃO: Melhor controle de retry com informação sobre última tentativa
	if job.RetryCount <= maxRetries {
		delaySeconds := 15 * (1 << (job.RetryCount - 1))
		retryTime := time.Now().Add(time.Duration(delaySeconds) * time.Second)
		
		job.Data["next_retry_at"] = retryTime
		job.Data["is_last_attempt"] = job.RetryCount == maxRetries // CORREÇÃO: Flag para última tentativa
		
		updatedJobJSON, _ := json.Marshal(job)
		
		delayedQueueName := q.queueName + ":delayed"
		score := float64(retryTime.Unix())
		
		if err := q.client.ZAdd(ctx, delayedQueueName, &redis.Z{
			Score:  score,
			Member: updatedJobJSON,
		}).Err(); err != nil {
			log.Printf("Warning: Failed to add job to delayed queue, adding to failed queue: %v", err)
			if err := q.client.RPush(ctx, q.failed, updatedJobJSON).Err(); err != nil {
				return fmt.Errorf("failed to push job to failed queue: %v", err)
			}
		}
		
		log.Printf("Job %s of type %s scheduled for retry %d/%d in %d seconds (last_attempt: %v)", 
			job.ID, job.Type, job.RetryCount, maxRetries, delaySeconds, job.RetryCount == maxRetries)
		return nil
	}
	
	// CORREÇÃO: Marcar explicitamente que todas as tentativas falharam
	job.Data["all_retries_exhausted"] = true
	job.Data["final_failure_at"] = time.Now()
	finalJobJSON, _ := json.Marshal(job)
	
	if err := q.client.RPush(ctx, q.failed, finalJobJSON).Err(); err != nil {
		return fmt.Errorf("failed to push job to failed queue: %v", err)
	}

	log.Printf("Job %s of type %s moved to failed queue after %d retries (all attempts exhausted)", job.ID, job.Type, job.RetryCount)
	return nil
}

func (q *Queue) ProcessDelayedJobs(ctx context.Context) error {
	delayedQueueName := q.queueName + ":delayed"
	now := float64(time.Now().Unix())
	
	jobs, err := q.client.ZRangeByScore(ctx, delayedQueueName, &redis.ZRangeBy{
		Min: "0",
		Max: fmt.Sprintf("%f", now),
	}).Result()
	
	if err != nil {
		return fmt.Errorf("failed to get delayed jobs: %v", err)
	}
	
	for _, jobJSON := range jobs {
		if err := q.client.RPush(ctx, q.queueName, jobJSON).Err(); err != nil {
			log.Printf("Warning: Failed to move delayed job to main queue: %v", err)
			continue
		}
		
		if err := q.client.ZRem(ctx, delayedQueueName, jobJSON).Err(); err != nil {
			log.Printf("Warning: Failed to remove job from delayed queue: %v", err)
			continue
		}
		
		var job Job
		if err := json.Unmarshal([]byte(jobJSON), &job); err != nil {
			log.Printf("Warning: Failed to unmarshal job: %v", err)
			continue
		}
		
		log.Printf("Moved delayed job %s of type %s to main queue for processing (retry %d)", 
			job.ID, job.Type, job.RetryCount)
	}
	
	return nil
}

func (q *Queue) RetryJob(ctx context.Context, jobID string) error {
	jobs, err := q.client.LRange(ctx, q.failed, 0, -1).Result()
	if err != nil {
		return fmt.Errorf("failed to list failed jobs: %v", err)
	}

	for _, jobJSON := range jobs {
		var job Job
		if err := json.Unmarshal([]byte(jobJSON), &job); err != nil {
			log.Printf("Warning: Failed to unmarshal job: %v", err)
			continue
		}

		if job.ID == jobID {
			if err := q.client.LRem(ctx, q.failed, 1, jobJSON).Err(); err != nil {
				return fmt.Errorf("failed to remove job from failed queue: %v", err)
			}

			// CORREÇÃO: Resetar contador de retry para retry manual
			job.RetryCount = 0
			job.Data["manual_retry"] = true
			job.Data["manual_retry_at"] = time.Now()
			
			// Remover flags de falha anterior
			delete(job.Data, "all_retries_exhausted")
			delete(job.Data, "final_failure_at")
			delete(job.Data, "is_last_attempt")
			
			updatedJobJSON, _ := json.Marshal(job)

			if err := q.client.RPush(ctx, q.queueName, updatedJobJSON).Err(); err != nil {
				return fmt.Errorf("failed to push job to main queue: %v", err)
			}

			log.Printf("Manually requeued job %s of type %s (retry count reset)", job.ID, job.Type)
			return nil
		}
	}

	return fmt.Errorf("job %s not found in failed queue", jobID)
}

// CORREÇÃO: Nova função para verificar se é a última tentativa baseada nos dados do job
func (q *Queue) IsLastAttempt(job *Job) bool {
	if isLast, exists := job.Data["is_last_attempt"]; exists {
		if lastAttempt, ok := isLast.(bool); ok {
			return lastAttempt
		}
	}
	// Fallback: verificar se atingiu o máximo de retries
	const maxRetries = 5
	return job.RetryCount >= maxRetries
}

func (q *Queue) Client() *redis.Client {
	return q.client
}

func (q *Queue) Close() error {
	return q.client.Close()
}