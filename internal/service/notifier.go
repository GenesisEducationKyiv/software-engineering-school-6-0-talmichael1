package service

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github-release-notifier/internal/domain"
)

const maxRetries = 5

// JobDequeuer reads notification jobs from the queue.
type JobDequeuer interface {
	Dequeue(ctx context.Context, timeout time.Duration) (*domain.NotificationJob, error)
	IsSent(ctx context.Context, subscriptionID int64, tag string) (bool, error)
	MarkSent(ctx context.Context, subscriptionID int64, tag string) error
	Requeue(ctx context.Context, job domain.NotificationJob) error
}

// Notifier runs a pool of workers that consume notification jobs and send emails.
type Notifier struct {
	queue      JobDequeuer
	email      EmailSender
	baseURL    string
	numWorkers int
}

func NewNotifier(queue JobDequeuer, email EmailSender, baseURL string, numWorkers int) *Notifier {
	return &Notifier{
		queue:      queue,
		email:      email,
		baseURL:    baseURL,
		numWorkers: numWorkers,
	}
}

// Run starts all notification workers. Blocks until the context is cancelled.
func (n *Notifier) Run(ctx context.Context) {
	slog.Info("notifier started", "workers", n.numWorkers)
	var wg sync.WaitGroup

	for i := 0; i < n.numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			n.worker(ctx, workerID)
		}(i)
	}

	wg.Wait()
	slog.Info("notifier stopped")
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
			slog.Error("notifier: dequeue error", "worker", id, "error", err)
			continue
		}
		if job == nil {
			continue
		}

		if err := n.processJob(ctx, job); err != nil {
			slog.Error("notifier: processing job",
				"worker", id,
				"email", job.Email,
				"repo", job.Repo,
				"error", err)
		}
	}
}

func (n *Notifier) processJob(ctx context.Context, job *domain.NotificationJob) error {
	// Check dedup: skip if we've already sent this notification.
	sent, err := n.queue.IsSent(ctx, job.SubscriptionID, job.Tag)
	if err != nil {
		return fmt.Errorf("checking dedup: %w", err)
	}
	if sent {
		slog.Debug("duplicate notification skipped",
			"subscription_id", job.SubscriptionID,
			"tag", job.Tag)
		return nil
	}

	unsubURL := fmt.Sprintf("%s/api/unsubscribe/%s", n.baseURL, job.UnsubToken)

	err = n.email.SendReleaseNotification(ctx, job.Email, job.Repo, job.Tag, job.ReleaseURL, unsubURL)
	if err != nil {
		if job.Attempt < maxRetries {
			slog.Warn("notification send failed, requeuing",
				"email", job.Email,
				"attempt", job.Attempt+1,
				"error", err)
			return n.queue.Requeue(ctx, *job)
		}
		return fmt.Errorf("max retries exceeded for %s: %w", job.Email, err)
	}

	// Mark as sent only after successful delivery.
	if err := n.queue.MarkSent(ctx, job.SubscriptionID, job.Tag); err != nil {
		slog.Error("failed to mark notification as sent (email was delivered)",
			"subscription_id", job.SubscriptionID,
			"tag", job.Tag,
			"error", err)
	}

	slog.Info("notification sent",
		"email", job.Email,
		"repo", job.Repo,
		"tag", job.Tag)
	return nil
}
