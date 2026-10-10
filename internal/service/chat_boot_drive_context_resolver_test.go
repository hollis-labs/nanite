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
	"path/filepath"
	"strings"
	"testing"

	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
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

// Fresh boot prompt assembly preserves resolved dynamic context; bound-root
// updates are explicitly unavailable and use a separate refusal test.
func TestDynamicContext_FoldsIntoAssembledBootContent(t *testing.T) {
	st := newContextResolverTestStore(t)
	ctx := context.Background()
	agent := &store.AgentProfile{Name: "Weather Agent", Slug: "weather-agent-2", SystemPrompt: "You help with weather."}
	if err := st.CreateAgent(ctx, agent); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertAgentContextResolver(ctx, store.AgentContextResolver{AgentID: agent.ID, SlotName: "weather", Kind: "cmd", Run: "printf 72F-and-sunny", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	s := &chatServiceImpl{store: st}
	blocks, err := s.resolveAgentContextForBoot(ctx, agent.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	prompt := runtimeagent.ResolveSystemPrompt(agent.Slug, agent, runtimeagent.ModeLongLived, "", blocks, "")
	content := runtimeagent.BuildCLAUDEMD(agent.Name, agent.Description, prompt)
	if !strings.Contains(content, "You help with weather.") || !strings.Contains(content, "## Dynamic context: weather") || !strings.Contains(content, "72F-and-sunny") {
		t.Fatalf("fresh boot content lost resolved context: %s", content)
	}
}
