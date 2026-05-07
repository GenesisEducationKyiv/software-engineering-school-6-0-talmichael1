package repository

import (
	"context"
	"time"

	"github-release-notifier/internal/domain"
)

type RepositoryRepo interface {
	GetOrCreate(ctx context.Context, owner, name string) (*domain.Repository, error)
	GetByID(ctx context.Context, id int64) (*domain.Repository, error)

	// ListWithActiveSubscriptions returns repos that have at least one confirmed subscription.
	ListWithActiveSubscriptions(ctx context.Context) ([]domain.Repository, error)

	// UpdateLastSeenTag also bumps checked_at.
	UpdateLastSeenTag(ctx context.Context, id int64, tag string) error

	// UpdateCheckedAt bumps the timestamp without touching the tag.
	UpdateCheckedAt(ctx context.Context, id int64) error
}

type SubscriptionRepo interface {
	// Create returns ErrConflict if (email, repo_id) already exists.
	Create(ctx context.Context, sub *domain.Subscription) error

	GetByConfirmToken(ctx context.Context, token string) (*domain.Subscription, error)
	GetByUnsubscribeToken(ctx context.Context, token string) (*domain.Subscription, error)
	Confirm(ctx context.Context, id int64) error
	Delete(ctx context.Context, id int64) error

	// ListByEmail joins subscription and repository data into a SubscriptionView.
	ListByEmail(ctx context.Context, email string) ([]domain.SubscriptionView, error)

	ListConfirmedByRepoID(ctx context.Context, repoID int64) ([]domain.Subscription, error)
	DeleteUnconfirmedOlderThan(ctx context.Context, age time.Duration) (int64, error)
	CountConfirmed(ctx context.Context) (int64, error)
}
