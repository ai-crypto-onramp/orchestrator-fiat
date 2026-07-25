package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ai-crypto-onramp/payment-orchestrator/internal/domain"
	"github.com/ai-crypto-onramp/payment-orchestrator/internal/fraud"
	"github.com/ai-crypto-onramp/payment-orchestrator/internal/rail"
)

type errFraudClient struct{ err error }

func (c *errFraudClient) Score(_ context.Context, _ *domain.Intent) (fraud.Decision, error) {
	return fraud.Decision{}, c.err
}

func TestCreateIntentFraudScoreError(t *testing.T) {
	svc, _, st := newTestService(t, rail.NewDummy())
	svc.Fraud = &errFraudClient{err: errors.New("fraud service down")}
	mux := NewMux(svc)

	rec := doJSON(t, mux, http.MethodPost, "/v1/payments/intents", "k", map[string]interface{}{
		"rail": "card", "amount": 500, "currency": "USD", "payer_ref": "p1",
	})
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("code = %d, want 502 (body=%s)", rec.Code, rec.Body.String())
	}
	intents := st.ListIntents("", "card")
	if len(intents) != 1 || intents[0].Status != domain.StatusFailed {
		t.Fatalf("expected 1 failed card intent, got %d (%v)", len(intents), intents)
	}
}

func TestCreateIntentInstantSubmitFailure(t *testing.T) {
	dummy := rail.NewDummy()
	dummy.FailAuthorize = true
	svc, _, st := newTestService(t, dummy)
	mux := NewMux(svc)

	rec := doJSON(t, mux, http.MethodPost, "/v1/payments/intents", "k", map[string]interface{}{
		"rail": "pix", "amount": 500, "currency": "BRL", "payer_ref": "p1",
	})
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("code = %d, want 502 (body=%s)", rec.Code, rec.Body.String())
	}
	intents := st.ListIntents("", "pix")
	if len(intents) != 1 {
		t.Fatalf("expected 1 pix intent, got %d", len(intents))
	}
	if intents[0].Status != domain.StatusFailed {
		t.Fatalf("status = %q, want failed", intents[0].Status)
	}
}

func TestCaptureInvalidJSON(t *testing.T) {
	svc, _, _ := newTestService(t, rail.NewDummy())
	mux := NewMux(svc)

	req := httptest.NewRequest(http.MethodPost, "/v1/payments/x/capture", bytes.NewReader([]byte("not json")))
	req.Header.Set("Idempotency-Key", "k")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rec.Code)
	}
}

func TestCaptureZeroAmount(t *testing.T) {
	svc, _, _ := newTestService(t, rail.NewDummy())
	mux := NewMux(svc)

	rec1 := doJSON(t, mux, http.MethodPost, "/v1/payments/intents", "k", map[string]interface{}{
		"rail": "card", "amount": 1000, "currency": "USD", "payer_ref": "p1",
	})
	id := decodeBody(t, rec1)["id"].(string)
	zero := int64(0)
	rec2 := doJSON(t, mux, http.MethodPost, "/v1/payments/"+id+"/capture", "c", map[string]interface{}{"amount": zero})
	if rec2.Code != http.StatusBadGateway {
		t.Fatalf("code = %d, want 502 (body=%s)", rec2.Code, rec2.Body.String())
	}
}

func TestCaptureExceedsTotalAfterPartial(t *testing.T) {
	svc, _, st := newTestService(t, rail.NewDummy())
	mux := NewMux(svc)

	rec1 := doJSON(t, mux, http.MethodPost, "/v1/payments/intents", "k", map[string]interface{}{
		"rail": "card", "amount": 1000, "currency": "USD", "payer_ref": "p1",
	})
	id := decodeBody(t, rec1)["id"].(string)

	partial := int64(300)
	doJSON(t, mux, http.MethodPost, "/v1/payments/"+id+"/capture", "c1", map[string]interface{}{"amount": partial})

	tooMuch := int64(800)
	rec3 := doJSON(t, mux, http.MethodPost, "/v1/payments/"+id+"/capture", "c2", map[string]interface{}{"amount": tooMuch})
	if rec3.Code != http.StatusBadGateway {
		t.Fatalf("code = %d, want 502 (body=%s)", rec3.Code, rec3.Body.String())
	}
	if st.GetIntent(id).CapturedAmount != 300 {
		t.Fatalf("captured = %d, want 300", st.GetIntent(id).CapturedAmount)
	}
}

