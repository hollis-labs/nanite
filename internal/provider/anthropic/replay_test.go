package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/provider/failure"
	"github.com/hollis-labs/nanite/internal/provider/providertest"
	"github.com/hollis-labs/nanite/internal/provider/station"
	"github.com/hollis-labs/nanite/internal/provider/toolchoice"
)

// Replay tests run the adapter against recorded Anthropic Messages exchanges
// (testdata/replay, see TestRecordFixtures) and failures derived from them.

func fixture(t *testing.T, name string) providertest.Fixture {
	t.Helper()
	return providertest.Load(t, filepath.Join("testdata", "replay", name+".json"))
}

// replayClient points a client at a replay server for fixtures.
func replayClient(t *testing.T, fixtures ...providertest.Fixture) (*Client, *providertest.Server) {
	t.Helper()
	srv := providertest.Serve(t, fixtures...)
	c := New()
	c.SetAPIKey("replay")
	c.SetGateway(station.Settings{BaseURL: srv.URL})
	return c, srv
}

// scenarioRequest is the request a scenario recorded, with the committed
// model name, so replay sends the same bytes.
func scenarioRequest(t *testing.T, name string) llmtypes.ChatRequest {
	t.Helper()
	for _, s := range anthropicScenarios() {
		if s.name == name {
			s.request.Model = recordedModel
			return s.request
		}
	}
	t.Fatalf("no scenario %s", name)
	return llmtypes.ChatRequest{}
}

func drain(ctx context.Context, t *testing.T, c *Client, req llmtypes.ChatRequest) []llmtypes.StreamEvent {
	t.Helper()
	stream, err := c.StreamChat(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	var out []llmtypes.StreamEvent
	for ev := range stream {
		out = append(out, ev)
	}
	return out
}

func TestReplayWireMatchesRecording(t *testing.T) {
	// The adapter sends exactly what was recorded: the cache markers, tool
	// schema and tool_choice are part of the wire contract.
	for _, s := range anthropicScenarios() {
		t.Run(s.name, func(t *testing.T) {
			f := fixture(t, s.name)
			c, srv := replayClient(t, f)
			ctx := t.Context()
			if s.noTools {
				ctx = toolchoice.WithNone(ctx)
			}
			drain(ctx, t, c, scenarioRequest(t, s.name))
			got := srv.Received()
			if len(got) != 1 || !jsonEqual(t, got[0], f.Request.Body) {
				t.Fatalf("request differs from the recording:\n got %s\nwant %s", got, f.Request.Body)
			}
		})
	}
}

func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	ja, _ := json.Marshal(x)
	jb, _ := json.Marshal(y)
	return bytes.Equal(ja, jb)
}

func usageOf(events []llmtypes.StreamEvent) llmtypes.Usage {
	var u llmtypes.Usage
	for _, ev := range events {
		if ev.Usage == nil {
			continue
		}
		u.InputTokens += ev.Usage.InputTokens
		u.OutputTokens += ev.Usage.OutputTokens
		u.CacheReadTokens += ev.Usage.CacheReadTokens
		u.CacheCreationTokens += ev.Usage.CacheCreationTokens
		if ev.Usage.StopReason != "" {
			u.StopReason = ev.Usage.StopReason
		}
	}
	return u
}

func last(events []llmtypes.StreamEvent) llmtypes.StreamEvent {
	if len(events) == 0 {
		return llmtypes.StreamEvent{}
	}
	return events[len(events)-1]
}

func TestReplayText(t *testing.T) {
	c, _ := replayClient(t, fixture(t, "text"))
	events := drain(t.Context(), t, c, scenarioRequest(t, "text"))
	var text string
	for _, ev := range events {
		if ev.Type == llmtypes.EventDelta {
			text += ev.Content
		}
	}
	u := usageOf(events)
	if text != "pong" || last(events).Type != llmtypes.EventDone || u.InputTokens != 18 || u.OutputTokens != 4 || u.StopReason != "end_turn" {
		t.Fatalf("text %q usage %+v last %+v", text, u, last(events))
	}
}

