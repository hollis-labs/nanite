package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestGeminiBuildRequest(t *testing.T) {
	g := &Gemini{}

	t.Run("system prompt becomes systemInstruction", func(t *testing.T) {
		req := g.buildRequest("gemini-2.5-flash", "You are helpful.", []ChatMessage{
			{Role: "user", Content: "Hello"},
		})
		if req.SystemInstruct == nil {
			t.Fatal("expected systemInstruction to be set")
		}
		if req.SystemInstruct.Parts[0].Text != "You are helpful." {
			t.Errorf("unexpected system text: %s", req.SystemInstruct.Parts[0].Text)
		}
	})

	t.Run("empty system prompt omits systemInstruction", func(t *testing.T) {
		req := g.buildRequest("gemini-2.5-flash", "", []ChatMessage{
			{Role: "user", Content: "Hello"},
		})
		if req.SystemInstruct != nil {
			t.Error("expected nil systemInstruction for empty prompt")
		}
	})

	t.Run("assistant role maps to model", func(t *testing.T) {
		req := g.buildRequest("gemini-2.5-flash", "", []ChatMessage{
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi there"},
		})
		if len(req.Contents) != 2 {
			t.Fatalf("expected 2 contents, got %d", len(req.Contents))
		}
		if req.Contents[1].Role != "model" {
			t.Errorf("expected role=model, got %s", req.Contents[1].Role)
		}
	})

	t.Run("content blocks are joined", func(t *testing.T) {
		req := g.buildRequest("gemini-2.5-flash", "", []ChatMessage{
			{Role: "user", ContentBlocks: []ContentBlock{
				{Type: "text", Text: "Part 1"},
				{Type: "text", Text: "Part 2"},
			}},
		})
		if len(req.Contents) != 1 {
			t.Fatalf("expected 1 content, got %d", len(req.Contents))
		}
		if req.Contents[0].Parts[0].Text != "Part 1\nPart 2" {
			t.Errorf("unexpected joined text: %s", req.Contents[0].Parts[0].Text)
		}
	})

	t.Run("empty messages are skipped", func(t *testing.T) {
		req := g.buildRequest("gemini-2.5-flash", "", []ChatMessage{
			{Role: "user", Content: ""},
			{Role: "user", Content: "Real message"},
		})
		if len(req.Contents) != 1 {
			t.Fatalf("expected 1 content, got %d", len(req.Contents))
		}
	})
}

func TestGeminiStreamChat(t *testing.T) {
	t.Run("missing API key returns error", func(t *testing.T) {
		g := &Gemini{apiKey: "", client: &http.Client{}}
		_, err := g.StreamChat(context.Background(), "", nil, "")
		if err == nil {
			t.Fatal("expected error for missing API key")
		}
	})
}

func TestGeminiComplete(t *testing.T) {
	t.Run("missing API key returns error", func(t *testing.T) {
		g := &Gemini{apiKey: "", client: &http.Client{}}
		_, err := g.Complete(context.Background(), "", nil, "")
		if err == nil {
			t.Fatal("expected error for missing API key")
		}
	})
}

// fastRetry returns a RetryConfig with near-zero delays for tests.
func fastRetry(maxRetries int) RetryConfig {
	return RetryConfig{
		MaxRetries:   maxRetries,
		InitialDelay: 1 * time.Millisecond,
		MaxDelay:     2 * time.Millisecond,
		Multiplier:   2.0,
	}
}

// newGeminiWithServer points a Gemini adapter at a test server by overriding
// the client transport so the geminiAPI host is redirected. Returns the
// configured adapter. The caller is responsible for closing the server.
func newGeminiWithServer(t *testing.T, srv *httptest.Server) *Gemini {
	t.Helper()
	// Replace the global base URL is invasive; instead, wrap transport so any
	// outbound request to generativelanguage.googleapis.com routes to srv.
	rt := &rewriteTransport{target: srv.URL, base: http.DefaultTransport}
	g := &Gemini{
		apiKey:         "test-key",
		client:         &http.Client{Transport: rt},
		Retry:          fastRetry(3),
		CircuitBreaker: NewCircuitBreaker(3),
		RateTracker:    NewTokenRateTracker(30000),
	}
	return g
}

// rewriteTransport rewrites outgoing requests to hit a test server instead of
// the real Gemini host. It preserves headers (so x-goog-api-key survives).
type rewriteTransport struct {
	target  string
	base    http.RoundTripper
	observe func(*http.Request)
}

func (r *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if r.observe != nil {
		r.observe(req)
	}
	// Repoint to target host.
	u := *req.URL
	// Parse target once.
	tu, err := parseTargetURL(r.target)
	if err != nil {
		return nil, err
	}
	u.Scheme = tu.Scheme
	u.Host = tu.Host
	req.URL = &u
	req.Host = tu.Host
	return r.base.RoundTrip(req)
}

func parseTargetURL(s string) (*struct {
	Scheme, Host string
}, error) {
	// Minimal parser: expect http://host:port or https://host:port
	var scheme, host string
	switch {
	case len(s) >= 7 && s[:7] == "http://":
		scheme = "http"
		host = s[7:]
	case len(s) >= 8 && s[:8] == "https://":
		scheme = "https"
		host = s[8:]
	default:
		return nil, fmt.Errorf("unsupported target url: %s", s)
	}
	// strip path if any
	for i := 0; i < len(host); i++ {
		if host[i] == '/' {
			host = host[:i]
			break
		}
	}
	return &struct{ Scheme, Host string }{scheme, host}, nil
}

