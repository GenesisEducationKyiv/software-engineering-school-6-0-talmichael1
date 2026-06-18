package dedup

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestDeduper(t *testing.T) (*Redis, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return NewRedis(rdb), mr
}

func TestMarkSent_SetsKeyWithTTL(t *testing.T) {
	d, mr := newTestDeduper(t)

	if err := d.MarkSent(context.Background(), 42, "v1.0.0"); err != nil {
		t.Fatalf("MarkSent: %v", err)
	}

	wantKey := "notified:42:v1.0.0"
	if !mr.Exists(wantKey) {
		t.Fatalf("expected key %q to exist, keys: %v", wantKey, mr.Keys())
	}
	got := mr.TTL(wantKey)
	if got <= 0 {
		t.Fatalf("expected positive TTL on %q, got %v (would never expire)", wantKey, got)
	}
	if got > ttl || got < ttl-time.Second {
		t.Fatalf("TTL = %v, want ~%v", got, ttl)
	}
}

func TestIsSent_FalseWhenAbsent(t *testing.T) {
	d, _ := newTestDeduper(t)

	sent, err := d.IsSent(context.Background(), 42, "v1.0.0")
	if err != nil {
		t.Fatalf("IsSent: %v", err)
	}
	if sent {
		t.Fatal("expected IsSent=false for absent key")
	}
}

func TestIsSent_TrueAfterMarkSent(t *testing.T) {
	d, _ := newTestDeduper(t)
	ctx := context.Background()

	if err := d.MarkSent(ctx, 42, "v1.0.0"); err != nil {
		t.Fatalf("MarkSent: %v", err)
	}
	sent, err := d.IsSent(ctx, 42, "v1.0.0")
	if err != nil {
		t.Fatalf("IsSent: %v", err)
	}
	if !sent {
		t.Fatal("expected IsSent=true after MarkSent on the same key")
	}
}

func TestIsSent_KeysAreScopedByIDAndTag(t *testing.T) {
	d, _ := newTestDeduper(t)
	ctx := context.Background()

	if err := d.MarkSent(ctx, 1, "v1.0.0"); err != nil {
		t.Fatalf("MarkSent: %v", err)
	}

	sent, err := d.IsSent(ctx, 2, "v1.0.0")
	if err != nil {
		t.Fatalf("IsSent: %v", err)
	}
	if sent {
		t.Fatal("dedup must not leak across subscription IDs")
	}

	sent, err = d.IsSent(ctx, 1, "v2.0.0")
	if err != nil {
		t.Fatalf("IsSent: %v", err)
	}
	if sent {
		t.Fatal("dedup must not leak across tags")
	}
}
