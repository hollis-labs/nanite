package agent

// TASKS/agent-host-acp/11-nanite-per-agent-protocol-transport-config.md:
// 2026-08-21 fix-up addendum -- regression coverage for the three
// event-translation bugs a fresh reviewer found (TASKS/ESCALATIONS.md's
// "Task 11 review: FAIL" entry):
//
//  1. reasoning/thinking delta content silently persisting into the
//     visible answer (extractDeltaEvent should route thinking-tagged
//     content to llmtypes.EventThinking, not EventDelta);
//  2. a crashed ACP subprocess looking like a clean exit to Session.Wait
//     (handleProcessExited / bootACP's draining goroutine should surface
//     a real, classifier-actionable error);
//  3. token/cost usage never populated for ACP turns (handleTurnCompleted
//     should send EventUsage before the terminal EventDone when the
//     payload carries usage data).

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	agentsessions "github.com/hollis-labs/agentkit/agentsessions"
	"github.com/hollis-labs/go-agent-wrapper/acp"
	"github.com/hollis-labs/go-agent-wrapper/adapters"
	llmtypes "github.com/hollis-labs/go-llm-types"
	runtimeevents "github.com/hollis-labs/go-runtime-events/runtimeevents"
)

// ---------------------------------------------------------------------
// Finding 1: thinking/thought-tagged deltas must become EventThinking.
// ---------------------------------------------------------------------

func TestExtractDeltaEvent(t *testing.T) {
	cases := []struct {
		name       string
		raw        string
		wantType   llmtypes.EventType
		wantText   string
		wantThinks bool
	}{
		{
			name:     "plain message content stays EventDelta",
			raw:      `{"content":"hello","phase":"message"}`,
			wantType: llmtypes.EventDelta,
			wantText: "hello",
		},
		{
			name:       "opencodeacp's phase=thought tag becomes EventThinking",
			raw:        `{"content":"pondering...","phase":"thought"}`,
			wantType:   llmtypes.EventThinking,
			wantText:   "pondering...",
			wantThinks: true,
		},
		{
			name:       "copilotacp's thinking=true tag becomes EventThinking",
			raw:        `{"content":"reasoning...","thinking":true}`,
			wantType:   llmtypes.EventThinking,
			wantText:   "reasoning...",
			wantThinks: true,
		},
		{
			name:     "no phase/thinking key at all defaults to EventDelta",
			raw:      `{"content":"plain"}`,
			wantType: llmtypes.EventDelta,
			wantText: "plain",
		},
		{
			name:     "empty payload doesn't panic",
			raw:      ``,
			wantType: llmtypes.EventDelta,
			wantText: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ev := extractDeltaEvent(json.RawMessage(c.raw))
			if ev.Type != c.wantType {
				t.Errorf("Type = %v, want %v", ev.Type, c.wantType)
			}
			if c.wantThinks {
				if ev.ThinkingBlock == nil {
					t.Fatalf("ThinkingBlock = nil, want populated for a thinking-tagged delta")
				}
				if ev.ThinkingBlock.Thinking != c.wantText {
					t.Errorf("ThinkingBlock.Thinking = %q, want %q", ev.ThinkingBlock.Thinking, c.wantText)
				}
				if ev.Content != "" {
					t.Errorf("Content = %q, want empty -- thinking content must not also land in Content (that's what would leak it into the persisted answer)", ev.Content)
				}
			} else {
				if ev.Content != c.wantText {
					t.Errorf("Content = %q, want %q", ev.Content, c.wantText)
				}
				if ev.ThinkingBlock != nil {
					t.Errorf("ThinkingBlock = %+v, want nil for a non-thinking delta", ev.ThinkingBlock)
				}
			}
		})
	}
}

// TestHandleEvent_ThinkingDeltaRoutesToFanoutAsEventThinking exercises the
// real dispatch path (handleEvent's KindAgentDelta case), not just the
// pure extractor, confirming the fanout channel actually receives
// EventThinking end to end.
func TestHandleEvent_ThinkingDeltaRoutesToFanoutAsEventThinking(t *testing.T) {
	fanout := make(chan llmtypes.StreamEvent, 4)
	sink := &acpSession{fanout: fanout}

	sink.handleEvent(context.Background(), runtimeevents.Event{
		Kind:    runtimeevents.KindAgentDelta,
		Payload: json.RawMessage(`{"content":"secret reasoning","phase":"thought"}`),
	})
	sink.handleEvent(context.Background(), runtimeevents.Event{
		Kind:    runtimeevents.KindAgentDelta,
		Payload: json.RawMessage(`{"content":"visible answer","phase":"message"}`),
	})
	close(fanout)

	var got []llmtypes.StreamEvent
	for ev := range fanout {
		got = append(got, ev)
	}
	if len(got) != 2 {
		t.Fatalf("fanout events: got %d, want 2", len(got))
	}
	if got[0].Type != llmtypes.EventThinking {
		t.Errorf("event[0].Type = %v, want EventThinking", got[0].Type)
	}
	if got[1].Type != llmtypes.EventDelta {
		t.Errorf("event[1].Type = %v, want EventDelta", got[1].Type)
	}
	if got[1].Content != "visible answer" {
		t.Errorf("event[1].Content = %q, want %q", got[1].Content, "visible answer")
	}
}

