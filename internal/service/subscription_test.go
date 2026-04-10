package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github-release-notifier/internal/domain"
)

// --- mocks ---

type mockSubRepo struct {
	createFn            func(ctx context.Context, sub *domain.Subscription) error
	getByConfirmTokenFn func(ctx context.Context, token string) (*domain.Subscription, error)
	getByUnsubTokenFn   func(ctx context.Context, token string) (*domain.Subscription, error)
	confirmFn           func(ctx context.Context, id int64) error
	deleteFn            func(ctx context.Context, id int64) error
	listByEmailFn       func(ctx context.Context, email string) ([]domain.SubscriptionView, error)
	listConfirmedFn     func(ctx context.Context, repoID int64) ([]domain.Subscription, error)
}

func (m *mockSubRepo) Create(ctx context.Context, sub *domain.Subscription) error {
	if m.createFn != nil {
		return m.createFn(ctx, sub)
	}
	return nil
}
func (m *mockSubRepo) GetByConfirmToken(ctx context.Context, token string) (*domain.Subscription, error) {
	if m.getByConfirmTokenFn != nil {
		return m.getByConfirmTokenFn(ctx, token)
	}
	return &domain.Subscription{ID: 1}, nil
}
func (m *mockSubRepo) GetByUnsubscribeToken(ctx context.Context, token string) (*domain.Subscription, error) {
	if m.getByUnsubTokenFn != nil {
		return m.getByUnsubTokenFn(ctx, token)
	}
	return &domain.Subscription{ID: 1}, nil
}
func (m *mockSubRepo) Confirm(ctx context.Context, id int64) error {
	if m.confirmFn != nil {
		return m.confirmFn(ctx, id)
	}
	return nil
}
func (m *mockSubRepo) Delete(ctx context.Context, id int64) error {
	if m.deleteFn != nil {
		return m.deleteFn(ctx, id)
	}
	return nil
}
func (m *mockSubRepo) ListByEmail(ctx context.Context, email string) ([]domain.SubscriptionView, error) {
	if m.listByEmailFn != nil {
		return m.listByEmailFn(ctx, email)
	}
	return nil, nil
}
func (m *mockSubRepo) ListConfirmedByRepoID(ctx context.Context, repoID int64) ([]domain.Subscription, error) {
	if m.listConfirmedFn != nil {
		return m.listConfirmedFn(ctx, repoID)
	}
	return nil, nil
}
func (m *mockSubRepo) DeleteUnconfirmedOlderThan(_ context.Context, _ time.Duration) (int64, error) {
	return 0, nil
}

type mockRepoRepo struct {
	getOrCreateFn func(ctx context.Context, owner, name string) (*domain.Repository, error)
}

func (m *mockRepoRepo) GetOrCreate(ctx context.Context, owner, name string) (*domain.Repository, error) {
	if m.getOrCreateFn != nil {
		return m.getOrCreateFn(ctx, owner, name)
	}
	return &domain.Repository{ID: 1, Owner: owner, Name: name}, nil
}
func (m *mockRepoRepo) GetByID(ctx context.Context, id int64) (*domain.Repository, error) {
	return &domain.Repository{ID: id}, nil
}
func (m *mockRepoRepo) ListWithActiveSubscriptions(ctx context.Context) ([]domain.Repository, error) {
	return nil, nil
}
func (m *mockRepoRepo) UpdateLastSeenTag(ctx context.Context, id int64, tag string) error {
	return nil
}
func (m *mockRepoRepo) UpdateCheckedAt(ctx context.Context, id int64) error { return nil }

type mockGitHub struct {
	repoExistsFn   func(ctx context.Context, owner, repo string) error
	getLatestRelFn func(ctx context.Context, owner, repo string) (*domain.Release, error)
}

