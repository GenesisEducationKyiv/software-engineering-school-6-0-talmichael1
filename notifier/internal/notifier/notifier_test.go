package notifier

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github-release-notifier/notifier/internal/domain"
	"github-release-notifier/notifier/internal/email"
	"github-release-notifier/notifier/internal/urls"
)

type mockJobQueue struct {
	mu            sync.Mutex
	jobs          []*domain.NotificationJob
	sent          map[string]bool
	requeued      []domain.NotificationJob
	acked         []domain.NotificationJob
	reclaimCalled int
	isSentErr     error
}

func newMockJobQueue(jobs ...*domain.NotificationJob) *mockJobQueue {
	return &mockJobQueue{
		jobs: jobs,
		sent: make(map[string]bool),
	}
}

func (m *mockJobQueue) Dequeue(ctx context.Context, timeout time.Duration) (*domain.NotificationJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.jobs) == 0 {
		return nil, nil
	}
	job := m.jobs[0]
	m.jobs = m.jobs[1:]
	return job, nil
}

func (m *mockJobQueue) Ack(ctx context.Context, job domain.NotificationJob) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.acked = append(m.acked, job)
	return nil
}

func (m *mockJobQueue) Reclaim(ctx context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reclaimCalled++
	return 0, nil
}

func (m *mockJobQueue) IsSent(ctx context.Context, subscriptionID int64, tag string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.isSentErr != nil {
		return false, m.isSentErr
	}
	key := fmt.Sprintf("%d:%s", subscriptionID, tag)
	return m.sent[key], nil
}

func (m *mockJobQueue) MarkSent(ctx context.Context, subscriptionID int64, tag string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%d:%s", subscriptionID, tag)
	m.sent[key] = true
	return nil
}

func (m *mockJobQueue) Requeue(ctx context.Context, job domain.NotificationJob) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	job.Attempt++
	m.requeued = append(m.requeued, job)
	return nil
}

func TestNotifier_ProcessJob_SendsEmail(t *testing.T) {
	var sentTo string
	releaseSender := &releaseEmailMock{
		sendFn: func(ctx context.Context, msg email.Message) error {
			sentTo = msg.To
			return nil
		},
	}

	q := newMockJobQueue()
	n := New(q, releaseSender, urls.Builder{BaseURL: "http://localhost:8080"}, 1)

	job := &domain.NotificationJob{
		SubscriptionID: 1,
		Email:          "user@example.com",
		Repo:           "golang/go",
		Tag:            "go1.22.0",
		ReleaseURL:     "https://github.com/golang/go/releases/tag/go1.22.0",
		UnsubToken:     "unsub123",
	}

	err := n.processJob(context.Background(), job)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sentTo != "user@example.com" {
		t.Fatalf("expected email sent to user@example.com, got %s", sentTo)
	}
}

func TestNotifier_ProcessJob_AcksAfterSend(t *testing.T) {
	q := newMockJobQueue()
	n := New(q, &releaseEmailMock{}, urls.Builder{BaseURL: "http://localhost:8080"}, 1)

	job := &domain.NotificationJob{SubscriptionID: 1, Email: "user@example.com", Repo: "golang/go", Tag: "go1.22.0", UnsubToken: "t"}
	if err := n.processJob(context.Background(), job); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(q.acked) != 1 || q.acked[0].SubscriptionID != 1 {
		t.Fatalf("expected job acked after successful send, acked=%+v", q.acked)
	}
}

func TestNotifier_ProcessJob_AcksDuplicate(t *testing.T) {
	q := newMockJobQueue()
	q.sent["1:go1.22.0"] = true
	n := New(q, &releaseEmailMock{}, urls.Builder{BaseURL: "http://localhost:8080"}, 1)

	job := &domain.NotificationJob{SubscriptionID: 1, Email: "user@example.com", Repo: "golang/go", Tag: "go1.22.0", UnsubToken: "t"}
	if err := n.processJob(context.Background(), job); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(q.acked) != 1 {
		t.Fatalf("expected duplicate to be acked (removed from processing), acked=%+v", q.acked)
	}
}

