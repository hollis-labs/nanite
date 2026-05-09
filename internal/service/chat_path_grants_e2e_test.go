package service

// CW-fix-dev-glob-grant — integration test that mirrors the production
// dispatch chain for dev_* path-grant resolution.
//
// Glass-8's unit test (TestDevTools_PathGrants_C127TildeMention_NoHOME)
// calls dt.resolveAllowed(ctx, ...) directly. The c138 production
// reproduction failed because the regression hides between
// "ctx stamped in executeToolBatch" and "ctx read in
// tryResolveViaSessionGrant" — neither end of the unit test exercises
// the full chain.
//
// This test wires a real PathGrants into a real chatServiceImpl, plumbs
// it through a real toolServiceImpl that talks to a real mcp.Manager
// holding a real DevToolsTransport, and dispatches a dev_glob call
// through executeToolBatch. Asserts the call resolves successfully AND
// that the new grant-resolution observability log fires with
// match_found:true. If the production-shape regression returns, this
// test goes red whether or not the lower-level unit test stays green.

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/nanite/internal/store"
)

// e2eStore satisfies the small surface executeSingleTool calls on the
// store: just LogEvent. Pattern matches loopTestStore in
// chat_tool_executor_loop_test.go.
type e2eStore struct{ *store.Store }

func (*e2eStore) LogEvent(_, _, _, _, _ string) {}

