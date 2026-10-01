package agent

import (
	"context"
	"encoding/json"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	runtimeevents "github.com/hollis-labs/go-runtime-events/runtimeevents"
)

// blockSinkHarness drives a sink and records what both of its consumers see:
// the canonical normalized events and the legacy fanout projection.
type blockSinkHarness struct {
	sink      *runtimeEventSink
	canonical []runtimeevents.Event
	fanout    chan llmtypes.StreamEvent
}

func newBlockSinkHarness(t *testing.T, providerName string, isACP bool) *blockSinkHarness {
	t.Helper()
	h := &blockSinkHarness{fanout: make(chan llmtypes.StreamEvent, 64)}
	h.sink = newRuntimeEventSink(providerName, isACP, runtimeevents.SinkFunc(func(_ context.Context, ev runtimeevents.Event) error {
		h.canonical = append(h.canonical, ev)
		return nil
	}))
	h.sink.fanout = h.fanout
	return h
}

func (h *blockSinkHarness) write(t *testing.T, kind runtimeevents.EventKind, payload string) {
	t.Helper()
	ev := runtimeevents.Event{Kind: kind, SessionID: "s", TurnID: "t"}
	if payload != "" {
		ev.Payload = json.RawMessage(payload)
	}
	if err := h.sink.Write(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
}

func (h *blockSinkHarness) delta(t *testing.T, content string) {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"content": content})
	h.write(t, runtimeevents.KindAgentDelta, string(b))
}

// text returns the concatenated text as each consumer saw it.
func (h *blockSinkHarness) text(t *testing.T) (canonical, fanout string) {
	t.Helper()
	for _, ev := range h.canonical {
		if ev.Kind != runtimeevents.KindAgentDelta {
			continue
		}
		var p deltaPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if p.Phase != "thought" && len(p.Thinking) == 0 {
			canonical += p.Content
		}
	}
	for {
		select {
		case ev := <-h.fanout:
			if ev.Type == llmtypes.EventDelta {
				fanout += ev.Content
			}
		default:
			return canonical, fanout
		}
	}
}

func TestBlockDeltas_WholeBlockRuntimesGetParagraphBreaks(t *testing.T) {
	for _, provider := range []string{"claude", "pty", "pty-claude", "codex", "pty-codex", "opencode", "pty-opencode"} {
		t.Run(provider, func(t *testing.T) {
			h := newBlockSinkHarness(t, provider, false)
			h.delta(t, "Let me check the config.")
			h.delta(t, "The port is 8090.")
			canonical, fanout := h.text(t)
			want := "Let me check the config.\n\nThe port is 8090."
			if canonical != want || fanout != want {
				t.Fatalf("canonical %q, fanout %q; want %q for both", canonical, fanout, want)
			}
		})
	}
}

// The regression that matters: token-chunk streams must never be separated.
func TestBlockDeltas_TokenStreamsStayUnseparated(t *testing.T) {
	for _, tc := range []struct {
		name     string
		provider string
		acp      bool
	}{
		{"acp claude", "claude", true},
		{"acp codex", "codex", true},
		{"acp opencode", "opencode", true},
		{"http-style provider name", "anthropic", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newBlockSinkHarness(t, tc.provider, tc.acp)
			h.delta(t, "Hel")
			h.delta(t, "lo, wor")
			h.delta(t, "ld.")
			canonical, fanout := h.text(t)
			if canonical != "Hello, world." || fanout != "Hello, world." {
				t.Fatalf("canonical %q, fanout %q; want the chunks joined untouched", canonical, fanout)
			}
		})
	}
}

func TestBlockDeltas_NoBreakWhereOneAlreadyIs(t *testing.T) {
	h := newBlockSinkHarness(t, "claude", false)
	h.delta(t, "Line one.\n")
	h.delta(t, "Line two.")
	h.delta(t, " continued")
	h.delta(t, "\nNew line.")
	canonical, fanout := h.text(t)
	want := "Line one.\nLine two. continued\nNew line."
	if canonical != want || fanout != want {
		t.Fatalf("canonical %q, fanout %q; want %q", canonical, fanout, want)
	}
}

