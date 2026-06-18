package queue

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	notificationsQueue = "notifications"
	repoChecksQueue    = "repo_checks"
)

const (
	reconnectMinBackoff = 1 * time.Second
	reconnectMaxBackoff = 30 * time.Second
)

// Connection is a single long-lived AMQP connection that re-dials with backoff
// after a drop. Channels are multiplexed over it — concurrency comes from
// channels, never from extra connections or per-message dials.
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

// Publisher owns one channel on the resilient connection. The channel is created
// lazily and recreated only after an error or reconnect — never per message.
type Publisher struct {
	conn    *Connection
	declare func(*amqp.Channel) error
	mu      sync.Mutex
	ch      *amqp.Channel
}

func (c *Connection) NotificationPublisher() *Publisher {
	return &Publisher{conn: c, declare: declareNotifications}
}

func (c *Connection) RepoCheckPublisher() *Publisher {
	return &Publisher{conn: c, declare: declareRepoChecks}
}

func (p *Publisher) PublishWithContext(ctx context.Context, exchange, key string, mandatory, immediate bool, msg amqp.Publishing) error {
	ch, err := p.channel(ctx)
	if err != nil {
		return err
	}
	if err := ch.PublishWithContext(ctx, exchange, key, mandatory, immediate, msg); err != nil {
		p.reset()
		return err
	}
	return nil
}

func (p *Publisher) channel(ctx context.Context) (*amqp.Channel, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ch != nil && !p.ch.IsClosed() {
		return p.ch, nil
	}
	conn, err := p.conn.current(ctx)
	if err != nil {
		return nil, err
	}
	ch, err := conn.Channel()
	if err != nil {
		return nil, fmt.Errorf("opening channel: %w", err)
	}
	if err := p.declare(ch); err != nil {
		_ = ch.Close()
		return nil, err
	}
	p.ch = ch
	return ch, nil
}

func (p *Publisher) reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ch != nil {
		_ = p.ch.Close()
		p.ch = nil
	}
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

// declareRepoChecks declares the repo-check work queue. A durable classic queue
// is enough — repo checks are auto-acked and lossy by design (a dropped check is
// re-enqueued next scan cycle), so they need neither quorum nor persistence.
func declareRepoChecks(ch *amqp.Channel) error {
	_, err := ch.QueueDeclare(repoChecksQueue, true, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("declaring %q queue: %w", repoChecksQueue, err)
	}
	return nil
}
