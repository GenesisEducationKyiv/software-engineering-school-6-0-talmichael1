package service

import (
	"context"
	"fmt"
	"testing"
	"time"
)

type mockCleanupSubRepo struct {
	deleteFn func(ctx context.Context, age time.Duration) (int64, error)
}

func (m *mockCleanupSubRepo) DeleteUnconfirmedOlderThan(ctx context.Context, age time.Duration) (int64, error) {
	if m.deleteFn != nil {
		return m.deleteFn(ctx, age)
	}
	return 0, nil
}

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
