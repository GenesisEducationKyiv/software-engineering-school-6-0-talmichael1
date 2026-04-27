//go:build integration

package postgres

import (
	"context"
	"testing"
	"time"

	"github-release-notifier/internal/domain"
)

func TestSubscriptionStore_CreateAndGet(t *testing.T) {
	cleanTables(t)
	repoStore := NewRepositoryStore(testDB)
	subStore := NewSubscriptionStore(testDB)
	ctx := context.Background()

	repo, _ := repoStore.GetOrCreate(ctx, "golang", "go")

	sub := &domain.Subscription{
		Email:            "user@example.com",
		RepositoryID:     repo.ID,
		ConfirmToken:     "confirm-token-1",
		UnsubscribeToken: "unsub-token-1",
	}
	if err := subStore.Create(ctx, sub); err != nil {
		t.Fatalf("create: %v", err)
	}
	if sub.ID == 0 {
		t.Fatal("expected non-zero ID after create")
	}

	// Get by confirm token.
	found, err := subStore.GetByConfirmToken(ctx, "confirm-token-1")
	if err != nil {
		t.Fatalf("get by confirm token: %v", err)
	}
	if found.Email != "user@example.com" {
		t.Fatalf("expected user@example.com, got %s", found.Email)
	}

	// Get by unsub token.
	found, err = subStore.GetByUnsubscribeToken(ctx, "unsub-token-1")
	if err != nil {
		t.Fatalf("get by unsub token: %v", err)
	}
	if found.ID != sub.ID {
		t.Fatalf("expected ID %d, got %d", sub.ID, found.ID)
	}
}

func TestSubscriptionStore_CreateDuplicate(t *testing.T) {
	cleanTables(t)
	repoStore := NewRepositoryStore(testDB)
	subStore := NewSubscriptionStore(testDB)
	ctx := context.Background()

	repo, _ := repoStore.GetOrCreate(ctx, "golang", "go")

	sub1 := &domain.Subscription{
		Email:            "dup@example.com",
		RepositoryID:     repo.ID,
		ConfirmToken:     "ct-dup-1",
		UnsubscribeToken: "ut-dup-1",
	}
	if err := subStore.Create(ctx, sub1); err != nil {
		t.Fatalf("first create: %v", err)
	}

	sub2 := &domain.Subscription{
		Email:            "dup@example.com",
		RepositoryID:     repo.ID,
		ConfirmToken:     "ct-dup-2",
		UnsubscribeToken: "ut-dup-2",
	}
	err := subStore.Create(ctx, sub2)
	if err != domain.ErrConflict {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
}

func TestSubscriptionStore_Confirm(t *testing.T) {
	cleanTables(t)
	repoStore := NewRepositoryStore(testDB)
	subStore := NewSubscriptionStore(testDB)
	ctx := context.Background()

	repo, _ := repoStore.GetOrCreate(ctx, "golang", "go")
	sub := &domain.Subscription{
		Email:            "confirm@example.com",
		RepositoryID:     repo.ID,
		ConfirmToken:     "ct-confirm",
		UnsubscribeToken: "ut-confirm",
	}
	subStore.Create(ctx, sub)

	if err := subStore.Confirm(ctx, sub.ID); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	found, _ := subStore.GetByConfirmToken(ctx, "ct-confirm")
	if !found.Confirmed {
		t.Fatal("expected confirmed=true")
	}
}

func TestSubscriptionStore_Delete(t *testing.T) {
	cleanTables(t)
	repoStore := NewRepositoryStore(testDB)
	subStore := NewSubscriptionStore(testDB)
	ctx := context.Background()

	repo, _ := repoStore.GetOrCreate(ctx, "golang", "go")
	sub := &domain.Subscription{
		Email:            "delete@example.com",
		RepositoryID:     repo.ID,
		ConfirmToken:     "ct-delete",
		UnsubscribeToken: "ut-delete",
	}
	subStore.Create(ctx, sub)

	if err := subStore.Delete(ctx, sub.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	_, err := subStore.GetByConfirmToken(ctx, "ct-delete")
	if err != domain.ErrNotFound {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestSubscriptionStore_ListByEmail(t *testing.T) {
	cleanTables(t)
	repoStore := NewRepositoryStore(testDB)
	subStore := NewSubscriptionStore(testDB)
	ctx := context.Background()

	repo, _ := repoStore.GetOrCreate(ctx, "golang", "go")
	sub := &domain.Subscription{
		Email:            "list@example.com",
		RepositoryID:     repo.ID,
		ConfirmToken:     "ct-list",
		UnsubscribeToken: "ut-list",
	}
	subStore.Create(ctx, sub)

	views, err := subStore.ListByEmail(ctx, "list@example.com")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("expected 1, got %d", len(views))
	}
	if views[0].Repo != "golang/go" {
		t.Fatalf("expected golang/go, got %s", views[0].Repo)
	}
}

func TestSubscriptionStore_ListConfirmedByRepoID(t *testing.T) {
	cleanTables(t)
	repoStore := NewRepositoryStore(testDB)
	subStore := NewSubscriptionStore(testDB)
	ctx := context.Background()

	repo, _ := repoStore.GetOrCreate(ctx, "golang", "go")

	// One confirmed, one unconfirmed.
	s1 := &domain.Subscription{Email: "a@b.com", RepositoryID: repo.ID, ConfirmToken: "ct-lc1", UnsubscribeToken: "ut-lc1"}
	s2 := &domain.Subscription{Email: "c@d.com", RepositoryID: repo.ID, ConfirmToken: "ct-lc2", UnsubscribeToken: "ut-lc2"}
	subStore.Create(ctx, s1)
	subStore.Create(ctx, s2)
	subStore.Confirm(ctx, s1.ID)

	subs, err := subStore.ListConfirmedByRepoID(ctx, repo.ID)
	if err != nil {
		t.Fatalf("list confirmed: %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("expected 1 confirmed sub, got %d", len(subs))
	}
	if subs[0].Email != "a@b.com" {
		t.Fatalf("expected a@b.com, got %s", subs[0].Email)
	}
}

func TestSubscriptionStore_DeleteUnconfirmedOlderThan(t *testing.T) {
	cleanTables(t)
	repoStore := NewRepositoryStore(testDB)
	subStore := NewSubscriptionStore(testDB)
	ctx := context.Background()

	repo, _ := repoStore.GetOrCreate(ctx, "golang", "go")

	sub := &domain.Subscription{
		Email:            "stale@example.com",
		RepositoryID:     repo.ID,
		ConfirmToken:     "ct-stale",
		UnsubscribeToken: "ut-stale",
	}
	subStore.Create(ctx, sub)

	// Backdate created_at to 2 hours ago.
	testDB.Exec("UPDATE subscriptions SET created_at = NOW() - INTERVAL '2 hours' WHERE id = $1", sub.ID)

	deleted, err := subStore.DeleteUnconfirmedOlderThan(ctx, 1*time.Hour)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 deleted, got %d", deleted)
	}

	_, err = subStore.GetByConfirmToken(ctx, "ct-stale")
	if err != domain.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestSubscriptionStore_TokenNotFound(t *testing.T) {
	cleanTables(t)
	subStore := NewSubscriptionStore(testDB)
	ctx := context.Background()

	_, err := subStore.GetByConfirmToken(ctx, "nonexistent")
	if err != domain.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	_, err = subStore.GetByUnsubscribeToken(ctx, "nonexistent")
	if err != domain.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
