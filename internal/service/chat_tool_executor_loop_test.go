package service

// I2 integration tests: loop detector + inspector wiring (CW-20260420-0029).
//
// These tests exercise the integration between loopdetect.Detector and
// inspector.Service as wired through chatServiceImpl, without spinning up
// the full container.

import (
	"context"
	"encoding/json"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/inspector"
	"github.com/hollis-labs/nanite/internal/loopdetect"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/toolclient"
)

// ─── ToolService stub ─────────────────────────────────────────────────────────

type loopTestToolStub struct {
	output string
}

func (s *loopTestToolStub) SelectForAgent(_ context.Context, _, _, _, _ string, _ int) (*ToolSelection, error) {
	return &ToolSelection{}, nil
}
func (s *loopTestToolStub) Execute(_ context.Context, _, _ string, _ map[string]any) (*ToolResult, error) {
	return &ToolResult{Output: s.output}, nil
}
func (*loopTestToolStub) HandleRequestTools(_ context.Context, _ map[string]any) ([]llmtypes.ToolDefinition, string, error) {
	return nil, "no tools", nil
}
func (*loopTestToolStub) ListSummaries() []toolclient.ToolSummary { return nil }
func (*loopTestToolStub) GetToolMeta(_ context.Context, _ string) (ToolMetaInfo, bool) {
	return ToolMetaInfo{}, false
}
func (*loopTestToolStub) GetToolSchema(_ string) map[string]any { return nil }

// ─── Store stub ───────────────────────────────────────────────────────────────

// loopTestStore satisfies the Store interface by embedding *store.Store (which
// the package already asserts satisfies Store in store.go).  We can't create a
// real store.Store without a DB, so we use a wrapper that delegates only the
// calls executeSingleTool makes on the non-error, non-error path: LogEvent.
//
// Since all other methods will panic (unused in these tests), this is safe.
type loopTestStore struct {
	// Embed a nil pointer — methods that are NOT called won't panic; only
	// methods we explicitly override are safe to call.
	*store.Store
}

// LogEvent overrides the embedded nil pointer's method.
func (*loopTestStore) LogEvent(ctx context.Context, _, _, _, _, _ string) {}

// ─── Factory ──────────────────────────────────────────────────────────────────

// makeLoopTestService builds a minimal chatServiceImpl for loop-detector tests.
func makeLoopTestService() *chatServiceImpl {
	insp := inspector.NewService()
	det := loopdetect.New()
	return &chatServiceImpl{
		streams:      NewStreamManager(),
		tools:        &loopTestToolStub{output: "ok"},
		store:        &loopTestStore{},
		inspector:    insp,
		loopDetector: det,
		argValidator: newArgValidator(),
	}
}

// runTool drives executeSingleTool for one call, draining the event channel.
func runTool(svc *chatServiceImpl, sessionID, turnID, toolName, argsJSON string) {
	var input map[string]any
	_ = json.Unmarshal([]byte(argsJSON), &input)
	tu := llmtypes.ToolUseBlock{ID: "id-" + toolName, Name: toolName, Input: input}

	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	ls.inspectorTurnID = turnID

	ch := make(chan chat.StreamEvent, 32)
	svc.executeSingleTool(context.Background(), tu, ls, "agent1", sessionID, ch, nil)
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

// ─── Tests ────────────────────────────────────────────────────────────────────

// TestExecutorLoop_ThreeIdentical verifies that 3 identical tool calls populate
// the inspector snapshot with LoopStatus{Detected: true}.
func TestExecutorLoop_ThreeIdentical(t *testing.T) {
	svc := makeLoopTestService()
	const (
		sess   = "loop-sess-1"
		turnID = "1"
	)
	svc.inspector.EnsureTurn(sess, turnID)

	for i := 0; i < 3; i++ {
		runTool(svc, sess, turnID, "dev_bash", `{"command":"ls"}`)
	}

	snap := svc.inspector.Snapshot(sess, turnID)
	if snap == nil {
		t.Fatal("no inspector snapshot")
	}
	if snap.LoopStatus == nil {
		t.Fatal("LoopStatus should be non-nil after 3 identical calls")
	}
	if !snap.LoopStatus.Detected {
		t.Error("LoopStatus.Detected should be true")
	}
	if snap.LoopStatus.Reason == "" {
		t.Error("LoopStatus.Reason should be non-empty")
	}
}

// TestExecutorLoop_DifferentArgs_NoDetection verifies 3 calls with distinct args
// do NOT trigger loop detection.
func TestExecutorLoop_DifferentArgs_NoDetection(t *testing.T) {
	svc := makeLoopTestService()
	const (
		sess   = "loop-sess-2"
		turnID = "1"
	)
	svc.inspector.EnsureTurn(sess, turnID)

	runTool(svc, sess, turnID, "dev_bash", `{"command":"ls"}`)
	runTool(svc, sess, turnID, "dev_bash", `{"command":"pwd"}`)
	runTool(svc, sess, turnID, "dev_bash", `{"command":"whoami"}`)

	snap := svc.inspector.Snapshot(sess, turnID)
	if snap == nil {
		t.Fatal("no inspector snapshot")
	}
	if snap.LoopStatus != nil && snap.LoopStatus.Detected {
		t.Error("LoopStatus must not be detected for calls with distinct args")
	}
}

// TestExecutorLoop_NilDetector_NoPanic exercises the nil loopDetector path.
func TestExecutorLoop_NilDetector_NoPanic(t *testing.T) {
	svc := makeLoopTestService()
	svc.loopDetector = nil
	runTool(svc, "nil-sess", "t1", "dev_bash", `{"command":"ls"}`)
	// No panic = pass.
}

// TestExecutorLoop_NilInspector_SlogOnly exercises detection without inspector.
func TestExecutorLoop_NilInspector_SlogOnly(t *testing.T) {
	svc := makeLoopTestService()
	svc.inspector = nil
	for i := 0; i < 3; i++ {
		runTool(svc, "slog-sess", "t1", "dev_bash", `{"command":"ls"}`)
	}
	// No panic = pass.
}
