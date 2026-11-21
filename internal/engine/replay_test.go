package engine

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/libs/agent/core/turn"
	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/provider/anthropic"
	"github.com/hollis-labs/nanite/internal/provider/failure"
	"github.com/hollis-labs/nanite/internal/provider/openai"
	"github.com/hollis-labs/nanite/internal/provider/providertest"
	"github.com/hollis-labs/nanite/internal/provider/station"
	"github.com/hollis-labs/nanite/internal/tools"
)

// These tests run whole turns through the real Anthropic adapter against
// recorded exchanges (internal/provider/anthropic/testdata/replay) and
// failures derived from them, so the engine meets real stream shapes:
// usage split across events, input_json deltas, padded SSE frames.

func recorded(t *testing.T, name string) providertest.Fixture {
	t.Helper()
	return providertest.Load(t, filepath.Join("..", "provider", "anthropic", "testdata", "replay", name+".json"))
}

// replayEngine is an engine whose model is the Anthropic adapter, replaying
// fixtures in order. Retry waits are recorded instead of slept.
func replayEngine(t *testing.T, runtime string, fixtures ...providertest.Fixture) (*Engine, *providertest.Server, string, *[]time.Duration) {
	t.Helper()
	srv := providertest.Serve(t, fixtures...)
	model := anthropic.New()
	model.SetAPIKey("replay")
	model.SetGateway(station.Settings{BaseURL: srv.URL})
	e, _, id := setupEngine(t, model, runtime)
	var mu sync.Mutex
	waits := &[]time.Duration{}
	e.retryWait = func(ctx context.Context, d time.Duration) error {
		mu.Lock()
		*waits = append(*waits, d)
		mu.Unlock()
		return ctx.Err()
	}
	return e, srv, id, waits
}

func send(t *testing.T, e *Engine, id string) (events.Result, error) {
	t.Helper()
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	return run.Wait()
}

func TestReplayTruncatedStreamFailsTheTurn(t *testing.T) {
	// H5: a stream that ends without message_stop is a failed turn, not a
	// short answer, and its half-received tool call never runs.
	e, srv, id, _ := replayEngine(t, "", providertest.CutAfter(t, recorded(t, "tool_use"), "input_json_delta", 3))
	ran := false
	registerLookup(t, e, &ran, nil)
	_, err := send(t, e, id)
	if !errors.Is(err, turn.ErrTurnTruncated) {
		t.Fatalf("truncated turn: %v", err)
	}
	if status, _ := e.cfg.Store.TurnStatus(t.Context(), turnOf(t, e, id)); status != "failed" {
		t.Fatalf("turn status %q", status)
	}
	if ran || srv.Remaining() != 0 || len(srv.Received()) != 1 {
		t.Fatalf("ran=%v requests=%d", ran, len(srv.Received()))
	}
}

func TestReplayRateLimitRetriesAfterTheServersDelay(t *testing.T) {
	// H7: a 429 is retried after at least its Retry-After.
	e, srv, id, waits := replayEngine(t, "", providertest.AnthropicRateLimited(recorded(t, "text"), "7"), recorded(t, "text"))
	result, err := send(t, e, id)
	if err != nil || result.Text != "pong" {
		t.Fatalf("result %+v, %v", result, err)
	}
	if len(srv.Received()) != 2 || len(*waits) != 1 || (*waits)[0] < 7*time.Second {
		t.Fatalf("requests %d waits %v", len(srv.Received()), *waits)
	}
}

func TestReplayRetryCap(t *testing.T) {
	// H7: two retries, then the turn fails with the classified error.
	limited := providertest.AnthropicRateLimited(recorded(t, "text"), "1")
	e, srv, id, waits := replayEngine(t, "", limited, limited, limited, recorded(t, "text"))
	_, err := send(t, e, id)
	var classified *failure.Error
	if !errors.As(err, &classified) || classified.Class != failure.Retryable {
		t.Fatalf("after the cap: %v", err)
	}
	if got := len(srv.Received()); got != 3 || len(*waits) != 2 || srv.Remaining() != 1 {
		t.Fatalf("requests %d waits %v remaining %d, want 3 requests and 2 waits", got, *waits, srv.Remaining())
	}
	if status, _ := e.cfg.Store.TurnStatus(t.Context(), turnOf(t, e, id)); status != "failed" {
		t.Fatalf("turn status %q", status)
	}
}

