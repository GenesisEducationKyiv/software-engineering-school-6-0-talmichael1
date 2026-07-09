package postgres

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"

	"github-release-notifier/internal/domain"
)

const sagaColumns = `id, state, email, repo_full_name, confirm_token, subscription_id, attempts, last_error, created_at, updated_at`

type SagaStore struct {
	db *sqlx.DB
}

func NewSagaStore(db *sqlx.DB) *SagaStore {
	return &SagaStore{db: db}
}

// Create inserts a fresh saga in the pending state and fills in the generated id
// and timestamps. Written before step 1 so a record survives a crash mid-flight.
func (s *SagaStore) Create(ctx context.Context, saga *domain.SubscriptionSaga) error {
	return s.db.QueryRowContext(ctx,
		`INSERT INTO subscription_sagas (email, repo_full_name, confirm_token)
		 VALUES ($1, $2, $3)
		 RETURNING id, state, attempts, created_at, updated_at`,
		saga.Email, saga.RepoFullName, saga.ConfirmToken).
		Scan(&saga.ID, &saga.State, &saga.Attempts, &saga.CreatedAt, &saga.UpdatedAt)
}

// CreateSubscription runs step 1 atomically: it inserts the subscription and
// records its id on the saga in one transaction, so the saga log and the
// subscription are never out of sync. Returns domain.ErrConflict on a duplicate
// (email, repo) subscription, or domain.ErrNotFound if the saga id no longer exists.
func (s *SagaStore) CreateSubscription(ctx context.Context, sagaID string, sub *domain.Subscription) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err := insertSubscription(ctx, tx, sub); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE subscription_sagas SET subscription_id = $1, updated_at = NOW() WHERE id = $2`,
		sub.ID, sagaID)
	if err != nil {
		return err
	}
	attached, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if attached == 0 {
		return domain.ErrNotFound
	}
	return tx.Commit()
}

func (s *SagaStore) MarkCompleted(ctx context.Context, id string) error {
	return s.setState(ctx, id, domain.SagaCompleted, nil)
}

func (s *SagaStore) MarkCompensated(ctx context.Context, id, reason string) error {
	return s.setState(ctx, id, domain.SagaCompensated, &reason)
}

func (s *SagaStore) MarkFailed(ctx context.Context, id, reason string) error {
	return s.setState(ctx, id, domain.SagaFailed, &reason)
}

func (s *SagaStore) setState(ctx context.Context, id string, state domain.SagaState, reason *string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE subscription_sagas SET state = $1, last_error = $2, updated_at = NOW() WHERE id = $3`,
		state, reason, id)
	return err
}

// IncrementAttempts bumps the step-2 retry counter and returns the new value, so
// the reaper can decide retry-vs-give-up off a single round-trip.
func (s *SagaStore) IncrementAttempts(ctx context.Context, id string) (int, error) {
	var attempts int
	err := s.db.QueryRowContext(ctx,
		`UPDATE subscription_sagas SET attempts = attempts + 1, updated_at = NOW() WHERE id = $1 RETURNING attempts`,
		id).Scan(&attempts)
	return attempts, err
}

// ListStalePending returns pending sagas untouched for longer than olderThan —
// the reaper's input for forward-recovery (ADR-0007).
func (s *SagaStore) ListStalePending(ctx context.Context, olderThan time.Duration, limit int) ([]domain.SubscriptionSaga, error) {
	cutoff := time.Now().UTC().Add(-olderThan)
	var sagas []domain.SubscriptionSaga
	err := s.db.SelectContext(ctx, &sagas,
		`SELECT `+sagaColumns+`
		 FROM subscription_sagas
		 WHERE state = 'pending' AND updated_at < $1
		 ORDER BY updated_at ASC
		 LIMIT $2`, cutoff, limit)
	return sagas, err
}

func (s *SagaStore) DeleteTerminalOlderThan(ctx context.Context, age time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-age)
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM subscription_sagas
		 WHERE state IN ('completed', 'compensated', 'failed') AND updated_at < $1`, cutoff)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
