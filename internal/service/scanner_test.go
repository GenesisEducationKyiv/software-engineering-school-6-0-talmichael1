package service

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github-release-notifier/internal/domain"
)

type mockQueue struct {
	mu           sync.Mutex
	enqueuedJobs []domain.NotificationJob
	enqueueFn    func(ctx context.Context, jobs []domain.NotificationJob) error
}

func (m *mockQueue) EnqueueBatch(ctx context.Context, jobs []domain.NotificationJob) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.enqueueFn != nil {
		return m.enqueueFn(ctx, jobs)
	}
	m.enqueuedJobs = append(m.enqueuedJobs, jobs...)
	return nil
}

func TestScanner_NewRelease(t *testing.T) {
	q := &mockQueue{}
	var tagUpdated atomic.Value

	listRepos := func(ctx context.Context) ([]domain.Repository, error) {
		return []domain.Repository{
			{ID: 1, Owner: "golang", Name: "go", LastSeenTag: "go1.21.0"},
		}, nil
	}
	updateTag := func(ctx context.Context, id int64, tag string) error {
		tagUpdated.Store(tag)
		return nil
	}
	updateChecked := func(ctx context.Context, id int64) error { return nil }

	scannerRepoRepo := &scannerMockRepoRepo{
		listFn:          listRepos,
		updateTagFn:     updateTag,
		updateCheckedFn: updateChecked,
	}

	subRepo := &mockSubRepo{
		listConfirmedFn: func(ctx context.Context, repoID int64) ([]domain.Subscription, error) {
			return []domain.Subscription{
				{ID: 10, Email: "a@b.com", UnsubscribeToken: "tok1"},
				{ID: 11, Email: "c@d.com", UnsubscribeToken: "tok2"},
			}, nil
		},
	}

	gh := &mockGitHub{
		getLatestRelFn: func(ctx context.Context, owner, repo string) (*domain.Release, error) {
			return &domain.Release{
				TagName: "go1.22.0",
				Name:    "Go 1.22",
				HTMLURL: "https://github.com/golang/go/releases/tag/go1.22.0",
			}, nil
		},
	}

	scanner := NewScanner(scannerRepoRepo, subRepo, gh, q, "http://localhost:8080", time.Minute, 1)
	scanner.scan(context.Background())

	if len(q.enqueuedJobs) != 2 {
		t.Fatalf("expected 2 enqueued jobs, got %d", len(q.enqueuedJobs))
	}
	if q.enqueuedJobs[0].Tag != "go1.22.0" {
		t.Fatalf("expected tag go1.22.0, got %s", q.enqueuedJobs[0].Tag)
	}

	updated, ok := tagUpdated.Load().(string)
	if !ok || updated != "go1.22.0" {
		t.Fatalf("expected last_seen_tag updated to go1.22.0, got %v", updated)
	}
}

func TestScanner_NoNewRelease(t *testing.T) {
	q := &mockQueue{}

	scannerRepoRepo := &scannerMockRepoRepo{
		listFn: func(ctx context.Context) ([]domain.Repository, error) {
			return []domain.Repository{
				{ID: 1, Owner: "golang", Name: "go", LastSeenTag: "go1.22.0"},
			}, nil
		},
		updateCheckedFn: func(ctx context.Context, id int64) error { return nil },
	}

	gh := &mockGitHub{
		getLatestRelFn: func(ctx context.Context, owner, repo string) (*domain.Release, error) {
			return &domain.Release{TagName: "go1.22.0"}, nil
		},
	}

	scanner := NewScanner(scannerRepoRepo, &mockSubRepo{}, gh, q, "http://localhost:8080", time.Minute, 1)
	scanner.scan(context.Background())

	if len(q.enqueuedJobs) != 0 {
		t.Fatalf("expected 0 enqueued jobs for unchanged release, got %d", len(q.enqueuedJobs))
	}
}

