package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"

	"github-release-notifier/notifier/internal/domain"
)

var errConsumerClosed = errors.New("queue: consumer channel closed")

// Delivery is an in-flight job plus its broker handle. DeliveryCount is the
// quorum-queue x-delivery-count: the number of prior failed deliveries, which
// the consumer uses to cap retries (see ADR-0006).
type Delivery struct {
	Job           domain.NotificationJob
	DeliveryCount int
	raw           amqp.Delivery
}

// Consumer drains the notifications queue over a single channel. Each notifier
// worker owns one Consumer, per amqp091 guidance not to share a channel across
// goroutines.
type Consumer struct {
	ch         *amqp.Channel
	deliveries <-chan amqp.Delivery
}

// Dequeue blocks until a job arrives, the channel closes, or ctx is cancelled.
func (c *Consumer) Dequeue(ctx context.Context) (*Delivery, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case d, ok := <-c.deliveries:
		if !ok {
			return nil, errConsumerClosed
		}
		var job domain.NotificationJob
		if err := json.Unmarshal(d.Body, &job); err != nil {
			// Poison message: drop it so it can't redeliver forever.
			_ = d.Ack(false)
			return nil, fmt.Errorf("unmarshalling job: %w", err)
		}
		return &Delivery{Job: job, DeliveryCount: deliveryCount(d), raw: d}, nil
	}
}

func (c *Consumer) Ack(d *Delivery) error {
	return d.raw.Ack(false)
}

func (c *Consumer) Nack(d *Delivery) error {
	return d.raw.Nack(false, true)
}

func (c *Consumer) Close() error {
	if c.ch == nil {
		return nil
	}
	return c.ch.Close()
}

func deliveryCount(d amqp.Delivery) int {
	switch v := d.Headers["x-delivery-count"].(type) {
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	default:
		return 0
	}
}
