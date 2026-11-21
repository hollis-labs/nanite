package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/provider/failure"
	"github.com/hollis-labs/nanite/internal/provider/providertest"
	"github.com/hollis-labs/nanite/internal/provider/station"
	"github.com/hollis-labs/nanite/internal/provider/toolchoice"
)

// Replay tests run the adapter against recorded OpenAI Chat Completions
// exchanges (testdata/replay, see TestRecordFixtures) and failures derived
// from them.

func fixture(t *testing.T, name string) providertest.Fixture {
	t.Helper()
	return providertest.Load(t, filepath.Join("testdata", "replay", name+".json"))
}

func replayClient(t *testing.T, fixtures ...providertest.Fixture) (*Client, *providertest.Server) {
	t.Helper()
	srv := providertest.Serve(t, fixtures...)
	c := New("replay", nil)
	c.SetAPI(APIChat)
	c.SetGateway(station.Settings{BaseURL: srv.URL + "/v1"})
	return c, srv
}

func scenarioRequest(t *testing.T, name string) llmtypes.ChatRequest {
	t.Helper()
	for _, s := range openAIScenarios() {
		if s.name == name {
			s.request.Model = recordedModel
			return s.request
		}
	}
	t.Fatalf("no scenario %s", name)
	return llmtypes.ChatRequest{}
}

