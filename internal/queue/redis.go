package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github-release-notifier/internal/domain"
)

const (
	pendingQueue = "notifications:pending"
	dedupPrefix  = "notified:"
	dedupTTL     = 7 * 24 * time.Hour // 7 days
)

// NotificationQueue manages the Redis-backed notification job queue.
type NotificationQueue struct {
	rdb *redis.Client
}

func NewNotificationQueue(rdb *redis.Client) *NotificationQueue {
	return &NotificationQueue{rdb: rdb}
}

// Enqueue pushes a notification job onto the pending queue.
func (q *NotificationQueue) Enqueue(ctx context.Context, job domain.NotificationJob) error {
	data, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("marshalling job: %w", err)
	}
	return q.rdb.LPush(ctx, pendingQueue, data).Err()
}

// EnqueueBatch pushes multiple jobs in a single pipeline call.
func (q *NotificationQueue) EnqueueBatch(ctx context.Context, jobs []domain.NotificationJob) error {
	if len(jobs) == 0 {
		return nil
	}
	pipe := q.rdb.Pipeline()
	for _, job := range jobs {
		data, err := json.Marshal(job)
		if err != nil {
			return fmt.Errorf("marshalling job: %w", err)
		}
		pipe.LPush(ctx, pendingQueue, data)
	}
	_, err := pipe.Exec(ctx)
	return err
}

// Dequeue blocks until a job is available or the timeout expires.
// Returns nil, nil when the timeout is reached with no available jobs.
func (q *NotificationQueue) Dequeue(ctx context.Context, timeout time.Duration) (*domain.NotificationJob, error) {
	result, err := q.rdb.BRPop(ctx, timeout, pendingQueue).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dequeuing job: %w", err)
	}
	// BRPop returns [key, value].
	var job domain.NotificationJob
	if err := json.Unmarshal([]byte(result[1]), &job); err != nil {
		return nil, fmt.Errorf("unmarshalling job: %w", err)
	}
	return &job, nil
}

// MarkSent records that a notification was sent, preventing duplicate delivery.
// Returns true if this is the first time (not a duplicate).
func (q *NotificationQueue) MarkSent(ctx context.Context, subscriptionID int64, tag string) (bool, error) {
	key := fmt.Sprintf("%s%d:%s", dedupPrefix, subscriptionID, tag)
	_, err := q.rdb.SetArgs(ctx, key, "1", redis.SetArgs{
		TTL:  dedupTTL,
		Mode: "NX",
	}).Result()
	if err == redis.Nil {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("setting dedup key: %w", err)
	}
	return true, nil
}

// Requeue puts a failed job back into the pending queue for retry.
func (q *NotificationQueue) Requeue(ctx context.Context, job domain.NotificationJob) error {
	job.Attempt++
	return q.Enqueue(ctx, job)
}
