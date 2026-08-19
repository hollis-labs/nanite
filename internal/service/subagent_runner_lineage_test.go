package service

// CW-fix-prompt-worker-grant-lineage — verify ChatRunner.Run wires the
// (worker → parent) lineage entry around the chat-loop invocation.
//
// The unit test in path_grants_lineage_test.go covers the data shape;
// this test covers the spawn-time wiring: lineage present DURING the
// chat loop (so dev_* lookups against the worker session can fall
// through to the parent's grants) AND cleared AFTER Run returns.

import (
	"context"
	"testing"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/subagent"
)

// chatInvokerFunc adapts a function to the chatInvoker interface so the
// test can assert against PathGrants state at the moment generateResponse
// would run.
type chatInvokerFunc func(ctx context.Context, sessionID, msgID, prompt string, ch chan chat.StreamEvent)

func (f chatInvokerFunc) generateResponse(ctx context.Context, sessionID, msgID, prompt string, ch chan chat.StreamEvent) {
	f(ctx, sessionID, msgID, prompt, ch)
}

// TestChatRunner_RegistersAndClearsLineage verifies the spawn wiring:
//   - Before Run, the lineage map has no entry for the worker.
//   - DURING the chat loop (the moment dev_* tools would resolve a path),
//     the worker can reach the parent's grant via lineage walk.
//   - After Run returns, the lineage entry is gone.
func TestChatRunner_RegistersAndClearsLineage(t *testing.T) {
	const parentSessionID = "sess-parent"
	const grantedPath = "/tmp/lineage-fixture/foo.txt"

	grants := permission.NewPathGrants()
	registered := grants.RegisterFromUserMessage(parentSessionID, "see "+grantedPath)
	if len(registered) == 0 {
		t.Fatalf("setup: RegisterFromUserMessage returned no grants")
	}

	st := &recordingSessionStore{
		parents: map[string]*store.Session{
			parentSessionID: {ID: parentSessionID},
		},
	}

	var (
		invokedWithSessionID string
		matchedDuringRun     bool
		viaDuringRun         string
	)
	invoker := chatInvokerFunc(func(_ context.Context, sessionID, _, _ string, ch chan chat.StreamEvent) {
		defer close(ch)
		invokedWithSessionID = sessionID
		// At this point ChatRunner.Run has registered the lineage and
		// not yet cleared it (defer fires when Run returns). The dev_*
		// lookup the worker would issue runs against its own session
		// ID; assert lineage walks up to the parent and resolves.
		matched, _, via := grants.LookupPath(sessionID, grantedPath)
		matchedDuringRun = matched
		viaDuringRun = via
		ch <- chat.StreamEvent{Type: "delta", Content: "ok"}
		ch <- chat.StreamEvent{Type: "stream_end"}
	})

	runner := &ChatRunner{
		agents: &stubAgentReaderForRunner{agents: map[string]*store.AgentProfile{
			"worker": {ID: "ag-worker", DefaultProvider: "anthropic", DefaultModel: "claude-sonnet-4-6"},
		}},
		store:      st,
		invoker:    invoker,
		persistFn:  func(_ context.Context, _, _ string) error { return nil },
		pathGrants: grants,
	}

	run := &subagent.Run{
		ID:              "run-lineage",
		Role:            "worker",
		ParentSessionID: parentSessionID,
		Prompt:          "do the thing",
	}
	if _, err := runner.Run(context.Background(), run); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if invokedWithSessionID == "" {
		t.Fatal("chat invoker did not run — wiring assertion never executed")
	}
	if invokedWithSessionID != run.ChildSessionID {
		t.Errorf("invoker sessionID = %q, want childID %q", invokedWithSessionID, run.ChildSessionID)
	}
	if !matchedDuringRun {
		t.Errorf("during chat loop, lineage walk should have resolved %q for child %q",
			grantedPath, run.ChildSessionID)
	}
	if viaDuringRun != parentSessionID {
		t.Errorf("during chat loop, lineage via = %q, want %q", viaDuringRun, parentSessionID)
	}

	// After Run returns, the deferred ClearLineage must have wiped the entry.
	if grants.IsPathAllowed(run.ChildSessionID, grantedPath) {
		t.Errorf("lineage should be cleared after Run returns; child %q still resolves %q",
			run.ChildSessionID, grantedPath)
	}
}

// TestChatRunner_LineageNilSafe confirms the runner does not panic when
// pathGrants is nil. Tests that don't exercise grant flow (most of the
// existing ChatRunner suite) leave it nil; the spawn must still work.
func TestChatRunner_LineageNilSafe(t *testing.T) {
	st := &recordingSessionStore{
		parents: map[string]*store.Session{
			"sess-parent": {ID: "sess-parent"},
		},
	}
	invoker := chatInvokerFunc(func(_ context.Context, _, _, _ string, ch chan chat.StreamEvent) {
		defer close(ch)
		ch <- chat.StreamEvent{Type: "delta", Content: "ok"}
		ch <- chat.StreamEvent{Type: "stream_end"}
	})
	runner := &ChatRunner{
		agents: &stubAgentReaderForRunner{agents: map[string]*store.AgentProfile{
			"worker": {ID: "ag-worker", DefaultProvider: "anthropic", DefaultModel: "claude-sonnet-4-6"},
		}},
		store:     st,
		invoker:   invoker,
		persistFn: func(_ context.Context, _, _ string) error { return nil },
		// pathGrants intentionally nil
	}
	_, err := runner.Run(context.Background(), &subagent.Run{
		ID:              "run-nil",
		Role:            "worker",
		ParentSessionID: "sess-parent",
		Prompt:          "p",
	})
	if err != nil {
		t.Fatalf("Run with nil pathGrants must not error: %v", err)
	}
}
