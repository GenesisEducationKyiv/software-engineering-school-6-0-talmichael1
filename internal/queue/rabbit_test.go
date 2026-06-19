package queue

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"

	"github-release-notifier/internal/domain"
)

type fakePublisher struct {
	published []amqp.Publishing
	keys      []string
	err       error
}

func (f *fakePublisher) PublishWithContext(_ context.Context, _, key string, _, _ bool, msg amqp.Publishing) error {
	if f.err != nil {
		return f.err
	}
	f.keys = append(f.keys, key)
	f.published = append(f.published, msg)
	return nil
}

func sampleJob(id int64, tag string) domain.NotificationJob {
	return domain.NotificationJob{
		SubscriptionID: id,
		Email:          "user@example.com",
		Repo:           "golang/go",
		Tag:            tag,
		ReleaseURL:     "https://example.com/" + tag,
		UnsubToken:     "tok",
	}
}

func TestEnqueueBatch_PublishesPersistentJobs(t *testing.T) {
	pub := &fakePublisher{}
	q := NewNotificationQueue(pub)
	jobs := []domain.NotificationJob{sampleJob(1, "v1.0.0"), sampleJob(2, "v1.1.0")}

	if err := q.EnqueueBatch(context.Background(), jobs); err != nil {
		t.Fatalf("EnqueueBatch: %v", err)
	}

	if len(pub.published) != len(jobs) {
		t.Fatalf("published %d messages, want %d", len(pub.published), len(jobs))
	}
	for i, msg := range pub.published {
		if pub.keys[i] != notificationsQueue {
			t.Fatalf("routing key = %q, want %q", pub.keys[i], notificationsQueue)
		}
		if msg.DeliveryMode != amqp.Persistent {
			t.Fatalf("message %d not persistent (mode=%d) — would be lost on broker restart", i, msg.DeliveryMode)
		}
		var got domain.NotificationJob
		if err := json.Unmarshal(msg.Body, &got); err != nil {
			t.Fatalf("message %d body is not a valid job: %v", i, err)
		}
		if got != jobs[i] {
			t.Fatalf("message %d = %+v, want %+v", i, got, jobs[i])
		}
	}
}

func TestEnqueueBatch_EmptyIsNoOp(t *testing.T) {
	pub := &fakePublisher{}
	q := NewNotificationQueue(pub)

	if err := q.EnqueueBatch(context.Background(), nil); err != nil {
		t.Fatalf("EnqueueBatch(nil): %v", err)
	}
	if len(pub.published) != 0 {
		t.Fatalf("empty batch should publish nothing, got %d", len(pub.published))
	}
}

func TestEnqueueBatch_PropagatesPublishError(t *testing.T) {
	pub := &fakePublisher{err: errors.New("broker down")}
	q := NewNotificationQueue(pub)

	if err := q.EnqueueBatch(context.Background(), []domain.NotificationJob{sampleJob(1, "v1")}); err == nil {
		t.Fatal("expected EnqueueBatch to surface the publish error")
	}
}
