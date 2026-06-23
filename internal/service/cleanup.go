package service

import (
	"context"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"

	"github-release-notifier/internal/domain"
	"github-release-notifier/internal/urls"
)

var cleanupTracer = otel.Tracer("cleanup")

const (
	cleanupInterval   = 30 * time.Minute
	maxUnconfirmedAge = 1 * time.Hour

	// sagaStaleAfter is how long a pending saga may sit untouched before the
	// reaper treats its orchestrator as crashed and takes over (ADR-0007).
	sagaStaleAfter   = 5 * time.Minute
	sagaRetentionAge = 24 * time.Hour
	maxSagaAttempts  = 5
	sagaReapBatch    = 100
)

type subscriptionMaintainer interface {
	DeleteUnconfirmedOlderThan(ctx context.Context, age time.Duration) (int64, error)
	Delete(ctx context.Context, id int64) error
}

type sagaReaper interface {
	ListStalePending(ctx context.Context, olderThan time.Duration, limit int) ([]domain.SubscriptionSaga, error)
	IncrementAttempts(ctx context.Context, id string) (int, error)
	MarkCompleted(ctx context.Context, id string) error
	MarkFailed(ctx context.Context, id, reason string) error
	DeleteTerminalOlderThan(ctx context.Context, age time.Duration) (int64, error)
}

type Cleanup struct {
	subRepo subscriptionMaintainer
	sagas   sagaReaper
	confirm ConfirmationSender
	urls    urls.Builder
}

func NewCleanup(subs subscriptionMaintainer, sagas sagaReaper, confirm ConfirmationSender, urlBuilder urls.Builder) *Cleanup {
	return &Cleanup{subRepo: subs, sagas: sagas, confirm: confirm, urls: urlBuilder}
}

func (c *Cleanup) Run(ctx context.Context) {
	slog.InfoContext(ctx, "cleanup worker started",
		"interval", cleanupInterval,
		"max_age", maxUnconfirmedAge)

	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.InfoContext(ctx, "cleanup worker stopped")
			return
		case <-ticker.C:
			c.run(ctx)
		}
	}
}

func (c *Cleanup) run(ctx context.Context) {
	ctx, span := cleanupTracer.Start(ctx, "cleanup.run")
	defer span.End()

	c.deleteStaleUnconfirmed(ctx)
	c.reapSagas(ctx)
}

func (c *Cleanup) deleteStaleUnconfirmed(ctx context.Context) {
	deleted, err := c.subRepo.DeleteUnconfirmedOlderThan(ctx, maxUnconfirmedAge)
	if err != nil {
		slog.ErrorContext(ctx, "cleanup: deleting stale subscriptions", "error", err)
		return
	}
	if deleted > 0 {
		slog.InfoContext(ctx, "cleanup: removed stale unconfirmed subscriptions", "count", deleted)
	}
}

// reapSagas drives forward-recovery on interrupted sagas and garbage-collects
// old terminal rows (ADR-0007).
func (c *Cleanup) reapSagas(ctx context.Context) {
	stale, err := c.sagas.ListStalePending(ctx, sagaStaleAfter, sagaReapBatch)
	if err != nil {
		slog.ErrorContext(ctx, "cleanup: listing stale sagas", "error", err)
		return
	}
	for i := range stale {
		c.recoverSaga(ctx, &stale[i])
	}

	deleted, err := c.sagas.DeleteTerminalOlderThan(ctx, sagaRetentionAge)
	if err != nil {
		slog.ErrorContext(ctx, "cleanup: deleting old sagas", "error", err)
		return
	}
	if deleted > 0 {
		slog.InfoContext(ctx, "cleanup: removed old terminal sagas", "count", deleted)
	}
}

func (c *Cleanup) recoverSaga(ctx context.Context, sg *domain.SubscriptionSaga) {
	// A pending saga without a subscription crashed before step 1 committed —
	// nothing was created, so there is nothing to retry or compensate.
	if sg.SubscriptionID == nil {
		_ = c.sagas.MarkFailed(ctx, sg.ID, "abandoned before subscription created") //nolint:errcheck
		return
	}

	attempts, err := c.sagas.IncrementAttempts(ctx, sg.ID)
	if err != nil {
		slog.ErrorContext(ctx, "cleanup: incrementing saga attempts", "saga", sg.ID, "error", err)
		return
	}

	confirmURL := c.urls.Confirm(sg.ConfirmToken)
	if err := c.confirm.Send(ctx, sg.Email, sg.RepoFullName, confirmURL); err != nil {
		if attempts >= maxSagaAttempts {
			_ = c.subRepo.Delete(ctx, *sg.SubscriptionID)   //nolint:errcheck // best-effort compensation
			_ = c.sagas.MarkFailed(ctx, sg.ID, err.Error()) //nolint:errcheck
			slog.WarnContext(ctx, "cleanup: saga exhausted retries, compensated",
				"saga", sg.ID, "email", sg.Email, "error", err)
		}
		return
	}
	_ = c.sagas.MarkCompleted(ctx, sg.ID) //nolint:errcheck
	slog.InfoContext(ctx, "cleanup: recovered saga via retry", "saga", sg.ID, "email", sg.Email)
}
