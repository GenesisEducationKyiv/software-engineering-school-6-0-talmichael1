package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github-release-notifier/internal/domain"
	"github-release-notifier/internal/service"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestSubscribeHandler_Success(t *testing.T) {
	svc := service.NewSubscriptionService(
		&mockSubRepoHandler{},
		&mockRepoRepoHandler{},
		&mockGitHubHandler{},
		&mockEmailHandler{},
		"http://localhost:8080",
	)

	router := gin.New()
	router.POST("/api/subscribe", Subscribe(svc))

	body, _ := json.Marshal(map[string]string{
		"email": "user@example.com",
		"repo":  "golang/go",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/subscribe", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSubscribeHandler_InvalidBody(t *testing.T) {
	svc := service.NewSubscriptionService(
		&mockSubRepoHandler{},
		&mockRepoRepoHandler{},
		&mockGitHubHandler{},
		&mockEmailHandler{},
		"http://localhost:8080",
	)

	router := gin.New()
	router.POST("/api/subscribe", Subscribe(svc))

	req := httptest.NewRequest(http.MethodPost, "/api/subscribe", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestSubscribeHandler_RepoNotFound(t *testing.T) {
	svc := service.NewSubscriptionService(
		&mockSubRepoHandler{},
		&mockRepoRepoHandler{},
		&mockGitHubHandler{repoErr: domain.ErrNotFound},
		&mockEmailHandler{},
		"http://localhost:8080",
	)

	router := gin.New()
	router.POST("/api/subscribe", Subscribe(svc))

	body, _ := json.Marshal(map[string]string{
		"email": "user@example.com",
		"repo":  "nonexistent/repo",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/subscribe", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSubscribeHandler_Conflict(t *testing.T) {
	svc := service.NewSubscriptionService(
		&mockSubRepoHandler{createErr: domain.ErrConflict},
		&mockRepoRepoHandler{},
		&mockGitHubHandler{},
		&mockEmailHandler{},
		"http://localhost:8080",
	)

	router := gin.New()
	router.POST("/api/subscribe", Subscribe(svc))

	body, _ := json.Marshal(map[string]string{
		"email": "user@example.com",
		"repo":  "golang/go",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/subscribe", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestConfirmHandler_Success(t *testing.T) {
	svc := service.NewSubscriptionService(
		&mockSubRepoHandler{},
		&mockRepoRepoHandler{},
		&mockGitHubHandler{},
		&mockEmailHandler{},
		"http://localhost:8080",
	)

	router := gin.New()
	router.GET("/api/confirm/:token", Confirm(svc))

	req := httptest.NewRequest(http.MethodGet, "/api/confirm/validtoken", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestConfirmHandler_NotFound(t *testing.T) {
	svc := service.NewSubscriptionService(
		&mockSubRepoHandler{confirmTokenErr: domain.ErrNotFound},
		&mockRepoRepoHandler{},
		&mockGitHubHandler{},
		&mockEmailHandler{},
		"http://localhost:8080",
	)

	router := gin.New()
	router.GET("/api/confirm/:token", Confirm(svc))

	req := httptest.NewRequest(http.MethodGet, "/api/confirm/badtoken", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUnsubscribeHandler_Success(t *testing.T) {
	svc := service.NewSubscriptionService(
		&mockSubRepoHandler{},
		&mockRepoRepoHandler{},
		&mockGitHubHandler{},
		&mockEmailHandler{},
		"http://localhost:8080",
	)

	router := gin.New()
	router.GET("/api/unsubscribe/:token", Unsubscribe(svc))

	req := httptest.NewRequest(http.MethodGet, "/api/unsubscribe/validtoken", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSubscriptionsHandler_MissingEmail(t *testing.T) {
	svc := service.NewSubscriptionService(
		&mockSubRepoHandler{},
		&mockRepoRepoHandler{},
		&mockGitHubHandler{},
		&mockEmailHandler{},
		"http://localhost:8080",
	)

	router := gin.New()
	router.GET("/api/subscriptions", Subscriptions(svc))

	req := httptest.NewRequest(http.MethodGet, "/api/subscriptions", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestSubscriptionsHandler_Success(t *testing.T) {
	svc := service.NewSubscriptionService(
		&mockSubRepoHandler{
			listResult: []domain.SubscriptionView{
				{Email: "user@example.com", Repo: "golang/go", Confirmed: true, LastSeenTag: "go1.22"},
			},
		},
		&mockRepoRepoHandler{},
		&mockGitHubHandler{},
		&mockEmailHandler{},
		"http://localhost:8080",
	)

	router := gin.New()
	router.GET("/api/subscriptions", Subscriptions(svc))

	req := httptest.NewRequest(http.MethodGet, "/api/subscriptions?email=user@example.com", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result []domain.SubscriptionView
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(result) != 1 || result[0].Repo != "golang/go" {
		t.Fatalf("unexpected response: %+v", result)
	}
}
