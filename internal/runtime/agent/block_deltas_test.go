package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	runtimeevents "github.com/hollis-labs/substrate/harness/adapters/runtimeevents"
	llmtypes "github.com/hollis-labs/substrate/llm-core/llmtypes"
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

// block writes one text delta of block id (empty: no block_id).
func (h *blockSinkHarness) block(t *testing.T, id, content string) {
	t.Helper()
	p := map[string]string{"content": content}
	if id != "" {
		p["block_id"] = id
	}
	b, _ := json.Marshal(p)
	h.write(t, runtimeevents.KindAgentDelta, string(b))
}

// CW-20260930-0228: blocks are separated by block_id, which go-agent-wrapper
// v0.17.0 stamps on every runtime's deltas, native and ACP alike. Two blocks
// get a paragraph break whatever the provider.
func TestBlockDeltas_NewBlockGetsParagraphBreak(t *testing.T) {
	for _, tc := range []struct {
		provider string
		acp      bool
	}{
		{"claude", false}, {"codex", false}, {"opencode", false},
		{"claude", true}, {"copilot", true}, {"pi", true},
	} {
		t.Run(fmt.Sprintf("%s acp=%v", tc.provider, tc.acp), func(t *testing.T) {
			h := newBlockSinkHarness(t, tc.provider, tc.acp)
			h.block(t, "b1", "Let me check the config.")
			h.block(t, "b2", "The port is 8090.")
			canonical, fanout := h.text(t)
			want := "Let me check the config.\n\nThe port is 8090."
			if canonical != want || fanout != want {
				t.Fatalf("canonical %q, fanout %q; want %q for both", canonical, fanout, want)
			}
		})
	}
}

// The regression that matters: the tokens of one block must never be
// separated, and neither must deltas that carry no block_id at all.
func TestBlockDeltas_OneBlockStaysJoined(t *testing.T) {
	for _, id := range []string{"msg-1", ""} {
		t.Run(fmt.Sprintf("block_id=%q", id), func(t *testing.T) {
			h := newBlockSinkHarness(t, "claude", true)
			h.block(t, id, "Hel")
			h.block(t, id, "lo, wor")
			h.block(t, id, "ld.")
			canonical, fanout := h.text(t)
			if canonical != "Hello, world." || fanout != "Hello, world." {
				t.Fatalf("canonical %q, fanout %q; want the chunks joined untouched", canonical, fanout)
			}
		})
	}
}

func TestBlockDeltas_NoBreakWhereOneAlreadyIs(t *testing.T) {
	h := newBlockSinkHarness(t, "claude", false)
	h.block(t, "b1", "Line one.\n")
	h.block(t, "b2", "Line two.")
	h.block(t, "b3", " continued")
	h.block(t, "b4", "\nNew line.")
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
			h.block(t, "b1", "Turn one.")
			h.write(t, terminal, "")
			h.block(t, "b2", "Turn two.")
			canonical, _ := h.text(t)
			if canonical != "Turn one.Turn two." {
				t.Fatalf("text %q: the first block of a new turn must not get a break", canonical)
			}
		})
	}
}

// An opencode turn with a tool call: one block per step, then ONE
// KindTurnCompleted carrying the turn's usage. The blocks within the turn
// are separated; that terminal, usage and all, ends the turn.
func TestBlockDeltas_UsageBearingTerminalResets(t *testing.T) {
	h := newBlockSinkHarness(t, "opencode", false)
	h.block(t, "prt_1", "Let me read the file.")
	h.block(t, "prt_2", "The port is 8090.")
	h.write(t, runtimeevents.KindTurnCompleted, `{"usage":{"OutputTokens":17,"StopReason":"end_turn"}}`)
	h.block(t, "prt_3", "Next turn.")

	canonical, fanout := h.text(t)
	want := "Let me read the file.\n\nThe port is 8090.Next turn."
	if canonical != want || fanout != want {
		t.Fatalf("canonical %q, fanout %q; want %q for both", canonical, fanout, want)
	}
}

func TestBlockDeltas_ThinkingNeitherReceivesNorCountsAsText(t *testing.T) {
	h := newBlockSinkHarness(t, "claude", false)
	h.write(t, runtimeevents.KindAgentDelta, `{"content":"pondering","phase":"thought","block_id":"t1"}`)
	h.block(t, "b1", "First.")
	h.write(t, runtimeevents.KindAgentDelta, `{"content":"more pondering","thinking":true,"block_id":"t2"}`)
	h.block(t, "b2", "Second.")

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
	h.block(t, "item_1", "One.")
	h.write(t, runtimeevents.KindAgentDelta, `{"content":"Two.","phase":"final","block_id":"item_2","extra":{"k":1}}`)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(h.canonical[1].Payload, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["content"]) != `"\n\nTwo."` || string(fields["phase"]) != `"final"` || string(fields["block_id"]) != `"item_2"` || string(fields["extra"]) != `{"k":1}` {
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

// A turn.failed that CancelTurn caused (reason "interrupted") is an interrupt
// on the legacy fanout too: usage marked interrupted, then Done, never an
// error (CW-20261001-0168).
func TestRuntimeEventSink_InterruptedTurnIsNotAnError(t *testing.T) {
	h := newBlockSinkHarness(t, "claude", false)
	h.write(t, runtimeevents.KindTurnFailed, `{"error":"aborted by interrupt","reason":"interrupted","usage":{"OutputTokens":1}}`)
	first, second := <-h.fanout, <-h.fanout
	if first.Type != llmtypes.EventUsage || first.Usage == nil || first.Usage.StopReason != StopReasonInterrupted || first.Usage.OutputTokens != 1 {
		t.Fatalf("first fanout event = %+v, want the turn's usage marked interrupted", first)
	}
	if second.Type != llmtypes.EventDone {
		t.Fatalf("second fanout event = %+v, want done", second)
	}
	select {
	case ev := <-h.fanout:
		t.Fatalf("extra fanout event %+v after an interrupted turn", ev)
	default:
	}
}
