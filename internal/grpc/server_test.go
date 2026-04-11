package grpc

import (
	"context"
	"testing"
	"time"

	"github-release-notifier/internal/domain"
	pb "github-release-notifier/internal/grpc/proto"
	"github-release-notifier/internal/service"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// --- mocks ---

type mockSubRepo struct{}

func (m *mockSubRepo) Create(_ context.Context, _ *domain.Subscription) error { return nil }
func (m *mockSubRepo) GetByConfirmToken(_ context.Context, _ string) (*domain.Subscription, error) {
	return &domain.Subscription{ID: 1}, nil
}
func (m *mockSubRepo) GetByUnsubscribeToken(_ context.Context, _ string) (*domain.Subscription, error) {
	return &domain.Subscription{ID: 1}, nil
}
func (m *mockSubRepo) Confirm(_ context.Context, _ int64) error { return nil }
func (m *mockSubRepo) Delete(_ context.Context, _ int64) error  { return nil }
func (m *mockSubRepo) ListByEmail(_ context.Context, _ string) ([]domain.SubscriptionView, error) {
	return []domain.SubscriptionView{}, nil
}
func (m *mockSubRepo) ListConfirmedByRepoID(_ context.Context, _ int64) ([]domain.Subscription, error) {
	return nil, nil
}
func (m *mockSubRepo) DeleteUnconfirmedOlderThan(_ context.Context, _ time.Duration) (int64, error) {
	return 0, nil
}
func (m *mockSubRepo) CountConfirmed(_ context.Context) (int64, error) { return 0, nil }

type mockRepoRepo struct{}

func (m *mockRepoRepo) GetOrCreate(_ context.Context, owner, name string) (*domain.Repository, error) {
	return &domain.Repository{ID: 1, Owner: owner, Name: name}, nil
}
func (m *mockRepoRepo) GetByID(_ context.Context, id int64) (*domain.Repository, error) {
	return &domain.Repository{ID: id}, nil
}
func (m *mockRepoRepo) ListWithActiveSubscriptions(_ context.Context) ([]domain.Repository, error) {
	return nil, nil
}
func (m *mockRepoRepo) UpdateLastSeenTag(_ context.Context, _ int64, _ string) error { return nil }
func (m *mockRepoRepo) UpdateCheckedAt(_ context.Context, _ int64) error             { return nil }

type mockGitHub struct {
	err error
}

func (m *mockGitHub) RepoExists(_ context.Context, _, _ string) error { return m.err }
func (m *mockGitHub) GetLatestRelease(_ context.Context, _, _ string) (*domain.Release, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &domain.Release{TagName: "v1.0.0"}, nil
}

type mockEmail struct{}

func (m *mockEmail) SendConfirmation(_ context.Context, _, _, _ string) error { return nil }
func (m *mockEmail) SendReleaseNotification(_ context.Context, _, _, _, _, _ string) error {
	return nil
}

func newTestServer(opts ...func(*mockGitHub)) *Server {
	gh := &mockGitHub{}
	for _, opt := range opts {
		opt(gh)
	}
	svc := service.NewSubscriptionService(
		&mockSubRepo{},
		&mockRepoRepo{},
		gh,
		&mockEmail{},
		"http://localhost:8080",
	)
	return NewServer(svc)
}

// --- tests ---

func TestGRPC_Subscribe_Success(t *testing.T) {
	srv := newTestServer()
	resp, err := srv.Subscribe(context.Background(), &pb.SubscribeRequest{
		Email: "user@example.com",
		Repo:  "golang/go",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Message == "" {
		t.Fatal("expected non-empty message")
	}
}

func TestGRPC_Subscribe_InvalidInput(t *testing.T) {
	srv := newTestServer()
	_, err := srv.Subscribe(context.Background(), &pb.SubscribeRequest{
		Email: "user@example.com",
		Repo:  "badformat",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", status.Code(err))
	}
}

func TestGRPC_Subscribe_RepoNotFound(t *testing.T) {
	srv := newTestServer(func(gh *mockGitHub) {
		gh.err = domain.ErrNotFound
	})
	_, err := srv.Subscribe(context.Background(), &pb.SubscribeRequest{
		Email: "user@example.com",
		Repo:  "nonexistent/repo",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound, got %v", status.Code(err))
	}
}

func TestGRPC_Confirm_Success(t *testing.T) {
	srv := newTestServer()
	resp, err := srv.Confirm(context.Background(), &pb.ConfirmRequest{Token: "validtoken"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Message == "" {
		t.Fatal("expected non-empty message")
	}
}

func TestGRPC_Confirm_EmptyToken(t *testing.T) {
	srv := newTestServer()
	_, err := srv.Confirm(context.Background(), &pb.ConfirmRequest{Token: ""})
	if err == nil {
		t.Fatal("expected error")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", status.Code(err))
	}
}

func TestGRPC_Unsubscribe_Success(t *testing.T) {
	srv := newTestServer()
	resp, err := srv.Unsubscribe(context.Background(), &pb.UnsubscribeRequest{Token: "validtoken"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Message == "" {
		t.Fatal("expected non-empty message")
	}
}

func TestGRPC_ListSubscriptions_Success(t *testing.T) {
	srv := newTestServer()
	resp, err := srv.ListSubscriptions(context.Background(), &pb.ListRequest{Email: "user@example.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Subscriptions == nil {
		t.Fatal("expected non-nil subscriptions slice")
	}
}

func TestGRPC_ListSubscriptions_InvalidEmail(t *testing.T) {
	srv := newTestServer()
	_, err := srv.ListSubscriptions(context.Background(), &pb.ListRequest{Email: "notanemail"})
	if err == nil {
		t.Fatal("expected error")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", status.Code(err))
	}
}
