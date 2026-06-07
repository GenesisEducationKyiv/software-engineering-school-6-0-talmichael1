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

	repoCheckQueue = "repocheck:pending"
)

type NotificationQueue struct {
	rdb *redis.Client
}

func NewNotificationQueue(rdb *redis.Client) *NotificationQueue {
	return &NotificationQueue{rdb: rdb}
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
