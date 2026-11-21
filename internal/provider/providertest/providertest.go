// Package providertest records real provider HTTP exchanges into fixtures
// and replays them to the adapters offline.
//
// A fixture is one request and one response. Only what replay needs is kept:
// the request's method, path and JSON body (never its headers, so no key or
// token is ever written) and the response's status, Content-Type and
// Retry-After headers and raw body bytes. Source says where the fixture came
// from: "recorded" for a live exchange, or "derived from <file>: <edit>" for
// one built from a recording to reproduce a failure a live provider does not
// produce on demand (an overloaded error mid-stream, a 429, a cut stream).
package providertest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// Fixture is one recorded or derived HTTP exchange.
type Fixture struct {
	Source   string   `json:"source"`
	Request  Request  `json:"request"`
	Response Response `json:"response"`
}

type Request struct {
	Method string          `json:"method"`
	Path   string          `json:"path"`
	Body   json.RawMessage `json:"body,omitempty"`
}

type Response struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
	// Body is the exact response bytes: an SSE stream or a JSON error.
	Body string `json:"body"`
}

// keptHeaders are the response headers replay needs.
var keptHeaders = []string{"Content-Type", "Retry-After"}

// Load reads a fixture.
func Load(t testing.TB, path string) Fixture {
	t.Helper()
	raw, err := os.ReadFile(path) // #nosec G304 -- A fixture under the caller's testdata.
	if err != nil {
		t.Fatal(err)
	}
	var f Fixture
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return f
}

// Save writes a fixture, indented, with a trailing newline.
func Save(path string, f Fixture) error {
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}

// Recorder is an http.RoundTripper that sends each request through Next and
// keeps the exchange. Exchanges are kept in order; Take returns and clears
// them.
type Recorder struct {
	Next http.RoundTripper
	// Scrub rewrites a request or response body before it is kept, to drop
	// anything that must not be committed (a gateway's model alias, say).
	Scrub func(string) string

	mu    sync.Mutex
	taken []Fixture
}

func (r *Recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		var err error
		body, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		_ = req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	next := r.Next
	if next == nil {
		next = http.DefaultTransport
	}
	resp, err := next.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(raw))
	f := Fixture{Source: "recorded", Request: Request{Method: req.Method, Path: APIPath(req.URL.Path)}, Response: Response{Status: resp.StatusCode, Headers: map[string]string{}, Body: r.scrub(string(raw))}}
	if len(body) > 0 {
		f.Request.Body = json.RawMessage(r.scrub(string(body)))
	}
	for _, name := range keptHeaders {
		if v := resp.Header.Get(name); v != "" {
			f.Response.Headers[name] = v
		}
	}
	r.mu.Lock()
	r.taken = append(r.taken, f)
	r.mu.Unlock()
	return resp, nil
}

func (r *Recorder) scrub(s string) string {
	if r.Scrub == nil {
		return s
	}
	return r.Scrub(s)
}

// APIPath drops whatever a gateway mounts in front of the provider API: it
// keeps the path from /v1/ on, so no deployment route is committed.
func APIPath(path string) string {
	if i := strings.Index(path, "/v1/"); i >= 0 {
		return path[i:]
	}
	return path
}

// Take returns the exchanges kept so far and clears them.
func (r *Recorder) Take() []Fixture {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.taken
	r.taken = nil
	return out
}

// hostLike matches anything that looks like a hostname or URL, which a
// committed fixture must not carry.
var hostLike = regexp.MustCompile(`(?i)https?://|[a-z0-9-]+\.(cloud|com|net|org|io|dev|ai)\b`)

// CheckClean fails when a fixture carries a credential-shaped string or a
// hostname. Recorded bodies are provider output; this is the last guard
// before they are committed.
func CheckClean(t testing.TB, name string, f Fixture) {
	t.Helper()
	for _, s := range []string{string(f.Request.Body), f.Response.Body} {
		for _, bad := range []string{"sk-ant-", "sk-proj-", "Bearer ", "eyJ"} {
			if strings.Contains(s, bad) {
				t.Fatalf("%s carries a credential-shaped string (%q)", name, bad)
			}
		}
		if m := hostLike.FindString(s); m != "" {
			t.Fatalf("%s carries a hostname or URL (%q)", name, m)
		}
	}
}

