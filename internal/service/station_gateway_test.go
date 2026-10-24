package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	llmcontracts "github.com/hollis-labs/libs/llm/core/llmcontracts"
	llmtypes "github.com/hollis-labs/libs/llm/core/llmtypes"

	"github.com/hollis-labs/nanite/internal/llm/station"
)

// fakeGateway stands in for the Station LLM gateway. It serves each alias on
// one wire only, requires a valid X-Station-Session, and records every request
// so a test can assert what Nanite sent.
type fakeGateway struct {
	t   *testing.T
	srv *httptest.Server

	mu   sync.Mutex
	reqs []gatewayRequest
}

type gatewayRequest struct {
	Path   string
	Header http.Header
	Body   string
}

const gatewayRoot = "/api/llm-gateway"

var gatewaySessionRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

// aliasWire says which endpoint serves an alias, as the live gateway does.
var aliasWire = map[string]string{
	"go-glm":          "/v1/chat/completions",
	"openai-mini":     "/v1/chat/completions",
	"go-gpt":          "/v1/responses",
	"go-haiku":        "/v1/messages",
	"anthropic-haiku": "/v1/messages",
}

func newFakeGateway(t *testing.T) *fakeGateway {
	g := &fakeGateway{t: t}
	g.srv = httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *fakeGateway) last() gatewayRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.reqs) == 0 {
		g.t.Fatal("the gateway saw no request")
	}
	return g.reqs[len(g.reqs)-1]
}

func (g *fakeGateway) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.reqs)
}

func (g *fakeGateway) serve(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	g.mu.Lock()
	g.reqs = append(g.reqs, gatewayRequest{Path: r.URL.Path, Header: r.Header.Clone(), Body: string(raw)})
	g.mu.Unlock()

	fail := func(code, msg string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"code":"`+code+`","type":"`+code+`","message":"`+msg+`"}}`)
	}
	sess := r.Header.Get(station.SessionHeader)
	if sess == "" {
		fail("session_required", "X-Station-Session is required")
		return
	}
	if !gatewaySessionRe.MatchString(sess) {
		fail("invalid_session", "bad session id")
		return
	}
	var body struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	_ = json.Unmarshal(raw, &body)
	wire, ok := aliasWire[body.Model]
	if !ok || gatewayRoot+wire != r.URL.Path {
		fail("model_not_available", "alias "+body.Model+" is not served here")
		return
	}

	if strings.HasSuffix(r.URL.Path, "/messages") {
		g.anthropic(w, body.Stream)
		return
	}
	g.openai(w, body.Stream)
}

func (g *fakeGateway) openai(w http.ResponseWriter, stream bool) {
	if !stream {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"c1","object":"chat.completion","created":1,"model":"go-glm","choices":[{"index":0,"message":{"role":"assistant","content":"openai-wire answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	for _, s := range []string{
		`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"go-glm","choices":[{"index":0,"delta":{"role":"assistant","content":"openai-wire "},"finish_reason":""}]}`,
		`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"go-glm","choices":[{"index":0,"delta":{"content":"stream"},"finish_reason":"stop"}]}`,
		`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"go-glm","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`,
		`[DONE]`,
	} {
		_, _ = io.WriteString(w, "data: "+s+"\n\n")
	}
}

