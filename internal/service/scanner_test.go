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

func (m *mockQueue) jobCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.enqueuedJobs)
}

type mockRepoCheckQueue struct {
	mu        sync.Mutex
	enqueued  []domain.Repository
	enqueueFn func(ctx context.Context, repo domain.Repository) error
	dequeueFn func(ctx context.Context, timeout time.Duration) (*domain.Repository, error)
}

func (m *mockRepoCheckQueue) EnqueueRepo(ctx context.Context, repo domain.Repository) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.enqueueFn != nil {
		return m.enqueueFn(ctx, repo)
	}
	m.enqueued = append(m.enqueued, repo)
	return nil
}

func (m *mockRepoCheckQueue) DequeueRepo(ctx context.Context, timeout time.Duration) (*domain.Repository, error) {
	if m.dequeueFn != nil {
		return m.dequeueFn(ctx, timeout)
	}
	return nil, nil
}

type mockLock struct {
	acquired bool
	err      error
	calls    int32
	ttl      time.Duration
}

func (m *mockLock) Acquire(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	atomic.AddInt32(&m.calls, 1)
	m.ttl = ttl
	return m.acquired, m.err
}

func newScannerForCheck(repoRepo *scannerMockRepoRepo, subRepo *mockSubRepo, gh *mockGitHub, q *mockQueue) *Scanner {
	return NewScanner(repoRepo, subRepo, gh, q, &mockRepoCheckQueue{}, &mockLock{}, time.Minute, 1)
}

func TestScanner_CheckRepo_NewRelease(t *testing.T) {
	q := &mockQueue{}
	var tagUpdated atomic.Value

	repoRepo := &scannerMockRepoRepo{
		updateTagFn: func(ctx context.Context, id int64, tag string) error {
			tagUpdated.Store(tag)
			return nil
		},
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
			return &domain.Release{TagName: "go1.22.0", HTMLURL: "https://example.com"}, nil
		},
	}

	scanner := newScannerForCheck(repoRepo, subRepo, gh, q)
	repo := domain.Repository{ID: 1, Owner: "golang", Name: "go", LastSeenTag: "go1.21.0"}
	if err := scanner.checkRepo(context.Background(), repo); err != nil {
		t.Fatalf("checkRepo: %v", err)
	}

	if q.jobCount() != 2 {
		t.Fatalf("expected 2 enqueued jobs, got %d", q.jobCount())
	}
	if q.enqueuedJobs[0].Tag != "go1.22.0" {
		t.Fatalf("expected tag go1.22.0, got %s", q.enqueuedJobs[0].Tag)
	}
	if updated, _ := tagUpdated.Load().(string); updated != "go1.22.0" {
		t.Fatalf("expected last_seen_tag updated to go1.22.0, got %q", updated)
	}
}

func TestScanner_CheckRepo_NoNewRelease(t *testing.T) {
	q := &mockQueue{}
	repoRepo := &scannerMockRepoRepo{
		updateCheckedFn: func(ctx context.Context, id int64) error { return nil },
	}
	gh := &mockGitHub{
		getLatestRelFn: func(ctx context.Context, owner, repo string) (*domain.Release, error) {
			return &domain.Release{TagName: "go1.22.0"}, nil
		},
	}

	scanner := newScannerForCheck(repoRepo, &mockSubRepo{}, gh, q)
	repo := domain.Repository{ID: 1, Owner: "golang", Name: "go", LastSeenTag: "go1.22.0"}
	if err := scanner.checkRepo(context.Background(), repo); err != nil {
		t.Fatalf("checkRepo: %v", err)
	}

	if q.jobCount() != 0 {
		t.Fatalf("expected 0 enqueued jobs for unchanged release, got %d", q.jobCount())
	}
}