func TestNotifier_ProcessJob_AcksAfterMaxRetries(t *testing.T) {
	q := newMockJobQueue()
	failing := &releaseEmailMock{sendFn: func(ctx context.Context, msg email.Message) error {
		return fmt.Errorf("SMTP error")
	}}
	n := New(q, failing, urls.Builder{BaseURL: "http://localhost:8080"}, 1)

	job := &domain.NotificationJob{SubscriptionID: 1, Email: "user@example.com", Repo: "golang/go", Tag: "go1.22.0", UnsubToken: "t", Attempt: maxRetries}
	if err := n.processJob(context.Background(), job); err == nil {
		t.Fatal("expected error after max retries")
	}

	if len(q.acked) != 1 {
		t.Fatalf("expected dropped job to be acked, acked=%+v", q.acked)
	}
	if len(q.requeued) != 0 {
		t.Fatalf("expected no requeue after max retries, got %d", len(q.requeued))
	}
}

func TestNotifier_ProcessJob_NoAckWhenDedupCheckFails(t *testing.T) {
	q := newMockJobQueue()
	q.isSentErr = fmt.Errorf("redis unavailable")
	n := New(q, &releaseEmailMock{}, urls.Builder{BaseURL: "http://localhost:8080"}, 1)

	job := &domain.NotificationJob{SubscriptionID: 1, Email: "user@example.com", Repo: "golang/go", Tag: "go1.22.0", UnsubToken: "t"}
	if err := n.processJob(context.Background(), job); err == nil {
		t.Fatal("expected error when dedup check fails")
	}

	if len(q.acked) != 0 {
		t.Fatalf("must not ack when outcome is unknown — job stays for recovery, acked=%+v", q.acked)
	}
}

func TestNotifier_Run_ReclaimsOnStartup(t *testing.T) {
	q := newMockJobQueue()
	n := New(q, &releaseEmailMock{}, urls.Builder{BaseURL: "http://localhost:8080"}, 2)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	n.Run(ctx)

	if q.reclaimCalled < 1 {
		t.Fatalf("expected reaper to reclaim at least once on startup, got %d", q.reclaimCalled)
	}
}

