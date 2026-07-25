package otel

import (
	"context"
	"os"
	"testing"
)

func TestInitNoEndpointNoop(t *testing.T) {
	os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	shutdown, err := Init("test-svc")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if shutdown == nil {
		t.Fatal("expected non-nil shutdown")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown should be no-op: %v", err)
	}
}

func TestInitNoEndpointExplicitEmpty(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "   ")
	shutdown, err := Init("test-svc")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}