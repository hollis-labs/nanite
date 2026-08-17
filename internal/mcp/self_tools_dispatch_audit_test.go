package mcp

import (
	"context"
	"testing"

	"github.com/hollis-labs/nanite/internal/dispatch"
	"github.com/hollis-labs/nanite/internal/promptrouter"
)

// capturingReflexLogger records every ReflexMatchEntry passed to
// LogReflexMatch. Used to verify the callExecuteTask call site wiring for
// CW-20260816-0068 (raw-vs-sent audit trail) without standing up a real
// *store.Store.
type capturingReflexLogger struct {
	captured []promptrouter.ReflexMatchEntry
}

func (l *capturingReflexLogger) LogReflexMatch(entry promptrouter.ReflexMatchEntry) error {
	l.captured = append(l.captured, entry)
	return nil
}

// TestCallExecuteTask_ReflexMatch_LogsRawAndSentInputText is the
// CW-20260816-0068 wiring regression: the reflex match log call site in
// callExecuteTask (internal/mcp/self_tools_dispatch.go) must populate the
// new ReflexMatchEntry.RawInputText/SentInputText fields from the same two
// variables the dispatch call itself uses — message (raw user input) and
// dispatchMessage (the text actually sent to dispatch.ExecuteTask, which E2
// grounding may rewrite before this call site is reached). With no
// GroundingRecaller configured here, dispatchMessage never diverges from
// message, so both fields must come through identical to the raw message —
// proving the call site reads from the correct variables rather than, say,
// leaving the new fields zero-valued.
func TestCallExecuteTask_ReflexMatch_LogsRawAndSentInputText(t *testing.T) {
	spawner := &recordingSpawner{result: &dispatch.SpawnResult{Summary: "worker done"}}
	logger := &capturingReflexLogger{}
	st := &SelfToolsTransport{
		Dispatch:     spawner,
		ReflexSet:    promptrouter.BuiltinReflexes(),
		ReflexLogger: logger,
	}

	const msg = "Implement the reflex matcher module"
	res, err := st.callExecuteTask(context.Background(), map[string]any{
		"session_id": "sess-audit-1",
		"message":    msg,
	})
	if err != nil {
		t.Fatalf("callExecuteTask: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success result, got error: %+v", res)
	}
	if len(logger.captured) != 1 {
		t.Fatalf("expected 1 logged reflex match, got %d", len(logger.captured))
	}

	entry := logger.captured[0]
	if entry.RawInputText != msg {
		t.Errorf("RawInputText = %q, want %q", entry.RawInputText, msg)
	}
	if entry.SentInputText != msg {
		t.Errorf("SentInputText = %q, want %q", entry.SentInputText, msg)
	}
	if entry.RawInputText != entry.SentInputText {
		t.Errorf("expected RawInputText == SentInputText with no grounding rewrite, got %q vs %q",
			entry.RawInputText, entry.SentInputText)
	}
}
