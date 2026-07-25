package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ai-crypto-onramp/payment-orchestrator/internal/config"
	"github.com/ai-crypto-onramp/payment-orchestrator/internal/domain"
	"github.com/ai-crypto-onramp/payment-orchestrator/internal/mpi"
	"github.com/ai-crypto-onramp/payment-orchestrator/internal/rail"
)

func TestApplyConfigSetsWebhookKeysAndReplay(t *testing.T) {
	svc, _, _ := newTestService(t, rail.NewDummy())
	svc.ApplyConfig(config.Config{
		WebhookSecrets: map[domain.Rail]string{
			domain.RailCard: "card-secret",
			domain.RailACH:  "", // empty should be skipped
		},
		WebhookReplayWindow: 7 * time.Minute,
		LogLevel:            "warn",
	})
	if got := svc.WebhookKeys[domain.RailCard]; string(got) != "card-secret" {
		t.Fatalf("card key = %q", got)
	}
	if _, ok := svc.WebhookKeys[domain.RailACH]; ok {
		t.Fatal("ach empty secret should not be set")
	}
	if svc.ReplayWindow != 7*time.Minute {
		t.Fatalf("replay = %v", svc.ReplayWindow)
	}
	if svc.Logger == nil {
		t.Fatal("logger nil")
	}
}

func TestApplyConfigEmptyKeepsDefaults(t *testing.T) {
	svc, _, _ := newTestService(t, rail.NewDummy())
	orig := svc.WebhookKeys[domain.RailCard]
	svc.ApplyConfig(config.Config{})
	if svc.WebhookKeys[domain.RailCard] == nil || string(svc.WebhookKeys[domain.RailCard]) != string(orig) {
		t.Fatalf("ApplyConfig with empty secrets should keep default keys; got %v", svc.WebhookKeys[domain.RailCard])
	}
	if svc.Logger == nil {
		t.Fatal("logger nil")
	}
}

