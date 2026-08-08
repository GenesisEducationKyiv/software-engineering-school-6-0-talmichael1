package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"strings"

	"github-release-notifier/internal/domain"
	"github-release-notifier/internal/saga"
	"github-release-notifier/internal/urls"
)

type GitHubChecker interface {
	RepoExists(ctx context.Context, owner, repo string) error
	GetLatestRelease(ctx context.Context, owner, repo string) (*domain.Release, error)
}

type subscriptionRepo interface {
	GetByConfirmToken(ctx context.Context, token string) (*domain.Subscription, error)
	GetByUnsubscribeToken(ctx context.Context, token string) (*domain.Subscription, error)
	Confirm(ctx context.Context, id int64) error
	Delete(ctx context.Context, id int64) error
	ListByEmail(ctx context.Context, email string) ([]domain.SubscriptionView, error)
}

type repoUpserter interface {
	GetOrCreate(ctx context.Context, owner, name string) (*domain.Repository, error)
	UpdateLastSeenTag(ctx context.Context, id int64, tag string) error
}

// sagaStore persists the subscribe saga's state. CreateSubscription runs step 1
// atomically (insert subscription + attach it to the saga).
type sagaStore interface {
	Create(ctx context.Context, saga *domain.SubscriptionSaga) error
	CreateSubscription(ctx context.Context, sagaID string, sub *domain.Subscription) error
	MarkCompleted(ctx context.Context, id string) error
	MarkCompensated(ctx context.Context, id, reason string) error
}

// ConfirmationSender is the remote step-2 participant: it asks the Notifier to
// send the confirmation email. Satisfied by the REST client today, gRPC in HW10.
type ConfirmationSender interface {
	Send(ctx context.Context, to, repo, confirmURL string) error
}

type SubscriptionService struct {
	subRepo  subscriptionRepo
	repoRepo repoUpserter
	github   GitHubChecker
	sagas    sagaStore
	confirm  ConfirmationSender
	urls     urls.Builder
}

func NewSubscriptionService(
	subs subscriptionRepo,
	repos repoUpserter,
	github GitHubChecker,
	sagas sagaStore,
	confirm ConfirmationSender,
	urlBuilder urls.Builder,
) *SubscriptionService {
	return &SubscriptionService{
		subRepo:  subs,
		repoRepo: repos,
		github:   github,
		sagas:    sagas,
		confirm:  confirm,
		urls:     urlBuilder,
	}
}

// Subscribe runs the orchestrated saga: create the subscription (step 1) and ask
// the Notifier to send the confirmation email (step 2). A step-2 failure
// compensates step 1 by deleting the subscription. State lives in the saga log
// so an interrupted run is recoverable by the reaper (ADR-0007).
func (s *SubscriptionService) Subscribe(ctx context.Context, emailAddr, repoFullName string) error {
	if err := validateEmail(emailAddr); err != nil {
		return fmt.Errorf("%w: %s", domain.ErrInvalidInput, err.Error())
	}
	owner, name, err := parseRepoName(repoFullName)
	if err != nil {
		return fmt.Errorf("%w: %s", domain.ErrInvalidInput, err.Error())
	}

	repo, err := s.resolveRepo(ctx, owner, name)
	if err != nil {
		return err
	}

	sub, err := newPendingSubscription(emailAddr, repo.ID)
	if err != nil {
		return err
	}

	record := &domain.SubscriptionSaga{
		Email:        emailAddr,
		RepoFullName: repoFullName,
		ConfirmToken: sub.ConfirmToken,
	}
	if err := s.sagas.Create(ctx, record); err != nil {
		return fmt.Errorf("starting subscribe saga: %w", err)
	}

	confirmURL := s.urls.Confirm(sub.ConfirmToken)
	flow := saga.New(
		saga.Step{
			Name:       "create-subscription",
			Action:     func(ctx context.Context) error { return s.sagas.CreateSubscription(ctx, record.ID, sub) },
			Compensate: func(ctx context.Context) error { return s.subRepo.Delete(ctx, sub.ID) },
		},
		saga.Step{
			Name:   "send-confirmation",
			Action: func(ctx context.Context) error { return s.confirm.Send(ctx, emailAddr, repoFullName, confirmURL) },
		},
	)

	if err := flow.Run(ctx); err != nil {
		_ = s.sagas.MarkCompensated(ctx, record.ID, err.Error()) //nolint:errcheck // best-effort log update
		if errors.Is(err, domain.ErrConflict) {
			return domain.ErrConflict
		}
		return err
	}
	_ = s.sagas.MarkCompleted(ctx, record.ID) //nolint:errcheck // best-effort log update
	return nil
}