func TestScanner_NoReleases(t *testing.T) {
	q := &mockQueue{}
	var checkedAt bool

	scannerRepoRepo := &scannerMockRepoRepo{
		listFn: func(ctx context.Context) ([]domain.Repository, error) {
			return []domain.Repository{
				{ID: 1, Owner: "new", Name: "repo", LastSeenTag: ""},
			}, nil
		},
		updateCheckedFn: func(ctx context.Context, id int64) error {
			checkedAt = true
			return nil
		},
	}

	gh := &mockGitHub{
		getLatestRelFn: func(ctx context.Context, owner, repo string) (*domain.Release, error) {
			return nil, domain.ErrNotFound
		},
	}

	scanner := NewScanner(scannerRepoRepo, &mockSubRepo{}, gh, q, "http://localhost:8080", time.Minute, 1)
	scanner.scan(context.Background())

	if len(q.enqueuedJobs) != 0 {
		t.Fatalf("expected 0 jobs, got %d", len(q.enqueuedJobs))
	}
	if !checkedAt {
		t.Fatal("expected checked_at to be updated")
	}
}

func TestScanner_GitHubError(t *testing.T) {
	q := &mockQueue{}

	scannerRepoRepo := &scannerMockRepoRepo{
		listFn: func(ctx context.Context) ([]domain.Repository, error) {
			return []domain.Repository{
				{ID: 1, Owner: "golang", Name: "go", LastSeenTag: "go1.21.0"},
			}, nil
		},
	}

	gh := &mockGitHub{
		getLatestRelFn: func(ctx context.Context, owner, repo string) (*domain.Release, error) {
			return nil, domain.ErrExternalAPI
		},
	}

	scanner := NewScanner(scannerRepoRepo, &mockSubRepo{}, gh, q, "http://localhost:8080", time.Minute, 1)
	scanner.scan(context.Background())

	if len(q.enqueuedJobs) != 0 {
		t.Fatalf("expected 0 jobs on error, got %d", len(q.enqueuedJobs))
	}
}

func TestScanner_ListReposError(t *testing.T) {
	q := &mockQueue{}

	scannerRepoRepo := &scannerMockRepoRepo{
		listFn: func(ctx context.Context) ([]domain.Repository, error) {
			return nil, fmt.Errorf("database unavailable")
		},
	}

	scanner := NewScanner(scannerRepoRepo, &mockSubRepo{}, &mockGitHub{}, q, "http://localhost:8080", time.Minute, 1)
	scanner.scan(context.Background())

	if len(q.enqueuedJobs) != 0 {
		t.Fatalf("expected 0 jobs on list error, got %d", len(q.enqueuedJobs))
	}
}

