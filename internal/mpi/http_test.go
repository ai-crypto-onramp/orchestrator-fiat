package mpi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ai-crypto-onramp/orchestrator-fiat/internal/domain"
)

func newCardIntent() *domain.Intent {
	return &domain.Intent{ID: "p1", Rail: domain.RailCard, Amount: 1000, Currency: "USD", PayerRef: "payer1"}
}

func TestHTTPClientChallenge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/3ds/challenge" {
			t.Errorf("path = %q, want /v1/3ds/challenge", r.URL.Path)
		}
		var req challengeReq
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.PaymentID != "p1" {
			t.Errorf("payment_id = %q", req.PaymentID)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"acs_url":"https://acs.example/3ds/p1","payload":"payload-p1"}`))
	}))
	defer srv.Close()
	c := NewHTTP(srv.URL)
	ch, err := c.Challenge(newCardIntent())
	if err != nil {
		t.Fatalf("challenge: %v", err)
	}
	if ch.ACSURL != "https://acs.example/3ds/p1" {
		t.Errorf("acs_url = %q", ch.ACSURL)
	}
	if ch.Payload != "payload-p1" {
		t.Errorf("payload = %q", ch.Payload)
	}
}

func TestHTTPClientChallengeNonCard(t *testing.T) {
	c := NewHTTP("http://example")
	_, err := c.Challenge(&domain.Intent{ID: "p1", Rail: domain.RailACH})
	if err == nil || !strings.Contains(err.Error(), "card rail") {
		t.Fatalf("expected non-card error, got %v", err)
	}
}

func TestHTTPClientChallengeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "mpi down", http.StatusBadGateway)
	}))
	defer srv.Close()
	c := NewHTTP(srv.URL)
	_, err := c.Challenge(newCardIntent())
	if err == nil || !strings.Contains(err.Error(), "challenge failed") {
		t.Fatalf("expected ErrChallengeFailed wrap, got %v", err)
	}
}

func TestHTTPClientResumeOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/3ds/resume" {
			t.Errorf("path = %q, want /v1/3ds/resume", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := NewHTTP(srv.URL)
	if err := c.Resume(newCardIntent(), "assertion-1"); err != nil {
		t.Fatalf("resume: %v", err)
	}
}

func TestHTTPClientResumeFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad assertion", http.StatusBadRequest)
	}))
	defer srv.Close()
	c := NewHTTP(srv.URL)
	err := c.Resume(newCardIntent(), "bad")
	if err == nil || !strings.Contains(err.Error(), "challenge failed") {
		t.Fatalf("expected ErrChallengeFailed wrap, got %v", err)
	}
	if err == ErrTimeout {
		t.Fatal("should not be timeout")
	}
}

func TestHTTPClientResumeTimeout(t *testing.T) {
	for _, code := range []int{http.StatusRequestTimeout, http.StatusGatewayTimeout} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "timeout", code)
		}))
		c := NewHTTP(srv.URL)
		err := c.Resume(newCardIntent(), "x")
		if err != ErrTimeout {
			t.Fatalf("status %d: expected ErrTimeout, got %v", code, err)
		}
		srv.Close()
	}
}