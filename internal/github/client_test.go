package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github-release-notifier/internal/domain"
)

func TestClient_RepoExists(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/golang/go" {
			w.Header().Set("X-RateLimit-Remaining", "50")
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"full_name":"golang/go"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL, "")

	err := c.RepoExists(context.Background(), "golang", "go")
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	err = c.RepoExists(context.Background(), "nonexistent", "repo")
	if err != domain.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestClient_GetLatestRelease(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "50")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"tag_name":"v1.22.0","name":"Go 1.22","html_url":"https://github.com/golang/go/releases/tag/go1.22.0"}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL, "")
	release, err := c.GetLatestRelease(context.Background(), "golang", "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if release.TagName != "v1.22.0" {
		t.Fatalf("expected tag v1.22.0, got %s", release.TagName)
	}
}

func TestClient_RateLimitRetry(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("Retry-After", "1")
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Second).Unix(), 10))
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("X-RateLimit-Remaining", "50")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"full_name":"golang/go"}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL, "")
	err := c.RepoExists(context.Background(), "golang", "go")
	if err != nil {
		t.Fatalf("expected success after retry, got %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts)
	}
}

func TestClient_AuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("X-RateLimit-Remaining", "50")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL, "ghp_testtoken123")
	_ = c.RepoExists(context.Background(), "golang", "go")

	if gotAuth != "Bearer ghp_testtoken123" {
		t.Fatalf("expected Bearer token, got %q", gotAuth)
	}
}

func TestClient_ParseRateLimitHeaders(t *testing.T) {
	resetTime := time.Now().Add(30 * time.Minute)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "42")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetTime.Unix(), 10))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL, "")
	_ = c.RepoExists(context.Background(), "golang", "go")

	c.mu.Lock()
	remaining := c.rateRemaining
	reset := c.rateLimitReset
	c.mu.Unlock()

	if remaining != 42 {
		t.Fatalf("expected remaining=42, got %d", remaining)
	}
	if reset.Unix() != resetTime.Unix() {
		t.Fatalf("expected reset=%d, got %d", resetTime.Unix(), reset.Unix())
	}
}

// newTestClient creates a Client that points at a test server instead of github.com.
func newTestClient(baseURL, token string) *Client {
	c := NewClient(token)
	// Override the base URL by using a custom HTTP client transport.
	// Instead, we'll just replace the package-level baseURL for testing.
	// Since baseURL is a const, we need a different approach — use a wrapper.
	// The simplest: set the httpClient to redirect to our test server.
	c.httpClient.Transport = &rewriteTransport{base: baseURL}
	return c
}

// rewriteTransport replaces the GitHub API host with a test server URL.
type rewriteTransport struct {
	base string
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	testURL := t.base
	// Strip scheme from test URL.
	if len(testURL) > 7 && testURL[:7] == "http://" {
		testURL = testURL[7:]
	}
	req.URL.Host = testURL
	return http.DefaultTransport.RoundTrip(req)
}
