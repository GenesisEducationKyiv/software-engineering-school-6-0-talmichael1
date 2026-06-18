package queue

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github-release-notifier/internal/domain"
)

func newTestRepoCheckQueue(t *testing.T) (*RepoCheckQueue, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return NewRepoCheckQueue(rdb), mr
}

func TestEnqueueRepo_PushesToList(t *testing.T) {
	q, mr := newTestRepoCheckQueue(t)

	repo := domain.Repository{ID: 1, Owner: "golang", Name: "go", LastSeenTag: "v1.0.0"}
	if err := q.EnqueueRepo(context.Background(), repo); err != nil {
		t.Fatalf("EnqueueRepo: %v", err)
	}

	items, err := mr.List(repoCheckQueue)
	if err != nil {
		t.Fatalf("reading list %q: %v", repoCheckQueue, err)
	}
	if len(items) != 1 {
		t.Fatalf("list length = %d, want 1", len(items))
	}
}

func TestDequeueRepo_RoundTrips(t *testing.T) {
	q, _ := newTestRepoCheckQueue(t)
	ctx := context.Background()

	repo := domain.Repository{ID: 42, Owner: "gin-gonic", Name: "gin", LastSeenTag: "v1.9.0"}
	if err := q.EnqueueRepo(ctx, repo); err != nil {
		t.Fatalf("EnqueueRepo: %v", err)
	}

	got, err := q.DequeueRepo(ctx, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("DequeueRepo: %v", err)
	}
	if got == nil {
		t.Fatal("DequeueRepo returned nil, expected a repo")
	}
	if got.ID != repo.ID || got.FullName() != "gin-gonic/gin" || got.LastSeenTag != "v1.9.0" {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", *got, repo)
	}
}

func TestDequeueRepo_EmptyReturnsNilNil(t *testing.T) {
	q, _ := newTestRepoCheckQueue(t)

	got, err := q.DequeueRepo(context.Background(), 50*time.Millisecond)
	if err != nil {
		t.Fatalf("expected (nil, nil) on empty queue, got err: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil repo, got %+v", got)
	}
}
