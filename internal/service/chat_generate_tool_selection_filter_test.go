package service

// TASKS/phase-4/06-add-filter-tool-selection.md — Done-means acceptance
// test: a plugin registered on the new FilterToolSelection filter point can
// genuinely add, remove, and reshape the tool list offered to the model for
// a turn.
//
// Exercised against a REAL SelectForAgent call (real toolServiceImpl, real
// mcp.Manager + DevToolsTransport discovery, real AgentReader resolution),
// not a hand-built/synthetic tool slice, and through applyToolSelectionFilter
// — the exact function chat_generate.go's generateResponse calls at the
// production insertion point — rather than calling pluginHost.ApplyFilter
// directly. This demonstrates "an actual filtered turn," per the task's own
// Done-means bar, not just that the hook fires.

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/mcp"
	pluginpkg "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/store"
)

// toolSelectionFilterAgentReader satisfies AgentReader for
// TestFilterToolSelection_PluginAddsRemovesReshapesOfferedTools. Only
// GetAgent is exercised by SelectForAgent's dbAgent-resolution and
// discoverAgentMCPTools fallback paths (internal/service/tool.go) — every
// other embedded *store.Store method stays nil and would panic if called.
// Same intentionally-narrow test-double pattern as e2eStore in
// chat_path_grants_e2e_test.go.
type toolSelectionFilterAgentReader struct {
	*store.Store
	agent *store.AgentProfile
}

func (f *toolSelectionFilterAgentReader) GetAgent(id string) (*store.AgentProfile, error) {
	if f.agent != nil && id == f.agent.ID {
		return f.agent, nil
	}
	return nil, fmt.Errorf("agent %q not found", id)
}

func TestFilterToolSelection_PluginAddsRemovesReshapesOfferedTools(t *testing.T) {
	ctx := context.Background()

	// Real mcp.Manager + DevToolsTransport (6 real tools: dev_read,
	// dev_grep, dev_write, dev_glob, dev_edit, dev_bash) — the same
	// already-proven construction as chat_path_grants_e2e_test.go, kept
	// well under ProgressiveDiscoveryThreshold (10) so this exercises the
	// plain (non-progressive) selection path.
	mgr := mcp.NewManager()
	if err := mgr.AddServer("dev", mcp.NewDevToolsTransport(nil), mcp.TierBuiltin); err != nil {
		t.Fatalf("AddServer: %v", err)
	}
	if err := mgr.DiscoverTools(ctx); err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}

	agent := &store.AgentProfile{
		ID:   "agent-tsf-1",
		Slug: "tsf-test-agent", // deliberately not "default" — bypasses the chat-role surface filter so it doesn't interfere with this test.
		// dbAgent != nil once GetAgent resolves this row, so SelectForAgent
		// tries filterToolsByAgentTools — but s.toolClient is nil here
		// (see NewToolService call below), so agentToolsStore() returns
		// nil and that filter is a documented no-op pass-through.
		MCPServers: `["dev"]`,
	}
	agents := &toolSelectionFilterAgentReader{agent: agent}

	// toolClient == nil throughout: SelectForAgent falls back to direct MCP
	// discovery (discoverAgentMCPTools) via s.mcpManager + s.agents, which
	// is exactly what's under test here — the filter runs on whatever
	// SelectForAgent actually produces, not a hand-built list.
	tools := NewToolService(nil, mgr, agents)

	selection, err := tools.SelectForAgent(ctx, "sess-tsf", agent.ID, "list some files", "", 0)
	if err != nil {
		t.Fatalf("SelectForAgent: %v", err)
	}
	if len(selection.Tools) == 0 {
		t.Fatal("setup: SelectForAgent returned zero tools — test cannot demonstrate a filtered turn")
	}

	// Confirm the pre-filter baseline actually contains what this test
	// exercises removing/reshaping, so a later assertion failure can't be
	// confused with "the tool was never there to begin with."
	if !containsToolNamed(selection.Tools, "dev_bash") {
		t.Fatalf("setup: expected dev_bash in pre-filter selection, got %v", toolSelectionNames(selection.Tools))
	}
	if !containsToolNamed(selection.Tools, "dev_read") {
		t.Fatalf("setup: expected dev_read in pre-filter selection, got %v", toolSelectionNames(selection.Tools))
	}
	preFilterCount := len(selection.Tools)

	// Real plugin.Host, real filter registration — not a stub PluginEventSink.
	host := pluginpkg.NewHost(http.NewServeMux(), pluginpkg.NewLogger("test"))
	const injectedToolName = "plugin_injected_tool"
	const reshapedDescription = "RESHAPED BY PLUGIN: reads a file, plugin-narrated"
	if err := host.RegisterFilter(pluginpkg.FilterToolSelection, 10, func(data interface{}, _ pluginpkg.FilterContext) (interface{}, error) {
		in, ok := data.([]llmtypes.ToolDefinition)
		if !ok {
			return data, fmt.Errorf("unexpected data type %T", data)
		}
		out := make([]llmtypes.ToolDefinition, 0, len(in)+1)
		for _, td := range in {
			switch td.Name {
			case "dev_bash":
				// Remove: a destructive shell tool this plugin's policy
				// never wants offered.
				continue
			case "dev_read":
				// Reshape: rewrite the description in place.
				td.Description = reshapedDescription
				out = append(out, td)
			default:
				out = append(out, td)
			}
		}
		// Add: a brand-new tool this plugin contributes.
		out = append(out, llmtypes.ToolDefinition{
			Name:        injectedToolName,
			Description: "Injected by the test plugin.",
			InputSchema: map[string]any{"type": "object"},
		})
		return out, nil
	}); err != nil {
		t.Fatalf("RegisterFilter: %v", err)
	}

	fctx := pluginpkg.FilterContext{SessionID: "sess-tsf", AgentID: agent.ID}
	got := applyToolSelectionFilter(host, selection.Tools, fctx)

	// Add.
	if !containsToolNamed(got, injectedToolName) {
		t.Errorf("expected plugin-injected tool %q in filtered list, got %v", injectedToolName, toolSelectionNames(got))
	}
	// Remove.
	if containsToolNamed(got, "dev_bash") {
		t.Errorf("expected dev_bash removed by plugin filter, still present: %v", toolSelectionNames(got))
	}
	// Reshape.
	var sawReshaped bool
	for _, td := range got {
		if td.Name == "dev_read" {
			sawReshaped = true
			if td.Description != reshapedDescription {
				t.Errorf("expected dev_read description reshaped to %q, got %q", reshapedDescription, td.Description)
			}
		}
	}
	if !sawReshaped {
		t.Fatal("expected dev_read to survive (reshaped, not removed) — was not found in filtered list")
	}

	// Net count sanity: -1 (dev_bash) +1 (injected) relative to the
	// pre-filter baseline confirms the filter actually ran end-to-end
	// against the real SelectForAgent output, not a coincidental
	// passthrough.
	if len(got) != preFilterCount {
		t.Errorf("expected net-unchanged tool count (one removed, one added), got %d want %d (pre-filter=%v, post-filter=%v)",
			len(got), preFilterCount, toolSelectionNames(selection.Tools), toolSelectionNames(got))
	}
}

