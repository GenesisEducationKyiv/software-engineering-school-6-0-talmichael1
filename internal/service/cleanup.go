package service

import (
	"context"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
)

var cleanupTracer = otel.Tracer("cleanup")

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

	deleted, err := c.subRepo.DeleteUnconfirmedOlderThan(ctx, maxUnconfirmedAge)
	if err != nil {
		slog.ErrorContext(ctx, "cleanup: deleting stale subscriptions", "error", err)
		return
	}
	if deleted > 0 {
		slog.InfoContext(ctx, "cleanup: removed stale unconfirmed subscriptions", "count", deleted)
	}
}
