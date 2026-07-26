package rail

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ai-crypto-onramp/orchestrator-fiat/internal/domain"
)

func newIntent(rail domain.Rail, amount int64) *domain.Intent {
	return &domain.Intent{ID: "p1", Rail: rail, Amount: amount, Currency: "USD", PayerRef: "payer1"}
}

func TestHTTPAdapterAuthorize(t *testing.T) {
	var gotPath, gotRail, gotAmount string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		var req authorizeReq
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotRail = req.Rail
		gotAmount = req.Amount
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"status":"authorized","rail_ref":"ext-p1"}`)
	}))
	defer srv.Close()
	a := NewHTTP(srv.URL, "card")
	i := newIntent(domain.RailCard, 1000)
	if err := a.Authorize(i); err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if gotPath != "/v1/authorize" {
		t.Errorf("path = %q, want /v1/authorize", gotPath)
	}
	if gotRail != "card" {
		t.Errorf("rail = %q, want card", gotRail)
	}
	if gotAmount != "1000" {
		t.Errorf("amount = %q, want 1000", gotAmount)
	}
	if i.ExternalID != "ext-p1" {
		t.Errorf("external id = %q, want ext-p1", i.ExternalID)
	}
}

func TestHTTPAdapterAuthorizeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, `{"status":"failed","error_code":"RAIL_DOWN","error_message":"rail unavailable"}`)
	}))
	defer srv.Close()
	a := NewHTTP(srv.URL, "card")
	err := a.Authorize(newIntent(domain.RailCard, 1000))
	if err == nil || !strings.Contains(err.Error(), "rail authorize failed") {
		t.Fatalf("expected ErrAuthorize wrap, got %v", err)
	}
	if !strings.Contains(err.Error(), "RAIL_DOWN") {
		t.Fatalf("expected error code in message, got %v", err)
	}
}

func TestHTTPAdapterCapture(t *testing.T) {
	var gotPath, gotAmount string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		var req amountReq
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotAmount = req.Amount
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"status":"captured"}`)
	}))
	defer srv.Close()
	a := NewHTTP(srv.URL, "card")
	i := newIntent(domain.RailCard, 1000)
	i.ExternalID = "ext-p1"
	if err := a.Capture(i, 500); err != nil {
		t.Fatalf("capture: %v", err)
	}
	if gotPath != "/v1/capture/ext-p1" {
		t.Errorf("path = %q, want /v1/capture/ext-p1", gotPath)
	}
	if gotAmount != "500" {
		t.Errorf("amount = %q, want 500", gotAmount)
	}
}

func TestHTTPAdapterRefund(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"status":"refunded"}`)
	}))
	defer srv.Close()
	a := NewHTTP(srv.URL, "ach")
	i := newIntent(domain.RailACH, 1000)
	i.ExternalID = "ext-p1"
	if err := a.Refund(i, 100); err != nil {
		t.Fatalf("refund: %v", err)
	}
	if gotPath != "/v1/refund/ext-p1" {
		t.Errorf("path = %q, want /v1/refund/ext-p1", gotPath)
	}
}

func TestHTTPAdapterSubmitCapturesSettleAmount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"status":"captured","rail_ref":"ext-p1","settle_amount":"1000"}`)
	}))
	defer srv.Close()
	a := NewHTTP(srv.URL, "pix")
	i := newIntent(domain.RailPIX, 1000)
	if err := a.Submit(i); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if i.ExternalID != "ext-p1" {
		t.Errorf("external id = %q, want ext-p1", i.ExternalID)
	}
	if i.CapturedAmount != 1000 {
		t.Errorf("captured = %d, want 1000", i.CapturedAmount)
	}
}

func TestHTTPAdapterVoid(t *testing.T) {
	var gotAmount string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req amountReq
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotAmount = req.Amount
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"status":"voided"}`)
	}))
	defer srv.Close()
	a := NewHTTP(srv.URL, "card")
	if err := a.Void(newIntent(domain.RailCard, 1000)); err != nil {
		t.Fatalf("void: %v", err)
	}
	if gotAmount != "0" {
		t.Errorf("amount = %q, want 0", gotAmount)
	}
}

func TestHTTPAdapterVerify3DSNonCard(t *testing.T) {
	a := NewHTTP("http://example", "ach")
	if err := a.Verify3DS(newIntent(domain.RailACH, 1000), "ok"); err != ErrUnsupported3DS {
		t.Fatalf("expected ErrUnsupported3DS, got %v", err)
	}
}

func TestHTTPAdapterVerify3DSFailAssertion(t *testing.T) {
	a := NewHTTP("http://example", "card")
	if err := a.Verify3DS(newIntent(domain.RailCard, 1000), ""); err != Err3DSVerify {
		t.Fatalf("expected Err3DSVerify, got %v", err)
	}
	if err := a.Verify3DS(newIntent(domain.RailCard, 1000), "fail"); err != Err3DSVerify {
		t.Fatalf("expected Err3DSVerify, got %v", err)
	}
}

func TestHTTPAdapterVerify3DSStatusFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"status":"failed"}`)
	}))
	defer srv.Close()
	a := NewHTTP(srv.URL, "card")
	if err := a.Verify3DS(newIntent(domain.RailCard, 1000), "ok"); err != Err3DSVerify {
		t.Fatalf("expected Err3DSVerify, got %v", err)
	}
}

func TestHTTPAdapterVerify3DSOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"status":"authorized"}`)
	}))
	defer srv.Close()
	a := NewHTTP(srv.URL, "card")
	if err := a.Verify3DS(newIntent(domain.RailCard, 1000), "ok"); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestRegistryFromMapFallsBack(t *testing.T) {
	d := NewDummy()
	card := NewHTTP("http://card", "card")
	r := NewRegistryFromMap(map[domain.Rail]Adapter{domain.RailCard: card}, d)
	if r.For(domain.RailCard) != card {
		t.Fatal("expected card adapter for card rail")
	}
	if r.For(domain.RailACH) != d {
		t.Fatal("expected fallback dummy for ACH rail")
	}
}

func TestRegistryFromMapNoFallback(t *testing.T) {
	card := NewHTTP("http://card", "card")
	r := NewRegistryFromMap(map[domain.Rail]Adapter{domain.RailCard: card}, nil)
	if r.For(domain.RailCard) != card {
		t.Fatal("expected card adapter for card rail")
	}
	if r.For(domain.RailACH) != nil {
		t.Fatal("expected nil fallback for ACH rail")
	}
}