func (m *mockGitHub) RepoExists(ctx context.Context, owner, repo string) error {
	if m.repoExistsFn != nil {
		return m.repoExistsFn(ctx, owner, repo)
	}
	return nil
}
func (m *mockGitHub) GetLatestRelease(ctx context.Context, owner, repo string) (*domain.Release, error) {
	if m.getLatestRelFn != nil {
		return m.getLatestRelFn(ctx, owner, repo)
	}
	return &domain.Release{TagName: "v1.0.0"}, nil
}

type mockEmail struct {
	sendConfirmFn func(ctx context.Context, to, repo, confirmURL string) error
}

func (m *mockEmail) SendConfirmation(ctx context.Context, to, repo, confirmURL string) error {
	if m.sendConfirmFn != nil {
		return m.sendConfirmFn(ctx, to, repo, confirmURL)
	}
	return nil
}
func (m *mockEmail) SendReleaseNotification(ctx context.Context, to, repo, tag, releaseURL, unsubURL string) error {
	return nil
}

func newTestService(opts ...func(*SubscriptionService)) *SubscriptionService {
	svc := NewSubscriptionService(
		&mockSubRepo{},
		&mockRepoRepo{},
		&mockGitHub{},
		&mockEmail{},
		"http://localhost:8080",
	)
	for _, opt := range opts {
		opt(svc)
	}
	return svc
}

// --- tests ---

func TestSubscribe_InvalidEmail(t *testing.T) {
	svc := newTestService()
	err := svc.Subscribe(context.Background(), "not-an-email", "golang/go")
	assertErrorIs(t, err, domain.ErrInvalidInput)
}

func TestSubscribe_EmptyEmail(t *testing.T) {
	svc := newTestService()
	err := svc.Subscribe(context.Background(), "", "golang/go")
	assertErrorIs(t, err, domain.ErrInvalidInput)
}

func TestSubscribe_InvalidRepoFormat(t *testing.T) {
	svc := newTestService()
	tests := []string{"", "noslash", "too/many/slashes", "/empty", "empty/"}
	for _, repo := range tests {
		err := svc.Subscribe(context.Background(), "user@example.com", repo)
		assertErrorIs(t, err, domain.ErrInvalidInput)
	}
}

func TestSubscribe_RepoNotFound(t *testing.T) {
	svc := newTestService(func(s *SubscriptionService) {
		s.github = &mockGitHub{
			getLatestRelFn: func(ctx context.Context, owner, repo string) (*domain.Release, error) {
				return nil, domain.ErrNotFound
			},
			repoExistsFn: func(ctx context.Context, owner, repo string) error {
				return domain.ErrNotFound
			},
		}
	})
	err := svc.Subscribe(context.Background(), "user@example.com", "nonexistent/repo")
	assertErrorIs(t, err, domain.ErrNotFound)
}

func TestSubscribe_Conflict(t *testing.T) {
	svc := newTestService(func(s *SubscriptionService) {
		s.subRepo = &mockSubRepo{
			createFn: func(ctx context.Context, sub *domain.Subscription) error {
				return domain.ErrConflict
			},
		}
	})
	err := svc.Subscribe(context.Background(), "user@example.com", "golang/go")
	assertErrorIs(t, err, domain.ErrConflict)
}

func TestSubscribe_Success(t *testing.T) {
	var sentTo string
	svc := newTestService(func(s *SubscriptionService) {
		s.email = &mockEmail{
			sendConfirmFn: func(ctx context.Context, to, repo, confirmURL string) error {
				sentTo = to
				return nil
			},
		}
	})
	err := svc.Subscribe(context.Background(), "user@example.com", "golang/go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sentTo != "user@example.com" {
		t.Fatalf("expected confirmation email sent to user@example.com, got %s", sentTo)
	}
}

func TestConfirm_EmptyToken(t *testing.T) {
	svc := newTestService()
	err := svc.Confirm(context.Background(), "")
	assertErrorIs(t, err, domain.ErrInvalidInput)
}

