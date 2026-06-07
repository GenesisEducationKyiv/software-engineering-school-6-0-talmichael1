package notifier

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github-release-notifier/notifier/internal/domain"
	"github-release-notifier/notifier/internal/email"
	"github-release-notifier/notifier/internal/metrics"
	"github-release-notifier/notifier/internal/urls"
)

var tracer = otel.Tracer("notifier")

const (
	maxRetries   = 5
	reapInterval = 30 * time.Second
)

type JobDequeuer interface {
	Dequeue(ctx context.Context, timeout time.Duration) (*domain.NotificationJob, error)
	Ack(ctx context.Context, job domain.NotificationJob) error
	Reclaim(ctx context.Context) (int, error)
	IsSent(ctx context.Context, subscriptionID int64, tag string) (bool, error)
	MarkSent(ctx context.Context, subscriptionID int64, tag string) error
	Requeue(ctx context.Context, job domain.NotificationJob) error
}

type Notifier struct {
	queue      JobDequeuer
	email      email.Sender
	templates  email.Templates
	urls       urls.Builder
	numWorkers int
}

func New(queue JobDequeuer, sender email.Sender, urlBuilder urls.Builder, numWorkers int) *Notifier {
	return &Notifier{
		queue:      queue,
		email:      sender,
		templates:  email.Templates{},
		urls:       urlBuilder,
		numWorkers: numWorkers,
	}
}

func (n *Notifier) Run(ctx context.Context) {
	slog.InfoContext(ctx, "notifier started", "workers", n.numWorkers)
	var wg sync.WaitGroup

	for i := 0; i < n.numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			n.worker(ctx, workerID)
		}(i)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		n.reaper(ctx)
	}()

	wg.Wait()
	slog.InfoContext(ctx, "notifier stopped")
}

func (n *Notifier) reaper(ctx context.Context) {
	n.reclaim(ctx)
	ticker := time.NewTicker(reapInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n.reclaim(ctx)
		}
	}
}

func (n *Notifier) reclaim(ctx context.Context) {
	ctx, span := tracer.Start(ctx, "notifier.reclaim")
	defer span.End()

	reclaimed, err := n.queue.Reclaim(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "notifier: reclaiming expired jobs", "error", err)
		return
	}
	if reclaimed > 0 {
		slog.WarnContext(ctx, "notifier: reclaimed expired in-flight jobs", "count", reclaimed)
	}
}

func (n *Notifier) worker(ctx context.Context, id int) {
	for {
		if ctx.Err() != nil {
			return
		}

		job, err := n.queue.Dequeue(ctx, 5*time.Second)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.ErrorContext(ctx, "notifier: dequeue error", "worker", id, "error", err)
			continue
		}
		if job == nil {
			continue
		}

		if err := n.processJob(ctx, job); err != nil {
			slog.ErrorContext(ctx, "notifier: processing job",
				"worker", id,
				"email", job.Email,
				"repo", job.Repo,
				"error", err)
		}
	}
}

func (n *Notifier) processJob(ctx context.Context, job *domain.NotificationJob) error {
	ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier{
		"traceparent": job.Traceparent,
		"tracestate":  job.Tracestate,
	})
	ctx, span := tracer.Start(ctx, "notifier.process_job",
		trace.WithAttributes(
			attribute.String("repo", job.Repo),
			attribute.String("tag", job.Tag),
		))
	defer span.End()

	timer := prometheus.NewTimer(metrics.NotifierJobDuration)
	defer timer.ObserveDuration()

	sent, err := n.queue.IsSent(ctx, job.SubscriptionID, job.Tag)
	if err != nil {
		return fmt.Errorf("checking dedup: %w", err)
	}
	if sent {
		slog.DebugContext(ctx, "duplicate notification skipped",
			"subscription_id", job.SubscriptionID,
			"tag", job.Tag)
		n.ack(ctx, job)
		metrics.NotifierJobsProcessed.WithLabelValues("duplicate").Inc()
		return nil
	}

	unsubURL := n.urls.Unsubscribe(job.UnsubToken)
	msg := n.templates.ReleaseNotification(job.Email, job.Repo, job.Tag, job.ReleaseURL, unsubURL)

	err = n.email.Send(ctx, msg)
	if err != nil {
		if job.Attempt < maxRetries {
			slog.WarnContext(ctx, "notification send failed, requeuing",
				"email", job.Email,
				"attempt", job.Attempt+1,
				"error", err)
			metrics.NotifierJobsProcessed.WithLabelValues("retried").Inc()
			return n.queue.Requeue(ctx, *job)
		}
		metrics.NotifierJobsProcessed.WithLabelValues("failed").Inc()
		n.ack(ctx, job)
		return fmt.Errorf("max retries exceeded for %s: %w", job.Email, err)
	}

	if err := n.queue.MarkSent(ctx, job.SubscriptionID, job.Tag); err != nil {
		slog.ErrorContext(ctx, "failed to mark notification as sent (email was delivered)",
			"subscription_id", job.SubscriptionID,
			"tag", job.Tag,
			"error", err)
	}
	n.ack(ctx, job)
	metrics.NotifierJobsProcessed.WithLabelValues("sent").Inc()

	slog.InfoContext(ctx, "notification sent",
		"email", job.Email,
		"repo", job.Repo,
		"tag", job.Tag)
	return nil
}

func (n *Notifier) ack(ctx context.Context, job *domain.NotificationJob) {
	if err := n.queue.Ack(ctx, *job); err != nil {
		slog.ErrorContext(ctx, "notifier: ack failed",
			"subscription_id", job.SubscriptionID,
			"tag", job.Tag,
			"error", err)
	}
}
