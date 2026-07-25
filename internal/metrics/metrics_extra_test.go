package metrics

import (
	"strings"
	"testing"
	"time"
)

func TestIntToStrNegative(t *testing.T) {
	if got := intToStr(-42); got != "-42" {
		t.Fatalf("intToStr(-42) = %q", got)
	}
	if got := intToStr(0); got != "0" {
		t.Fatalf("intToStr(0) = %q", got)
	}
	if got := intToStr(42); got != "42" {
		t.Fatalf("intToStr(42) = %q", got)
	}
}

func TestFloatToStrNegative(t *testing.T) {
	out := floatToStr(-1.5)
	if !strings.HasPrefix(out, "-") {
		t.Fatalf("floatToStr(-1.5) = %q, want negative", out)
	}
	if got := floatToStr(0); got != "0" {
		t.Fatalf("floatToStr(0) = %q", got)
	}
}

func TestAppendIntZero(t *testing.T) {
	if got := appendInt(nil, 0); string(got) != "0" {
		t.Fatalf("appendInt(0) = %q", got)
	}
	if got := appendInt(nil, 123); string(got) != "123" {
		t.Fatalf("appendInt(123) = %q", got)
	}
}

func TestMetricsLatencyTrimOver1000(t *testing.T) {
	m := New()
	for i := 0; i < 1100; i++ {
		m.ObserveIntentCreation(time.Duration(i) * time.Microsecond)
	}
	out := m.FormatPrometheus()
	if !strings.Contains(out, "payment_intent_creation_p99_latency_seconds") {
		t.Fatalf("expected p99 line: %s", out)
	}
}

func TestMetricsP99Empty(t *testing.T) {
	m := New()
	if m.p99() != 0 {
		t.Fatal("p99 of empty should be 0")
	}
}

func TestMetricsWebhookBacklogNegative(t *testing.T) {
	m := New()
	m.DecWebhookBacklog()
	m.DecWebhookBacklog()
	out := m.FormatPrometheus()
	if !strings.Contains(out, "payment_webhook_backlog -2") {
		t.Fatalf("expected backlog -2: %s", out)
	}
}

func TestMetricsHandlerLarge(t *testing.T) {
	m := New()
	for i := 0; i < 50; i++ {
		m.IncTransition("captured")
		m.ObserveIntentCreation(time.Millisecond)
	}
	out := m.FormatPrometheus()
	if !strings.Contains(out, "captured") {
		t.Fatalf("expected captured: %s", out)
	}
}