func TestNotifier_ProcessJob_Dedup(t *testing.T) {
	q := newMockJobQueue()
	// Pre-mark as sent.
	q.sent["1:go1.22.0"] = true

	releaseSender := &releaseEmailMock{
		sendFn: func(ctx context.Context, msg email.Message) error {
			t.Fatal("should not send duplicate notification")
			return nil
		},
	}

	n := New(q, releaseSender, urls.Builder{BaseURL: "http://localhost:8080"}, 1)

	job := &domain.NotificationJob{
		SubscriptionID: 1,
		Email:          "user@example.com",
		Repo:           "golang/go",
		Tag:            "go1.22.0",
		UnsubToken:     "unsub123",
	}

	err := n.processJob(context.Background(), job)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNotifier_ProcessJob_RetryOnFailure(t *testing.T) {
	q := newMockJobQueue()
	releaseSender := &releaseEmailMock{
		sendFn: func(ctx context.Context, msg email.Message) error {
			return fmt.Errorf("SMTP error")
		},
	}

	n := New(q, releaseSender, urls.Builder{BaseURL: "http://localhost:8080"}, 1)

	job := &domain.NotificationJob{
		SubscriptionID: 1,
		Email:          "user@example.com",
		Repo:           "golang/go",
		Tag:            "go1.22.0",
		UnsubToken:     "unsub123",
		Attempt:        0,
	}

	err := n.processJob(context.Background(), job)
	if err != nil {
		t.Fatalf("unexpected error (should requeue, not fail): %v", err)
	}
	if len(q.requeued) != 1 {
		t.Fatalf("expected 1 requeued job, got %d", len(q.requeued))
	}
	if q.requeued[0].Attempt < 1 {
		t.Fatalf("expected attempt > 0, got %d", q.requeued[0].Attempt)
	}
}

func TestNotifier_ProcessJob_MaxRetries(t *testing.T) {
	q := newMockJobQueue()
	releaseSender := &releaseEmailMock{
		sendFn: func(ctx context.Context, msg email.Message) error {
			return fmt.Errorf("SMTP error")
		},
	}

	n := New(q, releaseSender, urls.Builder{BaseURL: "http://localhost:8080"}, 1)

	job := &domain.NotificationJob{
		SubscriptionID: 1,
		Email:          "user@example.com",
		Repo:           "golang/go",
		Tag:            "go1.22.0",
		UnsubToken:     "unsub123",
		Attempt:        maxRetries,
	}

	err := n.processJob(context.Background(), job)
	if err == nil {
		t.Fatal("expected error after max retries")
	}
	if len(q.requeued) != 0 {
		t.Fatalf("expected no requeue after max retries, got %d", len(q.requeued))
	}
}

func TestNotifier_ProcessJob_MarkSentError(t *testing.T) {
	q := &errMarkSentQueue{
		markErr: fmt.Errorf("redis unavailable"),
	}

	var sent bool
	releaseSender := &releaseEmailMock{
		sendFn: func(ctx context.Context, msg email.Message) error {
			sent = true
			return nil
		},
	}

	n := New(q, releaseSender, urls.Builder{BaseURL: "http://localhost:8080"}, 1)

	job := &domain.NotificationJob{
		SubscriptionID: 1,
		Email:          "user@example.com",
		Repo:           "golang/go",
		Tag:            "go1.22.0",
		UnsubToken:     "unsub123",
	}

	// MarkSent failing should not cause processJob to return an error —
	// the email was already delivered, so we log and move on.
	err := n.processJob(context.Background(), job)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !sent {
		t.Fatal("expected email to be sent despite MarkSent failure")
	}
}

func TestNotifier_ProcessJob_DedupError(t *testing.T) {
	releaseSender := &releaseEmailMock{
		sendFn: func(ctx context.Context, msg email.Message) error {
			t.Fatal("should not send when dedup check fails")
			return nil
		},
	}

	n := New(&errIsSentQueue{err: fmt.Errorf("redis unavailable")}, releaseSender, urls.Builder{BaseURL: "http://localhost:8080"}, 1)

	job := &domain.NotificationJob{
		SubscriptionID: 1,
		Email:          "user@example.com",
		Repo:           "golang/go",
		Tag:            "go1.22.0",
		UnsubToken:     "unsub123",
	}

	err := n.processJob(context.Background(), job)
	if err == nil {
		t.Fatal("expected error when IsSent fails")
	}
}

// errIsSentQueue is a mock queue that always fails on IsSent.
type errIsSentQueue struct {
	err error
}

func (m *errIsSentQueue) Dequeue(ctx context.Context, timeout time.Duration) (*domain.NotificationJob, error) {
	return nil, nil
}
func (m *errIsSentQueue) Ack(ctx context.Context, job domain.NotificationJob) error { return nil }
func (m *errIsSentQueue) Reclaim(ctx context.Context) (int, error)                  { return 0, nil }
func (m *errIsSentQueue) IsSent(ctx context.Context, subscriptionID int64, tag string) (bool, error) {
	return false, m.err
}
func (m *errIsSentQueue) MarkSent(ctx context.Context, subscriptionID int64, tag string) error {
	return nil
}
func (m *errIsSentQueue) Requeue(ctx context.Context, job domain.NotificationJob) error {
	return nil
}

// errMarkSentQueue is a mock queue where IsSent works but MarkSent fails.
type errMarkSentQueue struct {
	markErr error
}

func (m *errMarkSentQueue) Dequeue(ctx context.Context, timeout time.Duration) (*domain.NotificationJob, error) {
	return nil, nil
}
func (m *errMarkSentQueue) Ack(ctx context.Context, job domain.NotificationJob) error { return nil }
func (m *errMarkSentQueue) Reclaim(ctx context.Context) (int, error)                  { return 0, nil }
func (m *errMarkSentQueue) IsSent(ctx context.Context, subscriptionID int64, tag string) (bool, error) {
	return false, nil
}
func (m *errMarkSentQueue) MarkSent(ctx context.Context, subscriptionID int64, tag string) error {
	return m.markErr
}
func (m *errMarkSentQueue) Requeue(ctx context.Context, job domain.NotificationJob) error {
	return nil
}

// releaseEmailMock implements email.Sender for notification-specific tests.
type releaseEmailMock struct {
	sendFn func(ctx context.Context, msg email.Message) error
}

func (m *releaseEmailMock) Send(ctx context.Context, msg email.Message) error {
	if m.sendFn != nil {
		return m.sendFn(ctx, msg)
	}
	return nil
}
