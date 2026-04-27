package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github-release-notifier/internal/domain"
	"github-release-notifier/internal/metrics"
	"github-release-notifier/internal/repository"
)

// NotificationEnqueuer pushes notification jobs to the queue.
type NotificationEnqueuer interface {
	EnqueueBatch(ctx context.Context, jobs []domain.NotificationJob) error
}

// Scanner periodically checks GitHub repositories for new releases and enqueues
// notifications for all confirmed subscribers.
type Scanner struct {
	repoRepo repository.RepositoryRepo
	subRepo  repository.SubscriptionRepo
	github   GitHubChecker
	queue    NotificationEnqueuer
	baseURL  string
	interval time.Duration
	workers  int
}

func NewScanner(
	repoRepo repository.RepositoryRepo,
	subRepo repository.SubscriptionRepo,
	github GitHubChecker,
	queue NotificationEnqueuer,
	baseURL string,
	interval time.Duration,
	workers int,
) *Scanner {
	if workers < 1 {
		workers = 1
	}
	return &Scanner{
		repoRepo: repoRepo,
		subRepo:  subRepo,
		github:   github,
		queue:    queue,
		baseURL:  baseURL,
		interval: interval,
		workers:  workers,
	}
}

// Run starts the scanner loop. It blocks until the context is cancelled.
func (s *Scanner) Run(ctx context.Context) {
	slog.Info("scanner started", "interval", s.interval)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	// Run immediately on startup, then on each tick.
	s.scan(ctx)
	for {
		select {
		case <-ctx.Done():
			slog.Info("scanner stopped")
			return
		case <-ticker.C:
			s.scan(ctx)
		}
	}
}

func (s *Scanner) scan(ctx context.Context) {
	metrics.ScannerRuns.Inc()
	timer := prometheus.NewTimer(metrics.ScannerDuration)
	defer timer.ObserveDuration()

	if n, err := s.subRepo.CountConfirmed(ctx); err == nil {
		metrics.ActiveSubscriptions.Set(float64(n))
	}

	repos, err := s.repoRepo.ListWithActiveSubscriptions(ctx)
	if err != nil {
		slog.Error("scanner: listing repos", "error", err)
		return
	}
	slog.Info("scanner: checking repositories", "count", len(repos), "workers", s.workers)

	ch := make(chan domain.Repository)
	var wg sync.WaitGroup

	for range s.workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for repo := range ch {
				if err := s.checkRepo(ctx, repo); err != nil {
					slog.Error("scanner: checking repo",
						"repo", repo.FullName(),
						"error", err)
				}
			}
		}()
	}

	for _, repo := range repos {
		if ctx.Err() != nil {
			break
		}
		ch <- repo
	}
	close(ch)
	wg.Wait()
}

func (s *Scanner) checkRepo(ctx context.Context, repo domain.Repository) error {
	release, err := s.github.GetLatestRelease(ctx, repo.Owner, repo.Name)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			// No releases for this repo yet — just update checked_at.
			return s.repoRepo.UpdateCheckedAt(ctx, repo.ID)
		}
		return fmt.Errorf("fetching latest release: %w", err)
	}

	if release.TagName == repo.LastSeenTag {
		return s.repoRepo.UpdateCheckedAt(ctx, repo.ID)
	}

	slog.Info("new release detected",
		"repo", repo.FullName(),
		"tag", release.TagName,
		"previous", repo.LastSeenTag)

	subs, err := s.subRepo.ListConfirmedByRepoID(ctx, repo.ID)
	if err != nil {
		return fmt.Errorf("listing subscribers: %w", err)
	}

	jobs := make([]domain.NotificationJob, 0, len(subs))
	for _, sub := range subs {
		jobs = append(jobs, domain.NotificationJob{
			SubscriptionID: sub.ID,
			Email:          sub.Email,
			Repo:           repo.FullName(),
			Tag:            release.TagName,
			ReleaseName:    release.Name,
			ReleaseURL:     release.HTMLURL,
			UnsubToken:     sub.UnsubscribeToken,
		})
	}

	if err := s.queue.EnqueueBatch(ctx, jobs); err != nil {
		return fmt.Errorf("enqueuing notifications: %w", err)
	}
	metrics.NotificationsEnqueued.Add(float64(len(jobs)))

	// Update the tag only after successful enqueue to guarantee at-least-once delivery.
	if err := s.repoRepo.UpdateLastSeenTag(ctx, repo.ID, release.TagName); err != nil {
		return fmt.Errorf("updating last seen tag: %w", err)
	}

	slog.Info("notifications enqueued",
		"repo", repo.FullName(),
		"tag", release.TagName,
		"count", len(jobs))
	return nil
}
