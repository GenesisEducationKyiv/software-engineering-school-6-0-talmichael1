package service

import (
	"context"
	"log/slog"
	"time"
)

const (
	cleanupInterval   = 30 * time.Minute
	maxUnconfirmedAge = 1 * time.Hour
)

type unconfirmedDeleter interface {
	DeleteUnconfirmedOlderThan(ctx context.Context, age time.Duration) (int64, error)
}

type Cleanup struct {
	subRepo unconfirmedDeleter
}

func NewCleanup(subs unconfirmedDeleter) *Cleanup {
	return &Cleanup{subRepo: subs}
}

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