// safeBuf is a goroutine-safe slog sink.
type safeBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *safeBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}
func (s *safeBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// TestChat_PathGrants_DevGlobE2E exercises the production dispatch chain
// from executeToolBatch all the way down to DevToolsTransport.callGlob.
// This is the regression guard the c138 reproduction needed: the
// existing unit test stayed green while the integration regressed.
func TestChat_PathGrants_DevGlobE2E(t *testing.T) {
	tmpDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "hello.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Real path-grant store. Register the tempdir via the same parser
	// the chat layer uses on inbound user messages. The "user message"
	// shape mimics c138 so the registration path is identical.
	grants := permission.NewPathGrants()
	registered := grants.RegisterFromUserMessage(
		"sess-e2e",
		"Quick test — list the contents of "+tmpDir+", just top level, first 10 entries.",
	)
	if len(registered) == 0 {
		t.Fatalf("RegisterFromUserMessage returned no grants for tempdir %q", tmpDir)
	}

	// Real mcp.Manager with a DevToolsTransport. Empty static AllowedPaths
	// so the only thing letting dev_glob through is the session grant —
	// matches c138's observed `paths:null source:default:none` boot log.
	mgr := mcp.NewManager()
	if err := mgr.AddServer("dev", mcp.NewDevToolsTransport(nil), mcp.TierBuiltin); err != nil {
		t.Fatalf("AddServer: %v", err)
	}
	if err := mgr.DiscoverTools(context.Background()); err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}

	// Real toolServiceImpl wired direct to the manager (no toolclient —
	// the dev-tool gate it would apply is orthogonal to this regression).
	tools := NewToolService(nil, mgr, nil)

	// Capture slog so we can assert the new grant-resolution log fires.
	prev := slog.Default()
	sink := &safeBuf{}
	slog.SetDefault(slog.New(slog.NewJSONHandler(sink, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	svc := &chatServiceImpl{
		streams:      NewStreamManager(),
		tools:        tools,
		store:        &e2eStore{},
		pathGrants:   grants,
		argValidator: newArgValidator(),
	}

	plans := []toolPlan{{
		tu: provider.ToolUseBlock{
			ID:    "tu-glob-1",
			Name:  "dev_glob",
			Input: map[string]any{"pattern": "*.md", "directory": tmpDir},
		},
		status: toolPlanReady,
	}}

	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	ch := make(chan chat.StreamEvent, 64)
	go func() {
		for range ch {
		}
	}()
	defer close(ch)

	results := svc.executeToolBatch(
		context.Background(), plans, ls, "agent-test", ch, "sess-e2e", "ws-test",
	)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	got := results[0]
	if got.isError {
		t.Fatalf("dev_glob result was error — production-shape grant resolution regressed.\n  output: %s",
			got.rawOutput)
	}
	if !strings.Contains(got.rawOutput, "hello.md") {
		t.Errorf("expected hello.md in output, got: %s", got.rawOutput)
	}

	// Assert the grant-resolution log fired with match_found=true. The
	// JSON sink emits one line per slog.Info call; we look for the
	// "permission: dev_tools grant-resolution" message.
	logs := sink.String()
	if !strings.Contains(logs, "permission: dev_tools grant-resolution") {
		t.Fatalf("expected grant-resolution log line, got: %s", logs)
	}
	if !strings.Contains(logs, `"match_found":true`) {
		t.Errorf("expected match_found:true in grant-resolution log, got: %s", logs)
	}
	if !strings.Contains(logs, `"had_checker":true`) {
		t.Errorf("expected had_checker:true in grant-resolution log, got: %s", logs)
	}
}

// TestE2E_WorkerInheritsParentGrant exercises the full integration of
// the lineage-aware grant lookup: a parent chat session registers a
// path grant, a worker session is associated via RegisterLineage (the
// same call ChatRunner makes at spawn time), and the worker's dev_glob
// — dispatched through executeToolBatch under the worker session ID —
// resolves successfully via the parent's grant.
//
// Asserts the new grant-resolution log surfaces match_kind:ancestor_session
// and the matching parent in match_via_session_id, so the c146 surface
// the SP-20260501-0003 acceptance smoke uncovered ("worker can't act on
// the path the user mentioned to chat") is regression-locked end-to-end.
func TestE2E_WorkerInheritsParentGrant(t *testing.T) {
	tmpDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "hello.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	const parentSessionID = "sess-parent-e2e"
	const workerSessionID = "sess-worker-e2e"

	grants := permission.NewPathGrants()
	registered := grants.RegisterFromUserMessage(
		parentSessionID,
		"Use task_execute to spawn a Worker that lists "+tmpDir+", first 10 entries.",
	)
	if len(registered) == 0 {
		t.Fatalf("setup: RegisterFromUserMessage returned no grants")
	}

	// Stamp the (worker → parent) lineage the way ChatRunner.Run does at
	// spawn time. Without this the worker's bucket is empty and the
	// dev_glob below would fail — exactly the c146 reproduction.
	grants.RegisterLineage(workerSessionID, parentSessionID)
	t.Cleanup(func() { grants.ClearLineage(workerSessionID) })

	mgr := mcp.NewManager()
	if err := mgr.AddServer("dev", mcp.NewDevToolsTransport(nil), mcp.TierBuiltin); err != nil {
		t.Fatalf("AddServer: %v", err)
	}
	if err := mgr.DiscoverTools(context.Background()); err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}
	tools := NewToolService(nil, mgr, nil)

	prev := slog.Default()
	sink := &safeBuf{}
	slog.SetDefault(slog.New(slog.NewJSONHandler(sink, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	svc := &chatServiceImpl{
		streams:      NewStreamManager(),
		tools:        tools,
		store:        &e2eStore{},
		pathGrants:   grants,
		argValidator: newArgValidator(),
	}

	plans := []toolPlan{{
		tu: provider.ToolUseBlock{
			ID:    "tu-glob-worker",
			Name:  "dev_glob",
			Input: map[string]any{"pattern": "*.md", "directory": tmpDir},
		},
		status: toolPlanReady,
	}}

	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	ch := make(chan chat.StreamEvent, 64)
	go func() {
		for range ch {
		}
	}()
	defer close(ch)

	// Dispatch under the WORKER session ID. Pre-fix this fails with
	// bucket_size:0; post-fix it resolves via the lineage walk to parent.
	results := svc.executeToolBatch(
		context.Background(), plans, ls, "agent-worker", ch, workerSessionID, "ws-test",
	)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	got := results[0]
	if got.isError {
		t.Fatalf("worker dev_glob errored — lineage-aware grant lookup did not fire.\n  output: %s",
			got.rawOutput)
	}
	if !strings.Contains(got.rawOutput, "hello.md") {
		t.Errorf("expected hello.md in output, got: %s", got.rawOutput)
	}

	logs := sink.String()
	if !strings.Contains(logs, `"match_kind":"ancestor_session"`) {
		t.Errorf("expected match_kind:ancestor_session in grant-resolution log, got: %s", logs)
	}
	if !strings.Contains(logs, `"match_via_session_id":"`+parentSessionID+`"`) {
		t.Errorf("expected match_via_session_id:%q in grant-resolution log, got: %s",
			parentSessionID, logs)
	}
}