// geminiCompleteBody returns a minimal generateContent response with usage.
func geminiCompleteBody(text string, promptTokens, candidateTokens int) []byte {
	resp := map[string]any{
		"candidates": []map[string]any{{
			"content": map[string]any{
				"parts": []map[string]any{{"text": text}},
			},
		}},
		"usageMetadata": map[string]any{
			"promptTokenCount":     promptTokens,
			"candidatesTokenCount": candidateTokens,
			"totalTokenCount":      promptTokens + candidateTokens,
		},
	}
	b, _ := json.Marshal(resp)
	return b
}

func TestGeminiComplete_RetryOnRetryableStatus(t *testing.T) {
	var calls int32
	var statusMsgs []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if r.Header.Get("x-goog-api-key") != "test-key" {
			t.Errorf("missing x-goog-api-key on attempt %d", n)
		}
		if n == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate limited"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(geminiCompleteBody("ok", 10, 5))
	}))
	defer srv.Close()

	g := newGeminiWithServer(t, srv)
	g.OnStatus = func(msg string) { statusMsgs = append(statusMsgs, msg) }

	out, err := g.Complete(context.Background(), "", []ChatMessage{{Role: "user", Content: "hi"}}, "gemini-2.5-flash")
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	if out != "ok" {
		t.Errorf("unexpected output: %q", out)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("expected 2 HTTP attempts (1 retry), got %d", got)
	}
	sawRetry := false
	for _, m := range statusMsgs {
		if len(m) >= 12 && m[:12] == "Rate limited" {
			sawRetry = true
			break
		}
	}
	if !sawRetry {
		t.Errorf("expected OnStatus to fire a 'Rate limited' retry message, got %v", statusMsgs)
	}
}

func TestGeminiComplete_HeaderPreservedUnderRetry(t *testing.T) {
	var calls int32
	var headersPerCall []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		headersPerCall = append(headersPerCall, r.Header.Get("x-goog-api-key"))
		// URL must not carry ?key= — that's the audit-closure guardrail.
		if r.URL.Query().Get("key") != "" {
			t.Errorf("URL contains ?key= on attempt %d — TASK-007 regression", n)
		}
		if n < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"try later"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(geminiCompleteBody("done", 3, 2))
	}))
	defer srv.Close()

	g := newGeminiWithServer(t, srv)

	_, err := g.Complete(context.Background(), "", []ChatMessage{{Role: "user", Content: "hi"}}, "gemini-2.5-flash")
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	if len(headersPerCall) < 2 {
		t.Fatalf("expected >=2 calls, got %d", len(headersPerCall))
	}
	for i, h := range headersPerCall {
		if h != "test-key" {
			t.Errorf("attempt %d: expected x-goog-api-key=test-key, got %q", i+1, h)
		}
	}
}

func TestGeminiComplete_CircuitBreakerOpenShortCircuits(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(geminiCompleteBody("ok", 1, 1))
	}))
	defer srv.Close()

	g := newGeminiWithServer(t, srv)
	// Force-trip the breaker: threshold=1 then record failure.
	g.CircuitBreaker = NewCircuitBreaker(1)
	_ = g.CircuitBreaker.RecordFailure() // trips to open

	var circuitOpenFired int32
	g.OnCircuitOpen = func() { atomic.AddInt32(&circuitOpenFired, 1) }

	_, err := g.Complete(context.Background(), "", []ChatMessage{{Role: "user", Content: "hi"}}, "gemini-2.5-flash")
	if err == nil {
		t.Fatal("expected error when circuit is open")
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Errorf("expected 0 HTTP calls while circuit open, got %d", got)
	}
	if atomic.LoadInt32(&circuitOpenFired) == 0 {
		t.Errorf("expected OnCircuitOpen to fire on short-circuit")
	}
}

func TestGeminiComplete_RateTrackerAccounting(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// UsageMetadata drives output-token recording.
		_, _ = w.Write(geminiCompleteBody("hello world", 20, 7))
	}))
	defer srv.Close()

	g := newGeminiWithServer(t, srv)
	// Give the tracker a generous limit so no pacing kicks in.
	g.RateTracker = NewTokenRateTracker(1000000)

	preAvail, preLimit := g.RateTracker.Remaining()
	if preLimit <= 0 {
		t.Fatalf("unexpected limit: %d", preLimit)
	}

	_, err := g.Complete(context.Background(), "sys prompt used for est", []ChatMessage{{Role: "user", Content: "hi there"}}, "gemini-2.5-flash")
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}

	postAvail, _ := g.RateTracker.Remaining()
	if postAvail >= preAvail {
		t.Errorf("expected rate tracker to record tokens (pre=%d, post=%d)", preAvail, postAvail)
	}
	// Both input (estimated) and output (7 from usageMetadata) should have landed.
	diff := preAvail - postAvail
	if diff < 7 {
		t.Errorf("expected >=7 tokens recorded (output), got diff=%d", diff)
	}
}

func TestGeminiCapabilities(t *testing.T) {
	g := NewGemini()
	caps := g.Capabilities()
	if !caps.SupportsStreamJSON {
		t.Error("expected SupportsStreamJSON to be true")
	}
	if !caps.SupportsImageInput {
		t.Error("expected SupportsImageInput to be true")
	}
	if caps.MaxTokens != 65536 {
		t.Errorf("expected MaxTokens=65536, got %d", caps.MaxTokens)
	}
	if caps.ContextWindowSize != 1048576 {
		t.Errorf("expected ContextWindowSize=1048576, got %d", caps.ContextWindowSize)
	}
}
