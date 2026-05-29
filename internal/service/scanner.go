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
)

const (
	scanLockKey        = "scanner:leader"
	repoDequeueTimeout = 5 * time.Second
)

type NotificationEnqueuer interface {
	EnqueueBatch(ctx context.Context, jobs []domain.NotificationJob) error
}

type scannerRepoStore interface {
	ListWithActiveSubscriptions(ctx context.Context) ([]domain.Repository, error)
	UpdateLastSeenTag(ctx context.Context, id int64, tag string) error
	UpdateCheckedAt(ctx context.Context, id int64) error
}

type scannerSubStore interface {
	ListConfirmedByRepoID(ctx context.Context, repoID int64) ([]domain.Subscription, error)
	CountConfirmed(ctx context.Context) (int64, error)
}

type repoCheckQueue interface {
	EnqueueRepo(ctx context.Context, repo domain.Repository) error
	DequeueRepo(ctx context.Context, timeout time.Duration) (*domain.Repository, error)
}

type leaderLock interface {
	Acquire(ctx context.Context, key string, ttl time.Duration) (bool, error)
}

type Scanner struct {
	repoRepo   scannerRepoStore
	subRepo    scannerSubStore
	github     GitHubChecker
	queue      NotificationEnqueuer
	repoChecks repoCheckQueue
	lock       leaderLock
	interval   time.Duration
	workers    int
}

func NewScanner(
	repos scannerRepoStore,
	subs scannerSubStore,
	github GitHubChecker,
	queue NotificationEnqueuer,
	repoChecks repoCheckQueue,
	lock leaderLock,
	interval time.Duration,
	workers int,
) *Scanner {
	if workers < 1 {
		workers = 1
	}
	return &Scanner{
		repoRepo:   repos,
		subRepo:    subs,
		github:     github,
		queue:      queue,
		repoChecks: repoChecks,
		lock:       lock,
		interval:   interval,
		workers:    workers,
	}
}

func (s *Scanner) Run(ctx context.Context) {
	slog.InfoContext(ctx, "scanner started", "interval", s.interval, "workers", s.workers)

	var wg sync.WaitGroup
	for range s.workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.worker(ctx)
		}()
	}

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	s.enqueueDueRepos(ctx)
	for {
		select {
		case <-ctx.Done():
			slog.InfoContext(ctx, "scanner stopped")
			wg.Wait()
			return
		case <-ticker.C:
			s.enqueueDueRepos(ctx)
		}
	}
}

func (s *Scanner) worker(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		repo, err := s.repoChecks.DequeueRepo(ctx, repoDequeueTimeout)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.ErrorContext(ctx, "scanner: dequeue repo", "error", err)
			continue
		}
		if repo == nil {
			continue
		}
		if err := s.checkRepo(ctx, *repo); err != nil {
			slog.ErrorContext(ctx, "scanner: checking repo", "repo", repo.FullName(), "error", err)
		}
	}
}

func (s *Scanner) enqueueDueRepos(ctx context.Context) {
	acquired, err := s.lock.Acquire(ctx, scanLockKey, s.interval/2)
	if err != nil {
		slog.ErrorContext(ctx, "scanner: acquiring leader lock", "error", err)
		return
	}
	if !acquired {
		slog.DebugContext(ctx, "scanner: another instance is leader, skipping enqueue")
		return
	}

	metrics.ScannerRuns.Inc()
	timer := prometheus.NewTimer(metrics.ScannerDuration)
	defer timer.ObserveDuration()

	if n, err := s.subRepo.CountConfirmed(ctx); err == nil {
		metrics.ActiveSubscriptions.Set(float64(n))
	}

	repos, err := s.repoRepo.ListWithActiveSubscriptions(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "scanner: listing repos", "error", err)
		return
	}
	slog.InfoContext(ctx, "scanner: enqueueing repositories", "count", len(repos))

	for _, repo := range repos {
		if ctx.Err() != nil {
			return
		}
		if err := s.repoChecks.EnqueueRepo(ctx, repo); err != nil {
			slog.ErrorContext(ctx, "scanner: enqueueing repo", "repo", repo.FullName(), "error", err)
		}
	}
}

func (s *Scanner) checkRepo(ctx context.Context, repo domain.Repository) error {
	release, err := s.github.GetLatestRelease(ctx, repo.Owner, repo.Name)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return s.repoRepo.UpdateCheckedAt(ctx, repo.ID)
		}
		return fmt.Errorf("fetching latest release: %w", err)
	}

	if release.TagName == repo.LastSeenTag {
		return s.repoRepo.UpdateCheckedAt(ctx, repo.ID)
	}

	slog.InfoContext(ctx, "new release detected",
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

	// Update the tag only after enqueue succeeds — at-least-once delivery.
	if err := s.repoRepo.UpdateLastSeenTag(ctx, repo.ID, release.TagName); err != nil {
		return fmt.Errorf("updating last seen tag: %w", err)
	}

	slog.InfoContext(ctx, "notifications enqueued",
		"repo", repo.FullName(),
		"tag", release.TagName,
		"count", len(jobs))
	return nil
}