func TestScanner_CheckRepo_NoReleases(t *testing.T) {
	q := &mockQueue{}
	var checkedAt bool
	repoRepo := &scannerMockRepoRepo{
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

	scanner := newScannerForCheck(repoRepo, &mockSubRepo{}, gh, q)
	repo := domain.Repository{ID: 1, Owner: "new", Name: "repo", LastSeenTag: ""}
	if err := scanner.checkRepo(context.Background(), repo); err != nil {
		t.Fatalf("checkRepo: %v", err)
	}

	if q.jobCount() != 0 {
		t.Fatalf("expected 0 jobs, got %d", q.jobCount())
	}
	if !checkedAt {
		t.Fatal("expected checked_at to be updated")
	}
}

func TestScanner_CheckRepo_GitHubError(t *testing.T) {
	q := &mockQueue{}
	gh := &mockGitHub{
		getLatestRelFn: func(ctx context.Context, owner, repo string) (*domain.Release, error) {
			return nil, domain.ErrExternalAPI
		},
	}

	scanner := newScannerForCheck(&scannerMockRepoRepo{}, &mockSubRepo{}, gh, q)
	repo := domain.Repository{ID: 1, Owner: "golang", Name: "go", LastSeenTag: "go1.21.0"}
	if err := scanner.checkRepo(context.Background(), repo); err == nil {
		t.Fatal("expected error from GitHub failure")
	}
	if q.jobCount() != 0 {
		t.Fatalf("expected 0 jobs on error, got %d", q.jobCount())
	}
}

func TestScanner_CheckRepo_ListSubscribersError(t *testing.T) {
	q := &mockQueue{}
	subRepo := &mockSubRepo{
		listConfirmedFn: func(ctx context.Context, repoID int64) ([]domain.Subscription, error) {
			return nil, fmt.Errorf("database unavailable")
		},
	}
	gh := &mockGitHub{
		getLatestRelFn: func(ctx context.Context, owner, repo string) (*domain.Release, error) {
			return &domain.Release{TagName: "go1.22.0"}, nil
		},
	}

	scanner := newScannerForCheck(&scannerMockRepoRepo{}, subRepo, gh, q)
	repo := domain.Repository{ID: 1, Owner: "golang", Name: "go", LastSeenTag: "go1.21.0"}
	if err := scanner.checkRepo(context.Background(), repo); err == nil {
		t.Fatal("expected error when listing subscribers fails")
	}
	if q.jobCount() != 0 {
		t.Fatalf("expected 0 enqueued jobs, got %d", q.jobCount())
	}
}

func TestScanner_CheckRepo_EnqueueError(t *testing.T) {
	q := &mockQueue{enqueueFn: func(ctx context.Context, jobs []domain.NotificationJob) error {
		return fmt.Errorf("redis unavailable")
	}}
	var tagUpdated bool
	repoRepo := &scannerMockRepoRepo{
		updateTagFn: func(ctx context.Context, id int64, tag string) error {
			tagUpdated = true
			return nil
		},
	}
	subRepo := &mockSubRepo{
		listConfirmedFn: func(ctx context.Context, repoID int64) ([]domain.Subscription, error) {
			return []domain.Subscription{{ID: 10, Email: "a@b.com", UnsubscribeToken: "tok1"}}, nil
		},
	}
	gh := &mockGitHub{
		getLatestRelFn: func(ctx context.Context, owner, repo string) (*domain.Release, error) {
			return &domain.Release{TagName: "go1.22.0"}, nil
		},
	}

	scanner := newScannerForCheck(repoRepo, subRepo, gh, q)
	repo := domain.Repository{ID: 1, Owner: "golang", Name: "go", LastSeenTag: "go1.21.0"}
	if err := scanner.checkRepo(context.Background(), repo); err == nil {
		t.Fatal("expected error when enqueue fails")
	}
	if tagUpdated {
		t.Fatal("tag must not be updated when enqueue fails (would lose the release)")
	}
}

func TestScanner_CheckRepo_UpdateTagError(t *testing.T) {
	q := &mockQueue{}
	var tagUpdated bool
	repoRepo := &scannerMockRepoRepo{
		updateTagFn: func(ctx context.Context, id int64, tag string) error {
			tagUpdated = true
			return fmt.Errorf("database unavailable")
		},
	}
	subRepo := &mockSubRepo{
		listConfirmedFn: func(ctx context.Context, repoID int64) ([]domain.Subscription, error) {
			return []domain.Subscription{{ID: 10, Email: "a@b.com", UnsubscribeToken: "tok1"}}, nil
		},
	}
	gh := &mockGitHub{
		getLatestRelFn: func(ctx context.Context, owner, repo string) (*domain.Release, error) {
			return &domain.Release{TagName: "go1.22.0"}, nil
		},
	}

	scanner := newScannerForCheck(repoRepo, subRepo, gh, q)
	repo := domain.Repository{ID: 1, Owner: "golang", Name: "go", LastSeenTag: "go1.21.0"}
	if err := scanner.checkRepo(context.Background(), repo); err == nil {
		t.Fatal("expected error when tag update fails")
	}
	if q.jobCount() != 1 {
		t.Fatalf("expected 1 enqueued job before tag update, got %d", q.jobCount())
	}
	if !tagUpdated {
		t.Fatal("expected UpdateLastSeenTag to be attempted")
	}
}

func TestScanner_EnqueueDueRepos_LeaderEnqueuesAll(t *testing.T) {
	repoChecks := &mockRepoCheckQueue{}
	repoRepo := &scannerMockRepoRepo{
		listFn: func(ctx context.Context) ([]domain.Repository, error) {
			return []domain.Repository{
				{ID: 1, Owner: "golang", Name: "go"},
				{ID: 2, Owner: "gin-gonic", Name: "gin"},
			}, nil
		},
	}
	scanner := NewScanner(repoRepo, &mockSubRepo{}, &mockGitHub{}, &mockQueue{}, repoChecks, &mockLock{acquired: true}, time.Minute, 1)

	scanner.enqueueDueRepos(context.Background())

	if len(repoChecks.enqueued) != 2 {
		t.Fatalf("leader should enqueue all active-subscription repos, got %d", len(repoChecks.enqueued))
	}
}

func TestScanner_EnqueueDueRepos_LockExpiresBeforeNextCycle(t *testing.T) {
	lock := &mockLock{acquired: true}
	repoRepo := &scannerMockRepoRepo{
		listFn: func(ctx context.Context) ([]domain.Repository, error) { return nil, nil },
	}
	interval := time.Minute
	scanner := NewScanner(repoRepo, &mockSubRepo{}, &mockGitHub{}, &mockQueue{}, &mockRepoCheckQueue{}, lock, interval, 1)

	scanner.enqueueDueRepos(context.Background())

	if lock.ttl <= 0 || lock.ttl >= interval {
		t.Fatalf("leader lock TTL = %v, want in (0, %v) so it expires before the next tick", lock.ttl, interval)
	}
}

func TestScanner_EnqueueDueRepos_SkipsWhenNotLeader(t *testing.T) {
	repoChecks := &mockRepoCheckQueue{}
	listed := false
	repoRepo := &scannerMockRepoRepo{
		listFn: func(ctx context.Context) ([]domain.Repository, error) {
			listed = true
			return []domain.Repository{{ID: 1, Owner: "golang", Name: "go"}}, nil
		},
	}
	scanner := NewScanner(repoRepo, &mockSubRepo{}, &mockGitHub{}, &mockQueue{}, repoChecks, &mockLock{acquired: false}, time.Minute, 1)

	scanner.enqueueDueRepos(context.Background())

	if len(repoChecks.enqueued) != 0 {
		t.Fatalf("non-leader must not enqueue, got %d", len(repoChecks.enqueued))
	}
	if listed {
		t.Fatal("non-leader must not even list repos")
	}
}

func TestScanner_EnqueueDueRepos_ListError(t *testing.T) {
	repoChecks := &mockRepoCheckQueue{}
	repoRepo := &scannerMockRepoRepo{
		listFn: func(ctx context.Context) ([]domain.Repository, error) {
			return nil, fmt.Errorf("database unavailable")
		},
	}
	scanner := NewScanner(repoRepo, &mockSubRepo{}, &mockGitHub{}, &mockQueue{}, repoChecks, &mockLock{acquired: true}, time.Minute, 1)

	scanner.enqueueDueRepos(context.Background())

	if len(repoChecks.enqueued) != 0 {
		t.Fatalf("expected 0 enqueued on list error, got %d", len(repoChecks.enqueued))
	}
}

func TestScanner_EnqueueDueRepos_StopsOnContextCancel(t *testing.T) {
	repoChecks := &mockRepoCheckQueue{}
	repoRepo := &scannerMockRepoRepo{
		listFn: func(ctx context.Context) ([]domain.Repository, error) {
			return []domain.Repository{
				{ID: 1, Owner: "golang", Name: "go"},
				{ID: 2, Owner: "gin-gonic", Name: "gin"},
			}, nil
		},
	}
	scanner := NewScanner(repoRepo, &mockSubRepo{}, &mockGitHub{}, &mockQueue{}, repoChecks, &mockLock{acquired: true}, time.Minute, 1)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	scanner.enqueueDueRepos(ctx)

	if len(repoChecks.enqueued) != 0 {
		t.Fatalf("expected no enqueues after cancel, got %d", len(repoChecks.enqueued))
	}
}

func TestScanner_Worker_ChecksDequeuedRepo(t *testing.T) {
	q := &mockQueue{}
	ctx, cancel := context.WithCancel(context.Background())

	handed := false
	repoChecks := &mockRepoCheckQueue{
		dequeueFn: func(_ context.Context, _ time.Duration) (*domain.Repository, error) {
			if handed {
				cancel()
				return nil, nil
			}
			handed = true
			return &domain.Repository{ID: 1, Owner: "golang", Name: "go", LastSeenTag: "go1.21.0"}, nil
		},
	}
	subRepo := &mockSubRepo{
		listConfirmedFn: func(ctx context.Context, repoID int64) ([]domain.Subscription, error) {
			return []domain.Subscription{{ID: 10, Email: "a@b.com", UnsubscribeToken: "tok1"}}, nil
		},
	}
	gh := &mockGitHub{
		getLatestRelFn: func(ctx context.Context, owner, repo string) (*domain.Release, error) {
			return &domain.Release{TagName: "go1.22.0"}, nil
		},
	}

	scanner := NewScanner(&scannerMockRepoRepo{}, subRepo, gh, q, repoChecks, &mockLock{}, time.Minute, 1)
	scanner.worker(ctx)

	if q.jobCount() != 1 {
		t.Fatalf("worker should check the dequeued repo and enqueue notifications, got %d jobs", q.jobCount())
	}
}

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
