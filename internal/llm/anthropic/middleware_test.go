package anthropic

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	llmcontracts "github.com/hollis-labs/go-llm-contracts"
)

// stubResponse builds a synthetic *http.Response with the given status,
// headers, and body — used by the middleware tests to inspect calibration
// and breaker behavior without standing up a real Anthropic endpoint.
func stubResponse(status int, headers http.Header, body string) *http.Response {
	if headers == nil {
		headers = http.Header{}
	}
	return &http.Response{
		StatusCode: status,
		Header:     headers,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestRateAwareMiddleware_HeaderCalibration(t *testing.T) {
	rt := llmcontracts.NewTokenRateTracker(50000)
	cb := llmcontracts.NewCircuitBreaker(3)
	var calibrated atomic.Bool

	mw := rateAwareMiddleware(rt, cb, &calibrated)

	headers := http.Header{}
	headers.Set("x-ratelimit-limit-input-tokens", "80000")
	headers.Set("x-ratelimit-remaining-input-tokens", "75000")
	headers.Set("x-ratelimit-reset-input-tokens", "2026-05-09T18:00:00Z")

	req := &http.Request{Header: http.Header{}}
	resp, err := mw(req, func(*http.Request) (*http.Response, error) {
		return stubResponse(http.StatusOK, headers, ""), nil
	})
	if err != nil {
		t.Fatalf("middleware returned err: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d want 200", resp.StatusCode)
	}
	if !calibrated.Load() {
		t.Fatal("calibrated flag not set after header read")
	}
	_, limit := rt.Remaining()
	if limit != 80000 {
		t.Fatalf("limit=%d want 80000", limit)
	}
}

func TestRateAwareMiddleware_429RecordsBreakerFailure(t *testing.T) {
	rt := llmcontracts.NewTokenRateTracker(50000)
	cb := llmcontracts.NewCircuitBreaker(2) // trip after 2 consecutive
	var calibrated atomic.Bool

	mw := rateAwareMiddleware(rt, cb, &calibrated)

	req := &http.Request{Header: http.Header{}}
	for i := 0; i < 2; i++ {
		_, err := mw(req, func(*http.Request) (*http.Response, error) {
			return stubResponse(http.StatusTooManyRequests, nil, ""), nil
		})
		if err != nil {
			t.Fatalf("middleware iter %d err: %v", i, err)
		}
	}
	if !cb.IsOpen() {
		t.Fatal("breaker did not trip after consecutive 429s")
	}
}

func TestRateAwareMiddleware_5xxRecordsBreakerFailure(t *testing.T) {
	rt := llmcontracts.NewTokenRateTracker(50000)
	cb := llmcontracts.NewCircuitBreaker(2)
	var calibrated atomic.Bool

	mw := rateAwareMiddleware(rt, cb, &calibrated)

	req := &http.Request{Header: http.Header{}}
	for i := 0; i < 2; i++ {
		_, err := mw(req, func(*http.Request) (*http.Response, error) {
			return stubResponse(http.StatusInternalServerError, nil, ""), nil
		})
		if err != nil {
			t.Fatalf("middleware iter %d err: %v", i, err)
		}
	}
	if !cb.IsOpen() {
		t.Fatal("breaker did not trip after consecutive 5xxs")
	}
}

func TestRateAwareMiddleware_2xxClosesCircuit(t *testing.T) {
	rt := llmcontracts.NewTokenRateTracker(50000)
	cb := llmcontracts.NewCircuitBreaker(2)
	var calibrated atomic.Bool

	// Trip the breaker first.
	for i := 0; i < 2; i++ {
		cb.RecordFailure()
	}
	if !cb.IsOpen() {
		t.Fatal("setup: breaker should be open")
	}

	mw := rateAwareMiddleware(rt, cb, &calibrated)
	req := &http.Request{Header: http.Header{}}
	_, err := mw(req, func(*http.Request) (*http.Response, error) {
		return stubResponse(http.StatusOK, nil, ""), nil
	})
	if err != nil {
		t.Fatalf("middleware err: %v", err)
	}
	if cb.IsOpen() {
		t.Fatal("breaker should have closed after 2xx success")
	}
}

func TestRateAwareMiddleware_4xxOtherDoesNotTrip(t *testing.T) {
	rt := llmcontracts.NewTokenRateTracker(50000)
	cb := llmcontracts.NewCircuitBreaker(2)
	var calibrated atomic.Bool

	mw := rateAwareMiddleware(rt, cb, &calibrated)

	req := &http.Request{Header: http.Header{}}
	for i := 0; i < 5; i++ {
		_, err := mw(req, func(*http.Request) (*http.Response, error) {
			return stubResponse(http.StatusBadRequest, nil, ""), nil
		})
		if err != nil {
			t.Fatalf("middleware iter %d err: %v", i, err)
		}
	}
	if cb.IsOpen() {
		t.Fatal("breaker should NOT trip on 4xx-other (caller fault)")
	}
}

func TestRateAwareMiddleware_TransportErrorRecordsFailure(t *testing.T) {
	rt := llmcontracts.NewTokenRateTracker(50000)
	cb := llmcontracts.NewCircuitBreaker(2)
	var calibrated atomic.Bool

	mw := rateAwareMiddleware(rt, cb, &calibrated)

	req := &http.Request{Header: http.Header{}}
	transportErr := errors.New("connection refused")
	for i := 0; i < 2; i++ {
		_, err := mw(req, func(*http.Request) (*http.Response, error) {
			return nil, transportErr
		})
		if !errors.Is(err, transportErr) {
			t.Fatalf("expected transport err passthrough, got %v", err)
		}
	}
	if !cb.IsOpen() {
		t.Fatal("breaker should trip on consecutive transport errors")
	}
}

func TestRateAwareMiddleware_NilSafe(t *testing.T) {
	// Middleware must tolerate nil tracker / breaker (defensive — the
	// constructor wires real ones, but tests of edge components shouldn't
	// crash on misconfiguration).
	mw := rateAwareMiddleware(nil, nil, nil)
	req := &http.Request{Header: http.Header{}}
	resp, err := mw(req, func(*http.Request) (*http.Response, error) {
		return stubResponse(http.StatusOK, nil, ""), nil
	})
	if err != nil {
		t.Fatalf("nil-safe middleware err: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d want 200", resp.StatusCode)
	}
}

func TestCalibrateRateTracker_BadHeaderIgnored(t *testing.T) {
	rt := llmcontracts.NewTokenRateTracker(50000)
	var calibrated atomic.Bool

	headers := http.Header{}
	headers.Set("x-ratelimit-limit-input-tokens", "not-a-number")
	resp := &http.Response{Header: headers}
	calibrateRateTracker(rt, resp, &calibrated)
	if calibrated.Load() {
		t.Fatal("calibrated should NOT be set when header is unparseable")
	}
	_, limit := rt.Remaining()
	if limit != 50000 {
		t.Fatalf("limit changed despite bad header: %d", limit)
	}
}

func TestCalibrateRateTracker_NoHeaderNoOp(t *testing.T) {
	rt := llmcontracts.NewTokenRateTracker(50000)
	var calibrated atomic.Bool

	resp := &http.Response{Header: http.Header{}}
	calibrateRateTracker(rt, resp, &calibrated)
	if calibrated.Load() {
		t.Fatal("calibrated should NOT be set when header is absent")
	}
}
