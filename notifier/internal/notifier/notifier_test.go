package notifier

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github-release-notifier/notifier/internal/domain"
	"github-release-notifier/notifier/internal/email"
	"github-release-notifier/notifier/internal/queue"
	"github-release-notifier/notifier/internal/urls"
)

// mockQueue implements both JobConsumer and Deduper so a single fake backs the
// notifier in tests, mirroring the two seams it depends on.
type mockQueue struct {
	mu        sync.Mutex
	sent      map[string]bool
	acked     []domain.NotificationJob
	nacked    []domain.NotificationJob
	isSentErr error
	markErr   error
}

func newMockQueue() *mockQueue {
	return &mockQueue{sent: make(map[string]bool)}
}

func (m *mockQueue) Dequeue(context.Context) (*queue.Delivery, error) { return nil, nil }

func (m *mockQueue) Ack(d *queue.Delivery) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.acked = append(m.acked, d.Job)
	return nil
}

func (m *mockQueue) Nack(d *queue.Delivery) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nacked = append(m.nacked, d.Job)
	return nil
}

func (m *mockQueue) Close() error { return nil }

func (m *mockQueue) IsSent(_ context.Context, subscriptionID int64, tag string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.isSentErr != nil {
		return false, m.isSentErr
	}
	return m.sent[fmt.Sprintf("%d:%s", subscriptionID, tag)], nil
}

func (m *mockQueue) MarkSent(_ context.Context, subscriptionID int64, tag string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.markErr != nil {
		return m.markErr
	}
	m.sent[fmt.Sprintf("%d:%s", subscriptionID, tag)] = true
	return nil
}

// deliver wraps a job as it would arrive on its first delivery (count 0).
func deliver(job domain.NotificationJob) *queue.Delivery {
	return &queue.Delivery{Job: job}
}

// redelivered wraps a job that has already failed deliveryCount times.
func redelivered(job domain.NotificationJob, deliveryCount int) *queue.Delivery {
	return &queue.Delivery{Job: job, DeliveryCount: deliveryCount}
}

func sampleJob() domain.NotificationJob {
	return domain.NotificationJob{
		SubscriptionID: 1,
		Email:          "user@example.com",
		Repo:           "golang/go",
		Tag:            "go1.22.0",
		ReleaseURL:     "https://github.com/golang/go/releases/tag/go1.22.0",
		UnsubToken:     "unsub123",
	}
}

func newNotifier(q *mockQueue, sender email.Sender) *Notifier {
	return New(nil, q, sender, urls.Builder{BaseURL: "http://localhost:8080"}, 1)
}

func TestNotifier_ProcessJob_SendsEmail(t *testing.T) {
	var sentTo string
	sender := &releaseEmailMock{sendFn: func(_ context.Context, msg email.Message) error {
		sentTo = msg.To
		return nil
	}}
	q := newMockQueue()
	n := newNotifier(q, sender)

	if err := n.processJob(context.Background(), q, deliver(sampleJob())); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sentTo != "user@example.com" {
		t.Fatalf("expected email sent to user@example.com, got %s", sentTo)
	}
}

func TestNotifier_ProcessJob_AcksAfterSend(t *testing.T) {
	q := newMockQueue()
	n := newNotifier(q, &releaseEmailMock{})

	if err := n.processJob(context.Background(), q, deliver(sampleJob())); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(q.acked) != 1 || q.acked[0].SubscriptionID != 1 {
		t.Fatalf("expected job acked after successful send, acked=%+v", q.acked)
	}
}

func TestNotifier_ProcessJob_AcksDuplicate(t *testing.T) {
	q := newMockQueue()
	q.sent["1:go1.22.0"] = true
	n := newNotifier(q, &releaseEmailMock{})

	if err := n.processJob(context.Background(), q, deliver(sampleJob())); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(q.acked) != 1 {
		t.Fatalf("expected duplicate to be acked, acked=%+v", q.acked)
	}
}

func TestNotifier_ProcessJob_RetryNacksOnFailure(t *testing.T) {
	q := newMockQueue()
	failing := &releaseEmailMock{sendFn: func(_ context.Context, _ email.Message) error {
		return fmt.Errorf("SMTP error")
	}}
	n := newNotifier(q, failing)

	if err := n.processJob(context.Background(), q, deliver(sampleJob())); err != nil {
		t.Fatalf("unexpected error (should nack-requeue, not fail): %v", err)
	}
	if len(q.nacked) != 1 {
		t.Fatalf("expected 1 nacked (requeued) job, got %d", len(q.nacked))
	}
	if len(q.acked) != 0 {
		t.Fatalf("a retryable job must not be acked, acked=%+v", q.acked)
	}
}

func TestNotifier_ProcessJob_AcksAfterMaxRetries(t *testing.T) {
	q := newMockQueue()
	failing := &releaseEmailMock{sendFn: func(_ context.Context, _ email.Message) error {
		return fmt.Errorf("SMTP error")
	}}
	n := newNotifier(q, failing)

	err := n.processJob(context.Background(), q, redelivered(sampleJob(), maxRetries))
	if err == nil {
		t.Fatal("expected error after max retries")
	}
	if len(q.acked) != 1 {
		t.Fatalf("expected dropped job to be acked, acked=%+v", q.acked)
	}
	if len(q.nacked) != 0 {
		t.Fatalf("expected no requeue after max retries, nacked=%+v", q.nacked)
	}
}

func TestNotifier_ProcessJob_NacksWhenDedupCheckFails(t *testing.T) {
	q := newMockQueue()
	q.isSentErr = fmt.Errorf("redis unavailable")
	n := newNotifier(q, &releaseEmailMock{})

	if err := n.processJob(context.Background(), q, deliver(sampleJob())); err == nil {
		t.Fatal("expected error when dedup check fails")
	}
	if len(q.acked) != 0 {
		t.Fatalf("must not ack when outcome is unknown, acked=%+v", q.acked)
	}
	if len(q.nacked) != 1 {
		t.Fatalf("job must be requeued for recovery when dedup is unknown, nacked=%+v", q.nacked)
	}
}

func TestNotifier_ProcessJob_Dedup(t *testing.T) {
	q := newMockQueue()
	q.sent["1:go1.22.0"] = true
	sender := &releaseEmailMock{sendFn: func(_ context.Context, _ email.Message) error {
		t.Fatal("should not send duplicate notification")
		return nil
	}}
	n := newNotifier(q, sender)

	if err := n.processJob(context.Background(), q, deliver(sampleJob())); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNotifier_ProcessJob_MarkSentError(t *testing.T) {
	q := newMockQueue()
	q.markErr = fmt.Errorf("redis unavailable")
	var sent bool
	sender := &releaseEmailMock{sendFn: func(_ context.Context, _ email.Message) error {
		sent = true
		return nil
	}}
	n := newNotifier(q, sender)

	// MarkSent failing must not fail processJob — the email was already
	// delivered, so we log and move on.
	if err := n.processJob(context.Background(), q, deliver(sampleJob())); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !sent {
		t.Fatal("expected email to be sent despite MarkSent failure")
	}
	if len(q.acked) != 1 {
		t.Fatalf("delivered job must still be acked, acked=%+v", q.acked)
	}
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