func (g *fakeGateway) anthropic(w http.ResponseWriter, stream bool) {
	if !stream {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"m1","type":"message","role":"assistant","model":"go-haiku","content":[{"type":"text","text":"messages-wire answer"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	for _, e := range [][2]string{
		{"message_start", `{"type":"message_start","message":{"id":"m1","type":"message","role":"assistant","model":"go-haiku","content":[],"usage":{"input_tokens":5,"output_tokens":0}}}`},
		{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
		{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"messages-wire stream"}}`},
		{"content_block_stop", `{"type":"content_block_stop","index":0}`},
		{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4}}`},
		{"message_stop", `{"type":"message_stop"}`},
	} {
		_, _ = io.WriteString(w, "event: "+e[0]+"\ndata: "+e[1]+"\n\n")
	}
}

// stationProvider builds the named station adapter the way startup does: the
// spec bound to row settings, with a deferred key.
func stationProvider(t *testing.T, name, settings, key string) llmcontracts.Provider {
	t.Helper()
	for _, spec := range APIProviderSpecs() {
		if spec.Name != name {
			continue
		}
		bound, err := spec.WithSettings(settings)
		if err != nil {
			t.Fatalf("WithSettings: %v", err)
		}
		return bound.NewLazyProvider(func(context.Context) (string, error) { return key, nil })
	}
	t.Fatalf("no spec %q", name)
	return nil
}

func (g *fakeGateway) openaiSettings(extra string) string {
	return `{"base_url":"` + g.srv.URL + gatewayRoot + `/v1"` + extra + `}`
}

func (g *fakeGateway) anthropicSettings(extra string) string {
	return `{"base_url":"` + g.srv.URL + gatewayRoot + `"` + extra + `}`
}

func userTurn(model string) llmtypes.ChatRequest {
	return llmtypes.ChatRequest{Model: model, Messages: []llmtypes.ChatMessage{{Role: "user", Content: "hi"}}}
}

func drainStation(t *testing.T, ch <-chan llmtypes.StreamEvent) (text string) {
	t.Helper()
	var b strings.Builder
	for ev := range ch {
		switch ev.Type {
		case llmtypes.EventDelta:
			b.WriteString(ev.Content)
		case llmtypes.EventError:
			t.Fatalf("stream error event: %s", ev.Error)
		}
	}
	return b.String()
}

func TestStationOpenAIWire_CompleteAndStream(t *testing.T) {
	g := newFakeGateway(t)
	prov := stationProvider(t, "station-openai", g.openaiSettings(`,"headers":{"X-Trace-Tag":"nanite"}`), "tok-openai")
	ctx := station.WithSessionID(context.Background(), "ses_01HZ.abc:1")

	got, err := prov.Complete(ctx, userTurn("go-glm"))
	if err != nil || got != "openai-wire answer" {
		t.Fatalf("Complete = %q, %v", got, err)
	}
	req := g.last()
	if req.Path != gatewayRoot+"/v1/chat/completions" {
		t.Errorf("path = %s", req.Path)
	}
	if h := req.Header.Get("Authorization"); h != "Bearer tok-openai" {
		t.Errorf("Authorization = %q", h)
	}
	if h := req.Header.Get(station.SessionHeader); h != "ses_01HZ.abc:1" {
		t.Errorf("%s = %q", station.SessionHeader, h)
	}
	if h := req.Header.Get("X-Trace-Tag"); h != "nanite" {
		t.Errorf("static header = %q", h)
	}

	ch, err := prov.StreamChat(ctx, userTurn("go-glm"))
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	if text := drainStation(t, ch); text != "openai-wire stream" {
		t.Errorf("stream text = %q", text)
	}
	req = g.last()
	if req.Path != gatewayRoot+"/v1/chat/completions" || req.Header.Get(station.SessionHeader) != "ses_01HZ.abc:1" {
		t.Errorf("stream request: path %s session %q", req.Path, req.Header.Get(station.SessionHeader))
	}
	if !strings.Contains(req.Body, `"stream":true`) {
		t.Errorf("stream body: %s", req.Body)
	}
}

func TestStationAnthropicWire_CompleteAndStream(t *testing.T) {
	g := newFakeGateway(t)
	prov := stationProvider(t, "station-anthropic", g.anthropicSettings(""), "tok-anthropic")
	ctx := station.WithSessionID(context.Background(), "ses_01HZ.abc:1")

	got, err := prov.Complete(ctx, userTurn("go-haiku"))
	if err != nil || got != "messages-wire answer" {
		t.Fatalf("Complete = %q, %v", got, err)
	}
	req := g.last()
	if req.Path != gatewayRoot+"/v1/messages" {
		t.Errorf("path = %s", req.Path)
	}
	if h := req.Header.Get("X-Api-Key"); h != "tok-anthropic" {
		t.Errorf("x-api-key = %q", h)
	}
	if h := req.Header.Get(station.SessionHeader); h != "ses_01HZ.abc:1" {
		t.Errorf("%s = %q", station.SessionHeader, h)
	}
	if h := req.Header.Get("Anthropic-Beta"); h == "" {
		t.Errorf("anthropic-beta missing by default")
	}

	ch, err := prov.StreamChat(ctx, userTurn("go-haiku"))
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	if text := drainStation(t, ch); text != "messages-wire stream" {
		t.Errorf("stream text = %q", text)
	}
	if h := g.last().Header.Get(station.SessionHeader); h != "ses_01HZ.abc:1" {
		t.Errorf("stream session = %q", h)
	}
}

func TestStationAnthropicWire_FlagsAndAuth(t *testing.T) {
	g := newFakeGateway(t)
	prov := stationProvider(t, "station-anthropic",
		g.anthropicSettings(`,"disable_anthropic_beta":true,"disable_cache_control":true,"auth":"bearer"`), "tok-anthropic")
	ctx := station.WithSessionID(context.Background(), "ses-1")

	req := userTurn("go-haiku")
	req.SystemPrompt = "be brief"
	req.CacheHints = []llmcontracts.CacheHint{{Position: "system"}, {Position: "recent_message"}}
	for name, call := range map[string]func() error{
		"complete": func() error { _, err := prov.Complete(ctx, req); return err },
		"stream": func() error {
			ch, err := prov.StreamChat(ctx, req)
			if err == nil {
				drainStation(t, ch)
			}
			return err
		},
	} {
		if err := call(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		last := g.last()
		if h := last.Header.Get("Anthropic-Beta"); h != "" {
			t.Errorf("%s: anthropic-beta = %q, want none", name, h)
		}
		if strings.Contains(last.Body, "cache_control") {
			t.Errorf("%s: body carries cache_control: %s", name, last.Body)
		}
		if h := last.Header.Get("Authorization"); h != "Bearer tok-anthropic" {
			t.Errorf("%s: Authorization = %q", name, h)
		}
		if h := last.Header.Get("X-Api-Key"); h != "" {
			t.Errorf("%s: x-api-key = %q, want none with bearer auth", name, h)
		}
	}

	// With the flags off the markers and header are back.
	plain := stationProvider(t, "station-anthropic", g.anthropicSettings(""), "tok-anthropic")
	if _, err := plain.Complete(ctx, req); err != nil {
		t.Fatal(err)
	}
	last := g.last()
	if !strings.Contains(last.Body, "cache_control") || last.Header.Get("Anthropic-Beta") == "" {
		t.Errorf("default provider dropped cache_control or anthropic-beta: %v %s", last.Header, last.Body)
	}
}

// A key set after the base URL must not lose it: SetAPIKey rebuilds the SDK
// client, and so does resolving a deferred key.
func TestStationBaseURLSurvivesKeySet(t *testing.T) {
	g := newFakeGateway(t)
	ctx := station.WithSessionID(context.Background(), "ses-1")
	for _, tc := range []struct{ name, settings, model string }{
		{"station-openai", g.openaiSettings(""), "go-glm"},
		{"station-anthropic", g.anthropicSettings(""), "go-haiku"},
	} {
		spec, _ := func() (APIProviderSpec, bool) {
			for _, s := range APIProviderSpecs() {
				if s.Name == tc.name {
					return s, true
				}
			}
			return APIProviderSpec{}, false
		}()
		bound, err := spec.WithSettings(tc.settings)
		if err != nil {
			t.Fatal(err)
		}
		before := g.count()
		if _, err := bound.NewProvider("k").Complete(ctx, userTurn(tc.model)); err != nil {
			t.Fatalf("%s NewProvider: %v", tc.name, err)
		}
		if g.count() != before+1 {
			t.Errorf("%s: NewProvider (SetAPIKey) request did not reach the gateway", tc.name)
		}
	}
}

func TestStationSessionIDIsStableAcrossTurns(t *testing.T) {
	g := newFakeGateway(t)
	prov := stationProvider(t, "station-openai", g.openaiSettings(""), "k")
	ctx := station.WithSessionID(context.Background(), "ses 01/odd id")
	var seen []string
	for i := 0; i < 3; i++ {
		if _, err := prov.Complete(ctx, userTurn("go-glm")); err != nil {
			t.Fatal(err)
		}
		seen = append(seen, g.last().Header.Get(station.SessionHeader))
	}
	if seen[0] == "" || seen[0] != seen[1] || seen[1] != seen[2] || !gatewaySessionRe.MatchString(seen[0]) {
		t.Errorf("session ids across turns = %v", seen)
	}
}

func TestStationGatewayErrors(t *testing.T) {
	g := newFakeGateway(t)
	openai := stationProvider(t, "station-openai", g.openaiSettings(""), "k")
	anth := stationProvider(t, "station-anthropic", g.anthropicSettings(""), "k")
	withSession := station.WithSessionID(context.Background(), "ses-1")

	t.Run("session_required", func(t *testing.T) {
		for name, p := range map[string]struct {
			prov  llmcontracts.Provider
			model string
		}{"openai": {openai, "go-glm"}, "anthropic": {anth, "go-haiku"}} {
			_, err := p.prov.Complete(context.Background(), userTurn(p.model))
			if !errors.Is(err, station.ErrSessionRequired) {
				t.Errorf("%s complete: %v", name, err)
			}
			if ch, err := p.prov.StreamChat(context.Background(), userTurn(p.model)); err != nil {
				if !errors.Is(err, station.ErrSessionRequired) {
					t.Errorf("%s stream: %v", name, err)
				}
			} else {
				var msg string
				for ev := range ch {
					if ev.Type == llmtypes.EventError {
						msg = ev.Error
					}
				}
				if !strings.Contains(msg, "session id") {
					t.Errorf("%s stream error event = %q", name, msg)
				}
			}
		}
	})

	t.Run("model_not_available is a wrong-wire error", func(t *testing.T) {
		// go-gpt is served on /responses only; go-haiku on /messages only.
		for name, p := range map[string]struct {
			prov  llmcontracts.Provider
			model string
		}{"openai wire, messages alias": {openai, "go-haiku"}, "anthropic wire, chat alias": {anth, "go-glm"}} {
			_, err := p.prov.Complete(withSession, userTurn(p.model))
			if !errors.Is(err, station.ErrModelNotAvailable) {
				t.Errorf("%s: %v", name, err)
			} else if !strings.Contains(err.Error(), "wrong wire for this alias") {
				t.Errorf("%s: message %q does not say wrong wire", name, err)
			}
		}
	})

	t.Run("invalid_session from the gateway", func(t *testing.T) {
		bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"type":"error","error":{"type":"invalid_session","message":"bad"}}`)
		}))
		defer bad.Close()
		for name, p := range map[string]struct {
			prov  llmcontracts.Provider
			model string
		}{
			"openai":    {stationProvider(t, "station-openai", `{"base_url":"`+bad.URL+`/v1"}`, "k"), "go-glm"},
			"anthropic": {stationProvider(t, "station-anthropic", `{"base_url":"`+bad.URL+`"}`, "k"), "go-haiku"},
		} {
			_, err := p.prov.Complete(withSession, userTurn(p.model))
			if !errors.Is(err, station.ErrInvalidSession) {
				t.Errorf("%s: %v", name, err)
			}
		}
	})
}

// The Anthropic client seeds a 50000 TPM budget that nothing calibrates behind
// a gateway; a request larger than that must still go through, and the adapter
// must report its limit as unknown.
func TestStationAnthropicPassesThroughRateBudget(t *testing.T) {
	g := newFakeGateway(t)
	prov := stationProvider(t, "station-anthropic", g.anthropicSettings(""), "k")
	ctx := station.WithSessionID(context.Background(), "ses-1")

	big := userTurn("go-haiku")
	big.Messages[0].Content = strings.Repeat("x", 400_000) // ~100k tokens by the adapter's estimate
	ch, err := prov.StreamChat(ctx, big)
	if err != nil {
		t.Fatalf("large streaming request refused: %v", err)
	}
	drainStation(t, ch)
	if rl, ok := prov.(llmcontracts.RateLimited); !ok || rl.RateLimitTPM() != 0 {
		t.Errorf("RateLimitTPM must report unknown (0) behind a gateway")
	}
}

func TestStationSpecsNeedValidSettings(t *testing.T) {
	for _, spec := range APIProviderSpecs() {
		if !spec.Station {
			if _, err := spec.WithSettings("not json at all"); err != nil {
				t.Errorf("%s: a non-station spec must ignore settings: %v", spec.Name, err)
			}
			continue
		}
		for _, bad := range []string{"", `{}`, `{"base_url":"ftp://x"}`, `{"base_url":"https://x","headers":{"Authorization":"Bearer z"}}`} {
			if _, err := spec.WithSettings(bad); err == nil {
				t.Errorf("%s accepted settings %q", spec.Name, bad)
			}
		}
	}
}
