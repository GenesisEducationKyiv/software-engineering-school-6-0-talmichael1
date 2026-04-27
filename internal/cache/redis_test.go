package cache

import (
	"context"
	"errors"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github-release-notifier/internal/domain"
)

type fakeUpstream struct {
	repoExistsCalls  int
	getLatestCalls   int
	repoExistsErr    error
	getLatestRelease *domain.Release
	getLatestErr     error
}

func (f *fakeUpstream) RepoExists(_ context.Context, _, _ string) error {
	f.repoExistsCalls++
	return f.repoExistsErr
}

func (f *fakeUpstream) GetLatestRelease(_ context.Context, _, _ string) (*domain.Release, error) {
	f.getLatestCalls++
	return f.getLatestRelease, f.getLatestErr
}

func newTestCache(t *testing.T) (*CachedGitHubClient, *fakeUpstream, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	fake := &fakeUpstream{}
	return &CachedGitHubClient{client: fake, rdb: rdb}, fake, mr
}

func TestGetLatestRelease_CachesSuccess(t *testing.T) {
	c, fake, _ := newTestCache(t)
	fake.getLatestRelease = &domain.Release{TagName: "v1.2.3", Name: "Release 1.2.3", HTMLURL: "https://example.com/v1.2.3"}

	rel, err := c.GetLatestRelease(context.Background(), "golang", "go")
	if err != nil {
		t.Fatalf("first call: unexpected error: %v", err)
	}
	if rel.TagName != "v1.2.3" {
		t.Fatalf("first call: tag = %q, want v1.2.3", rel.TagName)
	}

	rel2, err := c.GetLatestRelease(context.Background(), "golang", "go")
	if err != nil {
		t.Fatalf("second call: unexpected error: %v", err)
	}
	if rel2.TagName != "v1.2.3" {
		t.Fatalf("second call: tag = %q, want v1.2.3", rel2.TagName)
	}
	if fake.getLatestCalls != 1 {
		t.Fatalf("upstream called %d times, want 1 (second call should hit cache)", fake.getLatestCalls)
	}
}

func TestGetLatestRelease_Caches404(t *testing.T) {
	c, fake, _ := newTestCache(t)
	fake.getLatestErr = domain.ErrNotFound

	_, err := c.GetLatestRelease(context.Background(), "nope", "nada")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("first call: err = %v, want ErrNotFound", err)
	}

	_, err = c.GetLatestRelease(context.Background(), "nope", "nada")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second call: err = %v, want ErrNotFound", err)
	}
	if fake.getLatestCalls != 1 {
		t.Fatalf("upstream called %d times, want 1 (404 should be cached)", fake.getLatestCalls)
	}
}

func TestGetLatestRelease_DoesNotCache429(t *testing.T) {
	c, fake, _ := newTestCache(t)
	fake.getLatestErr = domain.ErrRateLimited

	_, err := c.GetLatestRelease(context.Background(), "rate", "limited")
	if !errors.Is(err, domain.ErrRateLimited) {
		t.Fatalf("first call: err = %v, want ErrRateLimited", err)
	}

	_, err = c.GetLatestRelease(context.Background(), "rate", "limited")
	if !errors.Is(err, domain.ErrRateLimited) {
		t.Fatalf("second call: err = %v, want ErrRateLimited", err)
	}
	if fake.getLatestCalls != 2 {
		t.Fatalf("upstream called %d times, want 2 (429 must not be cached)", fake.getLatestCalls)
	}
}

func TestGetLatestRelease_DoesNotCacheOtherErrors(t *testing.T) {
	c, fake, _ := newTestCache(t)
	fake.getLatestErr = domain.ErrExternalAPI

	_, _ = c.GetLatestRelease(context.Background(), "flaky", "repo")
	_, _ = c.GetLatestRelease(context.Background(), "flaky", "repo")

	if fake.getLatestCalls != 2 {
		t.Fatalf("upstream called %d times, want 2 (transient errors must not be cached)", fake.getLatestCalls)
	}
}

func TestRepoExists_CachesSuccess(t *testing.T) {
	c, fake, _ := newTestCache(t)

	if err := c.RepoExists(context.Background(), "golang", "go"); err != nil {
		t.Fatalf("first call: unexpected error: %v", err)
	}
	if err := c.RepoExists(context.Background(), "golang", "go"); err != nil {
		t.Fatalf("second call: unexpected error: %v", err)
	}
	if fake.repoExistsCalls != 1 {
		t.Fatalf("upstream called %d times, want 1 (exists should be cached)", fake.repoExistsCalls)
	}
}

func TestRepoExists_Caches404(t *testing.T) {
	c, fake, _ := newTestCache(t)
	fake.repoExistsErr = domain.ErrNotFound

	if err := c.RepoExists(context.Background(), "nope", "nada"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("first call: err = %v, want ErrNotFound", err)
	}
	if err := c.RepoExists(context.Background(), "nope", "nada"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second call: err = %v, want ErrNotFound", err)
	}
	if fake.repoExistsCalls != 1 {
		t.Fatalf("upstream called %d times, want 1 (404 should be cached)", fake.repoExistsCalls)
	}
}

func TestRepoExists_DoesNotCache429(t *testing.T) {
	c, fake, _ := newTestCache(t)
	fake.repoExistsErr = domain.ErrRateLimited

	_ = c.RepoExists(context.Background(), "rate", "limited")
	_ = c.RepoExists(context.Background(), "rate", "limited")

	if fake.repoExistsCalls != 2 {
		t.Fatalf("upstream called %d times, want 2 (429 must not be cached)", fake.repoExistsCalls)
	}
}

func TestReleaseAndRepoCachesUseSeparateKeys(t *testing.T) {
	c, fake, mr := newTestCache(t)
	fake.getLatestErr = domain.ErrNotFound

	// Prime the release cache with a 404.
	_, _ = c.GetLatestRelease(context.Background(), "some", "repo")

	// RepoExists should NOT be served from the release cache key.
	fake.repoExistsErr = nil
	if err := c.RepoExists(context.Background(), "some", "repo"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.repoExistsCalls != 1 {
		t.Fatalf("RepoExists upstream calls = %d, want 1 (should not be served from release cache)", fake.repoExistsCalls)
	}

	// Both keys should now exist in Redis.
	if !mr.Exists("github:release:some/repo") {
		t.Error("release cache key missing")
	}
	if !mr.Exists("github:repo:some/repo") {
		t.Error("repo cache key missing")
	}
}