func TestConfirm_TokenNotFound(t *testing.T) {
	svc := newTestService(func(s *SubscriptionService) {
		s.subRepo = &mockSubRepo{
			getByConfirmTokenFn: func(ctx context.Context, token string) (*domain.Subscription, error) {
				return nil, domain.ErrNotFound
			},
		}
	})
	err := svc.Confirm(context.Background(), "badtoken")
	assertErrorIs(t, err, domain.ErrNotFound)
}

func TestConfirm_Success(t *testing.T) {
	var confirmedID int64
	svc := newTestService(func(s *SubscriptionService) {
		s.subRepo = &mockSubRepo{
			getByConfirmTokenFn: func(ctx context.Context, token string) (*domain.Subscription, error) {
				return &domain.Subscription{ID: 42}, nil
			},
			confirmFn: func(ctx context.Context, id int64) error {
				confirmedID = id
				return nil
			},
		}
	})
	err := svc.Confirm(context.Background(), "validtoken")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if confirmedID != 42 {
		t.Fatalf("expected subscription 42 to be confirmed, got %d", confirmedID)
	}
}

func TestUnsubscribe_EmptyToken(t *testing.T) {
	svc := newTestService()
	err := svc.Unsubscribe(context.Background(), "")
	assertErrorIs(t, err, domain.ErrInvalidInput)
}

func TestUnsubscribe_TokenNotFound(t *testing.T) {
	svc := newTestService(func(s *SubscriptionService) {
		s.subRepo = &mockSubRepo{
			getByUnsubTokenFn: func(ctx context.Context, token string) (*domain.Subscription, error) {
				return nil, domain.ErrNotFound
			},
		}
	})
	err := svc.Unsubscribe(context.Background(), "badtoken")
	assertErrorIs(t, err, domain.ErrNotFound)
}

func TestUnsubscribe_Success(t *testing.T) {
	var deletedID int64
	svc := newTestService(func(s *SubscriptionService) {
		s.subRepo = &mockSubRepo{
			getByUnsubTokenFn: func(ctx context.Context, token string) (*domain.Subscription, error) {
				return &domain.Subscription{ID: 7}, nil
			},
			deleteFn: func(ctx context.Context, id int64) error {
				deletedID = id
				return nil
			},
		}
	})
	err := svc.Unsubscribe(context.Background(), "validtoken")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deletedID != 7 {
		t.Fatalf("expected subscription 7 deleted, got %d", deletedID)
	}
}

func TestListByEmail_InvalidEmail(t *testing.T) {
	svc := newTestService()
	_, err := svc.ListByEmail(context.Background(), "bademail")
	assertErrorIs(t, err, domain.ErrInvalidInput)
}

