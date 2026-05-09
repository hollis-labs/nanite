package service

import (
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/toolclient"
)

// TestApplyModeToolOverridesToTools_DenyPath covers the deny-list path on
// the progressive seed builtins. F1 (CW-20260429-0001).
func TestApplyModeToolOverridesToTools_DenyPath(t *testing.T) {
	tools := []llmtypes.ToolDefinition{
		{Name: "dev_read"},
		{Name: "dev_bash"},
		{Name: "context_search"},
		{Name: "request_tools"}, // meta-tool — must survive deny list
	}
	spec := store.ToolOverrideSpec{
		Deny: []string{"dev_bash"},
	}

	got := applyModeToolOverridesToTools(tools, spec)

	wantNames := map[string]bool{
		"dev_read":       true,
		"context_search": true,
		"request_tools":  true,
	}
	if len(got) != len(wantNames) {
		t.Fatalf("got %d tools, want %d (%v)", len(got), len(wantNames), got)
	}
	for _, tool := range got {
		if !wantNames[tool.Name] {
			t.Errorf("unexpected tool surfaced after deny: %q", tool.Name)
		}
	}
}

// TestApplyModeToolOverridesToTools_DenyMetaToolStaysReachable verifies that
// even an explicit deny on a meta-tool name does not remove it. The agent's
// escape hatches (request_tools, fetch_tool_result, search_tool_result) must
// always be reachable; mode policy operates one rung up on the regular tool
// catalog. F1 (CW-20260429-0001).
func TestApplyModeToolOverridesToTools_DenyMetaToolStaysReachable(t *testing.T) {
	tools := []llmtypes.ToolDefinition{
		{Name: "request_tools"},
		{Name: "dev_read"},
	}
	spec := store.ToolOverrideSpec{
		Deny: []string{"request_tools", "dev_read"},
	}

	got := applyModeToolOverridesToTools(tools, spec)

	// dev_read should be denied; request_tools should NOT — meta-tool exempt.
	if len(got) != 1 || got[0].Name != "request_tools" {
		t.Errorf("expected only request_tools to survive (meta-tool exempt); got %v", got)
	}
}

// TestApplyModeToolOverridesToTools_DenyPattern covers glob-pattern denial,
// the second of the two paths called out in the F1 ticket. F1
// (CW-20260429-0001).
func TestApplyModeToolOverridesToTools_DenyPattern(t *testing.T) {
	tools := []llmtypes.ToolDefinition{
		{Name: "dev_read"},
		{Name: "dev_bash"},
		{Name: "dev_write"},
		{Name: "context_search"},
	}
	spec := store.ToolOverrideSpec{
		DenyPatterns: []string{"dev_*"},
	}

	got := applyModeToolOverridesToTools(tools, spec)

	if len(got) != 1 || got[0].Name != "context_search" {
		t.Errorf("expected only context_search to survive dev_* deny pattern; got %v", got)
	}
}

// TestApplyModeToolOverridesToTools_AllowOverridesDenyPattern verifies B1's
// resolution precedence is preserved end-to-end through the service-layer
// adapter: explicit Allow beats DenyPattern. F1 (CW-20260429-0001).
func TestApplyModeToolOverridesToTools_AllowOverridesDenyPattern(t *testing.T) {
	tools := []llmtypes.ToolDefinition{
		{Name: "dev_read"},
		{Name: "dev_bash"},
		{Name: "dev_write"},
	}
	spec := store.ToolOverrideSpec{
		Allow:        []string{"dev_read"}, // wins over deny pattern
		DenyPatterns: []string{"dev_*"},
	}

	got := applyModeToolOverridesToTools(tools, spec)

	if len(got) != 1 || got[0].Name != "dev_read" {
		t.Errorf("explicit Allow should override DenyPattern; got %v", got)
	}
}

// TestApplyModeToolOverridesToTools_EmptySpecPassthrough is the no-op path.
func TestApplyModeToolOverridesToTools_EmptySpecPassthrough(t *testing.T) {
	tools := []llmtypes.ToolDefinition{
		{Name: "dev_read"},
		{Name: "context_search"},
	}
	got := applyModeToolOverridesToTools(tools, store.ToolOverrideSpec{})
	if len(got) != len(tools) {
		t.Errorf("empty spec should be passthrough; got %d tools, want %d", len(got), len(tools))
	}
}

// TestApplyModeToolOverridesToSummaries_DenyPath validates that the catalog
// scrub used in progressive discovery removes denied tools from the LLM-
// visible summary list. F1 (CW-20260429-0001).
func TestApplyModeToolOverridesToSummaries_DenyPath(t *testing.T) {
	summaries := []toolclient.ToolSummary{
		{Name: "dev_read", Description: "read a file"},
		{Name: "dev_bash", Description: "run a shell command"},
		{Name: "context_search", Description: "search the context store"},
		{Name: "request_tools", Description: "load more tools"}, // meta — exempt
	}
	spec := store.ToolOverrideSpec{
		Deny: []string{"dev_bash"},
	}

	got := applyModeToolOverridesToSummaries(summaries, spec)

	want := map[string]bool{"dev_read": true, "context_search": true, "request_tools": true}
	if len(got) != len(want) {
		t.Fatalf("got %d summaries, want %d (%v)", len(got), len(want), got)
	}
	for _, s := range got {
		if !want[s.Name] {
			t.Errorf("unexpected summary surfaced after deny: %q", s.Name)
		}
	}
}

// TestApplyModeToolOverridesToSummaries_DenyPattern covers the glob-pattern
// path on the progressive-discovery catalog summaries.
func TestApplyModeToolOverridesToSummaries_DenyPattern(t *testing.T) {
	summaries := []toolclient.ToolSummary{
		{Name: "dev_read"},
		{Name: "dev_bash"},
		{Name: "context_search"},
	}
	spec := store.ToolOverrideSpec{
		DenyPatterns: []string{"dev_*"},
	}

	got := applyModeToolOverridesToSummaries(summaries, spec)
	if len(got) != 1 || got[0].Name != "context_search" {
		t.Errorf("expected only context_search to survive dev_* deny pattern; got %v", got)
	}
}
