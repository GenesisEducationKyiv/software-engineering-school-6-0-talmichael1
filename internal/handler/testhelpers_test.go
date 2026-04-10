package handler

import (
	"context"
	"time"

	"github-release-notifier/internal/domain"
)

// mockSubRepoHandler implements repository.SubscriptionRepo for handler tests.
type mockSubRepoHandler struct {
	createErr       error
	confirmTokenErr error
	unsubTokenErr   error
	listResult      []domain.SubscriptionView
}

func (m *mockSubRepoHandler) Create(_ context.Context, _ *domain.Subscription) error {
	return m.createErr
}
func (m *mockSubRepoHandler) GetByConfirmToken(_ context.Context, _ string) (*domain.Subscription, error) {
	if m.confirmTokenErr != nil {
		return nil, m.confirmTokenErr
	}
	return &domain.Subscription{ID: 1}, nil
}
func (m *mockSubRepoHandler) GetByUnsubscribeToken(_ context.Context, _ string) (*domain.Subscription, error) {
	if m.unsubTokenErr != nil {
		return nil, m.unsubTokenErr
	}
	return &domain.Subscription{ID: 1}, nil
}
func (m *mockSubRepoHandler) Confirm(_ context.Context, _ int64) error { return nil }
func (m *mockSubRepoHandler) Delete(_ context.Context, _ int64) error  { return nil }
func (m *mockSubRepoHandler) ListByEmail(_ context.Context, _ string) ([]domain.SubscriptionView, error) {
	if m.listResult != nil {
		return m.listResult, nil
	}
	return []domain.SubscriptionView{}, nil
}
func (m *mockSubRepoHandler) ListConfirmedByRepoID(_ context.Context, _ int64) ([]domain.Subscription, error) {
	return nil, nil
}
func (m *mockSubRepoHandler) DeleteUnconfirmedOlderThan(_ context.Context, _ time.Duration) (int64, error) {
	return 0, nil
}

// mockRepoRepoHandler implements repository.RepositoryRepo for handler tests.
type mockRepoRepoHandler struct{}

func (m *mockRepoRepoHandler) GetOrCreate(_ context.Context, owner, name string) (*domain.Repository, error) {
	return &domain.Repository{ID: 1, Owner: owner, Name: name}, nil
}
func (m *mockRepoRepoHandler) GetByID(_ context.Context, id int64) (*domain.Repository, error) {
	return &domain.Repository{ID: id}, nil
}
func (m *mockRepoRepoHandler) ListWithActiveSubscriptions(_ context.Context) ([]domain.Repository, error) {
	return nil, nil
}
func (m *mockRepoRepoHandler) UpdateLastSeenTag(_ context.Context, _ int64, _ string) error {
	return nil
}
func (m *mockRepoRepoHandler) UpdateCheckedAt(_ context.Context, _ int64) error { return nil }

// mockGitHubHandler implements service.GitHubChecker for handler tests.
type mockGitHubHandler struct {
	repoErr error
}

func (m *mockGitHubHandler) RepoExists(_ context.Context, _, _ string) error {
	return m.repoErr
}
func (m *mockGitHubHandler) GetLatestRelease(_ context.Context, _, _ string) (*domain.Release, error) {
	if m.repoErr != nil {
		return nil, m.repoErr
	}
	return &domain.Release{TagName: "v1.0.0"}, nil
}

// mockEmailHandler implements service.EmailSender for handler tests.
type mockEmailHandler struct{}

func (m *mockEmailHandler) SendConfirmation(_ context.Context, _, _, _ string) error { return nil }
func (m *mockEmailHandler) SendReleaseNotification(_ context.Context, _, _, _, _, _ string) error {
	return nil
}
