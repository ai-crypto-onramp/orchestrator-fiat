package rail

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ai-crypto-onramp/orchestrator-fiat/internal/domain"
)

// HTTPAdapter is a real rail.Adapter that submits instructions to a rail
// service (gateway-fiat) over HTTP. One adapter instance targets a single
// rail endpoint (RAIL_*_URL). It is safe for concurrent use.
//
// The rail service is expected to expose:
//
//	POST /v1/authorize            -> authorize or submit a payment
//	POST /v1/capture/{payment_id} -> capture an authorized payment
//	POST /v1/refund/{payment_id}  -> refund a captured payment
//	GET  /v1/status/{payment_id}  -> query the rail-side status
//
// All request bodies and responses are JSON. Amounts are decimal-encoded
// JSON strings (per the gateway-fiat contract); integer int64 amounts are
// rendered as their decimal string form.
type HTTPAdapter struct {
	BaseURL    string
	HTTPClient *http.Client
	railName   string
}

// NewHTTP returns an HTTPAdapter for the given rail name targeting baseURL.
func NewHTTP(baseURL, railName string) *HTTPAdapter {
	return &HTTPAdapter{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
		railName:   railName,
	}
}

// authorizeReq is the POST /v1/authorize body (subset of gateway-fiat's
// authorizeReq; amount is a decimal string).
type authorizeReq struct {
	PaymentID      string            `json:"payment_id"`
	Rail           string            `json:"rail"`
	Amount         string            `json:"amount"`
	Currency       string            `json:"currency"`
	PayerRef       string            `json:"payer_ref"`
	IdempotencyKey string            `json:"idempotency_key,omitempty"`
	RailSpecific   map[string]string `json:"rail_specific,omitempty"`
}

type railResponse struct {
	Status       string  `json:"status"`
	RailRef      string  `json:"rail_ref"`
	SettleAmount *string `json:"settle_amount,omitempty"`
	ErrorCode    string  `json:"error_code,omitempty"`
	ErrorMessage string  `json:"error_message,omitempty"`
}

type amountReq struct {
	Amount string `json:"amount"`
}

type statusResponse struct {
	PaymentID string `json:"payment_id"`
	Status    string `json:"status"`
	RailRef   string `json:"rail_ref,omitempty"`
}

// post sends a JSON POST and decodes the response. A 4xx/5xx response is
// translated into a rail error using the response error_code/message.
func (a *HTTPAdapter) post(ctx context.Context, path string, body any) (*railResponse, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.BaseURL+path, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if key, ok := idempotencyFromContext(ctx); ok {
		req.Header.Set("Idempotency-Key", key)
	}
	return a.do(req)
}

// get sends a GET and decodes the response.
func (a *HTTPAdapter) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.BaseURL+path, nil)
	if err != nil {
		return err
	}
	resp, err := a.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("rail %s: status %d: %s", a.railName, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("rail %s: decode: %w", a.railName, err)
		}
	}
	return nil
}

func (a *HTTPAdapter) do(req *http.Request) (*railResponse, error) {
	resp, err := a.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var r railResponse
	if err := json.Unmarshal(body, &r); err != nil {
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("rail %s: status %d: %s", a.railName, resp.StatusCode, strings.TrimSpace(string(body)))
		}
		return nil, fmt.Errorf("rail %s: decode: %w", a.railName, err)
	}
	if resp.StatusCode >= 400 || r.Status == "failed" {
		msg := r.ErrorMessage
		if msg == "" {
			msg = strings.TrimSpace(string(body))
		}
		code := r.ErrorCode
		if code == "" {
			code = fmt.Sprintf("HTTP_%d", resp.StatusCode)
		}
		return &r, fmt.Errorf("rail %s: %s: %s", a.railName, code, msg)
	}
	return &r, nil
}

// idempotencyKey context key.
type idemKey struct{}

func withIdempotency(ctx context.Context, key string) context.Context {
	if key == "" {
		return ctx
	}
	return context.WithValue(ctx, idemKey{}, key)
}

func idempotencyFromContext(ctx context.Context) (string, bool) {
	if v, ok := ctx.Value(idemKey{}).(string); ok && v != "" {
		return v, true
	}
	return "", false
}

// amountString renders an int64 amount as a decimal string.
func amountString(n int64) string { return strconv.FormatInt(n, 10) }

