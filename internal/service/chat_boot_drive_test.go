package service

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/go-providers/provider"
	ctxpkg "github.com/hollis-labs/nanite/internal/context"
	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
	"github.com/hollis-labs/nanite/internal/store"
)

// TestAgentEventBridge_RouterBindForwardsEvents verifies that when a
// per-session router is bound, runtime StreamEvents reach the bound turnCh
// instead of being broadcast as SSE.
func TestAgentEventBridge_RouterBindForwardsEvents(t *testing.T) {
	bridge := &agentEventBridge{streams: NewStreamManager()}
	in := bridge.fanout("sess-1")

	turnCh := make(chan provider.StreamEvent, 4)
	bridge.SetPerSessionRouter("sess-1", turnCh)

	in <- provider.StreamEvent{Type: provider.EventDelta, Content: "hello"}

	select {
	case ev := <-turnCh:
		if ev.Type != provider.EventDelta || ev.Content != "hello" {
			t.Fatalf("unexpected event: %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for routed delta")
	}

	// Done event closes the chan + unbinds the router.
	in <- provider.StreamEvent{Type: provider.EventDone}

	select {
	case ev, ok := <-turnCh:
		if !ok {
			// Done arrived as a closed-chan signal — acceptable depending on
			// scheduling; the close itself is what we care about.
			break
		}
		if ev.Type != provider.EventDone {
			t.Fatalf("expected Done, got %+v", ev)
		}
		// Drain to confirm close-after-Done.
		if _, stillOpen := <-turnCh; stillOpen {
			t.Fatal("turnCh should be closed after Done")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Done / close")
	}

	close(in)
}

// TestAgentEventBridge_RouterEvictionOnTakeover verifies that binding a new
// turnCh while a stale one is still bound closes the stale chan (the
// "session takeover" path).
func TestAgentEventBridge_RouterEvictionOnTakeover(t *testing.T) {
	bridge := &agentEventBridge{streams: NewStreamManager()}

	stale := make(chan provider.StreamEvent, 1)
	bridge.SetPerSessionRouter("sess-1", stale)

	fresh := make(chan provider.StreamEvent, 1)
	bridge.SetPerSessionRouter("sess-1", fresh)

	// Stale chan should be closed.
	select {
	case _, ok := <-stale:
		if ok {
			t.Fatal("stale turnCh should be closed after takeover")
		}
	case <-time.After(time.Second):
		t.Fatal("stale turnCh not closed after takeover")
	}

	// Fresh chan stays bound and open.
	bridge.SetPerSessionRouter("sess-1", nil) // cleanup
	select {
	case _, ok := <-fresh:
		if ok {
			t.Fatal("fresh turnCh produced an event before unbind")
		}
	case <-time.After(50 * time.Millisecond):
		// Expected: nil unbind closes fresh too.
		t.Fatal("fresh turnCh not closed after unbind")
	}
}

// TestAgentEventBridge_NilRouterUnbinds verifies SetPerSessionRouter(nil)
// closes the bound chan and removes the router entry.
func TestAgentEventBridge_NilRouterUnbinds(t *testing.T) {
	bridge := &agentEventBridge{streams: NewStreamManager()}
	turnCh := make(chan provider.StreamEvent, 1)
	bridge.SetPerSessionRouter("sess-1", turnCh)
	bridge.SetPerSessionRouter("sess-1", nil)

	select {
	case _, ok := <-turnCh:
		if ok {
			t.Fatal("turnCh should be closed after nil unbind")
		}
	case <-time.After(time.Second):
		t.Fatal("turnCh not closed after nil unbind")
	}

	// Idempotent: a second nil call must not panic.
	bridge.SetPerSessionRouter("sess-1", nil)
}

// TestAgentEventBridge_NoRouterFallsBackToSSE verifies that without a bound
// router, fanout broadcasts via the StreamManager (no panic, no leak).
func TestAgentEventBridge_NoRouterFallsBackToSSE(t *testing.T) {
	bridge := &agentEventBridge{streams: NewStreamManager()}
	in := bridge.fanout("sess-2")

	// Push a delta through; no router → translateStreamEvent → BroadcastSessionStreamEvent.
	// With no SSE subscribers registered the broadcast is a no-op; we're
	// asserting that the goroutine consumes the event without blocking.
	in <- provider.StreamEvent{Type: provider.EventDelta, Content: "x"}
	in <- provider.StreamEvent{Type: provider.EventDone}
	close(in)

	// Allow the goroutine to drain.
	time.Sleep(20 * time.Millisecond)
}

// TestComposeUserPayload_PrependsUserContext verifies UserContext slot
// content leads the user message when present, and that the user message
// flows through unchanged when the slot is empty.
func TestComposeUserPayload_PrependsUserContext(t *testing.T) {
	cw := ctxpkg.NewContextWindow(200_000, ctxpkg.DefaultEstimator{})
	cw.SetContent(ctxpkg.SlotUserContext, "remember: be terse")

	got := composeUserPayload(&SlotAssemblyResult{Window: cw}, "hello agent")
	want := "remember: be terse\n\nhello agent"
	if got != want {
		t.Fatalf("composeUserPayload prepend: got %q, want %q", got, want)
	}

	// No UserContext → bare user content.
	cwBare := ctxpkg.NewContextWindow(200_000, ctxpkg.DefaultEstimator{})
	if g := composeUserPayload(&SlotAssemblyResult{Window: cwBare}, "ping"); g != "ping" {
		t.Fatalf("composeUserPayload empty UserContext: got %q, want %q", g, "ping")
	}

	// Nil result → bare user content (defensive).
	if g := composeUserPayload(nil, "ping"); g != "ping" {
		t.Fatalf("composeUserPayload nil: got %q, want %q", g, "ping")
	}
}

// TestSlotsChangedFor_FirstCallStampsThenChangeDetect verifies the
// per-session hash stamp behavior: first call records the hash and reports
// no change; subsequent calls compare against the stamp.
func TestSlotsChangedFor_FirstCallStampsThenChangeDetect(t *testing.T) {
	s := &chatServiceImpl{activeSessionSlots: sync.Map{}}

	cw := ctxpkg.NewContextWindow(200_000, ctxpkg.DefaultEstimator{})
	cw.SetContent(ctxpkg.SlotSystem, "v1")
	cw.SetContent(ctxpkg.SlotAgent, "agent-1")

	slots := &SlotAssemblyResult{Window: cw}

	if changed := s.slotsChangedFor("sess-1", slots); changed {
		t.Fatal("first call must stamp without reporting change")
	}

	if changed := s.slotsChangedFor("sess-1", slots); changed {
		t.Fatal("identical second call must not report change")
	}

	cw.SetContent(ctxpkg.SlotMode, "code") // shifts hash
	if changed := s.slotsChangedFor("sess-1", slots); !changed {
		t.Fatal("hash drift must report change")
	}

	// Stamp updates after a true result; another identical call returns false.
	if changed := s.slotsChangedFor("sess-1", slots); changed {
		t.Fatal("post-change call should re-baseline to false")
	}
}

// TestSlotsChangedFor_UserContextIgnored confirms UserContext changes do
// NOT trigger boot-dir regeneration. UserContext flows via SendInput per
// turn; only System / Agent / Mode / Rules feed the boot dir.
func TestSlotsChangedFor_UserContextIgnored(t *testing.T) {
	s := &chatServiceImpl{activeSessionSlots: sync.Map{}}

	cw := ctxpkg.NewContextWindow(200_000, ctxpkg.DefaultEstimator{})
	cw.SetContent(ctxpkg.SlotSystem, "v1")
	slots := &SlotAssemblyResult{Window: cw}

	_ = s.slotsChangedFor("sess-2", slots)

	cw.SetContent(ctxpkg.SlotUserContext, "reminder: test")
	if changed := s.slotsChangedFor("sess-2", slots); changed {
		t.Fatal("UserContext drift must not trigger boot-dir regen")
	}
}

// TestRegenerateBootDirSlots_WritesAtomically verifies CLAUDE.md and
// .sandbox/agent-context.md land in the boot dir with non-empty content.
func TestRegenerateBootDirSlots_WritesAtomically(t *testing.T) {
	s := &chatServiceImpl{}
	bootDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(bootDir, ".sandbox"), 0o755); err != nil {
		t.Fatal(err)
	}

	agent := &store.AgentProfile{Name: "test-agent", Description: "for tests"}
	mode := &store.AgentMode{Name: "code", PromptAddendum: "be precise"}

	if err := s.regenerateBootDirSlots(bootDir, agent, mode); err != nil {
		t.Fatalf("regenerateBootDirSlots: %v", err)
	}

	claude, err := os.ReadFile(filepath.Join(bootDir, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("read CLAUDE.md: %v", err)
	}
	if len(claude) == 0 {
		t.Fatal("CLAUDE.md is empty")
	}

	ctxFile, err := os.ReadFile(filepath.Join(bootDir, ".sandbox", "agent-context.md"))
	if err != nil {
		t.Fatalf("read agent-context.md: %v", err)
	}
	if len(ctxFile) == 0 {
		t.Fatal("agent-context.md is empty")
	}
}

// TestRegenerateBootDirSlots_RejectsEmptyBootDir confirms the helper guards
// the bootDir argument so a misconfigured caller doesn't silently write to
// the working directory.
func TestRegenerateBootDirSlots_RejectsEmptyBootDir(t *testing.T) {
	s := &chatServiceImpl{}
	if err := s.regenerateBootDirSlots("", &store.AgentProfile{Name: "x"}, nil); err == nil {
		t.Fatal("expected error for empty bootDir")
	}
}

// TestRegenerateBootDirSlots_RejectsNilAgent confirms the helper rejects a
// nil agent profile (BuildAgentContext / BuildCLAUDEMD would panic on a
// nil pointer otherwise).
func TestRegenerateBootDirSlots_RejectsNilAgent(t *testing.T) {
	s := &chatServiceImpl{}
	bootDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(bootDir, ".sandbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.regenerateBootDirSlots(bootDir, nil, nil); err == nil {
		t.Fatal("expected error for nil agent")
	}
}

// TestBootSessionRole_ModeBeatsAgent verifies the role derivation precedence:
// mode slug wins, then agent slug, then empty.
func TestBootSessionRole_ModeBeatsAgent(t *testing.T) {
	cases := []struct {
		name string
		ap   *store.AgentProfile
		mode *store.AgentMode
		want string
	}{
		{"mode wins", &store.AgentProfile{Slug: "agent-x"}, &store.AgentMode{Slug: "mode-y"}, "mode-y"},
		{"agent fallback", &store.AgentProfile{Slug: "agent-x"}, nil, "agent-x"},
		{"empty mode skipped", &store.AgentProfile{Slug: "agent-x"}, &store.AgentMode{Slug: ""}, "agent-x"},
		{"both empty", &store.AgentProfile{}, &store.AgentMode{}, ""},
		{"both nil", nil, nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bootSessionRole(tc.ap, tc.mode); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDriveBootSession_RejectsMissingDeps confirms the no-runtime path
// surfaces a clear error rather than panicking on nil dereference.
func TestDriveBootSession_RejectsMissingDeps(t *testing.T) {
	s := &chatServiceImpl{}
	_, err := s.driveBootSession(context.Background(), "sess", &store.Session{}, &store.AgentProfile{Slug: "x"}, nil, nil, "hello", 0)
	if err == nil {
		t.Fatal("expected error when agent runtime not wired")
	}
}

// TestDriveBootSession_IterationGreaterThanZero returns a closed chan so the
// outer harness exits cleanly. Verifies the defensive guard against
// unexpected loop iteration on CLI sessions.
func TestDriveBootSession_IterationGreaterThanZero(t *testing.T) {
	s := &chatServiceImpl{
		// Non-nil placeholders so the iteration > 0 branch fires before
		// any field is accessed (no Boot, no SendInput, no router bind).
		agentDeps:        &runtimeagent.Dependencies{},
		agentEventBridge: &agentEventBridge{streams: NewStreamManager()},
	}
	ch, err := s.driveBootSession(context.Background(), "sess", &store.Session{}, &store.AgentProfile{Slug: "x"}, nil, nil, "ignored", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := <-ch; ok {
		t.Fatal("expected immediately-closed chan for iteration > 0")
	}
}