func TestReplayOverloadedBeforeOutputIsRetried(t *testing.T) {
	// Nothing reached the caller yet, so the attempt is retried.
	e, srv, id, waits := replayEngine(t, "", providertest.AnthropicOverloadedAfter(t, recorded(t, "text"), "content_block_start"), recorded(t, "text"))
	result, err := send(t, e, id)
	if err != nil || result.Text != "pong" || len(srv.Received()) != 2 || len(*waits) != 1 {
		t.Fatalf("result %+v, %v, requests %d waits %v", result, err, len(srv.Received()), *waits)
	}
}

func TestReplayOverloadedAfterOutputFailsTheTurn(t *testing.T) {
	// Text already streamed: a retry would repeat it, so the turn fails and
	// keeps what was said.
	e, srv, id, waits := replayEngine(t, "", providertest.AnthropicOverloadedAfter(t, recorded(t, "text"), "text_delta"), recorded(t, "text"))
	result, err := send(t, e, id)
	var classified *failure.Error
	if !errors.As(err, &classified) || classified.Class != failure.Retryable || result.Text != "pong" {
		t.Fatalf("result %+v, %v", result, err)
	}
	if len(srv.Received()) != 1 || len(*waits) != 0 {
		t.Fatalf("requests %d waits %v", len(srv.Received()), *waits)
	}
	if status, _ := e.cfg.Store.TurnStatus(t.Context(), turnOf(t, e, id)); status != "failed" {
		t.Fatalf("turn status %q", status)
	}
}

func TestReplayUsageSumsAcrossSteps(t *testing.T) {
	// The recorded tool step reports 392 in / 52 out and the text step 18 in
	// / 4 out, split across message_start and message_delta.
	e, _, id, _ := replayEngine(t, "", recorded(t, "tool_use"), recorded(t, "text"))
	registerLookup(t, e, nil, nil)
	result, err := send(t, e, id)
	if err != nil {
		t.Fatal(err)
	}
	if result.Usage.InputTokens != 392+18 || result.Usage.OutputTokens != 52+4 || result.Steps != 2 {
		t.Fatalf("usage %+v steps %d", result.Usage, result.Steps)
	}
}