// ---------------------------------------------------------------------
// Finding 3: usage data, when present, must reach EventUsage before the
// terminal EventDone.
// ---------------------------------------------------------------------

func TestHandleTurnCompleted(t *testing.T) {
	t.Run("usage present sends EventUsage then EventDone", func(t *testing.T) {
		fanout := make(chan llmtypes.StreamEvent, 4)
		sink := &acpSession{fanout: fanout}
		sink.handleTurnCompleted(context.Background(), json.RawMessage(
			`{"stop_reason":"end_turn","usage":{"InputTokens":12,"OutputTokens":34}}`,
		))
		close(fanout)

		var got []llmtypes.StreamEvent
		for ev := range fanout {
			got = append(got, ev)
		}
		if len(got) != 2 {
			t.Fatalf("fanout events: got %d, want 2 (EventUsage, EventDone): %+v", len(got), got)
		}
		if got[0].Type != llmtypes.EventUsage {
			t.Errorf("event[0].Type = %v, want EventUsage", got[0].Type)
		}
		if got[0].Usage == nil {
			t.Fatal("event[0].Usage = nil, want populated")
		}
		if got[0].Usage.InputTokens != 12 || got[0].Usage.OutputTokens != 34 {
			t.Errorf("Usage = %+v, want InputTokens=12 OutputTokens=34", got[0].Usage)
		}
		if got[1].Type != llmtypes.EventDone {
			t.Errorf("event[1].Type = %v, want EventDone (terminal)", got[1].Type)
		}
	})

	t.Run("no usage key sends only EventDone -- the pre-fix behavior, still correct for copilotacp", func(t *testing.T) {
		fanout := make(chan llmtypes.StreamEvent, 4)
		sink := &acpSession{fanout: fanout}
		sink.handleTurnCompleted(context.Background(), json.RawMessage(`{"stop_reason":"end_turn"}`))
		close(fanout)

		var got []llmtypes.StreamEvent
		for ev := range fanout {
			got = append(got, ev)
		}
		if len(got) != 1 {
			t.Fatalf("fanout events: got %d, want 1 (EventDone only): %+v", len(got), got)
		}
		if got[0].Type != llmtypes.EventDone {
			t.Errorf("event[0].Type = %v, want EventDone", got[0].Type)
		}
	})

	t.Run("empty payload doesn't panic and still terminates the turn", func(t *testing.T) {
		fanout := make(chan llmtypes.StreamEvent, 4)
		sink := &acpSession{fanout: fanout}
		sink.handleTurnCompleted(context.Background(), nil)
		close(fanout)
		var n int
		for range fanout {
			n++
		}
		if n != 1 {
			t.Fatalf("fanout events: got %d, want 1", n)
		}
	})
}

// ---------------------------------------------------------------------
// Finding 2: an abnormal ACP subprocess exit must reach Session.Wait()
// as a non-nil, classifier-actionable error.
// ---------------------------------------------------------------------

// fakeACPClient is a minimal acp.Client test double -- just enough surface
// to drive acpSession.drain via a controllable Events() channel. Launch/
// Prompt/Cancel/Close are no-ops; no real subprocess is ever spawned.
type fakeACPClient struct {
	events chan runtimeevents.Event
}

func newFakeACPClient() *fakeACPClient {
	return &fakeACPClient{events: make(chan runtimeevents.Event, 8)}
}

func (f *fakeACPClient) Launch(ctx context.Context, params acp.LaunchParams) error { return nil }
func (f *fakeACPClient) Prompt(ctx context.Context, prompt string) error           { return nil }
func (f *fakeACPClient) Cancel(ctx context.Context) error                          { return nil }
func (f *fakeACPClient) Events() <-chan runtimeevents.Event                        { return f.events }
func (f *fakeACPClient) InterruptCapability() adapters.InterruptCapability {
	return adapters.InterruptTurn
}
func (f *fakeACPClient) Close(ctx context.Context) error { return nil }

var _ acp.Client = (*fakeACPClient)(nil)

// runACPDrainAndWait mirrors bootACP's own draining goroutine
// (agent_acp.go) exactly -- drain until the Events channel closes, then
// assign sess.runErr from sink.processExitErr strictly before closing
// runDone -- so this test exercises the real production write-then-close
// ordering Session.Wait's happens-before argument depends on, not a
// simplified stand-in.
func runACPDrainAndWait(t *testing.T, sink *acpSession) *Session {
	t.Helper()
	sess := &Session{acp: sink, runDone: make(chan struct{})}
	go func() {
		defer close(sess.runDone)
		sink.drain(context.Background())
		sess.runErr = sink.processExitErr
	}()
	return sess
}

