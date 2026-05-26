package lock

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestLock(t *testing.T) (*RedisLock, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return NewRedisLock(rdb), mr
}

func TestAcquire_FirstCallerWins(t *testing.T) {
	l, _ := newTestLock(t)

	ok, err := l.Acquire(context.Background(), "scan:lock", time.Minute)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if !ok {
		t.Fatal("first Acquire should succeed")
	}
}

func TestAcquire_BlockedWhileHeld(t *testing.T) {
	l, _ := newTestLock(t)
	ctx := context.Background()

	if ok, err := l.Acquire(ctx, "scan:lock", time.Minute); err != nil || !ok {
		t.Fatalf("first Acquire: ok=%v err=%v", ok, err)
	}

	ok, err := l.Acquire(ctx, "scan:lock", time.Minute)
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	if ok {
		t.Fatal("second Acquire should fail while the lock is held")
	}
}

func TestAcquire_SucceedsAfterExpiry(t *testing.T) {
	l, mr := newTestLock(t)
	ctx := context.Background()

	if ok, err := l.Acquire(ctx, "scan:lock", time.Minute); err != nil || !ok {
		t.Fatalf("first Acquire: ok=%v err=%v", ok, err)
	}

	mr.FastForward(2 * time.Minute)

	ok, err := l.Acquire(ctx, "scan:lock", time.Minute)
	if err != nil {
		t.Fatalf("Acquire after expiry: %v", err)
	}
	if !ok {
		t.Fatal("Acquire should succeed once the previous lock has expired")
	}
}