func TestRefundInvalidJSON(t *testing.T) {
	svc, _, _ := newTestService(t, rail.NewDummy())
	mux := NewMux(svc)

	req := httptest.NewRequest(http.MethodPost, "/v1/payments/x/refund", bytes.NewReader([]byte("not json")))
	req.Header.Set("Idempotency-Key", "k")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rec.Code)
	}
}

func TestRefundZeroAmount(t *testing.T) {
	svc, _, _ := newTestService(t, rail.NewDummy())
	mux := NewMux(svc)

	rec1 := doJSON(t, mux, http.MethodPost, "/v1/payments/intents", "k", map[string]interface{}{
		"rail": "card", "amount": 1000, "currency": "USD", "payer_ref": "p1",
	})
	id := decodeBody(t, rec1)["id"].(string)
	doJSON(t, mux, http.MethodPost, "/v1/payments/"+id+"/capture", "c", map[string]interface{}{})

	zero := int64(0)
	rec2 := doJSON(t, mux, http.MethodPost, "/v1/payments/"+id+"/refund", "r", map[string]interface{}{"amount": zero})
	if rec2.Code != http.StatusBadGateway {
		t.Fatalf("code = %d, want 502 (body=%s)", rec2.Code, rec2.Body.String())
	}
}

func TestVoidTransitionError(t *testing.T) {
	svc, _, st := newTestService(t, rail.NewDummy())
	mux := NewMux(svc)

	st.CreateIntent(&domain.Intent{
		ID: "void-failed", Rail: domain.RailCard, Amount: 1000, Currency: "USD",
		PayerRef: "p1", Status: domain.StatusFailed, CreatedAt: time.Now().UTC(),
	})

	rec := doJSON(t, mux, http.MethodPost, "/v1/payments/void-failed/void", "k", map[string]interface{}{})
	if rec.Code != http.StatusConflict {
		t.Fatalf("code = %d, want 409 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestWebhookNoSecretConfigured(t *testing.T) {
	svc, _, _ := newTestService(t, rail.NewDummy())
	svc.WebhookKeys = map[domain.Rail][]byte{domain.RailCard: []byte("dev-secret")}
	mux := NewMux(svc)

	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/ach", bytes.NewReader([]byte("{}")))
	req.Header.Set("X-Webhook-Signature", "anything")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", rec.Code)
	}
}

func TestWebhookEmptySecretForRail(t *testing.T) {
	svc, _, _ := newTestService(t, rail.NewDummy())
	svc.WebhookKeys[domain.RailACH] = nil
	mux := NewMux(svc)

	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/ach", bytes.NewReader([]byte("{}")))
	req.Header.Set("X-Webhook-Signature", "x")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", rec.Code)
	}
}

func TestLookupIdemExpiredEntry(t *testing.T) {
	svc, _, _ := newTestService(t, rail.NewDummy())
	svc.idemCache["create\x00k-expired"] = idemEntry{
		status:  http.StatusCreated,
		body:    []byte(`{"x":1}`),
		expires: time.Now().Add(-time.Hour),
	}
	if _, _, ok := svc.lookupIdem("create", "k-expired"); ok {
		t.Fatal("expired entry should not be found")
	}
}

func TestSaveIdemEmptyKeyNoop(t *testing.T) {
	svc, _, _ := newTestService(t, rail.NewDummy())
	svc.saveIdem("create", "", http.StatusOK, []byte("x"))
	if len(svc.idemCache) != 0 {
		t.Fatal("empty key should not save")
	}
}

func TestReadAllBodyHelper(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte("hello")))
	b, err := ReadAllBody(req)
	if err != nil {
		t.Fatalf("ReadAllBody: %v", err)
	}
	if string(b) != "hello" {
		t.Fatalf("body = %q", b)
	}
}