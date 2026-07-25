package fraud

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ai-crypto-onramp/orchestrator-fiat/internal/domain"
)

func TestHTTPClientScoreOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/fraud/score" {
			t.Errorf("path = %q, want /v1/fraud/score", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewHTTP(srv.URL)
	dec, err := c.Score(context.Background(), &domain.Intent{ID: "i1"})
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	if !dec.Allowed {
		t.Fatal("expected allowed on 200")
	}
}

func TestHTTPClientScoreErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	c := NewHTTP(srv.URL)
	_, err := c.Score(context.Background(), &domain.Intent{ID: "i1"})
	if err == nil {
		t.Fatal("expected error on 403")
	}
}

func TestHTTPClientScoreRequestFailure(t *testing.T) {
	c := NewHTTP("http://127.0.0.1:0")
	_, err := c.Score(context.Background(), &domain.Intent{ID: "i1"})
	if err == nil {
		t.Fatal("expected error on unreachable server")
	}
}

func TestErrBlockedIsSentinel(t *testing.T) {
	if !errors.Is(ErrBlocked, ErrBlocked) {
		t.Fatal("ErrBlocked should satisfy errors.Is")
	}
}