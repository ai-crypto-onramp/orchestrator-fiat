package idempotency

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestMemStore_ClaimAndLookup(t *testing.T) {
	ctx := context.Background()
	s := NewMem()
	ok, err := s.Claim(ctx, "k1", time.Minute)
	if err != nil || !ok {
		t.Fatalf("first claim: err=%v ok=%v", err, ok)
	}
	ok, _ = s.Claim(ctx, "k1", time.Minute)
	if ok {
		t.Fatal("expected duplicate claim to fail")
	}
	if _, ok, err := s.LookupResponse(ctx, "k1"); err != nil || ok {
		t.Fatalf("lookup before store: err=%v ok=%v", err, ok)
	}
	if err := s.StoreResponse(ctx, "k1", Entry{Status: 201, Body: []byte(`{"x":1}`)}, time.Minute); err != nil {
		t.Fatal(err)
	}
	ent, ok, err := s.LookupResponse(ctx, "k1")
	if err != nil || !ok {
		t.Fatalf("lookup after store: err=%v ok=%v", err, ok)
	}
	if ent.Status != 201 || string(ent.Body) != `{"x":1}` {
		t.Fatalf("got %+v", ent)
	}
}

func TestMemStore_TTLExpiry(t *testing.T) {
	ctx := context.Background()
	s := NewMem()
	if ok, _ := s.Claim(ctx, "exp", 50*time.Millisecond); !ok {
		t.Fatal("expected first claim ok")
	}
	time.Sleep(60 * time.Millisecond)
	if ok, _ := s.Claim(ctx, "exp", 50*time.Millisecond); !ok {
		t.Fatal("expected re-claim after expiry")
	}
}

func TestMemStore_Delete(t *testing.T) {
	ctx := context.Background()
	s := NewMem()
	_, _ = s.Claim(ctx, "d", time.Minute)
	_ = s.StoreResponse(ctx, "d", Entry{Status: 200}, time.Minute)
	if err := s.Delete(ctx, "d"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Claim(ctx, "d", time.Minute); !ok {
		t.Fatal("expected re-claim after delete")
	}
}

func TestMemStore_ConcurrentClaim(t *testing.T) {
	ctx := context.Background()
	s := NewMem()
	const n = 50
	wins := make(chan bool, n)
	for i := 0; i < n; i++ {
		go func() { ok, _ := s.Claim(ctx, "race", time.Minute); wins <- ok }()
	}
	got := 0
	for i := 0; i < n; i++ {
		if <-wins {
			got++
		}
	}
	if got != 1 {
		t.Fatalf("expected exactly 1 winner, got %d", got)
	}
}

func newRedisStore(t *testing.T) (*RedisStore, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return NewRedis(client, "idempot"), mr
}

func TestRedisStore_ClaimAndLookup(t *testing.T) {
	ctx := context.Background()
	s, _ := newRedisStore(t)
	ok, err := s.Claim(ctx, "rk1", time.Minute)
	if err != nil || !ok {
		t.Fatalf("first claim: err=%v ok=%v", err, ok)
	}
	ok, err = s.Claim(ctx, "rk1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected duplicate claim to return false")
	}
	if _, ok, err := s.LookupResponse(ctx, "rk1"); err != nil || ok {
		t.Fatalf("lookup before store: err=%v ok=%v", err, ok)
	}
	if err := s.StoreResponse(ctx, "rk1", Entry{Status: 201, Body: []byte(`{"x":1}`)}, time.Minute); err != nil {
		t.Fatal(err)
	}
	ent, ok, err := s.LookupResponse(ctx, "rk1")
	if err != nil || !ok {
		t.Fatalf("lookup after store: err=%v ok=%v", err, ok)
	}
	if ent.Status != 201 || string(ent.Body) != `{"x":1}` {
		t.Fatalf("got %+v", ent)
	}
}

func TestRedisStore_TTLExpiry(t *testing.T) {
	ctx := context.Background()
	s, mr := newRedisStore(t)
	if ok, _ := s.Claim(ctx, "exp", time.Second); !ok {
		t.Fatal("expected first claim ok")
	}
	mr.FastForward(2 * time.Second)
	if ok, _ := s.Claim(ctx, "exp", time.Second); !ok {
		t.Fatal("expected re-claim after expiry")
	}
}

func TestRedisStore_Delete(t *testing.T) {
	ctx := context.Background()
	s, _ := newRedisStore(t)
	_, _ = s.Claim(ctx, "del", time.Minute)
	_ = s.StoreResponse(ctx, "del", Entry{Status: 200}, time.Minute)
	if err := s.Delete(ctx, "del"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Claim(ctx, "del", time.Minute); !ok {
		t.Fatal("expected re-claim after delete")
	}
}

func TestRedisStore_ErrorOnClosedClient(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	_ = client.Close()
	s := NewRedis(client, "idempot")
	if _, err := s.Claim(context.Background(), "k", time.Minute); err == nil {
		t.Fatal("expected error from closed client on Claim")
	}
	if err := s.Delete(context.Background(), "k"); err == nil {
		t.Fatal("expected error from closed client on Delete")
	}
}

func TestOpen_EmptyURLReturnsMem(t *testing.T) {
	s, err := Open(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.(*MemStore); !ok {
		t.Fatalf("expected MemStore, got %T", s)
	}
}

func TestOpen_RedisReachableReturnsRedisStore(t *testing.T) {
	mr := miniredis.RunT(t)
	s, err := Open(context.Background(), "redis://"+mr.Addr()+"/0")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.(*RedisStore); !ok {
		t.Fatalf("expected RedisStore, got %T", s)
	}
}

func TestOpen_UnreachableReturnsError(t *testing.T) {
	if _, err := Open(context.Background(), "redis://127.0.0.1:1/0"); err == nil {
		t.Fatal("expected error when redis unreachable")
	}
}