func TestThreeDSChallengeTimedOut(t *testing.T) {
	svc, _, st := newTestService(t, rail.NewDummy())
	svc.ReplayWindow = 10 * time.Millisecond
	mux := NewMux(svc)

	now := time.Now().UTC()
	intent := &domain.Intent{
		ID:    "intent-3ds-old",
		Rail:  domain.RailCard,
		Amount: 500,
		Currency: "USD",
		PayerRef: "p1",
		Status: domain.Status3DSPending,
		CreatedAt: now,
		UpdatedAt: now,
		History: []domain.Event{
			{Type: domain.EventCreated, At: now},
			{Type: domain.Event3DSPending, At: now.Add(-time.Hour)},
		},
	}
	st.CreateIntent(intent)

	time.Sleep(20 * time.Millisecond)

	rec := doJSON(t, mux, http.MethodPost, "/v1/payments/intent-3ds-old/3ds-challenge", "k", map[string]interface{}{
		"challenge_result": "ok",
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("code = %d, want 409 (body=%s)", rec.Code, rec.Body.String())
	}
	if st.GetIntent("intent-3ds-old").Status != domain.StatusFailed {
		t.Fatalf("status = %q, want failed", st.GetIntent("intent-3ds-old").Status)
	}
}

func TestThreeDSChallengeMPIResumeFailure(t *testing.T) {
	svc, _, st := newTestService(t, rail.NewDummy())
	svc.MPI = &mpi.DummyClient{FailResume: true}
	svc.ReplayWindow = 0 // disable expiry so resume path runs
	mux := NewMux(svc)

	rec1 := doJSON(t, mux, http.MethodPost, "/v1/payments/intents", "k", map[string]interface{}{
		"rail": "card", "amount": 500, "currency": "USD", "payer_ref": "p1", "three_ds_required": true,
	})
	id := decodeBody(t, rec1)["id"].(string)

	rec2 := doJSON(t, mux, http.MethodPost, "/v1/payments/"+id+"/3ds-challenge", "k2", map[string]interface{}{
		"challenge_result": "ok",
	})
	if rec2.Code != http.StatusBadGateway {
		t.Fatalf("code = %d, want 502 (body=%s)", rec2.Code, rec2.Body.String())
	}
	if st.GetIntent(id).Status != domain.StatusFailed {
		t.Fatalf("status = %q, want failed", st.GetIntent(id).Status)
	}
}

func TestThreeDSChallengeAuthorizeFailure(t *testing.T) {
	dummy := rail.NewDummy()
	dummy.FailAuthorize = true
	svc, _, st := newTestService(t, dummy)
	mux := NewMux(svc)

	rec1 := doJSON(t, mux, http.MethodPost, "/v1/payments/intents", "k", map[string]interface{}{
		"rail": "card", "amount": 500, "currency": "USD", "payer_ref": "p1", "three_ds_required": true,
	})
	id := decodeBody(t, rec1)["id"].(string)

	rec2 := doJSON(t, mux, http.MethodPost, "/v1/payments/"+id+"/3ds-challenge", "k2", map[string]interface{}{
		"challenge_result": "ok",
	})
	if rec2.Code != http.StatusBadGateway {
		t.Fatalf("code = %d, want 502 (body=%s)", rec2.Code, rec2.Body.String())
	}
	if st.GetIntent(id).Status != domain.StatusFailed {
		t.Fatalf("status = %q, want failed", st.GetIntent(id).Status)
	}
}

func TestListPaymentsEmptyReturnsArray(t *testing.T) {
	svc, _, _ := newTestService(t, rail.NewDummy())
	mux := NewMux(svc)
	rec := doJSON(t, mux, http.MethodGet, "/v1/payments", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	body := decodeBody(t, rec)
	payments, ok := body["payments"].([]interface{})
	if !ok {
		t.Fatalf("payments not array: %v", body["payments"])
	}
	if len(payments) != 0 {
		t.Fatalf("expected empty array, got %d", len(payments))
	}
}

func TestProcessWebhookEmptyTypeAppendsEvent(t *testing.T) {
	svc, _, st := newTestService(t, rail.NewDummy())
	mux := NewMux(svc)

	rec1 := doJSON(t, mux, http.MethodPost, "/v1/payments/intents", "k", map[string]interface{}{
		"rail": "card", "amount": 1000, "currency": "USD", "payer_ref": "p1",
	})
	id := decodeBody(t, rec1)["id"].(string)

	payload := map[string]interface{}{"payment_id": id, "type": ""}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/card", bytes.NewReader(body))
	req.Header.Set("X-Webhook-Signature", signBody([]byte("dev-secret"), body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d (body=%s)", rec.Code, rec.Body.String())
	}
	i := st.GetIntent(id)
	found := false
	for _, e := range i.History {
		if e.Type == domain.EventWebhook {
			found = true
		}
	}
	if !found {
		t.Fatal("expected webhook event in history")
	}
}

func TestWebhookSettlementIgnoredForNonCaptured(t *testing.T) {
	svc, _, st := newTestService(t, rail.NewDummy())
	mux := NewMux(svc)

	rec1 := doJSON(t, mux, http.MethodPost, "/v1/payments/intents", "k", map[string]interface{}{
		"rail": "card", "amount": 1000, "currency": "USD", "payer_ref": "p1",
	})
	id := decodeBody(t, rec1)["id"].(string)

	payload := map[string]interface{}{
		"payment_id": id, "type": "settlement", "amount": 1000,
		"external_event_id": "evt-set-ignored", "rail_ref": "ref-1",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/card", bytes.NewReader(body))
	req.Header.Set("X-Webhook-Signature", signBody([]byte("dev-secret"), body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	if st.GetIntent(id).Status != domain.StatusAuthorized {
		t.Fatalf("status = %q, expected unchanged authorized", st.GetIntent(id).Status)
	}
}

func TestWebhookChargebackInvalidStage(t *testing.T) {
	svc, _, _ := newTestService(t, rail.NewDummy())
	mux := NewMux(svc)

	payload := map[string]interface{}{
		"payment_id": "p1", "type": "chargeback", "amount": 1000,
		"stage": "bogus", "case_ref": "case-x", "external_event_id": "evt-bad",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/card", bytes.NewReader(body))
	req.Header.Set("X-Webhook-Signature", signBody([]byte("dev-secret"), body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestWebhookChargebackMissingCaseRef(t *testing.T) {
	svc, _, _ := newTestService(t, rail.NewDummy())
	mux := NewMux(svc)

	payload := map[string]interface{}{
		"payment_id": "p1", "type": "chargeback", "amount": 1000,
		"stage": "opened", "external_event_id": "evt-noref",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/card", bytes.NewReader(body))
	req.Header.Set("X-Webhook-Signature", signBody([]byte("dev-secret"), body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rec.Code)
	}
}

func TestWebhookChargebackOpenedIgnoredState(t *testing.T) {
	svc, _, st := newTestService(t, rail.NewDummy())
	mux := NewMux(svc)

	st.CreateIntent(&domain.Intent{
		ID: "intent-failed", Rail: domain.RailCard, Amount: 1000, Currency: "USD",
		PayerRef: "p1", Status: domain.StatusFailed, CreatedAt: time.Now().UTC(),
	})

	payload := map[string]interface{}{
		"payment_id": "intent-failed", "type": "chargeback", "amount": 1000,
		"stage": "opened", "case_ref": "case-fail", "external_event_id": "evt-fail",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/card", bytes.NewReader(body))
	req.Header.Set("X-Webhook-Signature", signBody([]byte("dev-secret"), body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	if st.GetIntent("intent-failed").Status != domain.StatusFailed {
		t.Fatal("intent status should remain failed (chargeback ignored)")
	}
}

func TestWebhookChargebackReversalNonChargedBack(t *testing.T) {
	svc, _, st := newTestService(t, rail.NewDummy())
	mux := NewMux(svc)

	st.CreateIntent(&domain.Intent{
		ID: "intent-cb", Rail: domain.RailCard, Amount: 1000, Currency: "USD",
		PayerRef: "p1", Status: domain.StatusCaptured, CreatedAt: time.Now().UTC(),
	})

	open := map[string]interface{}{
		"payment_id": "intent-cb", "type": "chargeback", "amount": 1000,
		"stage": "opened", "case_ref": "case-cb", "external_event_id": "evt-cb-open",
	}
	body, _ := json.Marshal(open)
	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/card", bytes.NewReader(body))
	req.Header.Set("X-Webhook-Signature", signBody([]byte("dev-secret"), body))
	mux.ServeHTTP(httptest.NewRecorder(), req)

	// Force intent back to captured so reversal does nothing (not in charged_back).
	_, _ = st.UpdateIntent("intent-cb", func(i *domain.Intent) error {
		i.Status = domain.StatusCaptured
		return nil
	})

	rev := map[string]interface{}{
		"payment_id": "intent-cb", "type": "chargeback", "amount": 1000,
		"stage": "reversal", "case_ref": "case-cb", "external_event_id": "evt-cb-rev",
	}
	body2, _ := json.Marshal(rev)
	req2 := httptest.NewRequest(http.MethodPost, "/v1/webhooks/card", bytes.NewReader(body2))
	req2.Header.Set("X-Webhook-Signature", signBody([]byte("dev-secret"), body2))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req2)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
}

func TestWebhookChargebackStageEvidenceProgression(t *testing.T) {
	svc, _, st := newTestService(t, rail.NewDummy())
	mux := NewMux(svc)

	st.CreateIntent(&domain.Intent{
		ID: "intent-ev", Rail: domain.RailCard, Amount: 1000, Currency: "USD",
		PayerRef: "p1", Status: domain.StatusCaptured, CreatedAt: time.Now().UTC(),
	})

	open := map[string]interface{}{
		"payment_id": "intent-ev", "type": "chargeback", "amount": 1000,
		"stage": "opened", "case_ref": "case-ev", "external_event_id": "evt-ev-open",
	}
	body, _ := json.Marshal(open)
	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/card", bytes.NewReader(body))
	req.Header.Set("X-Webhook-Signature", signBody([]byte("dev-secret"), body))
	mux.ServeHTTP(httptest.NewRecorder(), req)

	ev := map[string]interface{}{
		"payment_id": "intent-ev", "type": "chargeback", "amount": 1000,
		"stage": "evidence", "case_ref": "case-ev", "external_event_id": "evt-ev-evidence",
	}
	body2, _ := json.Marshal(ev)
	req2 := httptest.NewRequest(http.MethodPost, "/v1/webhooks/card", bytes.NewReader(body2))
	req2.Header.Set("X-Webhook-Signature", signBody([]byte("dev-secret"), body2))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req2)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	cbs := st.ChargebacksFor("intent-ev")
	if len(cbs) != 1 || cbs[0].Stage != domain.StageEvidence {
		t.Fatalf("chargeback stage = %v, want evidence", cbs)
	}
}

func TestWebhookUnknownPaymentIDDefaultBranch(t *testing.T) {
	svc, _, _ := newTestService(t, rail.NewDummy())
	mux := NewMux(svc)

	payload := map[string]interface{}{
		"payment_id": "does-not-exist", "type": "ping", "external_event_id": "evt-missing",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/card", bytes.NewReader(body))
	req.Header.Set("X-Webhook-Signature", signBody([]byte("dev-secret"), body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestProcessWebhookNoPaymentID(t *testing.T) {
	svc, _, _ := newTestService(t, rail.NewDummy())
	mux := NewMux(svc)

	payload := map[string]interface{}{"type": "ping", "external_event_id": "evt-nopay"}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/card", bytes.NewReader(body))
	req.Header.Set("X-Webhook-Signature", signBody([]byte("dev-secret"), body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	if decodeBody(t, rec)["status"] != "received" {
		t.Fatalf("status = %v, want received", decodeBody(t, rec)["status"])
	}
}

func TestIs3DSExpiredZeroWindow(t *testing.T) {
	svc, _, _ := newTestService(t, rail.NewDummy())
	svc.ReplayWindow = 0
	i := &domain.Intent{
		History: []domain.Event{{Type: domain.Event3DSPending, At: time.Now().Add(-time.Hour)}},
	}
	if svc.is3DSExpired(i) {
		t.Fatal("zero replay window should never expire")
	}
}

func TestIs3DSExpiredNoPendingEvent(t *testing.T) {
	svc, _, _ := newTestService(t, rail.NewDummy())
	svc.ReplayWindow = time.Minute
	i := &domain.Intent{
		History: []domain.Event{{Type: domain.EventCreated, At: time.Now()}},
	}
	if svc.is3DSExpired(i) {
		t.Fatal("no 3ds_pending event should not expire")
	}
}

func TestIs3DSExpiredFresh(t *testing.T) {
	svc, _, _ := newTestService(t, rail.NewDummy())
	svc.ReplayWindow = time.Hour
	i := &domain.Intent{
		History: []domain.Event{{Type: domain.Event3DSPending, At: time.Now()}},
	}
	if svc.is3DSExpired(i) {
		t.Fatal("fresh 3ds should not expire")
	}
}

func TestAbsHelper(t *testing.T) {
	if got := abs(-5 * time.Second); got != 5*time.Second {
		t.Fatalf("abs(-5s) = %v", got)
	}
	if got := abs(5 * time.Second); got != 5*time.Second {
		t.Fatalf("abs(5s) = %v", got)
	}
}

func TestItoaHelper(t *testing.T) {
	tests := map[int64]string{
		0:   "0",
		1:   "1",
		9:   "9",
		10:  "10",
		-1:  "-1",
		-10: "-10",
		123: "123",
	}
	for n, want := range tests {
		if got := itoa(n); got != want {
			t.Errorf("itoa(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestThreeDSChallengeInvalidJSON(t *testing.T) {
	svc, _, _ := newTestService(t, rail.NewDummy())
	mux := NewMux(svc)
	req := httptest.NewRequest(http.MethodPost, "/v1/payments/x/3ds-challenge", bytes.NewReader([]byte("not json")))
	req.Header.Set("Idempotency-Key", "k")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rec.Code)
	}
}