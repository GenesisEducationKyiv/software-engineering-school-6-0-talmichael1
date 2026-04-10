package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"

	"github-release-notifier/internal/domain"
)

// RepositoryStore implements repository.RepositoryRepo backed by PostgreSQL.
type RepositoryStore struct {
	db *sqlx.DB
}

func NewRepositoryStore(db *sqlx.DB) *RepositoryStore {
	return &RepositoryStore{db: db}
}

func (s *RepositoryStore) GetOrCreate(ctx context.Context, owner, name string) (*domain.Repository, error) {
	repo := &domain.Repository{}

	err := s.db.GetContext(ctx, repo,
		`SELECT id, owner, name, last_seen_tag, checked_at, created_at
		 FROM repositories WHERE owner = $1 AND name = $2`, owner, name)

	if err == nil {
		return repo, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	err = s.db.QueryRowxContext(ctx,
		`INSERT INTO repositories (owner, name) VALUES ($1, $2)
		 ON CONFLICT (owner, name) DO UPDATE SET owner = EXCLUDED.owner
		 RETURNING id, owner, name, last_seen_tag, checked_at, created_at`,
		owner, name).StructScan(repo)
	if err != nil {
		return nil, err
	}
	return repo, nil
}

func (s *RepositoryStore) GetByID(ctx context.Context, id int64) (*domain.Repository, error) {
	repo := &domain.Repository{}
	err := s.db.GetContext(ctx, repo,
		`SELECT id, owner, name, last_seen_tag, checked_at, created_at
		 FROM repositories WHERE id = $1`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return repo, err
}

func (s *RepositoryStore) ListWithActiveSubscriptions(ctx context.Context) ([]domain.Repository, error) {
	var repos []domain.Repository
	err := s.db.SelectContext(ctx, &repos,
		`SELECT DISTINCT r.id, r.owner, r.name, r.last_seen_tag, r.checked_at, r.created_at
		 FROM repositories r
		 INNER JOIN subscriptions s ON s.repository_id = r.id
		 WHERE s.confirmed = TRUE`)
	return repos, err
}

func (s *RepositoryStore) UpdateLastSeenTag(ctx context.Context, id int64, tag string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`UPDATE repositories SET last_seen_tag = $1, checked_at = $2 WHERE id = $3`,
		tag, now, id)
	return err
}

func (s *RepositoryStore) UpdateCheckedAt(ctx context.Context, id int64) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`UPDATE repositories SET checked_at = $1 WHERE id = $2`, now, id)
	return err
}
