package dedup

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	keyPrefix = "notified:"
	ttl       = 24 * time.Hour
)

// Redis records which (subscription, tag) notifications have already been sent,
// so a redelivered job doesn't email the same release twice. This is key-value
// state with a TTL, not a queue — it stays on Redis after the queue moved to
// RabbitMQ (see ADR-0006).
type Redis struct {
	rdb *redis.Client
}

func NewRedis(rdb *redis.Client) *Redis {
	return &Redis{rdb: rdb}
}

func (d *Redis) IsSent(ctx context.Context, subscriptionID int64, tag string) (bool, error) {
	exists, err := d.rdb.Exists(ctx, key(subscriptionID, tag)).Result()
	if err != nil {
		return false, fmt.Errorf("checking dedup key: %w", err)
	}
	return exists > 0, nil
}

func (d *Redis) MarkSent(ctx context.Context, subscriptionID int64, tag string) error {
	return d.rdb.Set(ctx, key(subscriptionID, tag), "1", ttl).Err()
}

func key(subscriptionID int64, tag string) string {
	return fmt.Sprintf("%s%d:%s", keyPrefix, subscriptionID, tag)
}
