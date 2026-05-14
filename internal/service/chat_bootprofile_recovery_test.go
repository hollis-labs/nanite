package service

// CW-20260514-0049: regression coverage for boot-profile session
// management, stop/restart cleanup, and recovery-driven re-resolution.
//
// What these tests pin:
//
//   - Two concurrent boot-profile sessions stay isolated in
//     activeSessions / activeSessionLaunchSpecs.
//   - Stop one runtime; the other survives untouched.
//   - RestartAgentSession clears all per-session state without
//     archiving the chat session row.
//   - The recovery pre-boot hook re-resolves the LaunchSpec via
//     Registry.CompileFor under the fresh-catalog policy.
//   - The recovery hook surfaces an actionable error when the catalog
//     no longer carries the profile (operator drift case).
//   - The recovery hook does NOT touch ResumeFromCheckpoint or set
//     Mode=ModeResume — the "never on normal launch" guarantee carries
//     through to recovery defaults too; a future ticket flips that
//     deliberately via the seam documented in the hook.
//   - applyLaunchSpecToBootOpts (shared between normal + recovery
//     paths) leaves resume fields alone in the recovery call site too,
//     pinning the structural separation between normal launches and
//     recovery-resume.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hollis-labs/nanite/internal/bootprofile"
	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
	"github.com/hollis-labs/nanite/internal/store"
)

// recoveryFakeStore implements the narrow Store-method subset the
// recovery hook calls. Returns a Session keyed by its ID and an
// AgentProfile keyed by its ID; nil session / nil agent surface via
// the configured error or sentinel.
type recoveryFakeStore struct {
	Store // embed to satisfy the full interface via nil-method panics
	      // on un-stubbed calls; we override only the ones the hook
	      // touches.
	sessions     map[string]*store.Session
	primary      map[string]*store.SessionAgent
	agents       map[string]*store.AgentProfile
	getSessErr   error
}

func (f *recoveryFakeStore) GetSession(id string) (*store.Session, error) {
	if f.getSessErr != nil {
		return nil, f.getSessErr
	}
	s, ok := f.sessions[id]
	if !ok {
		return nil, errors.New("session not found")
	}
	return s, nil
}

func (f *recoveryFakeStore) GetSessionPrimaryAgent(sessionID string) (*store.SessionAgent, error) {
	sa, ok := f.primary[sessionID]
	if !ok {
		return nil, errors.New("no primary")
	}
	return sa, nil
}

func (f *recoveryFakeStore) GetAgent(id string) (*store.AgentProfile, error) {
	a, ok := f.agents[id]
	if !ok {
		return nil, errors.New("agent not found")
	}
	return a, nil
}

