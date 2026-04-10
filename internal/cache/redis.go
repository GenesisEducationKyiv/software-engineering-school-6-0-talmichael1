package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github-release-notifier/internal/domain"
	"github-release-notifier/internal/github"
)

const releaseCacheTTL = 10 * time.Minute

// CachedGitHubClient wraps a GitHub client with Redis caching for release lookups.
type CachedGitHubClient struct {
	client *github.Client
	rdb    *redis.Client
}

func NewCachedGitHubClient(client *github.Client, rdb *redis.Client) *CachedGitHubClient {
	return &CachedGitHubClient{client: client, rdb: rdb}
}

// RepoExists delegates directly to GitHub (no caching for existence checks).
func (c *CachedGitHubClient) RepoExists(ctx context.Context, owner, repo string) error {
	return c.client.RepoExists(ctx, owner, repo)
}

// GetLatestRelease tries the Redis cache first, falling back to the GitHub API on miss.
func (c *CachedGitHubClient) GetLatestRelease(ctx context.Context, owner, repo string) (*domain.Release, error) {
	key := fmt.Sprintf("github:release:%s/%s", owner, repo)

	data, err := c.rdb.Get(ctx, key).Bytes()
	if err == nil {
		var release domain.Release
		if jsonErr := json.Unmarshal(data, &release); jsonErr == nil {
			return &release, nil
		}
	}

	release, err := c.client.GetLatestRelease(ctx, owner, repo)
	if err != nil {
		return nil, err
	}

	if encoded, err := json.Marshal(release); err == nil {
		_ = c.rdb.Set(ctx, key, encoded, releaseCacheTTL).Err() //nolint:errcheck // cache write is best-effort
	}
	return release, nil
}
