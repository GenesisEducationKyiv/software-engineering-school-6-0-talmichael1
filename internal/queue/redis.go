package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github-release-notifier/internal/domain"
)

const repoCheckQueue = "repocheck:pending"

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