// Authorize submits an authorization to the rail and records the external id.
func (a *HTTPAdapter) Authorize(i *domain.Intent) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ctx = withIdempotency(ctx, i.IdempotencyKey)
	req := authorizeReq{
		PaymentID:      i.ID,
		Rail:           a.railName,
		Amount:         amountString(i.Amount),
		Currency:       i.Currency,
		PayerRef:       i.PayerRef,
		IdempotencyKey: i.IdempotencyKey,
	}
	resp, err := a.post(ctx, "/v1/authorize", req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAuthorize, err)
	}
	if resp.RailRef != "" {
		i.ExternalID = resp.RailRef
	} else {
		i.ExternalID = "ext-" + i.ID
	}
	return nil
}

// Capture captures a previously authorized intent for the given amount.
func (a *HTTPAdapter) Capture(i *domain.Intent, amount int64) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pid := paymentIDFor(i)
	if _, err := a.post(ctx, "/v1/capture/"+pid, amountReq{Amount: amountString(amount)}); err != nil {
		return fmt.Errorf("%w: %v", ErrCapture, err)
	}
	return nil
}

// Refund refunds the given amount against a captured intent.
func (a *HTTPAdapter) Refund(i *domain.Intent, amount int64) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pid := paymentIDFor(i)
	if _, err := a.post(ctx, "/v1/refund/"+pid, amountReq{Amount: amountString(amount)}); err != nil {
		return fmt.Errorf("%w: %v", ErrRefund, err)
	}
	return nil
}

// Submit collapses auth+capture into a single authorize call for instant
// (PIX, UPI) and bank (ACH, SEPA) rails. gateway-fiat's /v1/authorize
// returns the final status; we record the external id and the captured
// amount on success.
func (a *HTTPAdapter) Submit(i *domain.Intent) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ctx = withIdempotency(ctx, i.IdempotencyKey)
	req := authorizeReq{
		PaymentID:      i.ID,
		Rail:           a.railName,
		Amount:         amountString(i.Amount),
		Currency:       i.Currency,
		PayerRef:       i.PayerRef,
		IdempotencyKey: i.IdempotencyKey,
	}
	resp, err := a.post(ctx, "/v1/authorize", req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAuthorize, err)
	}
	if resp.RailRef != "" {
		i.ExternalID = resp.RailRef
	} else {
		i.ExternalID = "ext-" + i.ID
	}
	if resp.SettleAmount != nil {
		if n, perr := strconv.ParseInt(*resp.SettleAmount, 10, 64); perr == nil {
			i.CapturedAmount = n
		} else {
			i.CapturedAmount = i.Amount
		}
	} else {
		i.CapturedAmount = i.Amount
	}
	return nil
}

// Void cancels a previously authorized but not-yet-captured intent. The
// rail service does not expose a dedicated void endpoint; we issue a
// refund for amount 0 which the gateway translates to a cancellation.
func (a *HTTPAdapter) Void(i *domain.Intent) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pid := paymentIDFor(i)
	if _, err := a.post(ctx, "/v1/refund/"+pid, amountReq{Amount: "0"}); err != nil {
		return fmt.Errorf("%w: %v", ErrVoid, err)
	}
	return nil
}

// Verify3DS is a card-rail only operation. The rail HTTP adapter does not
// own the 3DS challenge flow (handled by the MPI client); it records the
// verification result against the rail by querying status. For non-card
// rails it returns ErrUnsupported3DS.
func (a *HTTPAdapter) Verify3DS(i *domain.Intent, challengeResult string) error {
	if i.Rail != domain.RailCard {
		return ErrUnsupported3DS
	}
	if challengeResult == "" || challengeResult == "fail" {
		return Err3DSVerify
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var st statusResponse
	if err := a.get(ctx, "/v1/status/"+i.ID, &st); err != nil {
		return fmt.Errorf("%w: %v", Err3DSVerify, err)
	}
	if strings.EqualFold(st.Status, "failed") {
		return Err3DSVerify
	}
	return nil
}

// paymentIDFor returns the external id when present (preferred for
// rail-side operations) and falls back to the platform intent id.
func paymentIDFor(i *domain.Intent) string {
	if i.ExternalID != "" {
		return i.ExternalID
	}
	return i.ID
}