func TestReplayMaxTokensDuringToolCall(t *testing.T) {
	t.Run("recorded: cut as the tool block opened", func(t *testing.T) {
		// The call never closed, so there is nothing to run: the turn ends
		// reporting max_tokens and stores no call without a result.
		e, srv, id, _ := replayEngine(t, "", recorded(t, "tool_use_max_tokens"), recorded(t, "text"))
		ran := false
		registerLookup(t, e, &ran, nil)
		result, err := send(t, e, id)
		if err != nil || result.StopReason != "max_tokens" || ran || len(srv.Received()) != 1 {
			t.Fatalf("result %+v, %v, ran %v, requests %d", result, err, ran, len(srv.Received()))
		}
		report, err := e.cfg.Store.ValidateConversation(t.Context(), id, nil)
		if err != nil || len(report.Findings) != 0 {
			t.Fatalf("stored history: %v, %v", report.Findings, err)
		}
	})
	t.Run("derived: cut inside the arguments", func(t *testing.T) {
		// The partial arguments arrive as _raw, which no repair can fix: the
		// call is answered with an error, paired in the next request, and
		// the turn goes on.
		t.Setenv("NANITE_AUTO_REPAIR", "false")
		tool := recorded(t, "tool_use")
		var kept strings.Builder
		for _, ev := range providertest.Events(tool.Response.Body) {
			if strings.Contains(ev, "al-of-f") {
				break
			}
			kept.WriteString(ev)
		}
		kept.WriteString("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		kept.WriteString("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"max_tokens\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":40}}\n\n")
		kept.WriteString("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		e, srv, id, _ := replayEngine(t, "", providertest.Derive(tool, "max_tokens inside the tool arguments", kept.String()), recorded(t, "text"))
		ran := false
		registerLookup(t, e, &ran, nil)
		result, err := send(t, e, id)
		if err != nil || result.Text != "pong" || ran || len(srv.Received()) != 2 {
			t.Fatalf("result %+v, %v, ran %v, requests %d", result, err, ran, len(srv.Received()))
		}
		var next struct {
			Messages []struct {
				Content []struct {
					Type      string `json:"type"`
					ID        string `json:"id"`
					ToolUseID string `json:"tool_use_id"`
					IsError   bool   `json:"is_error"`
				} `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(srv.Received()[1], &next); err != nil {
			t.Fatal(err)
		}
		var callID string
		answered := false
		for _, m := range next.Messages {
			for _, b := range m.Content {
				if b.Type == "tool_use" {
					callID = b.ID
				}
				if b.Type == "tool_result" && b.ToolUseID == callID && b.IsError {
					answered = true
				}
			}
		}
		if callID == "" || !answered {
			t.Fatalf("call %q answered with an error: %v", callID, answered)
		}
	})
}

func TestReplayClosingReplyOnTheWire(t *testing.T) {
	// At the step limit the closing reply is sent with tool_choice none and
	// the same tools and cache markers as the step before it.
	e, srv, id, _ := replayEngine(t, "runtime:\n  max_steps: 1\n  cache_ttl: '1h'\n", recorded(t, "tool_use"), recorded(t, "closing_tool_choice_none"))
	registerLookup(t, e, nil, nil)
	result, err := send(t, e, id)
	if err != nil || !strings.HasPrefix(result.Text, "I can't continue") {
		t.Fatalf("result %+v, %v", result, err)
	}
	bodies := srv.Received()
	if len(bodies) != 2 {
		t.Fatalf("requests %d", len(bodies))
	}
	var first, closing struct {
		ToolChoice *struct{ Type string } `json:"tool_choice"`
		Tools      json.RawMessage        `json:"tools"`
		System     json.RawMessage        `json:"system"`
	}
	if json.Unmarshal(bodies[0], &first) != nil || json.Unmarshal(bodies[1], &closing) != nil {
		t.Fatal("unreadable request")
	}
	if closing.ToolChoice == nil || closing.ToolChoice.Type != "none" || first.ToolChoice != nil && first.ToolChoice.Type == "none" {
		t.Fatalf("tool_choice first %+v closing %+v", first.ToolChoice, closing.ToolChoice)
	}
	if string(first.Tools) != string(closing.Tools) || string(first.System) != string(closing.System) || len(first.Tools) == 0 {
		t.Fatal("the closing reply changed the cached prefix")
	}
}

func TestReplayCacheMarkersOnTheWire(t *testing.T) {
	// The engine's cache hints become at most four markers, 1h ones before
	// any 5m one, over a prefix that does not change between steps.
	e, srv, id, _ := replayEngine(t, "runtime:\n  cache_ttl: '1h'\n", recorded(t, "tool_use"), recorded(t, "text"))
	registerLookup(t, e, nil, nil)
	if _, err := send(t, e, id); err != nil {
		t.Fatal(err)
	}
	bodies := srv.Received()
	if len(bodies) != 2 {
		t.Fatalf("requests %d", len(bodies))
	}
	var prefixes []string
	for i, body := range bodies {
		var req struct {
			Tools    []map[string]json.RawMessage `json:"tools"`
			System   []map[string]json.RawMessage `json:"system"`
			Messages []struct {
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatal(err)
		}
		var ttls []string
		collect := func(blocks []map[string]json.RawMessage) {
			for _, b := range blocks {
				if raw, ok := b["cache_control"]; ok {
					var cc struct{ TTL string }
					_ = json.Unmarshal(raw, &cc)
					ttls = append(ttls, cc.TTL)
				}
			}
		}
		collect(req.Tools)
		collect(req.System)
		for _, m := range req.Messages {
			var blocks []map[string]json.RawMessage
			if json.Unmarshal(m.Content, &blocks) == nil {
				collect(blocks)
			}
		}
		if len(ttls) == 0 || len(ttls) > 4 {
			t.Fatalf("request %d: %d markers %v", i, len(ttls), ttls)
		}
		seenShort := false
		hasLong := false
		for _, ttl := range ttls {
			switch {
			case ttl == "1h" && seenShort:
				t.Fatalf("request %d: 1h marker after a 5m one: %v", i, ttls)
			case ttl == "1h":
				hasLong = true
			default:
				seenShort = true
			}
		}
		if !hasLong {
			t.Fatalf("request %d: cache_ttl 1h sent no 1h marker: %v", i, ttls)
		}
		tools, _ := json.Marshal(req.Tools)
		system, _ := json.Marshal(req.System)
		prefixes = append(prefixes, string(tools)+string(system))
	}
	if prefixes[0] != prefixes[1] {
		t.Fatal("the cached prefix changed between steps")
	}
}

func TestReplayRepairedArgumentsAreWrittenBack(t *testing.T) {
	// M-o: a call whose arguments miss the schema is repaired before it
	// runs, and the repaired arguments replace the model's in the stored
	// assistant message and in the history the next request sends.
	wrong := recorded(t, "tool_use")
	wrong = providertest.Derive(wrong, `argument "key" renamed "k"`, strings.Replace(wrong.Response.Body, `{\"key\":`, `{\"k\":`, 1))
	repair := providertest.AnthropicMessage(`{"repaired_args":{"key":"capital-of-france"},"missing_required":[]}`)
	t.Setenv("NANITE_AUTO_REPAIR", "true")
	e, srv, id, _ := replayEngine(t, "", wrong, repair, recorded(t, "text"))
	var got string
	registerLookup(t, e, nil, &got)
	result, err := send(t, e, id)
	if err != nil || result.Text != "pong" {
		t.Fatalf("result %+v, %v", result, err)
	}
	if got != `{"key":"capital-of-france"}` {
		t.Fatalf("the tool ran with %s", got)
	}
	history, err := e.cfg.Store.History(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	var stored map[string]any
	for _, m := range history {
		for _, b := range m.Blocks {
			if b.Type == "tool_use" && b.Input != nil {
				stored = *b.Input
			}
		}
	}
	if stored["key"] != "capital-of-france" || stored["k"] != nil {
		t.Fatalf("stored call arguments %v", stored)
	}
	bodies := srv.Received()
	if len(bodies) != 3 {
		t.Fatalf("requests %d", len(bodies))
	}
	var next struct {
		Messages []struct {
			Content []struct {
				Type  string         `json:"type"`
				Input map[string]any `json:"input"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(bodies[2], &next); err != nil {
		t.Fatal(err)
	}
	var sent map[string]any
	for _, m := range next.Messages {
		for _, b := range m.Content {
			if b.Type == "tool_use" {
				sent = b.Input
			}
		}
	}
	if len(sent) != 1 || sent["key"] != "capital-of-france" {
		t.Fatalf("the next request carried the call as %v", sent)
	}
}

// registerLookup adds the lookup tool the recordings call. ran, when set,
// records that it ran; args, when set, receives the arguments it ran with.
func registerLookup(t *testing.T, e *Engine, ran *bool, args *string) {
	t.Helper()
	if err := e.cfg.Tools.SetGroup("replay", tools.Tool{Name: "lookup", Description: "Look up a fact by its key.", ReadOnly: true,
		InputSchema: json.RawMessage(`{"type":"object","properties":{"key":{"type":"string"}},"required":["key"],"additionalProperties":false}`),
		Handler: func(_ context.Context, call tools.Call) (tools.Result, error) {
			if ran != nil {
				*ran = true
			}
			if args != nil {
				*args = string(call.Input)
			}
			return tools.Result{Text: "Paris"}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
}

// turnOf is the conversation's only turn, read from its stored messages.
func turnOf(t *testing.T, e *Engine, id string) string {
	t.Helper()
	history, err := e.cfg.Store.History(t.Context(), id)
	if err != nil || len(history) == 0 {
		t.Fatalf("history %v, %v", history, err)
	}
	return history[0].TurnID
}

func recordedOpenAI(t *testing.T, name string) providertest.Fixture {
	t.Helper()
	return providertest.Load(t, filepath.Join("..", "provider", "openai", "testdata", "replay", name+".json"))
}

func replayOpenAIEngine(t *testing.T, fixtures ...providertest.Fixture) (*Engine, *providertest.Server, string) {
	t.Helper()
	srv := providertest.Serve(t, fixtures...)
	model := openai.New("replay", nil)
	model.SetAPI(openai.APIChat)
	model.SetGateway(station.Settings{BaseURL: srv.URL + "/v1"})
	e, _, id := setupEngine(t, model, "")
	e.retryWait = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	return e, srv, id
}

func TestReplayOpenAITruncatedStreamFailsTheTurn(t *testing.T) {
	// A Chat Completions stream cut before its finish_reason is a failed
	// turn: the half-received call never runs.
	e, srv, id := replayOpenAIEngine(t, providertest.CutAfter(t, recordedOpenAI(t, "tool_use"), `"arguments"`, 2))
	ran := false
	registerLookup(t, e, &ran, nil)
	if _, err := send(t, e, id); !errors.Is(err, turn.ErrTurnTruncated) {
		t.Fatalf("truncated turn: %v", err)
	}
	if ran || len(srv.Received()) != 1 {
		t.Fatalf("ran %v requests %d", ran, len(srv.Received()))
	}
}

func TestReplayOpenAIUsageSumsAcrossSteps(t *testing.T) {
	e, _, id := replayOpenAIEngine(t, recordedOpenAI(t, "tool_use"), recordedOpenAI(t, "text"))
	var args string
	registerLookup(t, e, nil, &args)
	result, err := send(t, e, id)
	if err != nil || result.Text != "pong" || args != `{"key":"capital-of-france"}` {
		t.Fatalf("result %+v, %v, args %s", result, err, args)
	}
	in, out := openAIUsage(t, recordedOpenAI(t, "tool_use"))
	in2, out2 := openAIUsage(t, recordedOpenAI(t, "text"))
	if result.Usage.InputTokens != in+in2 || result.Usage.OutputTokens != out+out2 {
		t.Fatalf("usage %+v, want %d in / %d out", result.Usage, in+in2, out+out2)
	}
}

// openAIUsage reads a recorded stream's final usage chunk.
func openAIUsage(t *testing.T, f providertest.Fixture) (in, out int) {
	t.Helper()
	for _, ev := range providertest.Events(f.Response.Body) {
		data, ok := strings.CutPrefix(strings.TrimSpace(ev), "data: ")
		if !ok || data == "[DONE]" {
			continue
		}
		var chunk struct {
			Usage *struct {
				PromptTokens        int `json:"prompt_tokens"`
				CompletionTokens    int `json:"completion_tokens"`
				PromptTokensDetails struct {
					CachedTokens int `json:"cached_tokens"`
				} `json:"prompt_tokens_details"`
			} `json:"usage"`
		}
		if json.Unmarshal([]byte(data), &chunk) == nil && chunk.Usage != nil {
			return chunk.Usage.PromptTokens - chunk.Usage.PromptTokensDetails.CachedTokens, chunk.Usage.CompletionTokens
		}
	}
	t.Fatal("no usage chunk")
	return 0, 0
}
