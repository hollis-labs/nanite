package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	ctxpkg "github.com/hollis-labs/libs/agent/core/contextwindow"
	"github.com/hollis-labs/libs/agent/core/runloop"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
)

// flippingSessions reports an active session for the first `activeReads`
// lookups and an archived one afterwards, which is the interleaving a stop
// produces when it lands between a turn's pre-boot check and its boot.
type flippingSessions struct {
	SessionService
	reads       atomic.Int32
	activeReads int32
}

func (f *flippingSessions) Get(_ context.Context, id string) (*store.Session, error) {
	status := "active"
	if f.reads.Add(1) > f.activeReads {
		status = "archived"
	}
	return &store.Session{ID: id, Status: status}, nil
}

func endedTestService(t *testing.T, sessions SessionService) *chatServiceImpl {
	t.Helper()
	return &chatServiceImpl{
		agentDeps:        bootRealDeps(t),
		agentEventBridge: &agentEventBridge{streams: NewStreamManager()},
		sessions:         sessions,
	}
}

func driveEnded(s *chatServiceImpl, id string) error {
	slots := &SlotAssemblyResult{Window: ctxpkg.NewContextWindow(200_000, ctxpkg.DefaultEstimator{})}
	_, err := s.driveBootSession(context.Background(), id, &store.Session{ID: id}, nil, slots, "hi", 0, "codex")
	return err
}

// An ended session never starts a runtime.
func TestDriveBootSession_RefusesEndedSession(t *testing.T) {
	s := endedTestService(t, &flippingSessions{activeReads: 0})
	if err := driveEnded(s, "sess-ended-1"); !errors.Is(err, ErrSessionEnded) {
		t.Fatalf("err = %v, want ErrSessionEnded", err)
	}
	if _, ok := s.runtimeSessions().Load("sess-ended-1"); ok {
		t.Fatal("a runtime was registered for an ended session")
	}
}

// A stop that archives the session after the pre-boot check but before the
// process is registered must not leave that process running: the turn closes
// it itself, because the stop's close hook could not see it.
func TestDriveBootSession_StopRacingBootLeavesNoRuntime(t *testing.T) {
	s := endedTestService(t, &flippingSessions{activeReads: 1})
	err := driveEnded(s, "sess-race-1")
	if !errors.Is(err, ErrSessionEnded) {
		t.Fatalf("err = %v, want ErrSessionEnded", err)
	}
	if sess, ok := s.runtimeSessions().Load("sess-race-1"); ok {
		t.Fatalf("runtime %v survived a stop that raced the boot", sess)
	}
}

