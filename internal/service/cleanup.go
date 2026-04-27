package service

import (
	"context"
	"log/slog"
	"time"

	"github-release-notifier/internal/repository"
)

const (
	cleanupInterval   = 30 * time.Minute
	maxUnconfirmedAge = 1 * time.Hour
)

// Cleanup periodically removes unconfirmed subscriptions that have expired.
type Cleanup struct {
	subRepo repository.SubscriptionRepo
}

func NewCleanup(subRepo repository.SubscriptionRepo) *Cleanup {
	return &Cleanup{subRepo: subRepo}
}

// Run starts the cleanup loop. It blocks until the context is cancelled.
func (c *Cleanup) Run(ctx context.Context) {
	slog.Info("cleanup worker started",
		"interval", cleanupInterval,
		"max_age", maxUnconfirmedAge)

	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("cleanup worker stopped")
			return
		case <-ticker.C:
			c.run(ctx)
		}
	}
}

func (c *Cleanup) run(ctx context.Context) {
	deleted, err := c.subRepo.DeleteUnconfirmedOlderThan(ctx, maxUnconfirmedAge)
	if err != nil {
		slog.Error("cleanup: deleting stale subscriptions", "error", err)
		return
	}
	if deleted > 0 {
		slog.Info("cleanup: removed stale unconfirmed subscriptions", "count", deleted)
	}
}
