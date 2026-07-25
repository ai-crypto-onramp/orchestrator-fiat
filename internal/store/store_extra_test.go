package store

import (
	"testing"

	"github.com/ai-crypto-onramp/orchestrator-fiat/internal/domain"
)

func TestErrNotFoundMessage(t *testing.T) {
	if got := ErrNotFound.Error(); got != "intent not found" {
		t.Fatalf("ErrNotFound.Error() = %q", got)
	}
}

func TestErrDuplicateWebhookMessage(t *testing.T) {
	if got := ErrDuplicateWebhook.Error(); got != "duplicate webhook" {
		t.Fatalf("ErrDuplicateWebhook.Error() = %q", got)
	}
}

func TestWebhookKeyHelper(t *testing.T) {
	if got := webhookKey(domain.RailCard, "evt"); got != "card\x00evt" {
		t.Fatalf("webhookKey = %q", got)
	}
}

func TestCloneIntentNilHistory(t *testing.T) {
	i := &domain.Intent{ID: "i1"}
	clone := cloneIntent(i)
	if clone == nil || clone.ID != "i1" {
		t.Fatalf("clone = %+v", clone)
	}
}