func TestReplayToolUse(t *testing.T) {
	c, _ := replayClient(t, fixture(t, "tool_use"))
	events := drain(t.Context(), t, c, scenarioRequest(t, "tool_use"))
	var calls []*llmtypes.ToolUseBlock
	for _, ev := range events {
		if ev.ToolUse != nil {
			calls = append(calls, ev.ToolUse)
		}
	}
	u := usageOf(events)
	if len(calls) != 1 || calls[0].Name != "lookup" || calls[0].Input["key"] != "capital-of-france" || u.StopReason != "tool_use" ||
		u.InputTokens != 392 || u.OutputTokens != 52 || last(events).Type != llmtypes.EventDone {
		t.Fatalf("calls %+v usage %+v", calls, u)
	}
}

func TestReplayMaxTokensDuringToolCall(t *testing.T) {
	// Recorded: the budget ran out right after the tool block opened.
	c, _ := replayClient(t, fixture(t, "tool_use_max_tokens"))
	events := drain(t.Context(), t, c, scenarioRequest(t, "tool_use_max_tokens"))
	u := usageOf(events)
	if u.StopReason != "max_tokens" || last(events).Type != llmtypes.EventDone {
		t.Fatalf("usage %+v last %+v", u, last(events))
	}
	for _, ev := range events {
		if ev.ToolUse != nil && len(ev.ToolUse.Input) != 0 {
			t.Fatalf("a cut tool call carries arguments: %+v", ev.ToolUse)
		}
	}

	// Derived, in the recorded shape: the budget runs out inside the
	// argument JSON and, as recorded, no content_block_stop follows. The
	// unfinished call is never emitted: there is nothing to run, and nothing
	// stored without a result.
	tool := fixture(t, "tool_use")
	var kept strings.Builder
	for _, ev := range providertest.Events(tool.Response.Body) {
		if strings.Contains(ev, `al-of-f`) {
			break
		}
		kept.WriteString(ev)
	}
	if !strings.Contains(kept.String(), `{\"key\":`) {
		t.Fatal("the cut does not fall inside the arguments")
	}
	kept.WriteString("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"max_tokens\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":40}}\n\n")
	kept.WriteString("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	c, _ = replayClient(t, providertest.Derive(tool, "max_tokens inside the tool arguments, recorded shape", kept.String()))
	events = drain(t.Context(), t, c, scenarioRequest(t, "tool_use"))
	for _, ev := range events {
		if ev.ToolUse != nil {
			t.Fatalf("an unfinished call was emitted: %+v", ev.ToolUse)
		}
	}
	if usageOf(events).StopReason != "max_tokens" || last(events).Type != llmtypes.EventDone {
		t.Fatalf("usage %+v last %+v", usageOf(events), last(events))
	}
}

func TestReplayCacheMarkersAndUsage(t *testing.T) {
	c, srv := replayClient(t, fixture(t, "cache_write"), fixture(t, "cache_read"))
	req := scenarioRequest(t, "cache_write")
	write := usageOf(drain(t.Context(), t, c, req))
	read := usageOf(drain(t.Context(), t, c, req))
	if write.CacheCreationTokens != 5785 || write.CacheReadTokens != 0 || read.CacheReadTokens != 5785 || read.CacheCreationTokens != 0 || read.InputTokens != 4 {
		t.Fatalf("write %+v read %+v", write, read)
	}
	bodies := srv.Received()
	if len(bodies) != 2 {
		t.Fatalf("requests %d", len(bodies))
	}
	for i, body := range bodies {
		markers := cacheMarkers(t, body)
		if len(markers) == 0 || len(markers) > 4 {
			t.Fatalf("request %d: %d cache markers", i, len(markers))
		}
		// Anthropic requires every 1h marker to come before any 5m marker.
		seen5m := false
		for _, ttl := range markers {
			if ttl != "1h" {
				seen5m = true
			} else if seen5m {
				t.Fatalf("request %d: a 1h marker after a 5m one: %v", i, markers)
			}
		}
	}
	// The cached prefix (tools, then system) is byte-stable between turns.
	if a, b := prefix(t, bodies[0]), prefix(t, bodies[1]); a == "" || a != b {
		t.Fatalf("cached prefix changed:\n%s\n%s", a, b)
	}
}

// cacheMarkers lists each cache_control's TTL in prompt order (tools,
// system, messages); "5m" for a marker with none.
func cacheMarkers(t *testing.T, body []byte) []string {
	t.Helper()
	var req struct {
		Tools    []map[string]json.RawMessage `json:"tools"`
		System   []map[string]json.RawMessage `json:"system"`
		Messages []struct {
			Content []map[string]json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	var out []string
	add := func(blocks []map[string]json.RawMessage) {
		for _, b := range blocks {
			raw, ok := b["cache_control"]
			if !ok {
				continue
			}
			var cc struct{ TTL string }
			_ = json.Unmarshal(raw, &cc)
			if cc.TTL == "" {
				cc.TTL = "5m"
			}
			out = append(out, cc.TTL)
		}
	}
	add(req.Tools)
	add(req.System)
	for _, m := range req.Messages {
		add(m.Content)
	}
	return out
}

func prefix(t *testing.T, body []byte) string {
	t.Helper()
	var req struct {
		Tools  json.RawMessage `json:"tools"`
		System json.RawMessage `json:"system"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	return string(req.Tools) + string(req.System)
}

func TestReplayClosingReplySendsToolChoiceNone(t *testing.T) {
	c, srv := replayClient(t, fixture(t, "closing_tool_choice_none"))
	events := drain(toolchoice.WithNone(t.Context()), t, c, scenarioRequest(t, "closing_tool_choice_none"))
	for _, ev := range events {
		if ev.ToolUse != nil {
			t.Fatalf("a closing reply produced a tool call: %+v", ev.ToolUse)
		}
	}
	var req struct {
		ToolChoice struct{ Type string } `json:"tool_choice"`
	}
	if err := json.Unmarshal(srv.Received()[0], &req); err != nil || req.ToolChoice.Type != "none" || last(events).Type != llmtypes.EventDone {
		t.Fatalf("tool_choice %+v, %v", req.ToolChoice, err)
	}
}

func TestReplayOverloadedMidStream(t *testing.T) {
	c, _ := replayClient(t, providertest.AnthropicOverloadedAfter(t, fixture(t, "text"), "text_delta"))
	events := drain(t.Context(), t, c, scenarioRequest(t, "text"))
	end := last(events)
	if end.Type != llmtypes.EventError {
		t.Fatalf("last event %+v", end)
	}
	var classified *failure.Error
	if err := failure.FromEvent(end); !errors.As(err, &classified) || classified.Class != failure.Retryable {
		t.Fatalf("overloaded_error classified as %v", err)
	}
	for _, ev := range events {
		if ev.Type == llmtypes.EventDone {
			t.Fatal("an overloaded stream reported Done")
		}
	}
}

func TestReplayTruncatedStreamHasNoDone(t *testing.T) {
	for _, tc := range []struct {
		name, scenario string
		f              providertest.Fixture
		wantText       string
	}{
		{"mid text", "text", providertest.CutAfter(t, fixture(t, "text"), "text_delta", 1), "pong"},
		{"mid tool JSON", "tool_use", providertest.CutAfter(t, fixture(t, "tool_use"), "input_json_delta", 3), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, srv := replayClient(t, tc.f)
			events := drain(t.Context(), t, c, scenarioRequest(t, tc.scenario))
			var text string
			for _, ev := range events {
				switch {
				case ev.Type == llmtypes.EventDone || ev.ToolUse != nil || ev.Type == llmtypes.EventError:
					t.Fatalf("a cut stream produced %+v", ev)
				case ev.Type == llmtypes.EventDelta:
					text += ev.Content
				}
			}
			// Served and read up to the cut: truncation, not a failed request.
			if text != tc.wantText || len(srv.Received()) != 1 || usageOf(events).InputTokens == 0 {
				t.Fatalf("text %q requests %d usage %+v", text, len(srv.Received()), usageOf(events))
			}
		})
	}
}

func TestReplayRateLimitedIsRetryableWithRetryAfter(t *testing.T) {
	c, _ := replayClient(t, providertest.AnthropicRateLimited(fixture(t, "text"), "7"))
	_, err := c.StreamChat(t.Context(), scenarioRequest(t, "text"))
	var classified *failure.Error
	if !errors.As(err, &classified) || classified.Class != failure.Retryable || classified.RetryAfter != 7*time.Second {
		t.Fatalf("429: %#v", err)
	}
}