func TestBlockDeltas_TurnBoundaryResets(t *testing.T) {
	for _, terminal := range []runtimeevents.EventKind{runtimeevents.KindTurnCompleted, runtimeevents.KindTurnFailed, runtimeevents.KindTurnStarted} {
		t.Run(string(terminal), func(t *testing.T) {
			h := newBlockSinkHarness(t, "claude", false)
			h.delta(t, "Turn one.")
			h.write(t, terminal, "")
			h.delta(t, "Turn two.")
			canonical, _ := h.text(t)
			if canonical != "Turn one.Turn two." {
				t.Fatalf("text %q: the first block of a new turn must not get a break", canonical)
			}
		})
	}
}

// go-agent-wrapper v0.13.1 shape: an opencode turn with a tool call streams
// one whole text block per step and ends with ONE KindTurnCompleted that
// carries the usage summed over its steps. The blocks within the turn are
// separated; that terminal, usage and all, ends the turn.
func TestBlockDeltas_UsageBearingTerminalResets(t *testing.T) {
	h := newBlockSinkHarness(t, "opencode", false)
	h.delta(t, "Let me read the file.")
	h.delta(t, "The port is 8090.")
	h.write(t, runtimeevents.KindTurnCompleted, `{"usage":{"OutputTokens":17,"StopReason":"stop"}}`)
	h.delta(t, "Next turn.")

	canonical, fanout := h.text(t)
	want := "Let me read the file.\n\nThe port is 8090.Next turn."
	if canonical != want || fanout != want {
		t.Fatalf("canonical %q, fanout %q; want %q for both", canonical, fanout, want)
	}
}

func TestBlockDeltas_ThinkingNeitherReceivesNorCountsAsText(t *testing.T) {
	h := newBlockSinkHarness(t, "claude", false)
	h.write(t, runtimeevents.KindAgentDelta, `{"content":"pondering","phase":"thought"}`)
	h.delta(t, "First.")
	h.write(t, runtimeevents.KindAgentDelta, `{"content":"more pondering","thinking":true}`)
	h.delta(t, "Second.")

	canonical, _ := h.text(t)
	if canonical != "First.\n\nSecond." {
		t.Fatalf("text %q", canonical)
	}
	for _, ev := range h.canonical {
		var p deltaPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if (p.Phase == "thought" || len(p.Thinking) > 0) && p.Content != "pondering" && p.Content != "more pondering" {
			t.Fatalf("a thinking delta was altered: %q", p.Content)
		}
	}
}

// Rewriting the content must keep every other payload field.
func TestBlockDeltas_PreservesOtherPayloadFields(t *testing.T) {
	h := newBlockSinkHarness(t, "codex", false)
	h.delta(t, "One.")
	h.write(t, runtimeevents.KindAgentDelta, `{"content":"Two.","phase":"message","extra":{"k":1}}`)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(h.canonical[1].Payload, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["content"]) != `"\n\nTwo."` || string(fields["phase"]) != `"message"` || string(fields["extra"]) != `{"k":1}` {
		t.Fatalf("payload = %s", h.canonical[1].Payload)
	}
}

