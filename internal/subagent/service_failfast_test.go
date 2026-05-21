package subagent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// fakeProfileResolver is a minimal ProfileResolver. profiles maps slug
// → AgentProfile (CanExecute defaulted on the struct). A slug that is
// not in the map returns sql.ErrNoRows (wrapped, matching *store.Store's
// %w-wrap contract) so the fail-fast gate's errors.Is check fires.
//
// errOverride, when non-nil, is returned verbatim for every lookup —
// used by the "transient lookup error" case to exercise the non-
// ErrNoRows branch of the gate.
type fakeProfileResolver struct {
	profiles    map[string]*store.AgentProfile
	errOverride error
}

func (f *fakeProfileResolver) GetAgentBySlug(slug string) (*store.AgentProfile, error) {
	if f.errOverride != nil {
		return nil, f.errOverride
	}
	if p, ok := f.profiles[slug]; ok {
		return p, nil
	}
	return nil, fmt.Errorf("fake resolver: no profile %q: %w", slug, sql.ErrNoRows)
}

// recordingRunner tracks whether Run was ever invoked. Used to assert
// the fail-fast gate rejects BEFORE the runner is reached — the c271
// regression target is "no child session, no timer, no orphan row".
type recordingRunner struct{ called bool }

func (r *recordingRunner) Run(_ context.Context, _ *Run) (*Result, error) {
	r.called = true
	return &Result{Summary: "should not be reached", ResultJSON: "{}"}, nil
}

// TestSpawn_FailFast_NoProfileForRole covers the c271 pattern (an LLM
// supplying a role name like "system-architect" with no registered
// profile). The gate must reject at the Spawn boundary with an error
// wrapping ErrNoProfileForRole, before any DB write or runner call.
func TestSpawn_FailFast_NoProfileForRole(t *testing.T) {
	db, _ := newTestDB(t)
	resolver := &fakeProfileResolver{
		profiles: map[string]*store.AgentProfile{
			"worker": {Slug: "worker", CanExecute: true},
		},
	}
	runner := &recordingRunner{}
	svc := NewService(db, runner, &stubPoster{}, nil, stubSettings{})
	svc.SetProfileResolver(resolver)

	_, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-1",
		ParentAgentID:   "file-backend",
		Role:            "system-architect",
		Prompt:          "design the dispatch path",
		Mode:            ModeSync,
	})
	if err == nil {
		t.Fatal("Spawn unexpectedly succeeded for unknown role")
	}
	if !errors.Is(err, ErrNoProfileForRole) {
		t.Errorf("err = %v; want errors.Is ErrNoProfileForRole", err)
	}
	if !strings.Contains(err.Error(), "system-architect") {
		t.Errorf("err = %q; expected role name in message", err)
	}
	if runner.called {
		t.Error("runner.Run was called for unknown role; expected reject before runner")
	}
	// No DB row should have been inserted — exercise the c271 invariant
	// (no orphan row, child_session_id=''). A SELECT COUNT(*) over the
	// table is sufficient because newTestDB returns a fresh database.
	var rows int
	if qerr := db.QueryRow(`SELECT COUNT(*) FROM subagent_runs`).Scan(&rows); qerr != nil {
		t.Fatalf("count subagent_runs: %v", qerr)
	}
	if rows != 0 {
		t.Errorf("subagent_runs row count = %d; want 0 (gate must reject before insert)", rows)
	}
}

// TestSpawn_FailFast_RoleNotExecutable covers the c256 pattern: a
// profile exists but carries can_execute=false (e.g. the live planner
// profile, which had a real prompt but no tool surface and hung
// drainCapture for 300s with zero output). The gate must reject at
// the Spawn boundary so the parent receives a clear config error
// rather than a silent stall classified by CW-20260519-0067.
func TestSpawn_FailFast_RoleNotExecutable(t *testing.T) {
	db, _ := newTestDB(t)
	resolver := &fakeProfileResolver{
		profiles: map[string]*store.AgentProfile{
			"planner": {Slug: "planner", CanExecute: false},
		},
	}
	runner := &recordingRunner{}
	svc := NewService(db, runner, &stubPoster{}, nil, stubSettings{})
	svc.SetProfileResolver(resolver)

	_, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-1",
		ParentAgentID:   "file-backend",
		Role:            "planner",
		Prompt:          "plan the migration",
		Mode:            ModeSync,
	})
	if err == nil {
		t.Fatal("Spawn unexpectedly succeeded for can_execute=false role")
	}
	if !errors.Is(err, ErrRoleNotExecutable) {
		t.Errorf("err = %v; want errors.Is ErrRoleNotExecutable", err)
	}
	if !strings.Contains(err.Error(), "planner") {
		t.Errorf("err = %q; expected role name in message", err)
	}
	if runner.called {
		t.Error("runner.Run was called for non-executable role; expected reject before runner")
	}
}

