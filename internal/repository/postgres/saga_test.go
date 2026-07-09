//go:build integration

package postgres

import (
	"context"
	"testing"
	"time"

	"github-release-notifier/internal/domain"
)

func newSaga(email, repo, token string) *domain.SubscriptionSaga {
	return &domain.SubscriptionSaga{Email: email, RepoFullName: repo, ConfirmToken: token}
}

func TestSagaStore_CreateStartsPending(t *testing.T) {
	cleanTables(t)
	store := NewSagaStore(testDB)
	ctx := context.Background()

	saga := newSaga("user@example.com", "golang/go", "ct-1")
	if err := store.Create(ctx, saga); err != nil {
		t.Fatalf("create: %v", err)
	}
	if saga.ID == "" {
		t.Fatal("expected generated id")
	}
	if saga.State != domain.SagaPending {
		t.Fatalf("state = %q, want pending", saga.State)
	}
	if saga.SubscriptionID != nil {
		t.Fatal("expected nil subscription_id before step 1")
	}
}

func TestSagaStore_CreateSubscriptionAtomicAttach(t *testing.T) {
	cleanTables(t)
	repoStore := NewRepositoryStore(testDB)
	sagaStore := NewSagaStore(testDB)
	ctx := context.Background()

	repo, _ := repoStore.GetOrCreate(ctx, "golang", "go")
	saga := newSaga("user@example.com", "golang/go", "ct-2")
	if err := sagaStore.Create(ctx, saga); err != nil {
		t.Fatalf("create saga: %v", err)
	}

	sub := &domain.Subscription{
		Email:            "user@example.com",
		RepositoryID:     repo.ID,
		ConfirmToken:     "ct-2",
		UnsubscribeToken: "ut-2",
	}
	if err := sagaStore.CreateSubscription(ctx, saga.ID, sub); err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	if sub.ID == 0 {
		t.Fatal("expected subscription id")
	}

	stale, err := sagaStore.ListStalePending(ctx, 0, 10)
	if err != nil {
		t.Fatalf("list stale: %v", err)
	}
	if len(stale) != 1 || stale[0].SubscriptionID == nil || *stale[0].SubscriptionID != sub.ID {
		t.Fatalf("expected saga attached to subscription %d, got %+v", sub.ID, stale)
	}
}

func TestSagaStore_CreateSubscriptionConflictRollsBack(t *testing.T) {
	cleanTables(t)
	repoStore := NewRepositoryStore(testDB)
	subStore := NewSubscriptionStore(testDB)
	sagaStore := NewSagaStore(testDB)
	ctx := context.Background()

	repo, _ := repoStore.GetOrCreate(ctx, "golang", "go")
	existing := &domain.Subscription{
		Email: "dup@example.com", RepositoryID: repo.ID,
		ConfirmToken: "ct-x", UnsubscribeToken: "ut-x",
	}
	if err := subStore.Create(ctx, existing); err != nil {
		t.Fatalf("seed subscription: %v", err)
	}

	saga := newSaga("dup@example.com", "golang/go", "ct-3")
	_ = sagaStore.Create(ctx, saga)

	dup := &domain.Subscription{
		Email: "dup@example.com", RepositoryID: repo.ID,
		ConfirmToken: "ct-3", UnsubscribeToken: "ut-3",
	}
	if err := sagaStore.CreateSubscription(ctx, saga.ID, dup); err != domain.ErrConflict {
		t.Fatalf("err = %v, want ErrConflict", err)
	}

	stale, _ := sagaStore.ListStalePending(ctx, 0, 10)
	if len(stale) != 1 || stale[0].SubscriptionID != nil {
		t.Fatalf("expected saga unattached after rollback, got %+v", stale)
	}
}

func TestSagaStore_MarkTransitions(t *testing.T) {
	cleanTables(t)
	store := NewSagaStore(testDB)
	ctx := context.Background()

	completed := newSaga("a@example.com", "golang/go", "ct-c")
	_ = store.Create(ctx, completed)
	if err := store.MarkCompleted(ctx, completed.ID); err != nil {
		t.Fatalf("mark completed: %v", err)
	}

	failed := newSaga("b@example.com", "gin-gonic/gin", "ct-f")
	_ = store.Create(ctx, failed)
	if err := store.MarkFailed(ctx, failed.ID, "boom"); err != nil {
		t.Fatalf("mark failed: %v", err)
	}

	stale, _ := store.ListStalePending(ctx, 0, 10)
	if len(stale) != 0 {
		t.Fatalf("terminal sagas must not appear as stale pending, got %d", len(stale))
	}
}

func TestSagaStore_IncrementAttempts(t *testing.T) {
	cleanTables(t)
	store := NewSagaStore(testDB)
	ctx := context.Background()

	saga := newSaga("a@example.com", "golang/go", "ct-a")
	_ = store.Create(ctx, saga)

	n, err := store.IncrementAttempts(ctx, saga.ID)
	if err != nil {
		t.Fatalf("increment: %v", err)
	}
	if n != 1 {
		t.Fatalf("attempts = %d, want 1", n)
	}
	n, _ = store.IncrementAttempts(ctx, saga.ID)
	if n != 2 {
		t.Fatalf("attempts = %d, want 2", n)
	}
}

func TestSagaStore_DeleteTerminalOlderThan(t *testing.T) {
	cleanTables(t)
	store := NewSagaStore(testDB)
	ctx := context.Background()

	old := newSaga("a@example.com", "golang/go", "ct-old")
	_ = store.Create(ctx, old)
	_ = store.MarkCompleted(ctx, old.ID)
	testDB.MustExec(`UPDATE subscription_sagas SET updated_at = NOW() - INTERVAL '48 hours' WHERE id = $1`, old.ID)

	pending := newSaga("b@example.com", "gin-gonic/gin", "ct-p")
	_ = store.Create(ctx, pending)

	deleted, err := store.DeleteTerminalOlderThan(ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
}
