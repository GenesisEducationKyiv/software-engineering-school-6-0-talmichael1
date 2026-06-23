// Package confirm serves the saga participant endpoint that sends subscription
// confirmation emails on behalf of the API orchestrator (ADR-0007).
package confirm

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github-release-notifier/notifier/internal/email"
	"github-release-notifier/notifier/internal/metrics"
)

var tracer = otel.Tracer("confirm")

type request struct {
	Email      string `json:"email"`
	Repo       string `json:"repo"`
	ConfirmURL string `json:"confirm_url"`
}

// Handler renders and sends the confirmation email for one subscribe saga.
type Handler struct {
	sender    email.Sender
	templates email.Templates
}

func NewHandler(sender email.Sender) *Handler {
	return &Handler{sender: sender}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Email == "" || req.Repo == "" || req.ConfirmURL == "" {
		http.Error(w, "email, repo and confirm_url are required", http.StatusBadRequest)
		return
	}

	ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
	ctx, span := tracer.Start(ctx, "confirm.send", trace.WithAttributes())
	defer span.End()

	msg := h.templates.Confirmation(req.Email, req.Repo, req.ConfirmURL)
	if err := h.sender.Send(ctx, msg); err != nil {
		metrics.ConfirmationEmails.WithLabelValues("failed").Inc()
		slog.ErrorContext(ctx, "confirm: sending email", "email", req.Email, "repo", req.Repo, "error", err)
		http.Error(w, "sending confirmation email failed", http.StatusBadGateway)
		return
	}

	metrics.ConfirmationEmails.WithLabelValues("sent").Inc()
	slog.InfoContext(ctx, "confirmation email sent", "email", req.Email, "repo", req.Repo)
	w.WriteHeader(http.StatusOK)
}