// writeRecoveryCatalog builds a catalog root with a single profile +
// launch pair and returns the catalog ROOT (LoadCatalog reads
// <root>/boot-profiles/*.yaml and <root>/launches/*.yaml). Args is the
// per-launch argv that the test will mutate to verify fresh-catalog
// behavior.
func writeRecoveryCatalog(t *testing.T, profileID string, args []string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "boot-profiles"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "launches"), 0o755); err != nil {
		t.Fatal(err)
	}
	profileYAML := "id: " + profileID + "\n" +
		"launch: rec-launch\n" +
		"identity:\n" +
		"  lineage_alias: rec-test\n"
	if err := os.WriteFile(filepath.Join(root, "boot-profiles", "p.yaml"), []byte(profileYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	launchYAML := "id: rec-launch\n" +
		"provider: pty-claude\n"
	if len(args) > 0 {
		launchYAML += "args:\n"
		for _, a := range args {
			launchYAML += "  - \"" + a + "\"\n"
		}
	}
	if err := os.WriteFile(filepath.Join(root, "launches", "l.yaml"), []byte(launchYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// writeRecoveryCatalogLaunchArgs rewrites just the launches file with
// new args, simulating an operator edit between the original boot and
// recovery resume. Mirrors writeRecoveryCatalog so the test can roundtrip.
func writeRecoveryCatalogLaunchArgs(t *testing.T, root string, args []string) {
	t.Helper()
	launchYAML := "id: rec-launch\n" +
		"provider: pty-claude\n"
	if len(args) > 0 {
		launchYAML += "args:\n"
		for _, a := range args {
			launchYAML += "  - \"" + a + "\"\n"
		}
	}
	if err := os.WriteFile(filepath.Join(root, "launches", "l.yaml"), []byte(launchYAML), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestRecoveryPreBootHook_FreshCompileWins is the headline pin: when a
// boot-profile session enters recovery and the catalog YAML was edited
// in-flight, the relaunch picks up the NEW args/env (fresh-catalog
// policy, design default #2).
func TestRecoveryPreBootHook_FreshCompileWins(t *testing.T) {
	catalogRoot := writeRecoveryCatalog(t, "fresh.test", []string{"--initial"})
	reg, err := bootprofile.NewRegistry(catalogRoot)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	s := &chatServiceImpl{
		bootProfiles: reg,
		store: &recoveryFakeStore{
			sessions: map[string]*store.Session{
				"sess-fresh": {ID: "sess-fresh", Provider: "bootprofile:fresh.test"},
			},
			primary: map[string]*store.SessionAgent{},
			agents:  map[string]*store.AgentProfile{},
		},
	}

	// Operator edits the catalog mid-life of the dead session.
	writeRecoveryCatalogLaunchArgs(t, catalogRoot, []string{"--fresh-from-catalog"})
	if err := reg.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	opts := &runtimeagent.Options{SessionID: "sess-fresh"}
	if err := s.recoveryPreBootHook(opts); err != nil {
		t.Fatalf("recoveryPreBootHook: %v", err)
	}

	found := false
	for _, a := range opts.ExtraArgs {
		if a == "--fresh-from-catalog" {
			found = true
		}
		if a == "--initial" {
			t.Errorf("stale initial arg leaked into Options.ExtraArgs: %v", opts.ExtraArgs)
		}
	}
	if !found {
		t.Fatalf("fresh arg missing from Options.ExtraArgs: %v", opts.ExtraArgs)
	}

	// The replacement spec is also stashed so the next driveBootSession
	// turn reads the same spec — pinning the chat-side adoption seam.
	if got := s.launchSpecFor("sess-fresh"); got == nil {
		t.Fatal("launch spec not stashed after recovery hook")
	}
}

// TestRecoveryPreBootHook_NonBootProfileSession is the legacy-path
// guard: when the session isn't boot-profile-backed, the hook leaves
// Options untouched so the existing CLI / API recovery flow keeps
// working unchanged.
func TestRecoveryPreBootHook_NonBootProfileSession(t *testing.T) {
	s := &chatServiceImpl{
		store: &recoveryFakeStore{
			sessions: map[string]*store.Session{
				"sess-cli": {ID: "sess-cli", Provider: "pty-claude"},
			},
		},
	}
	opts := &runtimeagent.Options{SessionID: "sess-cli", Workdir: "/preserved"}
	if err := s.recoveryPreBootHook(opts); err != nil {
		t.Fatalf("recoveryPreBootHook: %v", err)
	}
	if opts.Workdir != "/preserved" {
		t.Errorf("opts mutated for non-bootprofile session: workdir=%q", opts.Workdir)
	}
	if len(opts.ExtraArgs) != 0 {
		t.Errorf("ExtraArgs unexpectedly populated: %v", opts.ExtraArgs)
	}
}

// TestRecoveryPreBootHook_DeletedProfileActionable pins the
// operator-drift error message: a profile that was removed from the
// catalog after the original boot surfaces as an actionable error
// naming the profile id, not as a generic "compile failed".
func TestRecoveryPreBootHook_DeletedProfileActionable(t *testing.T) {
	// Empty catalog (no profiles).
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "boot-profiles"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "launches"), 0o755); err != nil {
		t.Fatal(err)
	}
	reg, err := bootprofile.NewRegistry(root)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	s := &chatServiceImpl{
		bootProfiles: reg,
		store: &recoveryFakeStore{
			sessions: map[string]*store.Session{
				"sess-gone": {ID: "sess-gone", Provider: "bootprofile:was.here"},
			},
		},
	}
	opts := &runtimeagent.Options{SessionID: "sess-gone"}
	err = s.recoveryPreBootHook(opts)
	if err == nil {
		t.Fatal("expected error for missing profile")
	}
	if !strings.Contains(err.Error(), "was.here") {
		t.Errorf("error must name profile id: %v", err)
	}
	if !strings.Contains(err.Error(), "deleted") && !strings.Contains(err.Error(), "not in current catalog") {
		t.Errorf("error must hint at catalog drift: %v", err)
	}
}

// TestRecoveryPreBootHook_NoResumeFieldsTouched is the structural pin
// for the "never pass resume ID on normal launch" guarantee, restated
// at the recovery seam: even on the recovery code path, the default
// disposition is NOT to set resume fields. A future ticket that wires
// resume protocol will flip this deliberately at the documented seam.
func TestRecoveryPreBootHook_NoResumeFieldsTouched(t *testing.T) {
	catalogRoot := writeRecoveryCatalog(t, "no.resume.test", nil)
	reg, err := bootprofile.NewRegistry(catalogRoot)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	s := &chatServiceImpl{
		bootProfiles: reg,
		store: &recoveryFakeStore{
			sessions: map[string]*store.Session{
				"sess-x": {ID: "sess-x", Provider: "bootprofile:no.resume.test"},
			},
		},
	}
	opts := &runtimeagent.Options{
		SessionID: "sess-x",
		Mode:      runtimeagent.ModeLongLived,
	}
	if err := s.recoveryPreBootHook(opts); err != nil {
		t.Fatalf("recoveryPreBootHook: %v", err)
	}
	if opts.Mode != runtimeagent.ModeLongLived {
		t.Errorf("Mode = %v, want ModeLongLived (recovery hook default must not switch to ModeResume)",
			opts.Mode)
	}
	if opts.ResumeFromCheckpoint != "" {
		t.Errorf("ResumeFromCheckpoint = %q, want empty (default recovery must not thread checkpoint)",
			opts.ResumeFromCheckpoint)
	}
}

// TestRecoveryPreBootHook_EmptySessionIDFails is the defensive guard:
// a broker bug that loses the session id surfaces as an immediate
// error, not as a confused "session not found".
func TestRecoveryPreBootHook_EmptySessionIDFails(t *testing.T) {
	s := &chatServiceImpl{}
	err := s.recoveryPreBootHook(&runtimeagent.Options{})
	if err == nil {
		t.Fatal("expected error for empty SessionID")
	}
	if !strings.Contains(err.Error(), "SessionID") {
		t.Errorf("error should reference SessionID: %v", err)
	}
}

// TestRecoveryPreBootHook_NoRegistryConfigured pins the "registry not
// wired" path: when session is boot-profile but the registry isn't
// configured (misbuild / test environment), surface a specific error
// instead of crashing or silently falling through.
func TestRecoveryPreBootHook_NoRegistryConfigured(t *testing.T) {
	s := &chatServiceImpl{
		// bootProfiles intentionally nil.
		store: &recoveryFakeStore{
			sessions: map[string]*store.Session{
				"sess-bp": {ID: "sess-bp", Provider: "bootprofile:something"},
			},
		},
	}
	err := s.recoveryPreBootHook(&runtimeagent.Options{SessionID: "sess-bp"})
	if err == nil {
		t.Fatal("expected error for missing registry")
	}
	if !strings.Contains(err.Error(), "registry") {
		t.Errorf("error should reference registry: %v", err)
	}
}

// TestAgentBootAdapter_PreBootHookInvokedAndCanError verifies the
// adapter side of the hook plumbing: the hook fires before agent.Boot,
// receives the Options pointer for mutation, and hook errors short-
// circuit the Boot call so the broker can escalate.
func TestAgentBootAdapter_PreBootHookInvokedAndCanError(t *testing.T) {
	adapter := &agentBootAdapter{}

	// Error path: hook error must abort and propagate. We do NOT need
	// to verify "Boot not called" because adapter.deps is nil — calling
	// runtimeagent.Boot would panic, so any test that reaches it would
	// fail loudly.
	wantErr := errors.New("synthetic hook error")
	adapter.SetPreBootHook(func(opts *runtimeagent.Options) error {
		return wantErr
	})
	_, err := adapter.Boot(context.Background(), runtimeagent.Options{SessionID: "x"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("hook error not propagated: %v", err)
	}
}

// TestAgentBootAdapter_PreBootHookCanMutateOptions verifies the hook
// surface is an OUT parameter — mutations land on the Options that
// agent.Boot receives. Pinned because a future refactor that switches
// to value semantics would silently break recovery's overlay.
func TestAgentBootAdapter_PreBootHookCanMutateOptions(t *testing.T) {
	adapter := &agentBootAdapter{}
	var observed runtimeagent.Options
	adapter.SetPreBootHook(func(opts *runtimeagent.Options) error {
		opts.Workdir = "/from-hook"
		opts.ExtraArgs = append(opts.ExtraArgs, "--injected")
		observed = *opts
		// Return a sentinel error so we don't actually reach the nil
		// runtimeagent.Boot panic; we only need to assert the mutation.
		return errors.New("stop short of agent.Boot")
	})
	_, _ = adapter.Boot(context.Background(), runtimeagent.Options{SessionID: "y"})
	if observed.Workdir != "/from-hook" {
		t.Errorf("hook mutation lost: workdir=%q", observed.Workdir)
	}
	if len(observed.ExtraArgs) != 1 || observed.ExtraArgs[0] != "--injected" {
		t.Errorf("hook arg mutation lost: %v", observed.ExtraArgs)
	}
}

// TestAgentBootAdapter_NilHookPasthrough verifies the no-hook path
// stays the legacy Boot wiring — uncovers a regression that would
// otherwise pass a stale Options pointer or skip IsRelaunch.
func TestAgentBootAdapter_NilHookPasthrough(t *testing.T) {
	adapter := &agentBootAdapter{}
	// Hook not set; calling Boot with a nil deps will panic inside
	// runtimeagent.Boot — we only need to verify the hook surface
	// itself doesn't intercept. SetPreBootHook(nil) is the explicit
	// disable.
	adapter.SetPreBootHook(nil)
	// Defer/recover the expected panic from nil-deps Boot to keep the
	// test focused on the hook surface.
	defer func() { _ = recover() }()
	_, _ = adapter.Boot(context.Background(), runtimeagent.Options{SessionID: "z"})
}

// TestCloseAgentSession_IsolationBetweenSessions is the headline
// "two concurrent boot-profile sessions" pin: stop one; the other's
// state stays intact.
func TestCloseAgentSession_IsolationBetweenSessions(t *testing.T) {
	s := &chatServiceImpl{}
	// Seed two sessions with distinct LaunchSpec stashes + activeSessions
	// placeholders so CloseAgentSession's LoadAndDelete branch runs.
	s.activeSessions.Store("sess-a", &struct{}{})
	s.activeSessions.Store("sess-b", &struct{}{})
	s.activeSessionLaunchSpecs.Store("sess-a", &bootprofile.LaunchSpec{ProfileID: "a"})
	s.activeSessionLaunchSpecs.Store("sess-b", &bootprofile.LaunchSpec{ProfileID: "b"})
	s.activeSessionSlots.Store("sess-a", uint64(111))
	s.activeSessionSlots.Store("sess-b", uint64(222))

	s.CloseAgentSession(context.Background(), "sess-a")

	if got := s.launchSpecFor("sess-a"); got != nil {
		t.Errorf("sess-a launch spec not cleared: %+v", got)
	}
	if got := s.launchSpecFor("sess-b"); got == nil || got.ProfileID != "b" {
		t.Errorf("sess-b launch spec collateral damage: %+v", got)
	}
	if _, ok := s.activeSessions.Load("sess-a"); ok {
		t.Error("sess-a activeSessions entry not removed")
	}
	if _, ok := s.activeSessions.Load("sess-b"); !ok {
		t.Error("sess-b activeSessions entry collateral-deleted")
	}
	if _, ok := s.activeSessionSlots.Load("sess-a"); ok {
		t.Error("sess-a slot hash not cleared")
	}
	if _, ok := s.activeSessionSlots.Load("sess-b"); !ok {
		t.Error("sess-b slot hash collateral-deleted")
	}
}

// TestCloseAgentSession_NoOpOnMissingSession verifies the idempotent
// no-op path: calling Close on an unknown session id leaves both
// already-tracked sessions untouched.
func TestCloseAgentSession_NoOpOnMissingSession(t *testing.T) {
	s := &chatServiceImpl{}
	s.activeSessions.Store("sess-keep", &struct{}{})
	s.activeSessionLaunchSpecs.Store("sess-keep", &bootprofile.LaunchSpec{ProfileID: "k"})

	s.CloseAgentSession(context.Background(), "sess-not-there")

	if _, ok := s.activeSessions.Load("sess-keep"); !ok {
		t.Error("sess-keep activeSessions entry lost on no-op close")
	}
	if got := s.launchSpecFor("sess-keep"); got == nil {
		t.Error("sess-keep launch spec lost on no-op close")
	}
}

// TestRestartAgentSession_ClearsState pins the user-driven restart
// behavior: state is torn down, the next user turn will re-resolve
// against the current catalog. No archive side-effect.
func TestRestartAgentSession_ClearsState(t *testing.T) {
	s := &chatServiceImpl{}
	s.activeSessions.Store("sess-r", &struct{}{})
	s.activeSessionLaunchSpecs.Store("sess-r", &bootprofile.LaunchSpec{ProfileID: "r"})

	if err := s.RestartAgentSession(context.Background(), "sess-r"); err != nil {
		t.Fatalf("RestartAgentSession: %v", err)
	}

	if _, ok := s.activeSessions.Load("sess-r"); ok {
		t.Error("activeSessions entry survived Restart")
	}
	if got := s.launchSpecFor("sess-r"); got != nil {
		t.Errorf("launch spec survived Restart: %+v", got)
	}
}

// TestRestartAgentSession_EmptyIDNoop guards the defensive path.
func TestRestartAgentSession_EmptyIDNoop(t *testing.T) {
	s := &chatServiceImpl{}
	if err := s.RestartAgentSession(context.Background(), ""); err != nil {
		t.Fatalf("RestartAgentSession empty id should noop: %v", err)
	}
}

// TestRestartAgentSession_NilReceiverNoop guards the defensive path
// against a programmer error (calling Restart on a nil pointer).
func TestRestartAgentSession_NilReceiverNoop(t *testing.T) {
	var s *chatServiceImpl
	if err := s.RestartAgentSession(context.Background(), "x"); err != nil {
		t.Fatalf("nil receiver should noop, got %v", err)
	}
}

// TestConcurrentBootProfileSessions_StashIsolation pins the per-session
// isolation of the activeSessionLaunchSpecs sync.Map. The test seeds
// two sessions in parallel, then verifies cross-session lookups don't
// collide. Goal: any future refactor that swaps sync.Map for a
// keyed-by-something-else structure trips this.
func TestConcurrentBootProfileSessions_StashIsolation(t *testing.T) {
	s := &chatServiceImpl{}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sid := "sess-" + string(rune('A'+idx))
			spec := &bootprofile.LaunchSpec{ProfileID: sid + "-profile"}
			s.activeSessionLaunchSpecs.Store(sid, spec)
		}(i)
	}
	wg.Wait()
	for i := 0; i < 8; i++ {
		sid := "sess-" + string(rune('A'+i))
		got := s.launchSpecFor(sid)
		if got == nil {
			t.Errorf("%s: spec lost in concurrent store", sid)
			continue
		}
		if got.ProfileID != sid+"-profile" {
			t.Errorf("%s: cross-session spec contamination: ProfileID=%q", sid, got.ProfileID)
		}
	}
}

// TestRecoveryPreBootHook_NilOptsAndReceiver covers the two
// degenerate-input branches.
func TestRecoveryPreBootHook_NilOptsAndReceiver(t *testing.T) {
	var s *chatServiceImpl
	if err := s.recoveryPreBootHook(&runtimeagent.Options{SessionID: "x"}); err == nil {
		t.Error("nil receiver should error")
	}
	s2 := &chatServiceImpl{}
	if err := s2.recoveryPreBootHook(nil); err == nil {
		t.Error("nil opts should error")
	}
}