// Server replays fixtures in order, one per request, and keeps the bodies
// it was sent. A request beyond the last fixture gets a 500.
type Server struct {
	*httptest.Server

	mu       sync.Mutex
	queue    []Fixture
	received [][]byte
}

// Serve starts a replay server for fixtures, closed when the test ends.
func Serve(t testing.TB, fixtures ...Fixture) *Server {
	t.Helper()
	s := &Server{queue: fixtures}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.Close)
	return s
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.received = append(s.received, body)
	var f *Fixture
	if len(s.queue) > 0 {
		f = &s.queue[0]
		s.queue = s.queue[1:]
	}
	s.mu.Unlock()
	if f == nil {
		http.Error(w, fmt.Sprintf("replay: no fixture left for %s %s", r.Method, r.URL.Path), http.StatusInternalServerError)
		return
	}
	for name, v := range f.Response.Headers {
		w.Header().Set(name, v)
	}
	w.WriteHeader(f.Response.Status)
	_, _ = io.WriteString(w, f.Response.Body)
}

// Received returns the request bodies the server was sent, in order.
func (s *Server) Received() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]byte(nil), s.received...)
}

// Remaining is how many fixtures were not requested.
func (s *Server) Remaining() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queue)
}

// Events splits a recorded SSE body into its events, each with its
// trailing blank line.
func Events(body string) []string {
	var out []string
	for _, ev := range strings.SplitAfter(body, "\n\n") {
		if strings.TrimSpace(ev) != "" {
			out = append(out, ev)
		}
	}
	return out
}

// Derive is f with a new response body, labeled with the edit that made it.
func Derive(f Fixture, edit, body string) Fixture {
	f.Source = "derived: " + edit
	f.Response.Body = body
	return f
}

// CutAfter keeps a stream's events up to and including the n-th one that
// contains marker, and drops the rest: the stream ends without its terminal
// event, as when a connection drops mid-stream.
func CutAfter(t testing.TB, f Fixture, marker string, n int) Fixture {
	t.Helper()
	var kept strings.Builder
	for _, ev := range Events(f.Response.Body) {
		kept.WriteString(ev)
		if strings.Contains(ev, marker) {
			if n--; n == 0 {
				return Derive(f, fmt.Sprintf("cut after %q; no terminal event", marker), kept.String())
			}
		}
	}
	t.Fatalf("marker %q not found enough times", marker)
	return f
}

// AnthropicOverloadedAfter cuts an Anthropic stream after the first event
// containing marker and ends it with the overloaded_error event Anthropic
// sends mid-stream.
func AnthropicOverloadedAfter(t testing.TB, f Fixture, marker string) Fixture {
	t.Helper()
	cut := CutAfter(t, f, marker, 1)
	return Derive(f, "overloaded_error event after "+marker,
		cut.Response.Body+"event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n")
}

// AnthropicRateLimited is the 429 Anthropic answers with, Retry-After in
// seconds.
func AnthropicRateLimited(f Fixture, retryAfter string) Fixture {
	f.Source = "derived: 429 rate_limit_error, Retry-After " + retryAfter
	f.Response = Response{Status: http.StatusTooManyRequests, Headers: map[string]string{"Content-Type": "application/json", "Retry-After": retryAfter},
		Body: `{"type":"error","error":{"type":"rate_limit_error","message":"Number of request tokens has exceeded your per-minute rate limit"}}`}
	return f
}

// AnthropicMessage is a non-streaming Messages API answer with text, the
// shape Complete reads.
func AnthropicMessage(text string) Fixture {
	body, _ := json.Marshal(map[string]any{"id": "msg_derived", "type": "message", "role": "assistant", "model": "recorded-model",
		"content": []any{map[string]any{"type": "text", "text": text}}, "stop_reason": "end_turn", "stop_sequence": nil,
		"usage": map[string]any{"input_tokens": 50, "output_tokens": 20}})
	return Fixture{Source: "derived: non-streaming text answer", Request: Request{Method: http.MethodPost, Path: "/v1/messages"},
		Response: Response{Status: http.StatusOK, Headers: map[string]string{"Content-Type": "application/json"}, Body: string(body)}}
}