func TestScanner_ContextCancelled(t *testing.T) {
	q := &mockQueue{}

	scannerRepoRepo := &scannerMockRepoRepo{
		listFn: func(ctx context.Context) ([]domain.Repository, error) {
			return []domain.Repository{
				{ID: 1, Owner: "golang", Name: "go"},
				{ID: 2, Owner: "gin-gonic", Name: "gin"},
			}, nil
		},
	}

	callCount := 0
	gh := &mockGitHub{
		getLatestRelFn: func(ctx context.Context, owner, repo string) (*domain.Release, error) {
			callCount++
			return &domain.Release{TagName: "v1.0.0"}, nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	scanner := NewScanner(scannerRepoRepo, &mockSubRepo{}, gh, q, "http://localhost:8080", time.Minute, 1)
	scanner.scan(ctx)

	if callCount > 0 {
		t.Fatalf("expected no GitHub calls after cancel, got %d", callCount)
	}
}

func TestScanner_ListSubscribersError(t *testing.T) {
	q := &mockQueue{}

	scannerRepoRepo := &scannerMockRepoRepo{
		listFn: func(ctx context.Context) ([]domain.Repository, error) {
			return []domain.Repository{
				{ID: 1, Owner: "golang", Name: "go", LastSeenTag: "go1.21.0"},
			}, nil
		},
	}

	subRepo := &mockSubRepo{
		listConfirmedFn: func(ctx context.Context, repoID int64) ([]domain.Subscription, error) {
			return nil, fmt.Errorf("database unavailable")
		},
	}

	gh := &mockGitHub{
		getLatestRelFn: func(ctx context.Context, owner, repo string) (*domain.Release, error) {
			return &domain.Release{TagName: "go1.22.0", Name: "Go 1.22"}, nil
		},
	}

	scanner := NewScanner(scannerRepoRepo, subRepo, gh, q, "http://localhost:8080", time.Minute, 1)
	scanner.scan(context.Background())

	if len(q.enqueuedJobs) != 0 {
		t.Fatalf("expected 0 enqueued jobs, got %d", len(q.enqueuedJobs))
	}
}

func TestScanner_EnqueueError(t *testing.T) {
	q := &mockQueue{}
	q.enqueueFn = func(ctx context.Context, jobs []domain.NotificationJob) error {
		return fmt.Errorf("redis unavailable")
	}

	scannerRepoRepo := &scannerMockRepoRepo{
		listFn: func(ctx context.Context) ([]domain.Repository, error) {
			return []domain.Repository{
				{ID: 1, Owner: "golang", Name: "go", LastSeenTag: "go1.21.0"},
			}, nil
		},
	}

	subRepo := &mockSubRepo{
		listConfirmedFn: func(ctx context.Context, repoID int64) ([]domain.Subscription, error) {
			return []domain.Subscription{
				{ID: 10, Email: "a@b.com", UnsubscribeToken: "tok1"},
			}, nil
		},
	}

	gh := &mockGitHub{
		getLatestRelFn: func(ctx context.Context, owner, repo string) (*domain.Release, error) {
			return &domain.Release{TagName: "go1.22.0", Name: "Go 1.22"}, nil
		},
	}

	scanner := NewScanner(scannerRepoRepo, subRepo, gh, q, "http://localhost:8080", time.Minute, 1)
	scanner.scan(context.Background())

	// Enqueue failed, so no jobs should be recorded.
	if len(q.enqueuedJobs) != 0 {
		t.Fatalf("expected 0 enqueued jobs, got %d", len(q.enqueuedJobs))
	}
}

func TestScanner_UpdateTagError(t *testing.T) {
	q := &mockQueue{}
	var tagUpdated bool

	scannerRepoRepo := &scannerMockRepoRepo{
		listFn: func(ctx context.Context) ([]domain.Repository, error) {
			return []domain.Repository{
				{ID: 1, Owner: "golang", Name: "go", LastSeenTag: "go1.21.0"},
			}, nil
		},
		updateTagFn: func(ctx context.Context, id int64, tag string) error {
			tagUpdated = true
			return fmt.Errorf("database unavailable")
		},
	}

	subRepo := &mockSubRepo{
		listConfirmedFn: func(ctx context.Context, repoID int64) ([]domain.Subscription, error) {
			return []domain.Subscription{
				{ID: 10, Email: "a@b.com", UnsubscribeToken: "tok1"},
			}, nil
		},
	}

	gh := &mockGitHub{
		getLatestRelFn: func(ctx context.Context, owner, repo string) (*domain.Release, error) {
			return &domain.Release{TagName: "go1.22.0", Name: "Go 1.22"}, nil
		},
	}

	scanner := NewScanner(scannerRepoRepo, subRepo, gh, q, "http://localhost:8080", time.Minute, 1)
	scanner.scan(context.Background())

	// Jobs were enqueued but tag update failed — at-least-once delivery.
	if len(q.enqueuedJobs) != 1 {
		t.Fatalf("expected 1 enqueued job, got %d", len(q.enqueuedJobs))
	}
	if !tagUpdated {
		t.Fatal("expected UpdateLastSeenTag to be called")
	}
}

// scannerMockRepoRepo is a mock that implements repository.RepositoryRepo for scanner tests.
type scannerMockRepoRepo struct {
	listFn          func(ctx context.Context) ([]domain.Repository, error)
	updateTagFn     func(ctx context.Context, id int64, tag string) error
	updateCheckedFn func(ctx context.Context, id int64) error
}

func (m *scannerMockRepoRepo) GetOrCreate(ctx context.Context, owner, name string) (*domain.Repository, error) {
	return &domain.Repository{ID: 1, Owner: owner, Name: name}, nil
}
func (m *scannerMockRepoRepo) GetByID(ctx context.Context, id int64) (*domain.Repository, error) {
	return &domain.Repository{ID: id}, nil
}
func (m *scannerMockRepoRepo) ListWithActiveSubscriptions(ctx context.Context) ([]domain.Repository, error) {
	if m.listFn != nil {
		return m.listFn(ctx)
	}
	return nil, nil
}
func (m *scannerMockRepoRepo) UpdateLastSeenTag(ctx context.Context, id int64, tag string) error {
	if m.updateTagFn != nil {
		return m.updateTagFn(ctx, id, tag)
	}
	return nil
}
func (m *scannerMockRepoRepo) UpdateCheckedAt(ctx context.Context, id int64) error {
	if m.updateCheckedFn != nil {
		return m.updateCheckedFn(ctx, id)
	}
	return nil
}
