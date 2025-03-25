package queue

import (

	"github.com/go-redis/redis/v8"
)

// JobType defines the type of background job
type JobType string

const (
	JobTypeVoidTransaction   JobType = "void_transaction"
	JobTypeCreateSubscription JobType = "create_subscription"
)

// Job represents a background job
type Job struct {
	ID         string                 `json:"id"`
	Type       JobType               `json:"type"`
	Data       map[string]interface{} `json:"data"`
	CreatedAt  time.Time             `json:"created_at"`
	RetryCount int                   `json:"retry_count"`
}

// Queue represents a Redis-backed job queue
type Queue struct {
	client     *redis.Client
	queueName  string
	processing string
	failed     string
}

// NewQueue creates a new Redis queue
func NewQueue(redisURL, queueName string) (*Queue, error) {
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("invalid Redis URL: %v", err)
	}

	client := redis.NewClient(opt)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Test connection
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

func (q *Queue) Client() *redis.Client {
	return q.client
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

// Dequeue gets a job from the queue
func (q *Queue) Dequeue(ctx context.Context, timeout time.Duration) (*Job, error) {
	// Use BLPOP to wait for a job with a timeout
	result, err := q.client.BLPop(ctx, timeout, q.queueName).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, nil // No jobs available
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

	// Move to processing queue
	err = q.client.RPush(ctx, q.processing, result[1]).Err()
	if err != nil {
		log.Printf("Warning: Failed to move job %s to processing queue: %v", job.ID, err)
	}

	return &job, nil
}

// CompleteJob marks a job as completed by removing it from the processing queue
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

// FailJob moves a job to the failed queue
func (q *Queue) FailJob(ctx context.Context, job *Job, err error) error {
	job.RetryCount++
	
	// Add error information to the job
	job.Data["last_error"] = err.Error()
	job.Data["failed_at"] = time.Now()

	jobJSON, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("failed to marshal job: %v", err)
	}

	// Remove from processing queue
	if err := q.client.LRem(ctx, q.processing, 1, jobJSON).Err(); err != nil {
		log.Printf("Warning: Failed to remove job %s from processing queue: %v", job.ID, err)
	}

	// Add to failed queue
	if err := q.client.RPush(ctx, q.failed, jobJSON).Err(); err != nil {
		return fmt.Errorf("failed to push job to failed queue: %v", err)
	}

	log.Printf("Moved job %s of type %s to failed queue", job.ID, job.Type)
	return nil
}

// ProcessDelayedJobs moves jobs from the delayed queue to the main queue when they're ready
func (q *Queue) ProcessDelayedJobs(ctx context.Context) error {
	delayedQueueName := q.queueName + ":delayed"
	now := float64(time.Now().Unix())
	
	// Find all jobs that are ready to be processed (score <= current time)
	jobs, err := q.client.ZRangeByScore(ctx, delayedQueueName, &redis.ZRangeBy{
		Min: "0",
		Max: fmt.Sprintf("%f", now),
	}).Result()
	
	if err != nil {
		return fmt.Errorf("failed to get delayed jobs: %v", err)
	}
	
	for _, jobJSON := range jobs {
		// Move to main queue
		if err := q.client.RPush(ctx, q.queueName, jobJSON).Err(); err != nil {
			log.Printf("Warning: Failed to move delayed job to main queue: %v", err)
			continue
		}
		
		// Remove from delayed queue
		if err := q.client.ZRem(ctx, delayedQueueName, jobJSON).Err(); err != nil {
			log.Printf("Warning: Failed to remove job from delayed queue: %v", err)
			continue
		}
		
		var job Job
		if err := json.Unmarshal([]byte(jobJSON), &job); err != nil {
			log.Printf("Warning: Failed to unmarshal job: %v", err)
			continue
		}
		
		log.Printf("Moved delayed job %s of type %s to main queue for retry %d", 
			job.ID, job.Type, job.RetryCount)
	}
	
	return nil
}

// RetryJob moves a job back to the main queue for retry
func (q *Queue) RetryJob(ctx context.Context, jobID string) error {
	// Find job in failed queue
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
			// Remove from failed queue
			if err := q.client.LRem(ctx, q.failed, 1, jobJSON).Err(); err != nil {
				return fmt.Errorf("failed to remove job from failed queue: %v", err)
			}

			// Add back to main queue
			if err := q.client.RPush(ctx, q.queueName, jobJSON).Err(); err != nil {
				return fmt.Errorf("failed to push job to main queue: %v", err)
			}

			log.Printf("Requeued job %s of type %s", job.ID, job.Type)
			return nil
		}
	}

	return fmt.Errorf("job %s not found in failed queue", jobID)
}

// Close closes the Redis connection
func (q *Queue) Close() error {
	return q.client.Close()
}