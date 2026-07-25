package config

import (
	"os"
	"testing"
	"time"

	"github.com/ai-crypto-onramp/payment-orchestrator/internal/domain"
)

func setEnv(t *testing.T, k, v string) {
	t.Helper()
	old, had := os.LookupEnv(k)
	if v == "" {
		_ = os.Unsetenv(k)
	} else {
		_ = os.Setenv(k, v)
	}
	if had {
		t.Cleanup(func() { _ = os.Setenv(k, old) })
	} else {
		t.Cleanup(func() { _ = os.Unsetenv(k) })
	}
}

func TestFromEnvDefaults(t *testing.T) {
	for _, k := range []string{
		"PORT", "RAIL_CARD_URL", "RAIL_ACH_URL", "RAIL_SEPA_URL", "RAIL_PIX_URL", "RAIL_UPI_URL",
		"FRAUD_URL", "THREE_DS_MPI_URL",
		"WEBHOOK_SECRET_CARD", "WEBHOOK_SECRET_ACH", "WEBHOOK_SECRET_SEPA", "WEBHOOK_SECRET_PIX", "WEBHOOK_SECRET_UPI",
		"WEBHOOK_REPLAY_WINDOW", "IDEMPOTENCY_KEY_TTL", "LOG_LEVEL",
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"MTLS_CERT_FILE", "MTLS_KEY_FILE", "MTLS_CA_FILE",
	} {
		setEnv(t, k, "")
	}
	c := FromEnv()
	if c.Port != "8080" {
		t.Fatalf("Port = %q, want 8080", c.Port)
	}
	if c.WebhookReplayWindow != 5*time.Minute {
		t.Fatalf("ReplayWindow = %v", c.WebhookReplayWindow)
	}
	if c.IdempotencyKeyTTL != 24*time.Hour {
		t.Fatalf("IdempotencyKeyTTL = %v", c.IdempotencyKeyTTL)
	}
	if c.LogLevel != "info" {
		t.Fatalf("LogLevel = %q", c.LogLevel)
	}
	if c.OTLPEndpoint != "" {
		t.Fatalf("OTLPEndpoint = %q", c.OTLPEndpoint)
	}
	if c.MTLS.Enabled() {
		t.Fatal("MTLS should not be enabled")
	}
}

func TestFromEnvOverrides(t *testing.T) {
	setEnv(t, "PORT", "9090")
	setEnv(t, "RAIL_CARD_URL", "http://card")
	setEnv(t, "WEBHOOK_SECRET_CARD", "card-secret")
	setEnv(t, "WEBHOOK_REPLAY_WINDOW", "10m")
	setEnv(t, "IDEMPOTENCY_KEY_TTL", "1h")
	setEnv(t, "LOG_LEVEL", "DEBUG")
	setEnv(t, "OTEL_EXPORTER_OTLP_ENDPOINT", "otel:4317")
	setEnv(t, "MTLS_CERT_FILE", "cert.pem")
	setEnv(t, "MTLS_KEY_FILE", "key.pem")
	setEnv(t, "MTLS_CA_FILE", "ca.pem")
	c := FromEnv()
	if c.Port != "9090" {
		t.Fatalf("Port = %q", c.Port)
	}
	if c.RailURLs[domain.RailCard] != "http://card" {
		t.Fatalf("RailURLs[card] = %q", c.RailURLs[domain.RailCard])
	}
	if c.WebhookSecrets[domain.RailCard] != "card-secret" {
		t.Fatalf("WebhookSecret[card] = %q", c.WebhookSecrets[domain.RailCard])
	}
	if c.WebhookReplayWindow != 10*time.Minute {
		t.Fatalf("ReplayWindow = %v", c.WebhookReplayWindow)
	}
	if c.IdempotencyKeyTTL != time.Hour {
		t.Fatalf("IdempotencyKeyTTL = %v", c.IdempotencyKeyTTL)
	}
	if c.LogLevel != "debug" {
		t.Fatalf("LogLevel = %q", c.LogLevel)
	}
	if c.OTLPEndpoint != "otel:4317" {
		t.Fatalf("OTLPEndpoint = %q", c.OTLPEndpoint)
	}
	if !c.MTLS.Enabled() {
		t.Fatal("MTLS should be enabled")
	}
}