func TestListByEmail_Success(t *testing.T) {
	expected := []domain.SubscriptionView{
		{Email: "user@example.com", Repo: "golang/go", Confirmed: true, LastSeenTag: "go1.22.0"},
	}
	svc := newTestService(func(s *SubscriptionService) {
		s.subRepo = &mockSubRepo{
			listByEmailFn: func(ctx context.Context, email string) ([]domain.SubscriptionView, error) {
				return expected, nil
			},
		}
	})
	result, err := svc.ListByEmail(context.Background(), "user@example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 || result[0].Repo != "golang/go" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestSubscribe_EmailSendFailure_RollsBack(t *testing.T) {
	var deleted bool
	svc := newTestService(func(s *SubscriptionService) {
		s.subRepo = &mockSubRepo{
			deleteFn: func(ctx context.Context, id int64) error {
				deleted = true
				return nil
			},
		}
		s.email = &mockEmail{
			sendConfirmFn: func(ctx context.Context, to, repo, confirmURL string) error {
				return fmt.Errorf("SMTP connection refused")
			},
		}
	})
	err := svc.Subscribe(context.Background(), "user@example.com", "golang/go")
	if err == nil {
		t.Fatal("expected error when email fails")
	}
	if !deleted {
		t.Fatal("expected subscription to be rolled back on email failure")
	}
}

func TestSubscribe_RateLimited(t *testing.T) {
	svc := newTestService(func(s *SubscriptionService) {
		s.github = &mockGitHub{
			getLatestRelFn: func(ctx context.Context, owner, repo string) (*domain.Release, error) {
				return nil, domain.ErrRateLimited
			},
		}
	})
	err := svc.Subscribe(context.Background(), "user@example.com", "golang/go")
	assertErrorIs(t, err, domain.ErrRateLimited)
}

func TestSubscribe_RepoExistsButNoReleases(t *testing.T) {
	var sentTo string
	svc := newTestService(func(s *SubscriptionService) {
		s.github = &mockGitHub{
			getLatestRelFn: func(ctx context.Context, owner, repo string) (*domain.Release, error) {
				return nil, domain.ErrNotFound
			},
			repoExistsFn: func(ctx context.Context, owner, repo string) error {
				return nil // repo exists but has no releases
			},
		}
		s.email = &mockEmail{
			sendConfirmFn: func(ctx context.Context, to, repo, confirmURL string) error {
				sentTo = to
				return nil
			},
		}
	})
	err := svc.Subscribe(context.Background(), "user@example.com", "golang/go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sentTo != "user@example.com" {
		t.Fatalf("expected email sent, got %q", sentTo)
	}
}

func TestSubscribe_SeedsLastSeenTag(t *testing.T) {
	var seededTag string
	svc := newTestService(func(s *SubscriptionService) {
		s.repoRepo = &mockRepoRepo{
			getOrCreateFn: func(ctx context.Context, owner, name string) (*domain.Repository, error) {
				return &domain.Repository{ID: 1, Owner: owner, Name: name, LastSeenTag: ""}, nil
			},
		}
		s.github = &mockGitHub{
			getLatestRelFn: func(ctx context.Context, owner, repo string) (*domain.Release, error) {
				return &domain.Release{TagName: "v2.0.0"}, nil
			},
		}
	})
	// Override UpdateLastSeenTag to capture the seeded value.
	svc.repoRepo = &scannerMockRepoRepo{
		updateTagFn: func(ctx context.Context, id int64, tag string) error {
			seededTag = tag
			return nil
		},
		updateCheckedFn: func(ctx context.Context, id int64) error { return nil },
	}
	err := svc.Subscribe(context.Background(), "user@example.com", "golang/go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seededTag != "v2.0.0" {
		t.Fatalf("expected last_seen_tag seeded to v2.0.0, got %q", seededTag)
	}
}

func TestParseRepoName(t *testing.T) {
	tests := []struct {
		input     string
		wantOwner string
		wantName  string
		wantErr   bool
	}{
		{"golang/go", "golang", "go", false},
		{"facebook/react", "facebook", "react", false},
		{"", "", "", true},
		{"noslash", "", "", true},
		{"too/many/slashes", "", "", true},
		{"/empty", "", "", true},
		{"empty/", "", "", true},
	}
	for _, tt := range tests {
		owner, name, err := parseRepoName(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseRepoName(%q): err=%v, wantErr=%v", tt.input, err, tt.wantErr)
			continue
		}
		if owner != tt.wantOwner || name != tt.wantName {
			t.Errorf("parseRepoName(%q): got (%s, %s), want (%s, %s)",
				tt.input, owner, name, tt.wantOwner, tt.wantName)
		}
	}
}

func assertErrorIs(t *testing.T, err, target error) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error wrapping %v, got nil", target)
	}
	if !containsError(err, target) {
		t.Fatalf("expected error wrapping %v, got: %v", target, err)
	}
}

func containsError(err, target error) bool {
	for e := err; e != nil; {
		if e == target {
			return true
		}
		if unwrap, ok := e.(interface{ Unwrap() error }); ok {
			e = unwrap.Unwrap()
		} else {
			return false
		}
	}
	return false
}
