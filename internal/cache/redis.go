package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github-release-notifier/internal/domain"
	"github-release-notifier/internal/github"
)

const (
	cacheTTL         = 10 * time.Minute
	notFoundSentinel = "__notfound__"
	existsSentinel   = "__exists__"
)

// upstream is the subset of the GitHub client that the cache wraps. Kept as
// an interface so tests can substitute a fake without touching the real HTTP
// client.
type upstream interface {
	RepoExists(ctx context.Context, owner, repo string) error
	GetLatestRelease(ctx context.Context, owner, repo string) (*domain.Release, error)
}

// CachedGitHubClient wraps a GitHub client with Redis caching for successful
// and 404 responses. Rate-limit (429) and transient errors are never cached so
// the next caller can retry freely.
type CachedGitHubClient struct {
	client upstream
	rdb    *redis.Client
}

func NewCachedGitHubClient(client *github.Client, rdb *redis.Client) *CachedGitHubClient {
	return &CachedGitHubClient{client: client, rdb: rdb}
}

// RepoExists caches both 200 (exists) and 404 (not found) outcomes.
func (c *CachedGitHubClient) RepoExists(ctx context.Context, owner, repo string) error {
	key := fmt.Sprintf("github:repo:%s/%s", owner, repo)

	if data, err := c.rdb.Get(ctx, key).Bytes(); err == nil {
		switch string(data) {
		case existsSentinel:
			return nil
		case notFoundSentinel:
			return domain.ErrNotFound
		}
	}

	err := c.client.RepoExists(ctx, owner, repo)
	switch {
	case err == nil:
		_ = c.rdb.Set(ctx, key, existsSentinel, cacheTTL).Err() //nolint:errcheck // best-effort cache write
	case errors.Is(err, domain.ErrNotFound):
		_ = c.rdb.Set(ctx, key, notFoundSentinel, cacheTTL).Err() //nolint:errcheck // best-effort cache write
	}
	return err
}

// GetLatestRelease caches successful responses as JSON and 404s as a sentinel.
// Rate-limit and transient errors bypass the cache entirely.
func (c *CachedGitHubClient) GetLatestRelease(ctx context.Context, owner, repo string) (*domain.Release, error) {
	key := fmt.Sprintf("github:release:%s/%s", owner, repo)

	if data, err := c.rdb.Get(ctx, key).Bytes(); err == nil {
		if string(data) == notFoundSentinel {
			return nil, domain.ErrNotFound
		}
		var release domain.Release
		if jsonErr := json.Unmarshal(data, &release); jsonErr == nil {
			return &release, nil
		}
	}

	release, err := c.client.GetLatestRelease(ctx, owner, repo)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			_ = c.rdb.Set(ctx, key, notFoundSentinel, cacheTTL).Err() //nolint:errcheck // best-effort cache write
		}
		return nil, err
	}

	if encoded, err := json.Marshal(release); err == nil {
		_ = c.rdb.Set(ctx, key, encoded, cacheTTL).Err() //nolint:errcheck // best-effort cache write
	}
	return release, nil
}
