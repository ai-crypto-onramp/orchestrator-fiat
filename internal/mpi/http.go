package mpi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ai-crypto-onramp/orchestrator-fiat/internal/domain"
)

// HTTPClient is a real 3DS MPI client that delegates the challenge and
// resume (assertion validation) flow to an external MPI service over HTTP.
// It is safe for concurrent use.
//
// The MPI service is expected to expose:
//
//	POST /v1/3ds/challenge -> start a 3DS challenge for an intent; returns
//	                          {acs_url, payload}
//	POST /v1/3ds/resume    -> submit the assertion returned by the ACS;
//	                          2xx = valid assertion, 4xx = challenge failed,
//	                          408/504 = challenge timed out
type HTTPClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

// NewHTTP returns an HTTPClient targeting the given MPI base URL.
func NewHTTP(baseURL string) *HTTPClient {
	return &HTTPClient{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		HTTPClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// challengeReq is the POST /v1/3ds/challenge body.
type challengeReq struct {
	PaymentID string `json:"payment_id"`
	Rail      string `json:"rail"`
	Amount    string `json:"amount"`
	Currency  string `json:"currency"`
	PayerRef  string `json:"payer_ref"`
}

// resumeReq is the POST /v1/3ds/resume body.
type resumeReq struct {
	PaymentID string `json:"payment_id"`
	Assertion string `json:"assertion"`
}

// Challenge starts a 3DS challenge for the intent and returns the ACS
// artifact the client must use to complete the challenge. Non-card rails
// are rejected before the call.
func (c *HTTPClient) Challenge(i *domain.Intent) (Challenge, error) {
	if i.Rail != domain.RailCard {
		return Challenge{}, fmt.Errorf("3ds only supported for card rail")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	body, err := json.Marshal(challengeReq{
		PaymentID: i.ID,
		Rail:      string(i.Rail),
		Currency:  i.Currency,
		PayerRef:  i.PayerRef,
	})
	if err != nil {
		return Challenge{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/3ds/challenge", bytes.NewReader(body))
	if err != nil {
		return Challenge{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return Challenge{}, fmt.Errorf("%w: %v", ErrChallengeFailed, err)
	}
	defer resp.Body.Close()
	rb, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Challenge{}, err
	}
	if resp.StatusCode >= 400 {
		return Challenge{}, fmt.Errorf("%w: status %d: %s", ErrChallengeFailed, resp.StatusCode, strings.TrimSpace(string(rb)))
	}
	var out Challenge
	if err := json.Unmarshal(rb, &out); err != nil {
		return Challenge{}, fmt.Errorf("mpi: decode: %w", err)
	}
	return out, nil
}

// Resume submits the ACS assertion to the MPI service for validation. A
// 2xx response means the assertion is valid; a 408/504 is translated to
// ErrTimeout; any other 4xx/5xx is translated to ErrChallengeFailed.
func (c *HTTPClient) Resume(i *domain.Intent, assertion string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	body, err := json.Marshal(resumeReq{PaymentID: i.ID, Assertion: assertion})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/3ds/resume", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrChallengeFailed, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusGatewayTimeout {
		return ErrTimeout
	}
	if resp.StatusCode >= 400 {
		rb, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("%w: status %d: %s", ErrChallengeFailed, resp.StatusCode, strings.TrimSpace(string(rb)))
	}
	return nil
}