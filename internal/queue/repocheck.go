package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github-release-notifier/internal/domain"
)

var errRepoChannelClosed = errors.New("queue: repo-check channel closed")

// RepoCheckQueue distributes repository checks across scanner workers. It
// publishes on the producer's publish connection and consumes (auto-ack) on a
// separate connection, so publisher flow control can't stall consumption — see
// ADR-0006. Consumption is lossy by design: a dropped check is re-enqueued next
// scan cycle.
type RepoCheckQueue struct {
	pub      publisher
	sub      *Connection
	prefetch int

	mu         sync.Mutex
	ch         *amqp.Channel
	deliveries <-chan amqp.Delivery
}

func NewRepoCheckQueue(pub publisher, sub *Connection, prefetch int) *RepoCheckQueue {
	return &RepoCheckQueue{pub: pub, sub: sub, prefetch: prefetch}
}

func (q *RepoCheckQueue) EnqueueRepo(ctx context.Context, repo domain.Repository) error {
	data, err := json.Marshal(repo)
	if err != nil {
		return fmt.Errorf("marshalling repo: %w", err)
	}
	return q.pub.PublishWithContext(ctx, "", repoChecksQueue, false, false, amqp.Publishing{
		ContentType: "application/json",
		Body:        data,
	})
}

// DequeueRepo returns (nil, nil) when the timeout is reached with no repos.
func (q *RepoCheckQueue) DequeueRepo(ctx context.Context, timeout time.Duration) (*domain.Repository, error) {
	deliveries, err := q.consume(ctx)
	if err != nil {
		return nil, err
	}
	return q.read(ctx, deliveries, timeout)
}

func (q *RepoCheckQueue) read(ctx context.Context, deliveries <-chan amqp.Delivery, timeout time.Duration) (*domain.Repository, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, nil
	case d, ok := <-deliveries:
		if !ok {
			q.reset()
			return nil, errRepoChannelClosed
		}
		var repo domain.Repository
		if err := json.Unmarshal(d.Body, &repo); err != nil {
			return nil, fmt.Errorf("unmarshalling repo: %w", err)
		}
		return &repo, nil
	}
}

// consume lazily (re)opens the consume channel on the live connection, recreating
// it after a drop so DequeueRepo survives reconnects.
func (q *RepoCheckQueue) consume(ctx context.Context) (<-chan amqp.Delivery, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.ch != nil && !q.ch.IsClosed() {
		return q.deliveries, nil
	}
	conn, err := q.sub.current(ctx)
	if err != nil {
		return nil, err
	}
	ch, err := conn.Channel()
	if err != nil {
		return nil, fmt.Errorf("opening channel: %w", err)
	}
	if err := declareRepoChecks(ch); err != nil {
		_ = ch.Close()
		return nil, err
	}
	if err := ch.Qos(q.prefetch, 0, false); err != nil {
		_ = ch.Close()
		return nil, fmt.Errorf("setting qos: %w", err)
	}
	deliveries, err := ch.Consume(repoChecksQueue, "", true, false, false, false, nil)
	if err != nil {
		_ = ch.Close()
		return nil, fmt.Errorf("consuming %q: %w", repoChecksQueue, err)
	}
	q.ch = ch
	q.deliveries = deliveries
	return deliveries, nil
}

func (q *RepoCheckQueue) reset() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.ch != nil {
		_ = q.ch.Close()
		q.ch = nil
	}
	q.deliveries = nil
}