func TestACPSession_AbnormalProcessExitReachesSessionWait(t *testing.T) {
	client := newFakeACPClient()
	sink := &acpSession{client: client}
	sess := runACPDrainAndWait(t, sink)

	// A real, unprompted subprocess crash: KindProcessExited carrying a
	// non-empty "error" (opencodeacp's waitProcess populates this from
	// cmd.Wait()'s error when the process died on its own -- see
	// client.go's waitProcess).
	client.events <- runtimeevents.Event{
		Kind:    runtimeevents.KindProcessExited,
		Payload: json.RawMessage(`{"error":"exit status 1"}`),
	}
	close(client.events)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := sess.Wait(ctx)
	if err == nil {
		t.Fatal("Session.Wait() = nil, want a non-nil error for an abnormal process exit")
	}

	var xe *agentsessions.ExitError
	if !errors.As(err, &xe) {
		t.Fatalf("errors.As(err, &xe) = false -- internal/recovery/broker's Classify requires this exact "+
			"concrete type to route a crash to the broker at all (verified against classifier.go); err = %v", err)
	}
	if xe.Code == 0 {
		t.Errorf("ExitError.Code = 0, want non-zero -- Classify's default branch keys on xe.Code != 0 " +
			"to treat this as a real crash rather than falling through to the 'exit code 0, not a recovery " +
			"candidate' bottom branch")
	}
}

func TestACPSession_CleanExitLeavesSessionWaitNil(t *testing.T) {
	client := newFakeACPClient()
	sink := &acpSession{client: client}
	sess := runACPDrainAndWait(t, sink)

	// A clean exit: KindProcessExited fires (e.g. the subprocess exited 0
	// on its own) but carries no "error" -- matches opencodeacp's
	// waitProcess when cmd.Wait() returned nil.
	client.events <- runtimeevents.Event{
		Kind:    runtimeevents.KindProcessExited,
		Payload: json.RawMessage(`{}`),
	}
	close(client.events)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := sess.Wait(ctx); err != nil {
		t.Errorf("Session.Wait() = %v, want nil for a clean process exit", err)
	}
}

func TestACPSession_IntentionalStopClosesEventsWithoutProcessExited(t *testing.T) {
	// Mirrors acpSession.Stop / opencodeacp's real Close(): the Events
	// channel is closed directly, with no KindProcessExited event ever
	// observed by drain -- confirming this shape (the common "session
	// stopped on purpose" path) leaves runErr nil, same as before this
	// fix.
	client := newFakeACPClient()
	sink := &acpSession{client: client}
	sess := runACPDrainAndWait(t, sink)

	close(client.events)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := sess.Wait(ctx); err != nil {
		t.Errorf("Session.Wait() = %v, want nil -- an intentional Stop must not look like a crash", err)
	}
}

// TestACPSession_MissingProcessExitedEventDoesNotHang covers copilotacp's
// documented gap: its Client never emits KindProcessExited at all today.
// bootACP's draining goroutine must not assume the event always arrives
// -- it only reads sink.processExitErr AFTER drain(...) returns (i.e.
// after the Events channel closes for whatever reason), so a Client that
// simply closes its channel with no KindProcessExited at all still
// resolves Wait() (to nil, not a hang and not a false crash).
func TestACPSession_MissingProcessExitedEventDoesNotHang(t *testing.T) {
	client := newFakeACPClient()
	sink := &acpSession{client: client}
	sess := runACPDrainAndWait(t, sink)

	// copilotacp-shaped session end: some other terminal event (or none)
	// then the channel just closes, with no KindProcessExited ever sent.
	client.events <- runtimeevents.Event{Kind: runtimeevents.KindTurnCompleted, Payload: json.RawMessage(`{}`)}
	close(client.events)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := sess.Wait(ctx); err != nil {
		t.Errorf("Session.Wait() = %v, want nil -- a Client that never emits KindProcessExited must not hang or misreport a crash", err)
	}
}

// TestHandleProcessExited_DirectUnit pins handleProcessExited's own
// contract in isolation (independent of the drain/Wait plumbing tested
// above): empty/absent "error" is a no-op, a populated "error" produces
// a wrapped *agentsessions.ExitError with Code=-1.
func TestHandleProcessExited_DirectUnit(t *testing.T) {
	t.Run("no error key -- no-op", func(t *testing.T) {
		sink := &acpSession{}
		sink.handleProcessExited(json.RawMessage(`{}`))
		if sink.processExitErr != nil {
			t.Errorf("processExitErr = %v, want nil", sink.processExitErr)
		}
	})
	t.Run("empty payload -- no-op", func(t *testing.T) {
		sink := &acpSession{}
		sink.handleProcessExited(nil)
		if sink.processExitErr != nil {
			t.Errorf("processExitErr = %v, want nil", sink.processExitErr)
		}
	})
	t.Run("populated error -- wraps a real ExitError", func(t *testing.T) {
		sink := &acpSession{}
		sink.handleProcessExited(json.RawMessage(`{"error":"signal: killed"}`))
		if sink.processExitErr == nil {
			t.Fatal("processExitErr = nil, want non-nil")
		}
		var xe *agentsessions.ExitError
		if !errors.As(sink.processExitErr, &xe) {
			t.Fatalf("errors.As failed to find *agentsessions.ExitError in %v", sink.processExitErr)
		}
		if xe.Code != -1 {
			t.Errorf("ExitError.Code = %d, want -1", xe.Code)
		}
	})
}
