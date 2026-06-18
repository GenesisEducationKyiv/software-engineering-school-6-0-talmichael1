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

	"github-release-notifier/notifier/internal/email"
	"github-release-notifier/notifier/internal/metrics"
	"github-release-notifier/notifier/internal/queue"
	"github-release-notifier/notifier/internal/urls"
)

var tracer = otel.Tracer("notifier")

const (
	maxRetries       = 5
	reestablishDelay = 1 * time.Second
)

// JobConsumer drains one channel of the notifications queue. Each worker owns
// its own consumer; the queue redelivers a job to another worker if this one
// crashes before acking (ADR-0006).
type JobConsumer interface {
	Dequeue(ctx context.Context) (*queue.Delivery, error)
	Ack(d *queue.Delivery) error
	Nack(d *queue.Delivery) error
	Close() error
}

// Deduper suppresses re-sends of an already-delivered (subscription, tag). It is
// Redis-backed key-value state, separate from the queue (ADR-0006).
type Deduper interface {
	IsSent(ctx context.Context, subscriptionID int64, tag string) (bool, error)
	MarkSent(ctx context.Context, subscriptionID int64, tag string) error
}

type Notifier struct {
	consume    func(ctx context.Context) (JobConsumer, error)
	dedup      Deduper
	email      email.Sender
	templates  email.Templates
	urls       urls.Builder
	numWorkers int
}

func New(
	consume func(ctx context.Context) (JobConsumer, error),
	dedup Deduper,
	sender email.Sender,
	urlBuilder urls.Builder,
	numWorkers int,
) *Notifier {
	return &Notifier{
		consume:    consume,
		dedup:      dedup,
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

	wg.Wait()
	slog.InfoContext(ctx, "notifier stopped")
}

func (n *Notifier) worker(ctx context.Context, id int) {
	for ctx.Err() == nil {
		consumer, err := n.consume(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.ErrorContext(ctx, "notifier: establishing consumer", "worker", id, "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(reestablishDelay):
			}
			continue
		}
		n.drain(ctx, id, consumer)
		_ = consumer.Close()
	}
}

// drain processes deliveries until the channel closes (reconnect) or ctx is
// cancelled, returning so the worker can re-establish its consumer.
func (n *Notifier) drain(ctx context.Context, id int, c JobConsumer) {
	for {
		d, err := c.Dequeue(ctx)
		if err != nil {
			if ctx.Err() == nil {
				slog.WarnContext(ctx, "notifier: consumer closed, re-establishing", "worker", id, "error", err)
			}
			return
		}
		if err := n.processJob(ctx, c, d); err != nil {
			slog.ErrorContext(ctx, "notifier: processing job",
				"worker", id,
				"email", d.Job.Email,
				"repo", d.Job.Repo,
				"error", err)
		}
	}
}

func (n *Notifier) processJob(ctx context.Context, c JobConsumer, d *queue.Delivery) error {
	job := d.Job
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

	sent, err := n.dedup.IsSent(ctx, job.SubscriptionID, job.Tag)
	if err != nil {
		// Outcome unknown — return the job to the queue rather than risk a
		// missed send.
		n.nack(ctx, c, d)
		return fmt.Errorf("checking dedup: %w", err)
	}
	if sent {
		slog.DebugContext(ctx, "duplicate notification skipped",
			"subscription_id", job.SubscriptionID,
			"tag", job.Tag)
		n.ack(ctx, c, d)
		metrics.NotifierJobsProcessed.WithLabelValues("duplicate").Inc()
		return nil
	}

	unsubURL := n.urls.Unsubscribe(job.UnsubToken)
	msg := n.templates.ReleaseNotification(job.Email, job.Repo, job.Tag, job.ReleaseURL, unsubURL)

	if err := n.email.Send(ctx, msg); err != nil {
		if d.DeliveryCount < maxRetries {
			slog.WarnContext(ctx, "notification send failed, requeuing",
				"email", job.Email,
				"delivery", d.DeliveryCount+1,
				"error", err)
			metrics.NotifierJobsProcessed.WithLabelValues("retried").Inc()
			n.nack(ctx, c, d)
			return nil
		}
		metrics.NotifierJobsProcessed.WithLabelValues("failed").Inc()
		n.ack(ctx, c, d)
		return fmt.Errorf("max retries exceeded for %s: %w", job.Email, err)
	}

	if err := n.dedup.MarkSent(ctx, job.SubscriptionID, job.Tag); err != nil {
		slog.ErrorContext(ctx, "failed to mark notification as sent (email was delivered)",
			"subscription_id", job.SubscriptionID,
			"tag", job.Tag,
			"error", err)
	}
	n.ack(ctx, c, d)
	metrics.NotifierJobsProcessed.WithLabelValues("sent").Inc()

	slog.InfoContext(ctx, "notification sent",
		"email", job.Email,
		"repo", job.Repo,
		"tag", job.Tag)
	return nil
}

func (n *Notifier) ack(ctx context.Context, c JobConsumer, d *queue.Delivery) {
	if err := c.Ack(d); err != nil {
		slog.ErrorContext(ctx, "notifier: ack failed",
			"subscription_id", d.Job.SubscriptionID,
			"tag", d.Job.Tag,
			"error", err)
	}
}

func (n *Notifier) nack(ctx context.Context, c JobConsumer, d *queue.Delivery) {
	if err := c.Nack(d); err != nil {
		slog.ErrorContext(ctx, "notifier: nack failed",
			"subscription_id", d.Job.SubscriptionID,
			"tag", d.Job.Tag,
			"error", err)
	}
}
