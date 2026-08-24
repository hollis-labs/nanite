package mcp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// --- Unit tests: retry helper ---

// mockTransport is a configurable http.RoundTripper that returns pre-canned
// responses in sequence. Once the sequence is exhausted it panics (test bug).
type mockTransport struct {
	responses []*mockResponse
	idx       int
}

type mockResponse struct {
	status  int
	body    string
	headers http.Header
	err     error
}

func (m *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if m.idx >= len(m.responses) {
		panic(fmt.Sprintf("mockTransport: no response for attempt %d (have %d)", m.idx+1, len(m.responses)))
	}
	r := m.responses[m.idx]
	m.idx++
	if r.err != nil {
		return nil, r.err
	}
	rec := httptest.NewRecorder()
	// Set headers before WriteHeader so they appear in rec.Result().
	for k, vs := range r.headers {
		for _, v := range vs {
			rec.Header().Set(k, v)
		}
	}
	rec.WriteHeader(r.status)
	if r.body != "" {
		fmt.Fprint(rec, r.body)
	}
	resp := rec.Result()
	return resp, nil
}

func clientWithMock(responses ...*mockResponse) *http.Client {
	return &http.Client{
		Transport: &mockTransport{responses: responses},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
}

// --- retry succeeds after transient errors ---

func TestFetchWithRetry_SucceedAfter503(t *testing.T) {
	client := clientWithMock(
		&mockResponse{status: 503, body: "overloaded"},
		&mockResponse{status: 200, body: "hello world"},
	)
	resp, fe := fetchWithRetry(context.Background(), client, "http://example.com/test")
	if fe != nil {
		t.Fatalf("expected success, got FetchError: %v", fe)
	}
	resp.Body.Close()
}

func TestFetchWithRetry_SucceedAfterMultiple503(t *testing.T) {
	client := clientWithMock(
		&mockResponse{status: 503, body: "bad"},
		&mockResponse{status: 502, body: "bad"},
		&mockResponse{status: 200, body: "ok"},
	)
	resp, fe := fetchWithRetry(context.Background(), client, "http://example.com/")
	if fe != nil {
		t.Fatalf("expected success, got: %v", fe)
	}
	resp.Body.Close()
}

// --- 5xx exhausts all retries ---

func TestFetchWithRetry_5xxExhausted(t *testing.T) {
	client := clientWithMock(
		&mockResponse{status: 503},
		&mockResponse{status: 503},
		&mockResponse{status: 503},
		&mockResponse{status: 503},
	)
	_, fe := fetchWithRetry(context.Background(), client, "http://example.com/")
	if fe == nil {
		t.Fatal("expected FetchError")
	}
	if fe.Kind != FetchErr5xxAfterRetries {
		t.Errorf("expected FetchErr5xxAfterRetries, got %s", fe.Kind)
	}
	if fe.Attempts != fetchMaxRetries+1 {
		t.Errorf("expected %d attempts, got %d", fetchMaxRetries+1, fe.Attempts)
	}
}

// --- 404 never retried ---

func TestFetchWithRetry_404NoRetry(t *testing.T) {
	attempts := 0
	client := clientWithMock(
		&mockResponse{status: 404},
	)
	_ = client.Transport.(*mockTransport) // type assert for clarity
	attempts++                            // first and only call

	_, fe := fetchWithRetry(context.Background(), client, "http://example.com/notfound")
	if fe == nil {
		t.Fatal("expected FetchError for 404")
	}
	if fe.Kind != FetchErr4xx {
		t.Errorf("expected FetchErr4xx, got %s", fe.Kind)
	}
	if fe.Attempts != 1 {
		t.Errorf("expected exactly 1 attempt for 404, got %d", fe.Attempts)
	}
	_ = attempts
}

// --- 403 classified as blocked ---

func TestFetchWithRetry_403Blocked(t *testing.T) {
	client := clientWithMock(
		&mockResponse{status: 403},
	)
	_, fe := fetchWithRetry(context.Background(), client, "http://example.com/protected")
	if fe == nil {
		t.Fatal("expected FetchError")
	}
	if fe.Kind != FetchErrBlocked {
		t.Errorf("expected FetchErrBlocked, got %s", fe.Kind)
	}
}

// --- 429 with Retry-After header ---

func TestFetchWithRetry_429RespectsRetryAfter(t *testing.T) {
	// We set a very short Retry-After so the test doesn't actually wait long.
	retryAfterHeaders := http.Header{"Retry-After": []string{"1"}}
	client := clientWithMock(
		&mockResponse{status: 429, headers: retryAfterHeaders},
		&mockResponse{status: 200, body: "ok after rate limit"},
	)
	start := time.Now()
	resp, fe := fetchWithRetry(context.Background(), client, "http://example.com/api")
	if fe != nil {
		t.Fatalf("expected success after 429 retry, got: %v", fe)
	}
	resp.Body.Close()
	elapsed := time.Since(start)
	// Should have waited ~1s for Retry-After.
	if elapsed < 900*time.Millisecond {
		t.Errorf("expected at least 1s delay for Retry-After, elapsed %v", elapsed)
	}
}

// --- context cancellation stops immediately ---

func TestFetchWithRetry_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	client := &http.Client{
		Transport: &mockTransport{
			responses: []*mockResponse{
				{status: 200, body: "should not be reached"},
			},
		},
	}
	_, fe := fetchWithRetry(ctx, client, "http://example.com/")
	if fe == nil {
		t.Fatal("expected FetchError for cancelled context")
	}
	if fe.Kind != FetchErrTimeout {
		t.Errorf("expected FetchErrTimeout kind for cancellation, got %s", fe.Kind)
	}
}