func TestFromEnvNumericDuration(t *testing.T) {
	setEnv(t, "WEBHOOK_REPLAY_WINDOW", "120")
	c := FromEnv()
	if c.WebhookReplayWindow != 120*time.Second {
		t.Fatalf("ReplayWindow = %v, want 120s", c.WebhookReplayWindow)
	}
}

func TestFromEnvInvalidDurationFallback(t *testing.T) {
	setEnv(t, "WEBHOOK_REPLAY_WINDOW", "not-a-duration")
	c := FromEnv()
	if c.WebhookReplayWindow != 5*time.Minute {
		t.Fatalf("ReplayWindow = %v, want default 5m", c.WebhookReplayWindow)
	}
}

func TestEnabledRails(t *testing.T) {
	setEnv(t, "RAIL_CARD_URL", "http://card")
	setEnv(t, "RAIL_PIX_URL", "http://pix")
	for _, k := range []string{"RAIL_ACH_URL", "RAIL_SEPA_URL", "RAIL_UPI_URL"} {
		setEnv(t, k, "")
	}
	c := FromEnv()
	enabled := c.EnabledRails()
	if len(enabled) != 2 {
		t.Fatalf("expected 2 enabled rails, got %v", enabled)
	}
}

func TestWebhookSecretFallbackToGeneric(t *testing.T) {
	setEnv(t, "WEBHOOK_SECRET_CARD", "")
	setEnv(t, "WEBHOOK_SECRET", "generic-secret")
	c := FromEnv()
	if got := c.WebhookSecret(domain.RailCard); got != "generic-secret" {
		t.Fatalf("WebhookSecret(card) = %q", got)
	}
}

func TestWebhookSecretPerRail(t *testing.T) {
	setEnv(t, "WEBHOOK_SECRET_CARD", "card-only")
	setEnv(t, "WEBHOOK_SECRET", "generic")
	c := FromEnv()
	if got := c.WebhookSecret(domain.RailCard); got != "card-only" {
		t.Fatalf("WebhookSecret(card) = %q, want card-only", got)
	}
}

func TestDevModeAndMustEnv(t *testing.T) {
	setEnv(t, "DEV_MODE", "1")
	if !DevMode() {
		t.Fatal("DevMode should be true")
	}
	for _, k := range []string{"SOME_TEST_UNSET", "DEV_MODE"} {
		setEnv(t, k, "")
	}
	if DevMode() {
		t.Fatal("DevMode should be false when unset")
	}
}

func TestMustEnvDevModeUnset(t *testing.T) {
	setEnv(t, "DEV_MODE", "1")
	for _, k := range []string{"UNSET_VAR_X", "DEV_MODE"} {
		setEnv(t, k, "")
	}
	setEnv(t, "DEV_MODE", "1")
	setEnv(t, "UNSET_VAR_X", "")
	if got := MustEnv("UNSET_VAR_X"); got != "" {
		t.Fatalf("MustEnv dev mode should return empty, got %q", got)
	}
}

func TestMustEnvSetReturnsValue(t *testing.T) {
	setEnv(t, "DEV_MODE", "1")
	setEnv(t, "SET_VAR_X", "value-x")
	if got := MustEnv("SET_VAR_X"); got != "value-x" {
		t.Fatalf("MustEnv = %q, want value-x", got)
	}
}

func TestMustEnvOrFatalDevMode(t *testing.T) {
	setEnv(t, "DEV_MODE", "1")
	setEnv(t, "UNSET_VAR_Y", "")
	if got := MustEnvOrFatal("UNSET_VAR_Y", "custom msg"); got != "" {
		t.Fatalf("MustEnvOrFatal dev mode = %q, want empty", got)
	}
}

func TestMustEnvOrFatalSet(t *testing.T) {
	setEnv(t, "DEV_MODE", "1")
	setEnv(t, "SET_VAR_Y", "value-y")
	if got := MustEnvOrFatal("SET_VAR_Y", "custom msg"); got != "value-y" {
		t.Fatalf("MustEnvOrFatal = %q", got)
	}
}

func TestGetenvDefault(t *testing.T) {
	setEnv(t, "UNSET_GETENV_TEST", "")
	if got := getenv("UNSET_GETENV_TEST", "def"); got != "def" {
		t.Fatalf("getenv default = %q", got)
	}
}

func TestGetdurationZeroUsesDefault(t *testing.T) {
	setEnv(t, "DURATION_TEST", "")
	if got := getduration("DURATION_TEST", 3*time.Second); got != 3*time.Second {
		t.Fatalf("getduration default = %v", got)
	}
}