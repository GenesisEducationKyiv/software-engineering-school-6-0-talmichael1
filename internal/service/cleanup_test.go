package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github-release-notifier/internal/domain"
	"github-release-notifier/internal/urls"
)

type mockCleanupSubRepo struct {
	deleteFn      func(ctx context.Context, age time.Duration) (int64, error)
	deletedSubIDs []int64
}

func (m *mockCleanupSubRepo) DeleteUnconfirmedOlderThan(ctx context.Context, age time.Duration) (int64, error) {
	if m.deleteFn != nil {
		return m.deleteFn(ctx, age)
	}
	return 0, nil
}

func (m *mockCleanupSubRepo) Delete(_ context.Context, id int64) error {
	m.deletedSubIDs = append(m.deletedSubIDs, id)
	return nil
}

type mockSagaReaper struct {
	stale      []domain.SubscriptionSaga
	completed  []string
	failed     []string
	attempts   map[string]int
	deletedOld int64
}

func (m *mockSagaReaper) ListStalePending(_ context.Context, _ time.Duration, _ int) ([]domain.SubscriptionSaga, error) {
	if m.attempts == nil {
		m.attempts = map[string]int{}
	}
	for _, sg := range m.stale {
		m.attempts[sg.ID] = sg.Attempts
	}
	return m.stale, nil
}
func (m *mockSagaReaper) IncrementAttempts(_ context.Context, id string) (int, error) {
	if m.attempts == nil {
		m.attempts = map[string]int{}
	}
	m.attempts[id]++
	return m.attempts[id], nil
}
func (m *mockSagaReaper) MarkCompleted(_ context.Context, id string) error {
	m.completed = append(m.completed, id)
	return nil
}
func (m *mockSagaReaper) MarkFailed(_ context.Context, id, _ string) error {
	m.failed = append(m.failed, id)
	return nil
}
func (m *mockSagaReaper) DeleteTerminalOlderThan(_ context.Context, _ time.Duration) (int64, error) {
	return m.deletedOld, nil
}

type mockReapConfirm struct {
	err error
}

func (m *mockReapConfirm) Send(_ context.Context, _, _, _ string) error { return m.err }

func newCleanup(subs *mockCleanupSubRepo, sagas *mockSagaReaper, confirm ConfirmationSender) *Cleanup {
	return NewCleanup(subs, sagas, confirm, urls.Builder{BaseURL: "http://localhost:8080"})
}

func subID(id int64) *int64 { return &id }

func TestCleanup_DeletesStaleSubscriptions(t *testing.T) {
	var calledAge time.Duration
	repo := &mockCleanupSubRepo{
		deleteFn: func(ctx context.Context, age time.Duration) (int64, error) {
			calledAge = age
			return 3, nil
		},
	}

	c := newCleanup(repo, &mockSagaReaper{}, &mockReapConfirm{})
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

	c := newCleanup(repo, &mockSagaReaper{}, &mockReapConfirm{})
	c.run(context.Background()) // should not panic
}

func TestCleanup_RecoversSagaOnRetrySuccess(t *testing.T) {
	sagas := &mockSagaReaper{
		stale: []domain.SubscriptionSaga{
			{ID: "s1", Email: "u@e.com", RepoFullName: "golang/go", ConfirmToken: "ct", SubscriptionID: subID(42)},
		},
	}
	c := newCleanup(&mockCleanupSubRepo{}, sagas, &mockReapConfirm{})
	c.run(context.Background())

	if len(sagas.completed) != 1 || sagas.completed[0] != "s1" {
		t.Fatalf("expected saga s1 completed, got %v", sagas.completed)
	}
}

func TestCleanup_LeavesSagaPendingWhileRetriesRemain(t *testing.T) {
	sagas := &mockSagaReaper{
		stale: []domain.SubscriptionSaga{
			{ID: "s1", Email: "u@e.com", RepoFullName: "golang/go", ConfirmToken: "ct", SubscriptionID: subID(42), Attempts: 0},
		},
	}
	sub := &mockCleanupSubRepo{}
	c := newCleanup(sub, sagas, &mockReapConfirm{err: fmt.Errorf("notifier down")})
	c.run(context.Background())

	if len(sagas.failed) != 0 {
		t.Fatalf("expected saga left pending, got failed %v", sagas.failed)
	}
	if len(sub.deletedSubIDs) != 0 {
		t.Fatalf("expected no compensation yet, deleted %v", sub.deletedSubIDs)
	}
}

func TestCleanup_CompensatesSagaWhenRetriesExhausted(t *testing.T) {
	sagas := &mockSagaReaper{
		stale: []domain.SubscriptionSaga{
			{ID: "s1", Email: "u@e.com", RepoFullName: "golang/go", ConfirmToken: "ct", SubscriptionID: subID(42), Attempts: maxSagaAttempts},
		},
	}
	sub := &mockCleanupSubRepo{}
	c := newCleanup(sub, sagas, &mockReapConfirm{err: fmt.Errorf("notifier down")})
	c.run(context.Background())

	if len(sagas.failed) != 1 || sagas.failed[0] != "s1" {
		t.Fatalf("expected saga s1 failed, got %v", sagas.failed)
	}
	if len(sub.deletedSubIDs) != 1 || sub.deletedSubIDs[0] != 42 {
		t.Fatalf("expected subscription 42 compensated, got %v", sub.deletedSubIDs)
	}
}

func TestCleanup_FailsSagaAbandonedBeforeSubscription(t *testing.T) {
	sagas := &mockSagaReaper{
		stale: []domain.SubscriptionSaga{
			{ID: "s1", Email: "u@e.com", RepoFullName: "golang/go", ConfirmToken: "ct", SubscriptionID: nil},
		},
	}
	sub := &mockCleanupSubRepo{}
	confirm := &mockReapConfirm{}
	c := newCleanup(sub, sagas, confirm)
	c.run(context.Background())

	if len(sagas.failed) != 1 || sagas.failed[0] != "s1" {
		t.Fatalf("expected saga s1 failed, got %v", sagas.failed)
	}
	if len(sub.deletedSubIDs) != 0 {
		t.Fatalf("expected no compensation, got %v", sub.deletedSubIDs)
	}
}