func replayDrain(ctx context.Context, t *testing.T, c *Client, req llmtypes.ChatRequest) []llmtypes.StreamEvent {
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

func usageOf(events []llmtypes.StreamEvent) llmtypes.Usage {
	var u llmtypes.Usage
	for _, ev := range events {
		if ev.Usage != nil {
			u.InputTokens += ev.Usage.InputTokens
			u.OutputTokens += ev.Usage.OutputTokens
			u.CacheReadTokens += ev.Usage.CacheReadTokens
			if ev.Usage.StopReason != "" {
				u.StopReason = ev.Usage.StopReason
			}
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

func jsonEqual(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	ja, _ := json.Marshal(x)
	jb, _ := json.Marshal(y)
	return bytes.Equal(ja, jb)
}

func TestReplayWireMatchesRecording(t *testing.T) {
	for _, s := range openAIScenarios() {
		t.Run(s.name, func(t *testing.T) {
			f := fixture(t, s.name)
			c, srv := replayClient(t, f)
			ctx := t.Context()
			if s.noTools {
				ctx = toolchoice.WithNone(ctx)
			}
			replayDrain(ctx, t, c, scenarioRequest(t, s.name))
			got := srv.Received()
			if len(got) != 1 || !jsonEqual(got[0], f.Request.Body) {
				t.Fatalf("request differs from the recording:\n got %s\nwant %s", got, f.Request.Body)
			}
		})
	}
}

func TestReplayText(t *testing.T) {
	c, _ := replayClient(t, fixture(t, "text"))
	events := replayDrain(t.Context(), t, c, scenarioRequest(t, "text"))
	var text string
	for _, ev := range events {
		if ev.Type == llmtypes.EventDelta {
			text += ev.Content
		}
	}
	u := usageOf(events)
	if text != "pong" || last(events).Type != llmtypes.EventDone || u.InputTokens != 11 || u.OutputTokens != 10 || u.StopReason != "end_turn" {
		t.Fatalf("text %q usage %+v", text, u)
	}
}

func TestReplayToolUse(t *testing.T) {
	c, _ := replayClient(t, fixture(t, "tool_use"))
	events := replayDrain(t.Context(), t, c, scenarioRequest(t, "tool_use"))
	var calls []*llmtypes.ToolUseBlock
	for _, ev := range events {
		if ev.ToolUse != nil {
			calls = append(calls, ev.ToolUse)
		}
	}
	if len(calls) != 1 || calls[0].Name != "lookup" || calls[0].Input["key"] != "capital-of-france" || usageOf(events).StopReason != "tool_use" ||
		last(events).Type != llmtypes.EventDone {
		t.Fatalf("calls %+v", calls)
	}
}

func TestReplayClosingReplySendsToolChoiceNone(t *testing.T) {
	c, srv := replayClient(t, fixture(t, "closing_tool_choice_none"))
	events := replayDrain(toolchoice.WithNone(t.Context()), t, c, scenarioRequest(t, "closing_tool_choice_none"))
	var req struct {
		ToolChoice string `json:"tool_choice"`
	}
	if err := json.Unmarshal(srv.Received()[0], &req); err != nil || req.ToolChoice != "none" || last(events).Type != llmtypes.EventDone {
		t.Fatalf("tool_choice %q, %v", req.ToolChoice, err)
	}
}

func TestReplayLengthDuringToolCall(t *testing.T) {
	// Derived: the budget ends inside the tool arguments, with OpenAI's
	// finish_reason "length". The partial arguments are kept as _raw.
	tool := fixture(t, "tool_use")
	var kept strings.Builder
	n := 0
	for _, c := range providertest.Events(tool.Response.Body) {
		if strings.Contains(c, `"arguments"`) {
			if n++; n > 2 {
				break
			}
		}
		kept.WriteString(c)
	}
	id := `"id":"` + strings.SplitN(strings.SplitN(tool.Response.Body, `"id":"`, 2)[1], `"`, 2)[0] + `"`
	kept.WriteString(`data: {` + id + `,"object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"length"}]}` + "\n\n")
	kept.WriteString(`data: {` + id + `,"object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":120,"completion_tokens":64,"total_tokens":184}}` + "\n\n")
	kept.WriteString("data: [DONE]\n\n")
	c, _ := replayClient(t, providertest.Derive(tool, "finish_reason length inside the tool arguments", kept.String()))
	events := replayDrain(t.Context(), t, c, scenarioRequest(t, "tool_use"))
	var call *llmtypes.ToolUseBlock
	for _, ev := range events {
		if ev.ToolUse != nil {
			call = ev.ToolUse
		}
	}
	if call == nil || call.Input["_raw"] == nil || usageOf(events).StopReason != "max_tokens" {
		t.Fatalf("partial call %+v usage %+v", call, usageOf(events))
	}
}

func TestReplayTruncatedStreamHasNoDone(t *testing.T) {
	for name, f := range map[string]providertest.Fixture{
		"mid text":      providertest.CutAfter(t, fixture(t, "text"), `"content":"pong"`, 1),
		"mid tool JSON": providertest.CutAfter(t, fixture(t, "tool_use"), `"arguments"`, 2),
	} {
		t.Run(name, func(t *testing.T) {
			c, _ := replayClient(t, f)
			for _, ev := range replayDrain(t.Context(), t, c, scenarioRequest(t, "tool_use")) {
				if ev.Type == llmtypes.EventDone || ev.ToolUse != nil {
					t.Fatalf("a cut stream produced %+v", ev)
				}
			}
		})
	}
}

func TestReplayRateLimitedIsRetryableWithRetryAfter(t *testing.T) {
	f := fixture(t, "text")
	f.Source = "derived: 429 rate_limit_exceeded with Retry-After 7"
	f.Response = providertest.Response{Status: 429, Headers: map[string]string{"Content-Type": "application/json", "Retry-After": "7"},
		Body: `{"error":{"message":"Rate limit reached","type":"requests","code":"rate_limit_exceeded"}}`}
	c, _ := replayClient(t, f)
	stream, err := c.StreamChat(t.Context(), scenarioRequest(t, "text"))
	if err == nil {
		events := []llmtypes.StreamEvent{}
		for ev := range stream {
			events = append(events, ev)
		}
		err = failure.FromEvent(last(events))
	}
	var classified *failure.Error
	if !errors.As(err, &classified) || classified.Class != failure.Retryable || classified.RetryAfter != 7*time.Second {
		t.Fatalf("429: %#v", err)
	}
}

// countingTransport counts the requests that go through it.
type countingTransport struct{ n atomic.Int32 }

func (c *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	c.n.Add(1)
	return http.DefaultTransport.RoundTrip(req)
}

func TestGatewayKeepsTheHTTPClient(t *testing.T) {
	// SetGateway and SetAPIKey rebuild the SDK client; the HTTP client New
	// was given (its transport, timeouts, proxy) must survive both.
	srv := providertest.Serve(t, fixture(t, "text"), fixture(t, "text"))
	transport := &countingTransport{}
	c := New("replay", &http.Client{Transport: transport})
	c.SetAPI(APIChat)
	c.SetGateway(station.Settings{BaseURL: srv.URL + "/v1"})
	replayDrain(t.Context(), t, c, scenarioRequest(t, "text"))
	c.SetAPIKey("rotated")
	replayDrain(t.Context(), t, c, scenarioRequest(t, "text"))
	if got := transport.n.Load(); got != 2 {
		t.Fatalf("%d of 2 requests went through the configured client", got)
	}
}
