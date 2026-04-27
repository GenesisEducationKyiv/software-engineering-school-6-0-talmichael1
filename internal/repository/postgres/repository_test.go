//go:build integration

package postgres

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"

	"github-release-notifier/internal/domain"
	"github-release-notifier/migrations"
)

var testDB *sqlx.DB

func TestMain(m *testing.M) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		fmt.Println("DATABASE_URL not set, skipping repository tests")
		os.Exit(0)
	}

	var err error
	testDB, err = sqlx.Connect("postgres", dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connecting to database: %v\n", err)
		os.Exit(1)
	}
	defer testDB.Close()

	source, err := iofs.New(migrations.FS, ".")
	if err != nil {
		fmt.Fprintf(os.Stderr, "creating migration source: %v\n", err)
		os.Exit(1)
	}
	mig, err := migrate.NewWithSourceInstance("iofs", source, dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "creating migrator: %v\n", err)
		os.Exit(1)
	}
	if err := mig.Up(); err != nil && err != migrate.ErrNoChange {
		fmt.Fprintf(os.Stderr, "running migrations: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()

	testDB.Exec("DELETE FROM subscriptions")
	testDB.Exec("DELETE FROM repositories")

	os.Exit(code)
}

func cleanTables(t *testing.T) {
	t.Helper()
	testDB.Exec("DELETE FROM subscriptions")
	testDB.Exec("DELETE FROM repositories")
}

func TestRepositoryStore_GetOrCreate(t *testing.T) {
	cleanTables(t)
	store := NewRepositoryStore(testDB)
	ctx := context.Background()

	repo, err := store.GetOrCreate(ctx, "golang", "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.Owner != "golang" || repo.Name != "go" {
		t.Fatalf("expected golang/go, got %s/%s", repo.Owner, repo.Name)
	}
	if repo.ID == 0 {
		t.Fatal("expected non-zero ID")
	}

	// Second call should return the same repo.
	repo2, err := store.GetOrCreate(ctx, "golang", "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo2.ID != repo.ID {
		t.Fatalf("expected same ID %d, got %d", repo.ID, repo2.ID)
	}
}

func TestRepositoryStore_GetByID(t *testing.T) {
	cleanTables(t)
	store := NewRepositoryStore(testDB)
	ctx := context.Background()

	repo, _ := store.GetOrCreate(ctx, "gin-gonic", "gin")

	found, err := store.GetByID(ctx, repo.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found.Owner != "gin-gonic" {
		t.Fatalf("expected gin-gonic, got %s", found.Owner)
	}

	_, err = store.GetByID(ctx, 99999)
	if err != domain.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestRepositoryStore_UpdateLastSeenTag(t *testing.T) {
	cleanTables(t)
	store := NewRepositoryStore(testDB)
	ctx := context.Background()

	repo, _ := store.GetOrCreate(ctx, "golang", "go")
	if err := store.UpdateLastSeenTag(ctx, repo.ID, "v1.22.0"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updated, _ := store.GetByID(ctx, repo.ID)
	if updated.LastSeenTag != "v1.22.0" {
		t.Fatalf("expected v1.22.0, got %s", updated.LastSeenTag)
	}
	if updated.CheckedAt == nil {
		t.Fatal("expected checked_at to be set")
	}
}

func TestRepositoryStore_UpdateCheckedAt(t *testing.T) {
	cleanTables(t)
	store := NewRepositoryStore(testDB)
	ctx := context.Background()

	repo, err := store.GetOrCreate(ctx, "golang", "go")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	if err := store.UpdateCheckedAt(ctx, repo.ID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updated, _ := store.GetByID(ctx, repo.ID)
	if updated.CheckedAt == nil {
		t.Fatal("expected checked_at to be set")
	}
}

func TestRepositoryStore_ListWithActiveSubscriptions(t *testing.T) {
	cleanTables(t)
	repoStore := NewRepositoryStore(testDB)
	subStore := NewSubscriptionStore(testDB)
	ctx := context.Background()

	// Create two repos.
	repo1, _ := repoStore.GetOrCreate(ctx, "golang", "go")
	repo2, _ := repoStore.GetOrCreate(ctx, "gin-gonic", "gin")

	// Subscribe to repo1 and confirm.
	sub := &domain.Subscription{
		Email:            "test@example.com",
		RepositoryID:     repo1.ID,
		ConfirmToken:     "ct1",
		UnsubscribeToken: "ut1",
	}
	subStore.Create(ctx, sub)
	subStore.Confirm(ctx, sub.ID)

	// repo2 has no confirmed subscriptions.
	sub2 := &domain.Subscription{
		Email:            "test@example.com",
		RepositoryID:     repo2.ID,
		ConfirmToken:     "ct2",
		UnsubscribeToken: "ut2",
	}
	subStore.Create(ctx, sub2) // unconfirmed

	repos, err := repoStore.ListWithActiveSubscriptions(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo with active subs, got %d", len(repos))
	}
	if repos[0].ID != repo1.ID {
		t.Fatalf("expected repo %d, got %d", repo1.ID, repos[0].ID)
	}
}