// --- Unit tests: FetchError classification ---

func TestClassifyEmptyHTML_SmallHTML(t *testing.T) {
	if !classifyEmptyHTML("text/html; charset=utf-8", 100) {
		t.Error("100-byte HTML should be classified as empty_html")
	}
}

func TestClassifyEmptyHTML_SufficientHTML(t *testing.T) {
	if classifyEmptyHTML("text/html; charset=utf-8", 500) {
		t.Error("500-byte HTML should not be classified as empty_html")
	}
}

func TestClassifyEmptyHTML_NonHTMLSmall(t *testing.T) {
	if classifyEmptyHTML("application/json", 50) {
		t.Error("small JSON should not be classified as empty_html")
	}
}

func TestClassifyEmptyHTML_ExactThreshold(t *testing.T) {
	// Exactly at threshold → not empty (threshold is exclusive lower bound).
	if classifyEmptyHTML("text/html", emptyHTMLThreshold) {
		t.Errorf("body of exactly %d bytes should not be empty_html", emptyHTMLThreshold)
	}
}

func TestClassifyEmptyHTML_BelowThreshold(t *testing.T) {
	if !classifyEmptyHTML("text/html", emptyHTMLThreshold-1) {
		t.Errorf("body of %d bytes should be empty_html", emptyHTMLThreshold-1)
	}
}

func TestRetryableStatusCode(t *testing.T) {
	cases := []struct {
		code      int
		retryable bool
	}{
		{200, false},
		{404, false},
		{403, false},
		{400, false},
		{429, true},
		{500, true},
		{502, true},
		{503, true},
		{504, true},
		{599, true},
	}
	for _, c := range cases {
		got := retryableStatusCode(c.code)
		if got != c.retryable {
			t.Errorf("retryableStatusCode(%d) = %v, want %v", c.code, got, c.retryable)
		}
	}
}

func TestBackoffDelay_Increases(t *testing.T) {
	// Run multiple samples to confirm increasing trend (jitter may make a
	// single sample unreliable, but the range should be clearly ascending).
	for attempt := 0; attempt < fetchMaxRetries; attempt++ {
		d := backoffDelay(attempt)
		minExpected := fetchBaseDelay * (1 << uint(attempt)) * 3 / 4 // 75%
		maxExpected := fetchBaseDelay * (1 << uint(attempt)) * 5 / 4 // 125%
		if d < minExpected || d > maxExpected {
			t.Errorf("attempt %d: backoffDelay=%v, want [%v, %v]", attempt, d, minExpected, maxExpected)
		}
	}
}

// --- Integration tests: httptest.NewServer ---

