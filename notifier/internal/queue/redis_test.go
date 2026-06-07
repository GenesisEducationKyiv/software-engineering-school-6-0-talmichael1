package queue

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github-release-notifier/notifier/internal/domain"
)

func newTestQueue(t *testing.T) (*NotificationQueue, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return NewNotificationQueue(rdb), mr
}

func dial(t *testing.T, mr *miniredis.Miniredis) *redis.Client {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}

func processingMembers(t *testing.T, rdb *redis.Client) []string {
	t.Helper()
	members, err := rdb.ZRange(context.Background(), processingQueue, 0, -1).Result()
	if err != nil {
		t.Fatalf("reading processing zset: %v", err)
	}
	return members
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

func TestDequeue_FIFOOrder(t *testing.T) {
	q, _ := newTestQueue(t)
	ctx := context.Background()

	first := sampleJob(1, "v1.0.0")
	second := sampleJob(2, "v2.0.0")
	if err := q.enqueue(ctx, first); err != nil {
		t.Fatalf("enqueue first: %v", err)
	}
	if err := q.enqueue(ctx, second); err != nil {
		t.Fatalf("enqueue second: %v", err)
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

func TestDequeue_MovesJobToProcessing(t *testing.T) {
	q, mr := newTestQueue(t)
	ctx := context.Background()
	job := sampleJob(1, "v1.0.0")
	if err := q.enqueue(ctx, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	got, err := q.Dequeue(ctx, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}
	if got == nil || *got != job {
		t.Fatalf("Dequeue returned %+v, want %+v", got, job)
	}

	// The job must no longer sit in pending...
	if mr.Exists(pendingQueue) {
		t.Fatalf("pending list should be empty after dequeue")
	}
	// ...but must be held in the processing set with a visibility deadline so a
	// crash can't lose it.
	members := processingMembers(t, dial(t, mr))
	if len(members) != 1 {
		t.Fatalf("processing length = %d, want 1 (in-flight job)", len(members))
	}
	var inflight domain.NotificationJob
	if err := json.Unmarshal([]byte(members[0]), &inflight); err != nil {
		t.Fatalf("processing payload is not valid JSON: %v", err)
	}
	if inflight != job {
		t.Fatalf("processing payload = %+v, want %+v", inflight, job)
	}
}

func TestAck_RemovesJobFromProcessing(t *testing.T) {
	q, mr := newTestQueue(t)
	ctx := context.Background()
	job := sampleJob(1, "v1.0.0")
	if err := q.enqueue(ctx, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	got, err := q.Dequeue(ctx, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}

	if err := q.Ack(ctx, *got); err != nil {
		t.Fatalf("Ack: %v", err)
	}

	if mr.Exists(processingQueue) {
		t.Fatalf("processing list should be empty after Ack")
	}
}

func TestReclaim_MovesExpiredJobsBackToPending(t *testing.T) {
	q, mr := newTestQueue(t)
	ctx := context.Background()
	rdb := dial(t, mr)

	job := sampleJob(7, "v9.9.9")
	data, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// An in-flight job whose visibility deadline is already in the past: its
	// worker is presumed dead.
	if err := rdb.ZAdd(ctx, processingQueue, redis.Z{Score: 1, Member: data}).Err(); err != nil {
		t.Fatalf("seed processing: %v", err)
	}

	n, err := q.Reclaim(ctx)
	if err != nil {
		t.Fatalf("Reclaim: %v", err)
	}
	if n != 1 {
		t.Fatalf("reclaimed %d jobs, want 1", n)
	}
	if len(processingMembers(t, rdb)) != 0 {
		t.Fatalf("processing set should be empty after reclaim")
	}

	got, err := q.Dequeue(ctx, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("Dequeue after reclaim: %v", err)
	}
	if got == nil || *got != job {
		t.Fatalf("reclaimed job = %+v, want %+v", got, job)
	}
}

func TestReclaim_LeavesLiveJobs(t *testing.T) {
	q, mr := newTestQueue(t)
	ctx := context.Background()
	rdb := dial(t, mr)

	job := sampleJob(8, "v1.2.3")
	data, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Deadline well in the future: a healthy worker still holds it.
	future := float64(time.Now().Add(time.Hour).UnixMilli())
	if err := rdb.ZAdd(ctx, processingQueue, redis.Z{Score: future, Member: data}).Err(); err != nil {
		t.Fatalf("seed processing: %v", err)
	}

	n, err := q.Reclaim(ctx)
	if err != nil {
		t.Fatalf("Reclaim: %v", err)
	}
	if n != 0 {
		t.Fatalf("reclaimed %d jobs, want 0 (job still within visibility deadline)", n)
	}
	if len(processingMembers(t, rdb)) != 1 {
		t.Fatalf("live in-flight job must remain in processing")
	}
}

func TestRequeue_RemovesOriginalFromProcessing(t *testing.T) {
	q, mr := newTestQueue(t)
	ctx := context.Background()
	job := sampleJob(1, "v1.0.0")
	if err := q.enqueue(ctx, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	got, err := q.Dequeue(ctx, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}

	if err := q.Requeue(ctx, *got); err != nil {
		t.Fatalf("Requeue: %v", err)
	}

	// The in-flight copy must be cleared from processing...
	if mr.Exists(processingQueue) {
		t.Fatalf("processing set should be empty after Requeue")
	}
	// ...and a retry copy (incremented attempt) waiting in pending.
	next, err := q.Dequeue(ctx, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("Dequeue retry: %v", err)
	}
	if next == nil {
		t.Fatal("expected a requeued job in pending")
	}
	if next.Attempt != 1 {
		t.Fatalf("requeued Attempt = %d, want 1", next.Attempt)
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
