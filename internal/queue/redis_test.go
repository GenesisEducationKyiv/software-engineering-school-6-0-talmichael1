package queue

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github-release-notifier/internal/domain"
)

func newTestQueue(t *testing.T) (*NotificationQueue, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return NewNotificationQueue(rdb), mr
}

func sampleJob(id int64, tag string) domain.NotificationJob {
	return domain.NotificationJob{
		SubscriptionID: id,
		Email:          "user@example.com",
		Repo:           "golang/go",
		Tag:            tag,
		ReleaseName:    "Go " + tag,
		ReleaseURL:     "https://example.com/" + tag,
		UnsubToken:     "tok",
	}
}

func TestEnqueue_PushesJSONToPendingList(t *testing.T) {
	q, mr := newTestQueue(t)
	job := sampleJob(1, "v1.0.0")

	if err := q.Enqueue(context.Background(), job); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	items, err := mr.List(pendingQueue)
	if err != nil {
		t.Fatalf("reading list %q: %v", pendingQueue, err)
	}
	if len(items) != 1 {
		t.Fatalf("list length = %d, want 1", len(items))
	}

	var got domain.NotificationJob
	if err := json.Unmarshal([]byte(items[0]), &got); err != nil {
		t.Fatalf("payload is not valid NotificationJob JSON: %v", err)
	}
	if got != job {
		t.Fatalf("payload mismatch:\n got  %+v\n want %+v", got, job)
	}
}

func TestEnqueueBatch_PushesAllJobs(t *testing.T) {
	q, mr := newTestQueue(t)
	jobs := []domain.NotificationJob{
		sampleJob(1, "v1.0.0"),
		sampleJob(2, "v1.1.0"),
		sampleJob(3, "v1.2.0"),
	}

	if err := q.EnqueueBatch(context.Background(), jobs); err != nil {
		t.Fatalf("EnqueueBatch: %v", err)
	}

	items, err := mr.List(pendingQueue)
	if err != nil {
		t.Fatalf("reading list: %v", err)
	}
	if len(items) != len(jobs) {
		t.Fatalf("list length = %d, want %d", len(items), len(jobs))
	}
}

func TestEnqueueBatch_EmptyIsNoOp(t *testing.T) {
	q, mr := newTestQueue(t)

	if err := q.EnqueueBatch(context.Background(), nil); err != nil {
		t.Fatalf("EnqueueBatch(nil): %v", err)
	}
	if mr.Exists(pendingQueue) {
		t.Fatalf("empty batch should not create the list key")
	}
}

func TestDequeue_FIFOOrder(t *testing.T) {
	q, _ := newTestQueue(t)
	ctx := context.Background()

	first := sampleJob(1, "v1.0.0")
	second := sampleJob(2, "v2.0.0")
	if err := q.Enqueue(ctx, first); err != nil {
		t.Fatalf("Enqueue first: %v", err)
	}
	if err := q.Enqueue(ctx, second); err != nil {
		t.Fatalf("Enqueue second: %v", err)
	}

	got, err := q.Dequeue(ctx, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}
	if got == nil {
		t.Fatal("Dequeue returned nil, expected first job")
	}
	if *got != first {
		t.Fatalf("first dequeued: got %+v, want %+v", *got, first)
	}

	got, err = q.Dequeue(ctx, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}
	if got == nil || *got != second {
		t.Fatalf("second dequeued: got %+v, want %+v", got, second)
	}
}

func TestDequeue_EmptyReturnsNilNil(t *testing.T) {
	q, _ := newTestQueue(t)

	got, err := q.Dequeue(context.Background(), 50*time.Millisecond)
	if err != nil {
		t.Fatalf("expected (nil, nil) on empty queue, got err: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil job, got %+v", got)
	}
}

func TestMarkSent_SetsKeyWithTTL(t *testing.T) {
	q, mr := newTestQueue(t)

	if err := q.MarkSent(context.Background(), 42, "v1.0.0"); err != nil {
		t.Fatalf("MarkSent: %v", err)
	}

	wantKey := "notified:42:v1.0.0"
	if !mr.Exists(wantKey) {
		t.Fatalf("expected key %q to exist, keys: %v", wantKey, mr.Keys())
	}
	ttl := mr.TTL(wantKey)
	if ttl <= 0 {
		t.Fatalf("expected positive TTL on %q, got %v (would never expire)", wantKey, ttl)
	}
	if ttl > dedupTTL || ttl < dedupTTL-time.Second {
		t.Fatalf("TTL = %v, want ~%v", ttl, dedupTTL)
	}
}

func TestIsSent_FalseWhenAbsent(t *testing.T) {
	q, _ := newTestQueue(t)

	sent, err := q.IsSent(context.Background(), 42, "v1.0.0")
	if err != nil {
		t.Fatalf("IsSent: %v", err)
	}
	if sent {
		t.Fatal("expected IsSent=false for absent key")
	}
}

func TestIsSent_TrueAfterMarkSent(t *testing.T) {
	q, _ := newTestQueue(t)
	ctx := context.Background()

	if err := q.MarkSent(ctx, 42, "v1.0.0"); err != nil {
		t.Fatalf("MarkSent: %v", err)
	}
	sent, err := q.IsSent(ctx, 42, "v1.0.0")
	if err != nil {
		t.Fatalf("IsSent: %v", err)
	}
	if !sent {
		t.Fatal("expected IsSent=true after MarkSent on the same key")
	}
}

func TestIsSent_KeysAreScopedByIDAndTag(t *testing.T) {
	q, _ := newTestQueue(t)
	ctx := context.Background()

	if err := q.MarkSent(ctx, 1, "v1.0.0"); err != nil {
		t.Fatalf("MarkSent: %v", err)
	}

	// Different subscription, same tag.
	sent, err := q.IsSent(ctx, 2, "v1.0.0")
	if err != nil {
		t.Fatalf("IsSent: %v", err)
	}
	if sent {
		t.Fatal("dedup must not leak across subscription IDs")
	}

	// Same subscription, different tag.
	sent, err = q.IsSent(ctx, 1, "v2.0.0")
	if err != nil {
		t.Fatalf("IsSent: %v", err)
	}
	if sent {
		t.Fatal("dedup must not leak across tags")
	}
}

func TestRequeue_IncrementsAttempt(t *testing.T) {
	q, _ := newTestQueue(t)
	ctx := context.Background()

	job := sampleJob(1, "v1.0.0")
	job.Attempt = 2

	if err := q.Requeue(ctx, job); err != nil {
		t.Fatalf("Requeue: %v", err)
	}

	got, err := q.Dequeue(ctx, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}
	if got == nil {
		t.Fatal("Dequeue returned nil after Requeue")
	}
	if got.Attempt != 3 {
		t.Fatalf("requeued Attempt = %d, want 3", got.Attempt)
	}
}
