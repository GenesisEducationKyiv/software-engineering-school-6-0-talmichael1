package repository

import (
	"context"
	"time"

	"github-release-notifier/internal/domain"
)

// RepositoryRepo manages GitHub repository records in the database.
type RepositoryRepo interface {
	// GetOrCreate finds an existing repository or creates a new one, returning its ID.
	GetOrCreate(ctx context.Context, owner, name string) (*domain.Repository, error)

	// GetByID fetches a repository by its primary key.
	GetByID(ctx context.Context, id int64) (*domain.Repository, error)

	// ListWithActiveSubscriptions returns all repos that have at least one confirmed subscription.
	ListWithActiveSubscriptions(ctx context.Context) ([]domain.Repository, error)

	// UpdateLastSeenTag updates the last seen release tag and checked_at timestamp.
	UpdateLastSeenTag(ctx context.Context, id int64, tag string) error

	// UpdateCheckedAt sets the checked_at timestamp without changing the tag.
	UpdateCheckedAt(ctx context.Context, id int64) error
}

// SubscriptionRepo manages subscription records.
type SubscriptionRepo interface {
	// Create inserts a new subscription. Returns ErrConflict if (email, repo_id) already exists.
	Create(ctx context.Context, sub *domain.Subscription) error

	// GetByConfirmToken looks up a subscription by its confirmation token.
	GetByConfirmToken(ctx context.Context, token string) (*domain.Subscription, error)

	// GetByUnsubscribeToken looks up a subscription by its unsubscribe token.
	GetByUnsubscribeToken(ctx context.Context, token string) (*domain.Subscription, error)

	// Confirm sets confirmed=true for a subscription.
	Confirm(ctx context.Context, id int64) error

	// Delete removes a subscription entirely.
	Delete(ctx context.Context, id int64) error

	// ListByEmail returns all subscriptions for the given email with repository details.
	ListByEmail(ctx context.Context, email string) ([]domain.SubscriptionView, error)

	// ListConfirmedByRepoID returns all confirmed subscriptions for a given repository.
	ListConfirmedByRepoID(ctx context.Context, repoID int64) ([]domain.Subscription, error)

	// DeleteUnconfirmedOlderThan removes unconfirmed subscriptions older than the given age.
	DeleteUnconfirmedOlderThan(ctx context.Context, age time.Duration) (int64, error)

	// CountConfirmed returns the total number of confirmed subscriptions.
	CountConfirmed(ctx context.Context) (int64, error)
}
