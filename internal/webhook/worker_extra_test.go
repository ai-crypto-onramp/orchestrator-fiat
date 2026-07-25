package webhook

import (
	"context"
	"testing"
	"time"

	"github.com/ai-crypto-onramp/payment-orchestrator/internal/domain"
	"github.com/ai-crypto-onramp/payment-orchestrator/internal/logging"
	"github.com/ai-crypto-onramp/payment-orchestrator/internal/metrics"
	"github.com/ai-crypto-onramp/payment-orchestrator/internal/store"
)

func TestNewDefaultsBufferSize(t *testing.T) {
	s := store.New()
	w := New(s, func([]byte) error { return nil }, nil, nil, 0)
	if w == nil {
		t.Fatal("expected worker")
	}
	if cap(w.queue) != 64 {
		t.Fatalf("default buffer = %d, want 64", cap(w.queue))
	}
}

func TestNewDefaultsMetricsAndLogger(t *testing.T) {
	s := store.New()
	w := New(s, func([]byte) error { return nil }, nil, logging.New(nil, logging.LevelInfo), 8)
	if w.metrics == nil {
		t.Fatal("expected default metrics")
	}
	if w.logger == nil {
		t.Fatal("expected default logger")
	}
}

func TestStartDefaultConcurrency(t *testing.T) {
	s := store.New()
	w := New(s, func([]byte) error { return nil }, metrics.New(), logging.New(nil, logging.LevelInfo), 4)
	w.Start(0)
	defer w.Stop()
}

func TestEnqueueWhenFullDrops(t *testing.T) {
	s := store.New()
	w := New(s, func([]byte) error { return nil }, metrics.New(), logging.New(nil, logging.LevelInfo), 1)
	wh := domain.Webhook{ID: "w", Rail: domain.RailCard, ExternalEventID: "evt"}
	w.Enqueue(wh)
	w.Enqueue(wh) // should drop (queue full, not yet started)
	if w.Backlog() != 1 {
		t.Fatalf("backlog = %d, want 1", w.Backlog())
	}
}

func TestIdleTimeout(t *testing.T) {
	s := store.New()
	w := New(s, func([]byte) error { return nil }, metrics.New(), logging.New(nil, logging.LevelInfo), 4)
	w.Enqueue(domain.Webhook{ID: "w", Rail: domain.RailCard, ExternalEventID: "evt"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	err := w.Idle(ctx, time.Millisecond)
	if err == nil {
		t.Fatal("expected context deadline exceeded when worker not started")
	}
}