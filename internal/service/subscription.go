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
	"github-release-notifier/internal/metrics"
	"github-release-notifier/internal/repository"
)

// GitHubChecker verifies repository existence and fetches releases.
type GitHubChecker interface {
	RepoExists(ctx context.Context, owner, repo string) error
	GetLatestRelease(ctx context.Context, owner, repo string) (*domain.Release, error)
}

// EmailSender sends transactional emails.
type EmailSender interface {
	SendConfirmation(ctx context.Context, to, repo, confirmURL string) error
	SendReleaseNotification(ctx context.Context, to, repo, tag, releaseURL, unsubURL string) error
}

// SubscriptionService implements the business logic for managing subscriptions.
type SubscriptionService struct {
	subRepo  repository.SubscriptionRepo
	repoRepo repository.RepositoryRepo
	github   GitHubChecker
	email    EmailSender
	baseURL  string
}

func NewSubscriptionService(
	subRepo repository.SubscriptionRepo,
	repoRepo repository.RepositoryRepo,
	github GitHubChecker,
	email EmailSender,
	baseURL string,
) *SubscriptionService {
	return &SubscriptionService{
		subRepo:  subRepo,
		repoRepo: repoRepo,
		github:   github,
		email:    email,
		baseURL:  baseURL,
	}
}

// Subscribe creates a new subscription after validating the email and repository.
func (s *SubscriptionService) Subscribe(ctx context.Context, emailAddr, repoFullName string) error {
	if err := validateEmail(emailAddr); err != nil {
		return fmt.Errorf("%w: %s", domain.ErrInvalidInput, err.Error())
	}

	owner, name, err := parseRepoName(repoFullName)
	if err != nil {
		return fmt.Errorf("%w: %s", domain.ErrInvalidInput, err.Error())
	}

	// Verify the repo exists and seed the latest release tag in one call.
	// If the repo has no releases, we fall back to RepoExists to confirm it's valid.
	var initialTag string
	release, err := s.github.GetLatestRelease(ctx, owner, name)
	if err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			if errors.Is(err, domain.ErrRateLimited) {
				return err
			}
			return fmt.Errorf("checking repository: %w", err)
		}
		// No releases — verify the repo itself exists.
		if err := s.github.RepoExists(ctx, owner, name); err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				return domain.ErrNotFound
			}
			return fmt.Errorf("checking repository: %w", err)
		}
	} else {
		initialTag = release.TagName
	}

	repo, err := s.repoRepo.GetOrCreate(ctx, owner, name)
	if err != nil {
		return fmt.Errorf("upserting repository: %w", err)
	}

	if repo.LastSeenTag == "" && initialTag != "" {
		_ = s.repoRepo.UpdateLastSeenTag(ctx, repo.ID, initialTag)
	}

	confirmToken, err := generateToken()
	if err != nil {
		return fmt.Errorf("generating confirm token: %w", err)
	}
	unsubToken, err := generateToken()
	if err != nil {
		return fmt.Errorf("generating unsubscribe token: %w", err)
	}

	sub := &domain.Subscription{
		Email:            emailAddr,
		RepositoryID:     repo.ID,
		Confirmed:        false,
		ConfirmToken:     confirmToken,
		UnsubscribeToken: unsubToken,
	}
	if err := s.subRepo.Create(ctx, sub); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return domain.ErrConflict
		}
		return fmt.Errorf("creating subscription: %w", err)
	}

	confirmURL := fmt.Sprintf("%s/api/confirm/%s", s.baseURL, confirmToken)
	if err := s.email.SendConfirmation(ctx, emailAddr, repoFullName, confirmURL); err != nil {
		// Roll back the subscription so the user can retry.
		_ = s.subRepo.Delete(ctx, sub.ID) //nolint:errcheck // best-effort rollback
		return fmt.Errorf("sending confirmation email: %w", err)
	}
	metrics.ConfirmationEmailsSent.Inc()
	return nil
}

// Confirm activates a subscription using the provided confirmation token.
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

// Unsubscribe removes a subscription using the provided unsubscribe token.
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

// ListByEmail returns all subscriptions for the given email address.
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
