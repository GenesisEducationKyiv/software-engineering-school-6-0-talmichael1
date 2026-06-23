package confirm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github-release-notifier/notifier/internal/email"
)

type fakeSender struct {
	sent []email.Message
	err  error
}

func (f *fakeSender) Send(_ context.Context, msg email.Message) error {
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, msg)
	return nil
}

func post(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/internal/confirmations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHandlerSendsConfirmation(t *testing.T) {
	sender := &fakeSender{}
	h := NewHandler(sender)

	rec := post(t, h, `{"email":"user@example.com","repo":"golang/go","confirm_url":"https://x/confirm?t=abc"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(sender.sent) != 1 {
		t.Fatalf("sent %d messages, want 1", len(sender.sent))
	}
	msg := sender.sent[0]
	if msg.To != "user@example.com" {
		t.Errorf("To = %q, want user@example.com", msg.To)
	}
	if !strings.Contains(msg.Text, "https://x/confirm?t=abc") {
		t.Errorf("confirm URL missing from body: %q", msg.Text)
	}
	if !strings.Contains(msg.Subject, "golang/go") {
		t.Errorf("repo missing from subject: %q", msg.Subject)
	}
}

func TestHandlerRejectsBadJSON(t *testing.T) {
	sender := &fakeSender{}
	rec := post(t, NewHandler(sender), `{not json`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if len(sender.sent) != 0 {
		t.Errorf("sent %d messages, want 0", len(sender.sent))
	}
}

func TestHandlerRejectsMissingFields(t *testing.T) {
	cases := map[string]string{
		"no email":       `{"repo":"golang/go","confirm_url":"https://x"}`,
		"no repo":        `{"email":"u@e.com","confirm_url":"https://x"}`,
		"no confirm_url": `{"email":"u@e.com","repo":"golang/go"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			sender := &fakeSender{}
			rec := post(t, NewHandler(sender), body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			if len(sender.sent) != 0 {
				t.Errorf("sent %d messages, want 0", len(sender.sent))
			}
		})
	}
}

func TestHandlerReportsSendFailure(t *testing.T) {
	sender := &fakeSender{err: errors.New("mailgun down")}
	rec := post(t, NewHandler(sender), `{"email":"u@e.com","repo":"golang/go","confirm_url":"https://x"}`)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

func TestHandlerRejectsNonPost(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/internal/confirmations", nil)
	rec := httptest.NewRecorder()
	NewHandler(&fakeSender{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}
