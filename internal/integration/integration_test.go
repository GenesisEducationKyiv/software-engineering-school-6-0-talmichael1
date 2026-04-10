//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"

	"github-release-notifier/internal/domain"
	"github-release-notifier/internal/handler"
	"github-release-notifier/internal/repository/postgres"
	"github-release-notifier/internal/service"
	"github-release-notifier/migrations"
)

var (
	testDB     *sqlx.DB
	testRouter *gin.Engine
)

// stubGitHub always reports that the repo exists and has no releases.
type stubGitHub struct{}

func (s *stubGitHub) RepoExists(ctx context.Context, owner, repo string) error {
	return nil
}

func (s *stubGitHub) GetLatestRelease(ctx context.Context, owner, repo string) (*domain.Release, error) {
	return nil, domain.ErrNotFound
}

// stubEmail logs emails instead of sending them.
type stubEmail struct {
	lastTo         string
	lastConfirmURL string
}

func (s *stubEmail) SendConfirmation(ctx context.Context, to, repo, confirmURL string) error {
	s.lastTo = to
	s.lastConfirmURL = confirmURL
	return nil
}

func (s *stubEmail) SendReleaseNotification(ctx context.Context, to, repo, tag, releaseURL, unsubURL string) error {
	return nil
}

func TestMain(m *testing.M) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		fmt.Println("DATABASE_URL not set, skipping integration tests")
		os.Exit(0)
	}

	var err error
	testDB, err = sqlx.Connect("postgres", dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connecting to database: %v\n", err)
		os.Exit(1)
	}
	defer testDB.Close()

	runMigrations(dbURL)

	code := m.Run()

	// Clean up test data.
	testDB.Exec("DELETE FROM subscriptions")
	testDB.Exec("DELETE FROM repositories")

	os.Exit(code)
}

func runMigrations(dbURL string) {
	source, err := iofs.New(migrations.FS, ".")
	if err != nil {
		fmt.Fprintf(os.Stderr, "creating migration source: %v\n", err)
		os.Exit(1)
	}
	m, err := migrate.NewWithSourceInstance("iofs", source, dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "creating migrator: %v\n", err)
		os.Exit(1)
	}
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		fmt.Fprintf(os.Stderr, "running migrations: %v\n", err)
		os.Exit(1)
	}
}

func setupRouter(emailStub *stubEmail) *gin.Engine {
	gin.SetMode(gin.TestMode)

	repoStore := postgres.NewRepositoryStore(testDB)
	subStore := postgres.NewSubscriptionStore(testDB)

	svc := service.NewSubscriptionService(subStore, repoStore, &stubGitHub{}, emailStub, "http://localhost:8080")

	router := gin.New()
	api := router.Group("/api")
	{
		api.POST("/subscribe", handler.Subscribe(svc))
		api.GET("/confirm/:token", handler.Confirm(svc))
		api.GET("/unsubscribe/:token", handler.Unsubscribe(svc))
		api.GET("/subscriptions", handler.Subscriptions(svc))
	}
	return router
}

func cleanDB(t *testing.T) {
	t.Helper()
	testDB.Exec("DELETE FROM subscriptions")
	testDB.Exec("DELETE FROM repositories")
}

