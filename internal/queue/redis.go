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
	dedupTTL     = 1 * 24 * time.Hour // 1 day
)

type NotificationQueue struct {
	rdb *redis.Client
}

func NewNotificationQueue(rdb *redis.Client) *NotificationQueue {
	return &NotificationQueue{rdb: rdb}
}

func (q *NotificationQueue) Enqueue(ctx context.Context, job domain.NotificationJob) error {
	data, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("marshalling job: %w", err)
	}
	return q.rdb.LPush(ctx, pendingQueue, data).Err()
}

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

// Dequeue returns (nil, nil) when the timeout is reached with no jobs.
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

func (q *NotificationQueue) IsSent(ctx context.Context, subscriptionID int64, tag string) (bool, error) {
	key := fmt.Sprintf("%s%d:%s", dedupPrefix, subscriptionID, tag)
	exists, err := q.rdb.Exists(ctx, key).Result()
	if err != nil {
		return false, fmt.Errorf("checking dedup key: %w", err)
	}
	return exists > 0, nil
}

func (q *NotificationQueue) MarkSent(ctx context.Context, subscriptionID int64, tag string) error {
	key := fmt.Sprintf("%s%d:%s", dedupPrefix, subscriptionID, tag)
	return q.rdb.Set(ctx, key, "1", dedupTTL).Err()
}

func (q *NotificationQueue) Requeue(ctx context.Context, job domain.NotificationJob) error {
	job.Attempt++
	return q.Enqueue(ctx, job)
}