// resolveRepo verifies the repo exists on GitHub, upserts the local row, and
// seeds last_seen_tag from the current latest release on first sight.
func (s *SubscriptionService) resolveRepo(ctx context.Context, owner, name string) (*domain.Repository, error) {
	// Try the latest release first to seed last_seen_tag in one call;
	// fall back to RepoExists when the repo has no releases yet.
	var initialTag string
	release, err := s.github.GetLatestRelease(ctx, owner, name)
	switch {
	case err == nil:
		initialTag = release.TagName
	case errors.Is(err, domain.ErrNotFound):
		if err := s.github.RepoExists(ctx, owner, name); err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				return nil, domain.ErrNotFound
			}
			return nil, fmt.Errorf("checking repository: %w", err)
		}
	case errors.Is(err, domain.ErrRateLimited):
		return nil, err
	default:
		return nil, fmt.Errorf("checking repository: %w", err)
	}

	repo, err := s.repoRepo.GetOrCreate(ctx, owner, name)
	if err != nil {
		return nil, fmt.Errorf("upserting repository: %w", err)
	}
	if repo.LastSeenTag == "" && initialTag != "" {
		_ = s.repoRepo.UpdateLastSeenTag(ctx, repo.ID, initialTag) //nolint:errcheck // tag seed is best-effort
	}
	return repo, nil
}

func newPendingSubscription(emailAddr string, repoID int64) (*domain.Subscription, error) {
	confirmToken, err := generateToken()
	if err != nil {
		return nil, fmt.Errorf("generating confirm token: %w", err)
	}
	unsubToken, err := generateToken()
	if err != nil {
		return nil, fmt.Errorf("generating unsubscribe token: %w", err)
	}
	return &domain.Subscription{
		Email:            emailAddr,
		RepositoryID:     repoID,
		Confirmed:        false,
		ConfirmToken:     confirmToken,
		UnsubscribeToken: unsubToken,
	}, nil
}

func (s *SubscriptionService) Confirm(ctx context.Context, token string) error {
	if token == "" {
		return fmt.Errorf("%w: empty token", domain.ErrInvalidInput)
	}

	sub, err := s.subRepo.GetByConfirmToken(ctx, token)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.ErrNotFound
		}
		return fmt.Errorf("looking up confirm token: %w", err)
	}

	if err := s.subRepo.Confirm(ctx, sub.ID); err != nil {
		return fmt.Errorf("confirming subscription: %w", err)
	}
	return nil
}

func (s *SubscriptionService) Unsubscribe(ctx context.Context, token string) error {
	if token == "" {
		return fmt.Errorf("%w: empty token", domain.ErrInvalidInput)
	}

	sub, err := s.subRepo.GetByUnsubscribeToken(ctx, token)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.ErrNotFound
		}
		return fmt.Errorf("looking up unsubscribe token: %w", err)
	}

	if err := s.subRepo.Delete(ctx, sub.ID); err != nil {
		return fmt.Errorf("deleting subscription: %w", err)
	}
	return nil
}

func (s *SubscriptionService) ListByEmail(ctx context.Context, emailAddr string) ([]domain.SubscriptionView, error) {
	if err := validateEmail(emailAddr); err != nil {
		return nil, fmt.Errorf("%w: %s", domain.ErrInvalidInput, err.Error())
	}

	views, err := s.subRepo.ListByEmail(ctx, emailAddr)
	if err != nil {
		return nil, fmt.Errorf("listing subscriptions: %w", err)
	}
	return views, nil
}

func validateEmail(addr string) error {
	if addr == "" {
		return fmt.Errorf("email is required")
	}
	if _, err := mail.ParseAddress(addr); err != nil {
		return fmt.Errorf("invalid email format")
	}
	return nil
}

func parseRepoName(fullName string) (owner, name string, err error) {
	if fullName == "" {
		return "", "", fmt.Errorf("repo is required")
	}
	parts := strings.SplitN(fullName, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("repo must be in owner/repo format")
	}
	if strings.Contains(parts[1], "/") {
		return "", "", fmt.Errorf("repo must be in owner/repo format")
	}
	return parts[0], parts[1], nil
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
