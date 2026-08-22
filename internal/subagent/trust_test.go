package subagent

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/hollis-labs/nanite/internal/dispatch"
	"github.com/hollis-labs/nanite/internal/store"
)

// stubTrustResolver is a test seam for TrustResolverIface.
type stubTrustResolver struct {
	mu    sync.Mutex
	calls []trustResolveCall
	tier  dispatch.TrustTier
	err   error
}

type trustResolveCall struct {
	agentProfileID string
}

func (r *stubTrustResolver) ResolveTrust(_ context.Context, agentProfileID string) (dispatch.TrustTier, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, trustResolveCall{agentProfileID})
	return r.tier, r.err
}

func (r *stubTrustResolver) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// stubEventLogger captures LogEvent calls.
type stubEventLogger struct {
	mu    sync.Mutex
	calls []eventLogCall
}

type eventLogCall struct {
	sessionID string
	eventType string
	category  string
	detail    string
	metadata  string
}

func (l *stubEventLogger) LogEvent(ctx context.Context, sessionID, eventType, category, detail, metadata string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, eventLogCall{sessionID, eventType, category, detail, metadata})
}

func (l *stubEventLogger) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.calls)
}

func (l *stubEventLogger) last() eventLogCall {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls[len(l.calls)-1]
}

// TestSpawn_UntrustedRole returns ErrUntrustedRole and does not run the runner.
func TestSpawn_UntrustedRole(t *testing.T) {
	db, _ := newTestDB(t)
	runner := &notCalledRunner{t: t}
	resolver := &stubTrustResolver{tier: dispatch.TrustUntrusted}

	svc := NewService(db, runner, nil, nil, stubSettings{store.UserSettings{SubagentApprovalRequired: false}})
	svc.SetTrustResolver(resolver)

	_, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-1",
		ParentAgentID:   "agent-1",
		Role:            "plugin-agent",
		Prompt:          "do something",
		Mode:            ModeSync,
		AgentProfileID:  "ap-plugin",
	})

	if err == nil {
		t.Fatal("expected error for untrusted role, got nil")
	}
	if !errors.Is(err, dispatch.ErrUntrustedRole) {
		t.Errorf("expected ErrUntrustedRole, got: %v", err)
	}
	if resolver.callCount() != 1 {
		t.Errorf("expected 1 resolver call, got %d", resolver.callCount())
	}
}

// TestSpawn_TrustedRole bypasses approval and writes an audit event.
func TestSpawn_TrustedRole(t *testing.T) {
	db, _ := newTestDB(t)
	poster := &stubPoster{}
	resolver := &stubTrustResolver{tier: dispatch.TrustTrusted}
	logger := &stubEventLogger{}
	emitter := &stubEmitter{}

	svc := NewService(db, EchoRunner{}, poster, emitter, stubSettings{
		store.UserSettings{SubagentApprovalRequired: true},
	})
	svc.SetTrustResolver(resolver)
	svc.SetEventLogger(logger)

	id, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-2",
		ParentAgentID:   "agent-2",
		Role:            "worker",
		Prompt:          "do work",
		Mode:            ModeSync,
		AgentProfileID:  "ap-worker",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if id == "" {
		t.Fatal("empty run id")
	}

	// Verify run completed (not left in requested state).
	run, err := svc.Status(context.Background(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if run.Status != StatusCompleted {
		t.Errorf("expected StatusCompleted (trusted bypass), got %q", run.Status)
	}

	// Approval emitter must NOT have been called (bypass happened).
	if emitter.Count() != 0 {
		t.Errorf("expected 0 approval emissions for trusted role, got %d", emitter.Count())
	}

	// Audit event must have been written.
	if logger.count() == 0 {
		t.Fatal("expected at least one audit event, got 0")
	}
	ev := logger.last()
	if ev.eventType != "trust_dispatch" {
		t.Errorf("expected eventType=trust_dispatch, got %q", ev.eventType)
	}
	if ev.category != "trust" {
		t.Errorf("expected category=trust, got %q", ev.category)
	}
}

// TestSpawn_NormalRole_ApprovalRequired uses the existing approval gate
// when trust is normal and SubagentApprovalRequired is true.
func TestSpawn_NormalRole_ApprovalRequired(t *testing.T) {
	db, _ := newTestDB(t)
	emitter := &stubEmitter{}
	resolver := &stubTrustResolver{tier: dispatch.TrustNormal}

	svc := NewService(db, &notCalledRunner{t: t}, nil, emitter, stubSettings{
		store.UserSettings{SubagentApprovalRequired: true},
	})
	svc.SetTrustResolver(resolver)

	id, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-3",
		Role:            "chat",
		Prompt:          "hello",
		Mode:            ModeSync,
		AgentProfileID:  "ap-chat",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Should be in requested state (gated).
	run, err := svc.Status(context.Background(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if run.Status != StatusRequested {
		t.Errorf("expected StatusRequested for normal trust + approval required, got %q", run.Status)
	}

	// Approval envelope emitted once.
	if emitter.Count() != 1 {
		t.Errorf("expected 1 approval emission, got %d", emitter.Count())
	}
}

// TestSpawn_NoAgentProfileID_FallbackToNormal verifies that empty
// AgentProfileID causes the trust gate to skip resolution and fall through
// to normal gate.
func TestSpawn_NoAgentProfileID_FallbackToNormal(t *testing.T) {
	db, _ := newTestDB(t)
	resolver := &stubTrustResolver{tier: dispatch.TrustTrusted} // would be trusted if consulted
	poster := &stubPoster{}

	svc := NewService(db, EchoRunner{}, poster, nil, stubSettings{
		store.UserSettings{SubagentApprovalRequired: false},
	})
	svc.SetTrustResolver(resolver)

	// No AgentProfileID → resolver should NOT be called.
	id, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-4",
		Role:            "worker",
		Prompt:          "work",
		Mode:            ModeSync,
		// AgentProfileID intentionally empty
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if id == "" {
		t.Fatal("expected run id")
	}
	if resolver.callCount() != 0 {
		t.Errorf("expected 0 resolver calls when AgentProfileID empty, got %d", resolver.callCount())
	}
}

// TestSpawn_TrustResolverError_FailClosed verifies that a resolver error
// causes the gate to treat the call as TrustNormal (fail closed).
func TestSpawn_TrustResolverError_FailClosed(t *testing.T) {
	db, _ := newTestDB(t)
	emitter := &stubEmitter{}
	resolver := &stubTrustResolver{
		tier: dispatch.TrustNormal,
		err:  errors.New("db unavailable"),
	}

	svc := NewService(db, &notCalledRunner{t: t}, nil, emitter, stubSettings{
		store.UserSettings{SubagentApprovalRequired: true},
	})
	svc.SetTrustResolver(resolver)

	id, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-5",
		Role:            "worker",
		Prompt:          "work",
		Mode:            ModeSync,
		AgentProfileID:  "ap-worker",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Resolver returned error → treat as normal → approval required.
	run, err := svc.Status(context.Background(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if run.Status != StatusRequested {
		t.Errorf("expected StatusRequested (fail-closed to normal), got %q", run.Status)
	}
}
