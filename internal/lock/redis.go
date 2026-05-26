package lock

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisLock struct {
	rdb *redis.Client
}

func NewRedisLock(rdb *redis.Client) *RedisLock {
	return &RedisLock{rdb: rdb}
}

func (l *RedisLock) Acquire(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	err := l.rdb.SetArgs(ctx, key, "1", redis.SetArgs{Mode: "NX", TTL: ttl}).Err()
	if err == redis.Nil {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