// TestIntegration_RetryOn503ThenSuccess exercises the retry path through the
// real callWebFetch entrypoint using a local httptest server.
func TestIntegration_RetryOn503ThenSuccess(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, "service unavailable")
			return
		}
		w.WriteHeader(http.StatusOK)
		// Body > emptyHTMLThreshold to avoid empty_html classification.
		fmt.Fprint(w, strings.Repeat("x", 300))
	}))
	defer srv.Close()

	gt := newGeneralTools()
	gt.AllowLocalhost = true

	result, err := gt.CallTool(context.Background(), "web_fetch", map[string]any{
		"url": srv.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("expected success after 503→200, got error: %s", result.Content[0].Text)
	}
	if n := calls.Load(); n < 2 {
		t.Errorf("expected at least 2 server calls (retry), got %d", n)
	}
}

// TestIntegration_429WithRetryAfter verifies that Retry-After header is
// respected and a subsequent 200 succeeds.
func TestIntegration_429WithRetryAfter(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, strings.Repeat("real content ", 30)) // > emptyHTMLThreshold
	}))
	defer srv.Close()

	gt := newGeneralTools()
	gt.AllowLocalhost = true

	start := time.Now()
	result, err := gt.CallTool(context.Background(), "web_fetch", map[string]any{
		"url": srv.URL,
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("expected success after 429→200, got: %s", result.Content[0].Text)
	}
	if elapsed < 900*time.Millisecond {
		t.Errorf("expected at least 1s delay from Retry-After, elapsed %v", elapsed)
	}
}

// TestIntegration_EmptyHTML verifies that a 200 with tiny HTML is classified
// as empty_html and returned as an error result.
func TestIntegration_EmptyHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "<html></html>") // << emptyHTMLThreshold
	}))
	defer srv.Close()

	gt := newGeneralTools()
	gt.AllowLocalhost = true

	result, err := gt.CallTool(context.Background(), "web_fetch", map[string]any{
		"url": srv.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error result for empty HTML")
	}
	text := result.Content[0].Text
	if !strings.Contains(text, string(FetchErrEmptyHTML)) {
		t.Errorf("expected empty_html in error, got: %s", text)
	}
	if !strings.Contains(text, "JS-rendered") {
		t.Errorf("expected hint about JS-rendered in error, got: %s", text)
	}
}

// TestIntegration_404NoRetry verifies that a 404 is returned immediately as a
// structured error with kind=4xx and no retry.
func TestIntegration_404NoRetry(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, "not found")
	}))
	defer srv.Close()

	gt := newGeneralTools()
	gt.AllowLocalhost = true

	result, err := gt.CallTool(context.Background(), "web_fetch", map[string]any{
		"url": srv.URL + "/missing",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error result for 404")
	}
	text := result.Content[0].Text
	if !strings.Contains(text, string(FetchErr4xx)) {
		t.Errorf("expected 4xx kind in error, got: %s", text)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("expected exactly 1 server call (no retry on 404), got %d", n)
	}
}

// TestIntegration_UAHeaderSent verifies that requests include a User-Agent
// header drawn from the rotation pool.
func TestIntegration_UAHeaderSent(t *testing.T) {
	var receivedUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedUA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, strings.Repeat("body content ", 30)) // > emptyHTMLThreshold
	}))
	defer srv.Close()

	gt := newGeneralTools()
	gt.AllowLocalhost = true

	result, err := gt.CallTool(context.Background(), "web_fetch", map[string]any{
		"url": srv.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	// The UA must be one of our rotation pool entries.
	found := false
	for _, ua := range fetchUserAgents {
		if receivedUA == ua {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("UA %q not in rotation pool", receivedUA)
	}
}

// TestIntegration_StructuredErrorFields verifies that a 5xx exhaustion returns
// a structured error with kind, url, attempts, status, and hint fields.
func TestIntegration_StructuredErrorFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, "bad gateway")
	}))
	defer srv.Close()

	gt := newGeneralTools()
	gt.AllowLocalhost = true

	result, err := gt.CallTool(context.Background(), "web_fetch", map[string]any{
		"url": srv.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error result for persistent 502")
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "kind=") {
		t.Errorf("expected 'kind=' in error, got: %s", text)
	}
	if !strings.Contains(text, "attempts=") {
		t.Errorf("expected 'attempts=' in error, got: %s", text)
	}
	if !strings.Contains(text, "hint:") {
		t.Errorf("expected 'hint:' in error, got: %s", text)
	}
}