// The work root a defined CLI view carries is the directory the runtime starts
// in, and closing the session stops that runtime.
func TestDriveBootSession_WorkRootIsTheRuntimeWorkdirAndCloseStopsIt(t *testing.T) {
	root, other := canonicalTempDir(t), canonicalTempDir(t)
	s := endedTestService(t, &flippingSessions{activeReads: 1 << 20})
	st := s.agentDeps.Store.(*agentRuntimeStore).store
	s.store = st
	pin, err := st.InstallAgentDefinition(context.Background(), []byte("---\nschema_version: \"2\"\ndefinition_id: def:work-root\nrevision: \"1\"\nname: work-root\ndescription: Work root.\nbehavior:\n  purpose: Answer.\nrequirements: {}\nharness_profile:\n  context: {}\n  permissions:\n    profile: default\ncontinuity:\n  mode: ephemeral\n---\nBody.\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	settings := store.NativeHostSettings{Version: "1", Runtime: "cli", Provider: "codex", Directories: []string{root, other}}
	host, err := st.CreateAgentHostSettings(context.Background(), store.AgentHostSettings{Slug: "wr", Title: "wr", Source: "test", DefinitionRef: pin, Enabled: true, Settings: settings})
	if err != nil {
		t.Fatal(err)
	}
	agent := &store.AgentProfile{ID: "agent-wr", HostSettingsID: host.ID, Revision: host.Revision, RuntimeKind: "cli", NativeHost: &settings}
	bootProfile := *agent
	bootProfile.DefaultProvider = "codex"
	s.agentDeps.Agents = &fakeAgentProfilesResolver{profile: &bootProfile}
	slots := &SlotAssemblyResult{Window: ctxpkg.NewContextWindow(200_000, ctxpkg.DefaultEstimator{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, bootErr := s.driveBootSession(ctx, "sess-wr", &store.Session{ID: "sess-wr"}, agent, slots, "hi", 0, "codex")
	sess, ok := s.runtimeSessions().Load("sess-wr")
	if !ok {
		t.Fatalf("no runtime booted: %v", bootErr)
	}
	if got, _ := s.activeSessionWorkRoots.Load("sess-wr"); got != root {
		t.Fatalf("boot workdir = %v, want the work root %q", got, root)
	}
	raw, readErr := os.ReadFile(filepath.Join(sess.BootDir, "config.toml")) //nolint:gosec // reads the config this test's boot just planted
	if readErr != nil {
		t.Fatal(readErr)
	}
	if want := `"` + root + `"`; !strings.Contains(string(raw), want) {
		t.Fatalf("planted launch config does not name the work root %q:\n%s", root, raw)
	}
	s.CloseAgentSession(context.Background(), "sess-wr")
	if _, ok := s.runtimeSessions().Load("sess-wr"); ok {
		t.Fatal("runtime still registered after close")
	}
	waitCtx, stop := context.WithTimeout(context.Background(), 20*time.Second)
	defer stop()
	if err := sess.Wait(waitCtx); waitCtx.Err() != nil {
		t.Fatalf("runtime did not stop: %v", err)
	}
}

// A work root swapped for a symlink after create is refused at boot.
func TestResolveWorkRootRejectsSwappedSymlink(t *testing.T) {
	host := canonicalTempDir(t)
	root := filepath.Join(host, "work")
	if err := os.Mkdir(root, 0o750); err != nil {
		t.Fatal(err)
	}
	got, err := resolveWorkRoot(root, []string{host})
	if err != nil || got != root {
		t.Fatalf("resolve = %q, %v", got, err)
	}
	outside := canonicalTempDir(t)
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, root); err != nil {
		t.Fatal(err)
	}
	if again, err := resolveWorkRoot(root, []string{host}); err == nil && again == got {
		t.Fatalf("swapped symlink still resolved to the recorded root %q", again)
	}
}

// definedCLIView binds a CLI defined view with a create-time work_root to the
// characterization fixture's session and returns the work root and host.
func definedCLIView(t *testing.T, f *characterizationFixture, hostDir, workRoot string) {
	t.Helper()
	ctx := t.Context()
	if err := installEmbeddedChatDefinition(ctx, f.st); err != nil {
		t.Fatal(err)
	}
	verified, err := EmbeddedDefinition()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := MapChatDefinition(verified)
	if err != nil {
		t.Fatal(err)
	}
	settings := store.NativeHostSettings{Version: "1", Runtime: "cli", Provider: "codex", Directories: []string{hostDir}}
	host, err := f.st.CreateAgentHostSettings(ctx, store.AgentHostSettings{Slug: "prep", Title: "prep", Source: "test", DefinitionRef: verified.Ref.MeshRef(), Enabled: true, Settings: settings})
	if err != nil {
		t.Fatal(err)
	}
	cfg.Model = ModelSelection{"codex", "codex-model"}
	cfg.HostSettings = &settings
	cfg.HostSettingsRef = &HostSettingsRef{ID: host.ID, Revision: host.Revision}
	cfg.WorkRoot = workRoot
	pinJSON, _ := json.Marshal(verified.Ref)
	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.st.DB.ExecContext(ctx, `INSERT INTO cognitive_views(session_view_id,definition_ref_json,chat_config_json) VALUES(?,?,?)`, f.session, string(pinJSON), string(cfgJSON)); err != nil {
		t.Fatal(err)
	}
}

func cognitiveCtx() context.Context {
	return context.WithValue(context.Background(), cognitiveTurnContextKey{}, true)
}

// Through prepareTurn: the create-time work root leads the host directories and
// becomes the runtime's working directory.
func TestPrepareTurn_WorkRootBecomesTheRuntimeWorkdir(t *testing.T) {
	host := canonicalTempDir(t)
	root := filepath.Join(host, "work")
	if err := os.Mkdir(root, 0o750); err != nil {
		t.Fatal(err)
	}
	f := newHandleMessageFixture(t, nil)
	f.svc.agentDeps = bootRealDeps(t)
	definedCLIView(t, f, host, root)

	ch := make(chan chat.StreamEvent, 16)
	prepared := f.svc.prepareTurn(cognitiveCtx(), f.session, "hi", ch)
	if prepared.setup == nil {
		t.Fatalf("prepareTurn refused a valid work root: %v", drainEvents(ch))
	}
	agent := prepared.setup.agent
	if len(agent.NativeHost.Directories) != 2 || agent.NativeHost.Directories[0] != root || agent.NativeHost.Directories[1] != host {
		t.Fatalf("directories = %v, want [work root, host dir]", agent.NativeHost.Directories)
	}

	// Hand the prepared agent to the real boot path and read the workdir it used.
	f.svc.agentEventBridge = &agentEventBridge{streams: NewStreamManager()}
	bootProfile := *agent
	bootProfile.DefaultProvider = "codex"
	f.svc.agentDeps.Agents = &fakeAgentProfilesResolver{profile: &bootProfile}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, _ = f.svc.driveBootSession(ctx, f.session, prepared.setup.session, agent, prepared.setup.slotResult, "hi", 0, "codex")
	if got, _ := f.svc.activeSessionWorkRoots.Load(f.session); got != root {
		t.Fatalf("runtime workdir = %v, want %q", got, root)
	}
	f.svc.CloseAgentSession(context.Background(), f.session)
}

// A work root swapped for a symlink out of the host directories after create
// fails the next turn and stops it before any runtime starts.
func TestPrepareTurn_SwappedWorkRootFailsTheTurn(t *testing.T) {
	host := canonicalTempDir(t)
	root := filepath.Join(host, "work")
	if err := os.Mkdir(root, 0o750); err != nil {
		t.Fatal(err)
	}
	f := newHandleMessageFixture(t, nil)
	f.svc.agentDeps = bootRealDeps(t)
	definedCLIView(t, f, host, root)

	outside := canonicalTempDir(t)
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, root); err != nil {
		t.Fatal(err)
	}
	ch := make(chan chat.StreamEvent, 16)
	prepared := f.svc.prepareTurn(cognitiveCtx(), f.session, "hi", ch)
	if prepared.setup != nil || prepared.directive != runloop.Terminate {
		t.Fatalf("a swapped work root was accepted: %+v", prepared)
	}
	events := drainEvents(ch)
	if len(events) != 1 || !strings.Contains(events[0].Error, "work root") {
		t.Fatalf("events = %+v, want one error naming the work root", events)
	}
	if _, ok := f.svc.runtimeSessions().Load(f.session); ok {
		t.Fatal("a runtime started for a turn that was refused")
	}
}

// A native turn submitted after the session ended is refused at admission,
// under the conversation gate a stop holds while it archives and cancels.
func TestSubmitCognitiveTurn_RefusesEndedSession(t *testing.T) {
	f := newHandleMessageFixture(t, nil)
	bindTestDefinedConfiguration(t, f)
	if err := f.st.ArchiveSession(t.Context(), f.session); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SubmitCognitiveTurn(t.Context(), f.session, "hi"); !errors.Is(err, ErrSessionEnded) {
		t.Fatalf("err = %v, want ErrSessionEnded", err)
	}
	if f.provider.callCount() != 0 {
		t.Fatal("the provider was called for an ended session")
	}
}
