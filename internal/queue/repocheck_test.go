package queue

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github-release-notifier/internal/domain"
)

func sampleRepo() domain.Repository {
	return domain.Repository{ID: 42, Owner: "gin-gonic", Name: "gin", LastSeenTag: "v1.9.0"}
}

func TestEnqueueRepo_PublishesToRepoChecks(t *testing.T) {
	pub := &fakePublisher{}
	q := NewRepoCheckQueue(pub, nil, 1)

	if err := q.EnqueueRepo(context.Background(), sampleRepo()); err != nil {
		t.Fatalf("EnqueueRepo: %v", err)
	}
	if len(pub.published) != 1 {
		t.Fatalf("published %d messages, want 1", len(pub.published))
	}
	if pub.keys[0] != repoChecksQueue {
		t.Fatalf("routing key = %q, want %q", pub.keys[0], repoChecksQueue)
	}
	if h := pub.published[0].Headers[deduplicationHeader]; h != "42" {
		t.Fatalf("dedup header = %v, want %q — duplicate checks for one repo would fan out twice", h, "42")
	}
	var got domain.Repository
	if err := json.Unmarshal(pub.published[0].Body, &got); err != nil {
		t.Fatalf("body is not a valid repo: %v", err)
	}
	if got != sampleRepo() {
		t.Fatalf("published repo = %+v, want %+v", got, sampleRepo())
	}
}

func TestRead_RoundTripsRepo(t *testing.T) {
	q := &RepoCheckQueue{}
	ch := make(chan amqp.Delivery, 1)
	body, _ := json.Marshal(sampleRepo())
	ch <- amqp.Delivery{Body: body}

	got, err := q.read(context.Background(), ch, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got == nil || *got != sampleRepo() {
		t.Fatalf("read repo = %+v, want %+v", got, sampleRepo())
	}
}

func TestRead_TimeoutReturnsNilNil(t *testing.T) {
	q := &RepoCheckQueue{}
	got, err := q.read(context.Background(), make(chan amqp.Delivery), 20*time.Millisecond)
	if err != nil {
		t.Fatalf("expected (nil, nil) on timeout, got err: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil repo on timeout, got %+v", got)
	}
}

func TestRead_ClosedChannelReturnsError(t *testing.T) {
	q := &RepoCheckQueue{}
	ch := make(chan amqp.Delivery)
	close(ch)

	if _, err := q.read(context.Background(), ch, 100*time.Millisecond); err == nil {
		t.Fatal("expected an error when the consume channel is closed")
	}
}

func TestRead_CancelledContextReturnsError(t *testing.T) {
	q := &RepoCheckQueue{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := q.read(ctx, make(chan amqp.Delivery), time.Second); err == nil {
		t.Fatal("expected ctx error on a cancelled context")
	}
}
