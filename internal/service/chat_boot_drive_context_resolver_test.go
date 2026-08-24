package service

// Phase 2 item 02 (TASKS/phase-2/02-port-forward-dynamic-resolver.md):
// end-to-end verification that a DB-configured cmd-kind resolver's
// output is verifiably folded into an agent's assembled launch content
// — the same code path driveBootSession's cold-boot branch exercises
// (resolveAgentContextForBoot → Options.DynamicContext →
// resolveBootPrompt/ResolveSystemPrompt → BuildCLAUDEMD), against a
// real (temp-dir-isolated) *store.Store, not a mock.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

func newContextResolverTestStore(t *testing.T) *store.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "context-resolver.db")
	st, err := storetest.New(t, context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close(context.Background()) })
	return st
}

// TestResolveAgentContextForBoot_RealCmdResolverAgainstRealStore proves
// Done-means bullet 1 at the resolution layer: a real DB-backed cmd
// resolver row for a real agent resolves through the shared agentkit
// execution primitives when driveBootSession's helper is invoked.
func TestResolveAgentContextForBoot_RealCmdResolverAgainstRealStore(t *testing.T) {
	st := newContextResolverTestStore(t)
	ctx := context.Background()

	agent := &store.AgentProfile{Name: "Weather Agent", Slug: "weather-agent", SystemPrompt: "You help with weather."}
	if err := st.CreateAgent(context.Background(), agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if _, err := st.InsertAgentContextResolver(ctx, store.AgentContextResolver{
		AgentID:  agent.ID,
		SlotName: "weather",
		Kind:     "cmd",
		Run:      "printf 72F-and-sunny",
		Timeout:  "5s",
		Enabled:  true,
	}); err != nil {
		t.Fatalf("InsertAgentContextResolver: %v", err)
	}
	// A disabled resolver must NOT appear in the resolved output.
	if _, err := st.InsertAgentContextResolver(ctx, store.AgentContextResolver{
		AgentID:  agent.ID,
		SlotName: "disabled-slot",
		Kind:     "cmd",
		Run:      "printf should-not-appear",
		Enabled:  false,
	}); err != nil {
		t.Fatalf("InsertAgentContextResolver (disabled): %v", err)
	}

	s := &chatServiceImpl{store: st}
	blocks, err := s.resolveAgentContextForBoot(ctx, agent.ID, "")
	if err != nil {
		t.Fatalf("resolveAgentContextForBoot: %v", err)
	}
	if got := blocks["weather"]; got != "72F-and-sunny" {
		t.Errorf(`blocks["weather"] = %q, want "72F-and-sunny"`, got)
	}
	if _, ok := blocks["disabled-slot"]; ok {
		t.Errorf("resolveAgentContextForBoot returned a block for a disabled resolver: %v", blocks)
	}

	// No resolvers configured for a different agent -> nil, no error.
	otherAgent := &store.AgentProfile{Name: "Plain Agent", Slug: "plain-agent", SystemPrompt: "x"}
	if err := st.CreateAgent(context.Background(), otherAgent); err != nil {
		t.Fatalf("CreateAgent (other): %v", err)
	}
	none, err := s.resolveAgentContextForBoot(ctx, otherAgent.ID, "")
	if err != nil {
		t.Fatalf("resolveAgentContextForBoot (no resolvers configured): %v", err)
	}
	if len(none) != 0 {
		t.Errorf("expected no blocks for an agent with no configured resolvers, got %v", none)
	}
}

// TestResolveAgentContextForBoot_ResolverFailureAbortsBoot proves the
// preserved safety property: a broken resolver surfaces a pointed error
// naming the slot rather than silently degrading — mirroring the
// pre-port bootprofile.ResolveRequirements behavior.
func TestResolveAgentContextForBoot_ResolverFailureAbortsBoot(t *testing.T) {
	st := newContextResolverTestStore(t)
	ctx := context.Background()

	agent := &store.AgentProfile{Name: "Broken Agent", Slug: "broken-agent", SystemPrompt: "x"}
	if err := st.CreateAgent(context.Background(), agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if _, err := st.InsertAgentContextResolver(ctx, store.AgentContextResolver{
		AgentID: agent.ID, SlotName: "broken", Kind: "cmd", Run: "exit 7", Enabled: true,
	}); err != nil {
		t.Fatalf("InsertAgentContextResolver: %v", err)
	}

	s := &chatServiceImpl{store: st}
	_, err := s.resolveAgentContextForBoot(ctx, agent.ID, "")
	if err == nil {
		t.Fatal("expected resolveAgentContextForBoot to surface the resolver failure, got nil")
	}
	if !strings.Contains(err.Error(), "broken") {
		t.Errorf("error %q does not name the offending slot", err.Error())
	}
}

// TestDynamicContext_FoldsIntoAssembledBootContent is the full
// resolution-to-boot-content proof Done-means bullet 1 asks for: a real
// cmd resolver's output flows through resolveAgentContextForBoot, the
// activeSessionContextBlocks stash driveBootSession populates on cold
// boot, and regenerateBootDirSlots — the SAME function that (re)plants
// CLAUDE.md for a live CLI session — and lands, verbatim, in the actual
// file bytes a booted agent process would read.
func TestDynamicContext_FoldsIntoAssembledBootContent(t *testing.T) {
	st := newContextResolverTestStore(t)
	ctx := context.Background()

	agent := &store.AgentProfile{Name: "Weather Agent", Slug: "weather-agent-2", SystemPrompt: "You help with weather."}
	if err := st.CreateAgent(context.Background(), agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if _, err := st.InsertAgentContextResolver(ctx, store.AgentContextResolver{
		AgentID: agent.ID, SlotName: "weather", Kind: "cmd", Run: "printf 72F-and-sunny", Enabled: true,
	}); err != nil {
		t.Fatalf("InsertAgentContextResolver: %v", err)
	}

	s := &chatServiceImpl{store: st}
	sessionID := "sess-dynamic-context"

	blocks, err := s.resolveAgentContextForBoot(ctx, agent.ID, "")
	if err != nil {
		t.Fatalf("resolveAgentContextForBoot: %v", err)
	}
	if len(blocks) == 0 {
		t.Fatal("expected at least one resolved block")
	}
	// This Store call is exactly what driveBootSession's cold-boot branch
	// does with bootOpts.DynamicContext before calling runtimeagent.Boot.
	s.activeSessionContextBlocks.Store(sessionID, blocks)

	bootDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(bootDir, ".sandbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.regenerateBootDirSlots(sessionID, bootDir, agent); err != nil {
		t.Fatalf("regenerateBootDirSlots: %v", err)
	}

	claudeMD, err := os.ReadFile(filepath.Join(bootDir, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("read CLAUDE.md: %v", err)
	}
	content := string(claudeMD)
	if !strings.Contains(content, "You help with weather.") {
		t.Errorf("CLAUDE.md missing the agent's own system prompt: %s", content)
	}
	if !strings.Contains(content, "## Dynamic context: weather") || !strings.Contains(content, "72F-and-sunny") {
		t.Errorf("CLAUDE.md does not include the resolved dynamic-context block: %s", content)
	}
}