func TestTurnCompletedUsage(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload string
		acp     bool
		want    *llmtypes.Usage
	}{
		{"empty", "", true, nil},
		{"acp stop_reason only", `{"stop_reason":"max_tokens"}`, true, &llmtypes.Usage{StopReason: "max_tokens"}},
		{"acp usage without its own stop reason", `{"stop_reason":"end_turn","usage":{"InputTokens":3}}`, true, &llmtypes.Usage{InputTokens: 3, StopReason: "end_turn"}},
		{"acp usage keeps its own stop reason", `{"stop_reason":"end_turn","usage":{"StopReason":"max_tokens"}}`, true, &llmtypes.Usage{StopReason: "max_tokens"}},
		{"native ignores a top-level stop_reason", `{"stop_reason":"max_tokens"}`, false, nil},
		{"native usage unchanged", `{"usage":{"OutputTokens":7,"StopReason":"end_turn"}}`, false, &llmtypes.Usage{OutputTokens: 7, StopReason: "end_turn"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := TurnCompletedUsage(json.RawMessage(tc.payload), tc.acp)
			if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
				t.Fatalf("TurnCompletedUsage = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// An ACP completion carrying only a stop reason now reaches the stream as a
// usage event (so the chat loop sees max_tokens), still followed by Done.
func TestRuntimeEventSink_ACPStopReasonReachesFanout(t *testing.T) {
	h := newBlockSinkHarness(t, "claude", true)
	h.write(t, runtimeevents.KindTurnCompleted, `{"stop_reason":"max_tokens"}`)
	first, second := <-h.fanout, <-h.fanout
	if first.Type != llmtypes.EventUsage || first.Usage == nil || first.Usage.StopReason != "max_tokens" {
		t.Fatalf("first fanout event = %+v, want usage carrying max_tokens", first)
	}
	if second.Type != llmtypes.EventDone {
		t.Fatalf("second fanout event = %+v, want done", second)
	}
}

// Since go-agent-wrapper v0.13.1 every turn ends in one terminal event that
// carries the turn's usage. The legacy fanout projection delivers that usage
// first, then the terminal, for native and ACP alike (CW-20261001-0019).
func TestRuntimeEventSink_TerminalEventCarriesUsage(t *testing.T) {
	const usage = `"usage":{"InputTokens":10,"OutputTokens":5,"StopReason":"end_turn"}`
	for _, tc := range []struct {
		name      string
		acp       bool
		kind      runtimeevents.EventKind
		payload   string
		want      []llmtypes.EventType
		wantError string
	}{
		{"native completion with usage", false, runtimeevents.KindTurnCompleted, `{` + usage + `}`,
			[]llmtypes.EventType{llmtypes.EventUsage, llmtypes.EventDone}, ""},
		{"native completion without usage", false, runtimeevents.KindTurnCompleted, ``,
			[]llmtypes.EventType{llmtypes.EventDone}, ""},
		{"ACP completion with usage", true, runtimeevents.KindTurnCompleted, `{"stop_reason":"end_turn",` + usage + `}`,
			[]llmtypes.EventType{llmtypes.EventUsage, llmtypes.EventDone}, ""},
		{"native failure with usage", false, runtimeevents.KindTurnFailed, `{"error":"boom",` + usage + `}`,
			[]llmtypes.EventType{llmtypes.EventUsage, llmtypes.EventError}, "boom"},
		{"native process exited mid-turn", false, runtimeevents.KindTurnFailed,
			`{"error":"wrapper: process exited before the turn completed","reason":"process_exited","exit_code":1,` + usage + `}`,
			[]llmtypes.EventType{llmtypes.EventUsage, llmtypes.EventError}, "wrapper: process exited before the turn completed"},
		{"native failure without usage", false, runtimeevents.KindTurnFailed, `{"error":"boom"}`,
			[]llmtypes.EventType{llmtypes.EventError}, "boom"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newBlockSinkHarness(t, "claude", tc.acp)
			h.write(t, tc.kind, tc.payload)
			var got []llmtypes.EventType
			for len(h.fanout) > 0 {
				ev := <-h.fanout
				got = append(got, ev.Type)
				if ev.Type == llmtypes.EventUsage && (ev.Usage == nil || ev.Usage.InputTokens != 10 || ev.Usage.OutputTokens != 5) {
					t.Fatalf("usage = %+v, want input 10 / output 5", ev.Usage)
				}
				if ev.Type == llmtypes.EventError && ev.Error != tc.wantError {
					t.Fatalf("error = %q, want %q", ev.Error, tc.wantError)
				}
			}
			if len(got) != len(tc.want) {
				t.Fatalf("fanout = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("fanout = %v, want %v", got, tc.want)
				}
			}
		})
	}
}
