// Package idempotency provides a response-caching idempotency store for the
// fiat orchestrator's write-side handlers. It is backed by Redis in production
// (go-redis SET NX with TTL) and falls back to an in-memory implementation
// when REDIS_URL is unset and DEV_MODE=1 (or the test binary).
//
// The store deduplicates requests by key across replicas: the first caller
// claims the key via Claim, runs the handler, and stores the response via
// StoreResponse; subsequent callers retrieve the cached response via
// LookupResponse. A claim that is not followed by StoreResponse within ttl
// expires, allowing recovery from a crashed mid-flight handler.
package idempotency

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Entry is a cached idempotent response.
type Entry struct {
	Status int    `json:"status"`
	Body   []byte `json:"body"`
}

// Store is the idempotency-key response store used by the API service.
type Store interface {
	// LookupResponse returns the cached response for key, or ok=false if
	// absent (including when the key is claimed but not yet populated).
	LookupResponse(ctx context.Context, key string) (Entry, bool, error)
	// Claim atomically acquires key for ttl. Returns true if this caller
	// is the first; false if the key is already claimed or populated.
	Claim(ctx context.Context, key string, ttl time.Duration) (bool, error)
	// StoreResponse stores the response for a previously-claimed key and
	// refreshes the ttl.
	StoreResponse(ctx context.Context, key string, e Entry, ttl time.Duration) error
	// Delete removes the key (useful for tests).
	Delete(ctx context.Context, key string) error
}

// ErrUnavailable is returned when no store is configured.
var ErrUnavailable = errors.New("idempotency: store unavailable")

// MemStore is an in-memory idempotency store for tests and DEV_MODE.
type MemStore struct {
	mu  sync.Mutex
	mem map[string]memEntry
}

type memEntry struct {
	response []byte
	claimed  bool
	expires  time.Time
}

// NewMem returns an in-memory idempotency store.
func NewMem() *MemStore { return &MemStore{mem: map[string]memEntry{}} }

func (s *MemStore) LookupResponse(_ context.Context, key string) (Entry, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.mem[key]
	if !ok || time.Now().After(e.expires) {
		if ok {
			delete(s.mem, key)
		}
		return Entry{}, false, nil
	}
	if e.response == nil {
		return Entry{}, false, nil
	}
	var ent Entry
	if err := json.Unmarshal(e.response, &ent); err != nil {
		return Entry{}, false, err
	}
	return ent, true, nil
}

func (s *MemStore) Claim(_ context.Context, key string, ttl time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if e, ok := s.mem[key]; ok && now.Before(e.expires) {
		return false, nil
	}
	s.mem[key] = memEntry{claimed: true, expires: now.Add(ttl)}
	return true, nil
}

func (s *MemStore) StoreResponse(_ context.Context, key string, e Entry, ttl time.Duration) error {
	body, err := json.Marshal(e)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mem[key] = memEntry{response: body, expires: time.Now().Add(ttl)}
	return nil
}

func (s *MemStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.mem, key)
	return nil
}

// RedisStore is a Redis-backed idempotency store. A claim is recorded under
// "{prefix}:claim:{key}" via SET NX with ttl; the cached response is stored
// under "{prefix}:resp:{key}" via SET with ttl. LookupResponse reads the
// response key; a hit means the original handler completed.
type RedisStore struct {
	client *redis.Client
	prefix string
}

// NewRedis returns a Redis-backed store. prefix is prepended to all keys
// (default "idempot").
func NewRedis(client *redis.Client, prefix string) *RedisStore {
	if prefix == "" {
		prefix = "idempot"
	}
	return &RedisStore{client: client, prefix: prefix}
}

func (s *RedisStore) claimKey(key string) string { return s.prefix + ":claim:" + key }
func (s *RedisStore) respKey(key string) string  { return s.prefix + ":resp:" + key }

func (s *RedisStore) LookupResponse(ctx context.Context, key string) (Entry, bool, error) {
	raw, err := s.client.Get(ctx, s.respKey(key)).Bytes()
	if err == redis.Nil {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, fmt.Errorf("idempot redis get: %w", err)
	}
	var e Entry
	if err := json.Unmarshal(raw, &e); err != nil {
		return Entry{}, false, fmt.Errorf("idempot redis decode: %w", err)
	}
	return e, true, nil
}

func (s *RedisStore) Claim(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	ok, err := s.client.SetNX(ctx, s.claimKey(key), "1", ttl).Result()
	if err != nil {
		return false, fmt.Errorf("idempot redis setnx: %w", err)
	}
	return ok, nil
}

func (s *RedisStore) StoreResponse(ctx context.Context, key string, e Entry, ttl time.Duration) error {
	body, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("idempot redis encode: %w", err)
	}
	if err := s.client.Set(ctx, s.respKey(key), body, ttl).Err(); err != nil {
		return fmt.Errorf("idempot redis set: %w", err)
	}
	return nil
}

func (s *RedisStore) Delete(ctx context.Context, key string) error {
	if err := s.client.Del(ctx, s.claimKey(key), s.respKey(key)).Err(); err != nil {
		return fmt.Errorf("idempot redis del: %w", err)
	}
	return nil
}

// Open returns a Redis-backed store when url is non-empty and reachable,
// otherwise a MemStore. Callers must apply the DEV_MODE / prod gating
// before calling Open; Open itself never fatals.
func Open(ctx context.Context, url string) (Store, error) {
	if strings.TrimSpace(url) == "" {
		return NewMem(), nil
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse REDIS_URL: %w", err)
	}
	client := redis.NewClient(opts)
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return NewRedis(client, "idempot"), nil
}