// TestSpawn_FailFast_TextOnlyWhitelistAccepted ensures hint-selector
// (can_execute=false but a legitimate PeerQuery target) is admitted
// through the gate. This is the regression target for the spec's
// explicit text-only whitelist clause — without it the F5 think-block
// v2 hint dispatch would break.
func TestSpawn_FailFast_TextOnlyWhitelistAccepted(t *testing.T) {
	db, _ := newTestDB(t)
	resolver := &fakeProfileResolver{
		profiles: map[string]*store.AgentProfile{
			"hint-selector": {Slug: "hint-selector", CanExecute: false},
		},
	}
	svc := NewService(db, EchoRunner{}, &stubPoster{}, nil, stubSettings{})
	svc.SetProfileResolver(resolver)

	id, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-hint",
		ParentAgentID:   "_system_",
		Role:            "hint-selector",
		Prompt:          `{"user_input":"plan","scope_tier":"open"}`,
		Mode:            ModeSync,
	})
	if err != nil {
		t.Fatalf("Spawn rejected hint-selector despite whitelist: %v", err)
	}
	if id == "" {
		t.Fatal("Spawn returned empty run id for hint-selector")
	}
}

// TestSpawn_FailFast_ExecutableRoleAccepted covers the happy path —
// a registered, can_execute=true profile (e.g. worker) passes the gate.
func TestSpawn_FailFast_ExecutableRoleAccepted(t *testing.T) {
	db, _ := newTestDB(t)
	resolver := &fakeProfileResolver{
		profiles: map[string]*store.AgentProfile{
			"worker": {Slug: "worker", CanExecute: true},
		},
	}
	svc := NewService(db, EchoRunner{}, &stubPoster{}, nil, stubSettings{})
	svc.SetProfileResolver(resolver)

	id, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-1",
		ParentAgentID:   "file-backend",
		Role:            "worker",
		Prompt:          "do the work",
		Mode:            ModeSync,
	})
	if err != nil {
		t.Fatalf("Spawn unexpectedly failed for valid role: %v", err)
	}
	if id == "" {
		t.Fatal("Spawn returned empty run id")
	}
}

// TestSpawn_FailFast_LookupErrorPropagates ensures a non-ErrNoRows
// lookup error (e.g. DB closed mid-call) is surfaced as a wrapped
// internal error rather than being misclassified as a config issue.
// The caller's retry policy may differ for transient faults; the gate
// must not mask them.
func TestSpawn_FailFast_LookupErrorPropagates(t *testing.T) {
	db, _ := newTestDB(t)
	transientErr := errors.New("database is closed")
	resolver := &fakeProfileResolver{errOverride: transientErr}
	svc := NewService(db, EchoRunner{}, &stubPoster{}, nil, stubSettings{})
	svc.SetProfileResolver(resolver)

	_, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-1",
		ParentAgentID:   "file-backend",
		Role:            "worker",
		Prompt:          "do the work",
		Mode:            ModeSync,
	})
	if err == nil {
		t.Fatal("Spawn unexpectedly succeeded under transient lookup error")
	}
	if !errors.Is(err, transientErr) {
		t.Errorf("err = %v; want underlying transient error wrapped", err)
	}
	if errors.Is(err, ErrNoProfileForRole) || errors.Is(err, ErrRoleNotExecutable) {
		t.Errorf("err = %v; transient lookup error must NOT be reclassified as config", err)
	}
}

// TestSpawn_FailFast_DisabledWhenResolverNil preserves the
// SetProfileResolver opt-in contract: a service without a wired
// resolver runs the prior (pre-gate) behavior so existing tests using
// synthetic role names (e.g. "file-summarizer") continue to drive the
// runner end-to-end without needing a profile resolver.
func TestSpawn_FailFast_DisabledWhenResolverNil(t *testing.T) {
	db, _ := newTestDB(t)
	svc := NewService(db, EchoRunner{}, &stubPoster{}, nil, stubSettings{})
	// Deliberately do not call SetProfileResolver.

	id, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "sess-1",
		ParentAgentID:   "file-backend",
		Role:            "file-summarizer",
		Prompt:          "summarize",
		Mode:            ModeSync,
	})
	if err != nil {
		t.Fatalf("Spawn unexpectedly failed with no resolver wired: %v", err)
	}
	if id == "" {
		t.Fatal("Spawn returned empty run id")
	}
}
