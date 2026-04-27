package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github-release-notifier/internal/domain"
)

type mockCleanupSubRepo struct {
	deleteFn func(ctx context.Context, age time.Duration) (int64, error)
}

func (m *mockCleanupSubRepo) Create(_ context.Context, _ *domain.Subscription) error { return nil }
func (m *mockCleanupSubRepo) GetByConfirmToken(_ context.Context, _ string) (*domain.Subscription, error) {
	return nil, nil
}
func (m *mockCleanupSubRepo) GetByUnsubscribeToken(_ context.Context, _ string) (*domain.Subscription, error) {
	return nil, nil
}
func (m *mockCleanupSubRepo) Confirm(_ context.Context, _ int64) error { return nil }
func (m *mockCleanupSubRepo) Delete(_ context.Context, _ int64) error  { return nil }
func (m *mockCleanupSubRepo) ListByEmail(_ context.Context, _ string) ([]domain.SubscriptionView, error) {
	return nil, nil
}
func (m *mockCleanupSubRepo) ListConfirmedByRepoID(_ context.Context, _ int64) ([]domain.Subscription, error) {
	return nil, nil
}
func (m *mockCleanupSubRepo) DeleteUnconfirmedOlderThan(ctx context.Context, age time.Duration) (int64, error) {
	if m.deleteFn != nil {
		return m.deleteFn(ctx, age)
	}
	return 0, nil
}
func (m *mockCleanupSubRepo) CountConfirmed(_ context.Context) (int64, error) { return 0, nil }

func TestCleanup_DeletesStaleSubscriptions(t *testing.T) {
	var calledAge time.Duration
	repo := &mockCleanupSubRepo{
		deleteFn: func(ctx context.Context, age time.Duration) (int64, error) {
			calledAge = age
			return 3, nil
		},
	}

	c := NewCleanup(repo)
	c.run(context.Background())

	if calledAge != maxUnconfirmedAge {
		t.Fatalf("expected age %v, got %v", maxUnconfirmedAge, calledAge)
	}
}

func TestCleanup_HandlesError(t *testing.T) {
	repo := &mockCleanupSubRepo{
		deleteFn: func(ctx context.Context, age time.Duration) (int64, error) {
			return 0, fmt.Errorf("database error")
		},
	}

	c := NewCleanup(repo)
	// Should not panic.
	c.run(context.Background())
}

func TestCleanup_NothingToDelete(t *testing.T) {
	repo := &mockCleanupSubRepo{
		deleteFn: func(ctx context.Context, age time.Duration) (int64, error) {
			return 0, nil
		},
	}

	c := NewCleanup(repo)
	c.run(context.Background())
}
