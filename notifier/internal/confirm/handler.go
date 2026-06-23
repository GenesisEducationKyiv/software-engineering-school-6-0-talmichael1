package confirm

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"

	"github-release-notifier/notifier/internal/metrics"
)

var tracer = otel.Tracer("confirm")

type request struct {
	Email      string `json:"email"`
	Repo       string `json:"repo"`
	ConfirmURL string `json:"confirm_url"`
}

// Handler is the REST transport for the confirmation step (ADR-0007).
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
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
	ctx, span := tracer.Start(ctx, "confirm.rest")
	defer span.End()

	if err := h.svc.Send(ctx, req.Email, req.Repo, req.ConfirmURL); err != nil {
		metrics.ConfirmationEmails.WithLabelValues("rest", "failed").Inc()
		slog.ErrorContext(ctx, "confirm: sending email", "email", req.Email, "repo", req.Repo, "error", err)
		http.Error(w, "sending confirmation email failed", http.StatusBadGateway)
		return
	}

	metrics.ConfirmationEmails.WithLabelValues("rest", "sent").Inc()
	slog.InfoContext(ctx, "confirmation email sent", "transport", "rest", "email", req.Email, "repo", req.Repo)
	w.WriteHeader(http.StatusOK)
}