// TestFilterToolSelection_NoPluginHostIsPassthrough is the nil-safety
// companion: chat_generate.go calls applyToolSelectionFilter unconditionally
// (no s.pluginHost != nil guard at the call site any more — the guard moved
// inside the helper), so a nil pluginHost (no plugins loaded) must be a
// pure passthrough, not a panic or a silently emptied tool list.
func TestFilterToolSelection_NoPluginHostIsPassthrough(t *testing.T) {
	in := []llmtypes.ToolDefinition{{Name: "dev_read"}, {Name: "dev_bash"}}
	got := applyToolSelectionFilter(nil, in, pluginpkg.FilterContext{})
	if len(got) != len(in) {
		t.Fatalf("expected passthrough of %d tools, got %d", len(in), len(got))
	}
	for i := range in {
		if got[i].Name != in[i].Name {
			t.Errorf("expected passthrough tool[%d]=%q, got %q", i, in[i].Name, got[i].Name)
		}
	}
}

// TestFilterToolSelection_EmptyChainIsPassthrough covers a real pluginHost
// with zero handlers registered for FilterToolSelection (e.g. plugins are
// loaded but none of them registered this particular filter) — the registry
// itself already guarantees this (FilterRegistry.Apply's documented
// passthrough), but this pins the guarantee at the actual call site this
// task wired in, not just at the registry layer internal/plugin/filter_test.go
// already covers.
func TestFilterToolSelection_EmptyChainIsPassthrough(t *testing.T) {
	host := pluginpkg.NewHost(http.NewServeMux(), pluginpkg.NewLogger("test"))
	in := []llmtypes.ToolDefinition{{Name: "dev_read"}, {Name: "dev_bash"}}
	got := applyToolSelectionFilter(host, in, pluginpkg.FilterContext{SessionID: "s1"})
	if len(got) != len(in) {
		t.Fatalf("expected passthrough of %d tools, got %d", len(in), len(got))
	}
}

func toolSelectionNames(tools []llmtypes.ToolDefinition) []string {
	names := make([]string, len(tools))
	for i, td := range tools {
		names[i] = td.Name
	}
	return names
}
