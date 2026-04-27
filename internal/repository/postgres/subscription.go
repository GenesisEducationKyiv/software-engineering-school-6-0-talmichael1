package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"

	"github-release-notifier/internal/domain"
)

// SubscriptionStore implements repository.SubscriptionRepo backed by PostgreSQL.
type SubscriptionStore struct {
	db *sqlx.DB
}

func NewSubscriptionStore(db *sqlx.DB) *SubscriptionStore {
	return &SubscriptionStore{db: db}
}

func (s *SubscriptionStore) Create(ctx context.Context, sub *domain.Subscription) error {
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO subscriptions (email, repository_id, confirmed, confirm_token, unsubscribe_token)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		sub.Email, sub.RepositoryID, sub.Confirmed, sub.ConfirmToken, sub.UnsubscribeToken).Scan(&sub.ID)
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
			if strings.Contains(pqErr.Constraint, "email") {
				return domain.ErrConflict
			}
		}
		return err
	}
	return nil
}

func (s *SubscriptionStore) GetByConfirmToken(ctx context.Context, token string) (*domain.Subscription, error) {
	sub := &domain.Subscription{}
	err := s.db.GetContext(ctx, sub,
		`SELECT id, email, repository_id, confirmed, confirm_token, unsubscribe_token, created_at
		 FROM subscriptions WHERE confirm_token = $1`, token)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return sub, err
}

func (s *SubscriptionStore) GetByUnsubscribeToken(ctx context.Context, token string) (*domain.Subscription, error) {
	sub := &domain.Subscription{}
	err := s.db.GetContext(ctx, sub,
		`SELECT id, email, repository_id, confirmed, confirm_token, unsubscribe_token, created_at
		 FROM subscriptions WHERE unsubscribe_token = $1`, token)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return sub, err
}

func (s *SubscriptionStore) Confirm(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE subscriptions SET confirmed = TRUE WHERE id = $1`, id)
	return err
}

func (s *SubscriptionStore) Delete(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM subscriptions WHERE id = $1`, id)
	return err
}

func (s *SubscriptionStore) ListByEmail(ctx context.Context, email string) ([]domain.SubscriptionView, error) {
	var views []domain.SubscriptionView
	err := s.db.SelectContext(ctx, &views,
		`SELECT s.email,
		        r.owner || '/' || r.name AS repo,
		        s.confirmed,
		        r.last_seen_tag
		 FROM subscriptions s
		 INNER JOIN repositories r ON r.id = s.repository_id
		 WHERE s.email = $1`, email)
	return views, err
}

func (s *SubscriptionStore) ListConfirmedByRepoID(ctx context.Context, repoID int64) ([]domain.Subscription, error) {
	var subs []domain.Subscription
	err := s.db.SelectContext(ctx, &subs,
		`SELECT id, email, repository_id, confirmed, confirm_token, unsubscribe_token, created_at
		 FROM subscriptions
		 WHERE repository_id = $1 AND confirmed = TRUE`, repoID)
	return subs, err
}

func (s *SubscriptionStore) CountConfirmed(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.GetContext(ctx, &n,
		`SELECT COUNT(*) FROM subscriptions WHERE confirmed = TRUE`)
	return n, err
}

func (s *SubscriptionStore) DeleteUnconfirmedOlderThan(ctx context.Context, age time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-age)
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM subscriptions WHERE confirmed = FALSE AND created_at < $1`, cutoff)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
