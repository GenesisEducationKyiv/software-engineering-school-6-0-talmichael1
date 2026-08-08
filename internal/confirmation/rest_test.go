package confirmation

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRESTClientSendSuccess(t *testing.T) {
	var got payload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/confirmations" {
			t.Errorf("path = %q", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewRESTClient(srv.URL, time.Second)
	if err := c.Send(context.Background(), "user@example.com", "golang/go", "https://x/confirm?t=abc"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Email != "user@example.com" || got.Repo != "golang/go" || got.ConfirmURL != "https://x/confirm?t=abc" {
		t.Fatalf("payload = %+v", got)
	}
}

func TestRESTClientSendNon200IsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	c := NewRESTClient(srv.URL, time.Second)
	if err := c.Send(context.Background(), "u@e.com", "golang/go", "https://x"); err == nil {
		t.Fatal("expected error on non-200 response")
	}
}

func TestRESTClientSendTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close() // server is down — Do returns a transport error

	c := NewRESTClient(srv.URL, 200*time.Millisecond)
	if err := c.Send(context.Background(), "u@e.com", "golang/go", "https://x"); err == nil {
		t.Fatal("expected transport error")
	}
}
