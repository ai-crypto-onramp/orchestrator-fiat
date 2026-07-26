package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/ai-crypto-onramp/orchestrator-fiat/internal/audit"
	"github.com/ai-crypto-onramp/orchestrator-fiat/internal/idempotency"
	"github.com/ai-crypto-onramp/orchestrator-fiat/internal/rail"
	"github.com/ai-crypto-onramp/orchestrator-fiat/internal/store"
)

func newRedisIdemService(t *testing.T) (*Service, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	st := store.New()
	registry := rail.NewRegistry(rail.NewDummy())
	rec := audit.NewRecorder()
	svc := NewService(st, registry, rec, "dev-secret")
	svc.WithIdempotencyStore(idempotency.NewRedis(client, "idempot"), time.Hour)
	return svc, mr
}

func TestRedisIdempotency_ReplayReturnsCachedResponse(t *testing.T) {
	svc, _ := newRedisIdemService(t)
	mux := NewMux(svc)
	body := map[string]interface{}{"rail": "card", "amount": 100, "currency": "USD", "payer_ref": "p1"}

	rec1 := doJSON(t, mux, http.MethodPost, "/v1/payments/intents", "redis-k1", body)
	if rec1.Code != http.StatusCreated {
		t.Fatalf("first: code=%d body=%s", rec1.Code, rec1.Body.String())
	}
	if rec1.Header().Get("Idempotency-Replay") == "true" {
		t.Fatal("first call must not be a replay")
	}

	rec2 := doJSON(t, mux, http.MethodPost, "/v1/payments/intents", "redis-k1", body)
	if rec2.Code != http.StatusCreated {
		t.Fatalf("second: code=%d body=%s", rec2.Code, rec2.Body.String())
	}
	if rec2.Header().Get("Idempotency-Replay") != "true" {
		t.Fatal("second call must be a replay")
	}
	if !bytes.Equal(rec1.Body.Bytes(), rec2.Body.Bytes()) {
		t.Fatalf("replay body mismatch: first=%s second=%s", rec1.Body.String(), rec2.Body.String())
	}
}

func TestRedisIdempotency_DistinctKeysAreIndependent(t *testing.T) {
	svc, _ := newRedisIdemService(t)
	mux := NewMux(svc)
	body := map[string]interface{}{"rail": "card", "amount": 100, "currency": "USD", "payer_ref": "p1"}

	r1 := doJSON(t, mux, http.MethodPost, "/v1/payments/intents", "redis-a", body)
	r2 := doJSON(t, mux, http.MethodPost, "/v1/payments/intents", "redis-b", body)
	if r1.Code != http.StatusCreated || r2.Code != http.StatusCreated {
		t.Fatalf("codes: %d %d", r1.Code, r2.Code)
	}
	if r1.Header().Get("Idempotency-Replay") == "true" || r2.Header().Get("Idempotency-Replay") == "true" {
		t.Fatal("distinct keys must not be replays")
	}
	var i1, i2 map[string]interface{}
	_ = json.Unmarshal(r1.Body.Bytes(), &i1)
	_ = json.Unmarshal(r2.Body.Bytes(), &i2)
	if i1["id"] == i2["id"] {
		t.Fatal("distinct idem keys must produce distinct intents")
	}
}