func TestFullSubscriptionFlow(t *testing.T) {
	cleanDB(t)
	emailStub := &stubEmail{}
	router := setupRouter(emailStub)

	// 1. Subscribe.
	body, _ := json.Marshal(map[string]string{
		"email": "integration@example.com",
		"repo":  "golang/go",
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/subscribe", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("subscribe: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if emailStub.lastTo != "integration@example.com" {
		t.Fatalf("expected confirmation email to integration@example.com, got %s", emailStub.lastTo)
	}

	// 2. List subscriptions — should be unconfirmed.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/subscriptions?email=integration@example.com", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var subs []domain.SubscriptionView
	if err := json.Unmarshal(w.Body.Bytes(), &subs); err != nil {
		t.Fatalf("decode subscriptions: %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("expected 1 subscription, got %d", len(subs))
	}
	if subs[0].Confirmed {
		t.Fatal("subscription should be unconfirmed")
	}

	// 3. Extract confirm token from the database.
	var confirmToken string
	err := testDB.Get(&confirmToken,
		"SELECT confirm_token FROM subscriptions WHERE email = $1", "integration@example.com")
	if err != nil {
		t.Fatalf("fetching confirm token: %v", err)
	}

	// 4. Confirm.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/confirm/"+confirmToken, nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("confirm: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// 5. List again — now confirmed.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/subscriptions?email=integration@example.com", nil)
	router.ServeHTTP(w, req)

	if err := json.Unmarshal(w.Body.Bytes(), &subs); err != nil {
		t.Fatalf("decode subscriptions: %v", err)
	}
	if !subs[0].Confirmed {
		t.Fatal("subscription should be confirmed after confirm")
	}

	// 6. Unsubscribe.
	var unsubToken string
	err = testDB.Get(&unsubToken,
		"SELECT unsubscribe_token FROM subscriptions WHERE email = $1", "integration@example.com")
	if err != nil {
		t.Fatalf("fetching unsub token: %v", err)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/unsubscribe/"+unsubToken, nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("unsubscribe: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// 7. List after unsubscribe — should be empty.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/subscriptions?email=integration@example.com", nil)
	router.ServeHTTP(w, req)

	if err := json.Unmarshal(w.Body.Bytes(), &subs); err != nil {
		t.Fatalf("decode subscriptions: %v", err)
	}
	if len(subs) != 0 {
		t.Fatalf("expected 0 subscriptions after unsubscribe, got %d", len(subs))
	}
}

func TestSubscribeDuplicateReturns409(t *testing.T) {
	cleanDB(t)
	emailStub := &stubEmail{}
	router := setupRouter(emailStub)

	body, _ := json.Marshal(map[string]string{
		"email": "dup@example.com",
		"repo":  "golang/go",
	})

	// First subscription.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/subscribe", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("first subscribe: expected 200, got %d", w.Code)
	}

	// Duplicate subscription.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/subscribe", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicate subscribe: expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSubscribeInvalidEmail(t *testing.T) {
	cleanDB(t)
	emailStub := &stubEmail{}
	router := setupRouter(emailStub)

	body, _ := json.Marshal(map[string]string{
		"email": "not-an-email",
		"repo":  "golang/go",
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/subscribe", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSubscribeInvalidRepoFormat(t *testing.T) {
	cleanDB(t)
	emailStub := &stubEmail{}
	router := setupRouter(emailStub)

	body, _ := json.Marshal(map[string]string{
		"email": "user@example.com",
		"repo":  "invalid-format",
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/subscribe", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestConfirmInvalidToken(t *testing.T) {
	cleanDB(t)
	emailStub := &stubEmail{}
	router := setupRouter(emailStub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/confirm/nonexistent-token", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUnsubscribeInvalidToken(t *testing.T) {
	cleanDB(t)
	emailStub := &stubEmail{}
	router := setupRouter(emailStub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/unsubscribe/nonexistent-token", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSubscriptionsEmptyResponse(t *testing.T) {
	cleanDB(t)
	emailStub := &stubEmail{}
	router := setupRouter(emailStub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/subscriptions?email=nobody@example.com", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var subs []domain.SubscriptionView
	if err := json.Unmarshal(w.Body.Bytes(), &subs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(subs) != 0 {
		t.Fatalf("expected empty array, got %d items", len(subs))
	}

	// Verify it's [] not null.
	if w.Body.String() != "[]" {
		t.Fatalf("expected [], got %s", w.Body.String())
	}
}

func TestMultipleReposForSameEmail(t *testing.T) {
	cleanDB(t)
	emailStub := &stubEmail{}
	router := setupRouter(emailStub)

	for _, repo := range []string{"golang/go", "gin-gonic/gin"} {
		body, _ := json.Marshal(map[string]string{
			"email": "multi@example.com",
			"repo":  repo,
		})
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/subscribe", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("subscribe %s: expected 200, got %d", repo, w.Code)
		}
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/subscriptions?email=multi@example.com", nil)
	router.ServeHTTP(w, req)

	var subs []domain.SubscriptionView
	if err := json.Unmarshal(w.Body.Bytes(), &subs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(subs) != 2 {
		t.Fatalf("expected 2 subscriptions, got %d", len(subs))
	}
}
