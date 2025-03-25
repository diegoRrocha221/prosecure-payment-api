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
	JobTypeVoidTransaction   JobType = "void_transaction"
	JobTypeCreateSubscription JobType = "create_subscription"
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

// Enqueue adds a job to the queue
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

func (q *Queue) Dequeue(ctx context.Context, timeout time.Duration) (*Job, error) {
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

func (q *Queue) FailJob(ctx context.Context, job *Job, err error) error {
	job.RetryCount++
	
	job.Data["last_error"] = err.Error()
	job.Data["failed_at"] = time.Now()

	jobJSON, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("failed to marshal job: %v", err)
	}

	if err := q.client.LRem(ctx, q.processing, 1, jobJSON).Err(); err != nil {
		log.Printf("Warning: Failed to remove job %s from processing queue: %v", job.ID, err)
	}

	if err := q.client.RPush(ctx, q.failed, jobJSON).Err(); err != nil {
		return fmt.Errorf("failed to push job to failed queue: %v", err)
	}

	log.Printf("Moved job %s of type %s to failed queue", job.ID, job.Type)
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

			if err := q.client.RPush(ctx, q.queueName, jobJSON).Err(); err != nil {
				return fmt.Errorf("failed to push job to main queue: %v", err)
			}

			log.Printf("Requeued job %s of type %s", job.ID, job.Type)
			return nil
		}
	}

	return fmt.Errorf("job %s not found in failed queue", jobID)
}

func (q *Queue) Close() error {
	return q.client.Close()
}