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
	pendingQueue    = "notifications:pending"
	processingQueue = "notifications:processing"
	dedupPrefix     = "notified:"
	dedupTTL        = 1 * 24 * time.Hour // 1 day

	repoCheckQueue = "repocheck:pending"

	visibilityTimeout   = 60 * time.Second
	dequeuePollInterval = 200 * time.Millisecond
)

var dequeueScript = redis.NewScript(`
local job = redis.call('RPOP', KEYS[1])
if not job then
	return false
end
redis.call('ZADD', KEYS[2], ARGV[1], job)
return job
`)

var reclaimScript = redis.NewScript(`
local expired = redis.call('ZRANGEBYSCORE', KEYS[1], '-inf', ARGV[1])
for i = 1, #expired do
	redis.call('ZREM', KEYS[1], expired[i])
	redis.call('LPUSH', KEYS[2], expired[i])
end
return #expired
`)

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
	deadline := time.Now().Add(timeout)
	keys := []string{pendingQueue, processingQueue}
	for {
		score := time.Now().Add(visibilityTimeout).UnixMilli()
		data, err := dequeueScript.Run(ctx, q.rdb, keys, score).Text()
		if err == nil {
			var job domain.NotificationJob
			if err := json.Unmarshal([]byte(data), &job); err != nil {
				return nil, fmt.Errorf("unmarshalling job: %w", err)
			}
			return &job, nil
		}
		if err != redis.Nil {
			return nil, fmt.Errorf("dequeuing job: %w", err)
		}

		wait := time.Until(deadline)
		if wait <= 0 {
			return nil, nil
		}
		if wait > dequeuePollInterval {
			wait = dequeuePollInterval
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
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
	retry := job
	retry.Attempt++
	if err := q.Enqueue(ctx, retry); err != nil {
		return err
	}
	return q.Ack(ctx, job)
}

func (q *NotificationQueue) Ack(ctx context.Context, job domain.NotificationJob) error {
	data, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("marshalling job: %w", err)
	}
	return q.rdb.ZRem(ctx, processingQueue, data).Err()
}

func (q *NotificationQueue) Reclaim(ctx context.Context) (int, error) {
	now := time.Now().UnixMilli()
	n, err := reclaimScript.Run(ctx, q.rdb, []string{processingQueue, pendingQueue}, now).Int()
	if err != nil {
		return 0, fmt.Errorf("reclaiming expired jobs: %w", err)
	}
	return n, nil
}

type RepoCheckQueue struct {
	rdb *redis.Client
}

func NewRepoCheckQueue(rdb *redis.Client) *RepoCheckQueue {
	return &RepoCheckQueue{rdb: rdb}
}

func (q *RepoCheckQueue) EnqueueRepo(ctx context.Context, repo domain.Repository) error {
	data, err := json.Marshal(repo)
	if err != nil {
		return fmt.Errorf("marshalling repo: %w", err)
	}
	return q.rdb.LPush(ctx, repoCheckQueue, data).Err()
}

// DequeueRepo returns (nil, nil) when the timeout is reached with no repos.
func (q *RepoCheckQueue) DequeueRepo(ctx context.Context, timeout time.Duration) (*domain.Repository, error) {
	result, err := q.rdb.BRPop(ctx, timeout, repoCheckQueue).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dequeuing repo: %w", err)
	}
	var repo domain.Repository
	if err := json.Unmarshal([]byte(result[1]), &repo); err != nil {
		return nil, fmt.Errorf("unmarshalling repo: %w", err)
	}
	return &repo, nil
}
