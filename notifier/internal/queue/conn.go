package queue

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const notificationsQueue = "notifications"

const (
	reconnectMinBackoff = 1 * time.Second
	reconnectMaxBackoff = 30 * time.Second
)

// Connection is a single long-lived AMQP connection that re-dials with backoff
// after a drop. Each worker opens its own consumer channel over it — concurrency
// comes from channels, never from extra connections or per-message dials.
type Connection struct {
	url  string
	mu   sync.Mutex
	conn *amqp.Connection
}

func Dial(url string) (*Connection, error) {
	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, fmt.Errorf("dialing rabbitmq: %w", err)
	}
	return &Connection{url: url, conn: conn}, nil
}

func (c *Connection) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil || c.conn.IsClosed() {
		return nil
	}
	return c.conn.Close()
}

// current returns a live connection, re-dialing with backoff if the previous one
// closed. It blocks until connected or ctx is cancelled.
func (c *Connection) current(ctx context.Context) (*amqp.Connection, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil && !c.conn.IsClosed() {
		return c.conn, nil
	}
	backoff := reconnectMinBackoff
	for {
		conn, err := amqp.Dial(c.url)
		if err == nil {
			c.conn = conn
			slog.InfoContext(ctx, "rabbitmq reconnected")
			return conn, nil
		}
		slog.WarnContext(ctx, "rabbitmq dial failed, retrying", "backoff", backoff, "error", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > reconnectMaxBackoff {
			backoff = reconnectMaxBackoff
		}
	}
}

// Consumer opens a fresh channel on the live connection, (re)declares the queue,
// sets prefetch and starts consuming. Each worker calls this to (re)establish its
// own channel after a connection or channel drop.
func (c *Connection) Consumer(ctx context.Context, prefetch int) (*Consumer, error) {
	conn, err := c.current(ctx)
	if err != nil {
		return nil, err
	}
	ch, err := conn.Channel()
	if err != nil {
		return nil, fmt.Errorf("opening channel: %w", err)
	}
	if err := declareNotifications(ch); err != nil {
		_ = ch.Close()
		return nil, err
	}
	if err := ch.Qos(prefetch, 0, false); err != nil {
		_ = ch.Close()
		return nil, fmt.Errorf("setting qos: %w", err)
	}
	deliveries, err := ch.Consume(notificationsQueue, "", false, false, false, false, nil)
	if err != nil {
		_ = ch.Close()
		return nil, fmt.Errorf("consuming %q: %w", notificationsQueue, err)
	}
	return &Consumer{ch: ch, deliveries: deliveries}, nil
}

// declareNotifications declares the notifications queue idempotently. Producer
// and consumer must declare identical args or the broker rejects the channel —
// see ADR-0006.
func declareNotifications(ch *amqp.Channel) error {
	_, err := ch.QueueDeclare(notificationsQueue, true, false, false, false, amqp.Table{
		"x-queue-type": "quorum",
	})
	if err != nil {
		return fmt.Errorf("declaring %q queue: %w", notificationsQueue, err)
	}
	return nil
}
