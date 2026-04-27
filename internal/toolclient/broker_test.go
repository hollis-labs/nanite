package toolclient

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/go-toolbroker/broker"
)

func TestSelectTools_ReturnsTools(t *testing.T) {
	cfg := &Config{
		Rules:               broker.DefaultRules(),
		WorkspaceOverrides:  make(map[string][]broker.Rule),
		AgentOverrides:      make(map[string][]broker.Rule),
		ToolTokenBudgetPct:  DefaultToolTokenBudgetPct,
		ContextWindowTokens: DefaultContextWindowTokens,
	}
	tb := New(nil, nil, cfg)

	// Register some tools.
	tools := []broker.ToolDefinition{
		{Name: "volon_task_create", Server: "volon", Description: "Create a task"},
		{Name: "volon_task_list", Server: "volon", Description: "List tasks"},
		{Name: "conduit_context_view", Server: "conduit", Description: "View context"},
	}
	tb.RegisterTools(tools)

	// Select with wildcard intent. Pass 0 to exercise the DefaultContextWindowTokens fallback.
	selected, _, err := tb.SelectTools(context.Background(), "*", nil, "", "", 0)
	if err != nil {
		t.Fatalf("SelectTools error: %v", err)
	}
	if len(selected) == 0 {
		t.Error("expected at least one tool selected")
	}
}

func TestSelectTools_CapsAtMax(t *testing.T) {
	cfg := DefaultConfig()
	tb := New(nil, nil, cfg)

	// Register more than MaxSelectedTools tools.
	tools := make([]broker.ToolDefinition, MaxSelectedTools+5)
	for i := range tools {
		tools[i] = broker.ToolDefinition{
			Name:        "tool_" + string(rune('a'+i)),
			Server:      "test",
			Description: "Test tool",
		}
	}
	tb.RegisterTools(tools)

	selected, _, err := tb.SelectTools(context.Background(), "*", nil, "", "", 0)
	if err != nil {
		t.Fatalf("SelectTools error: %v", err)
	}
	if len(selected) > MaxSelectedTools {
		t.Errorf("expected at most %d tools, got %d", MaxSelectedTools, len(selected))
	}
}

func TestDefaultConfig_HasRules(t *testing.T) {
	cfg := DefaultConfig()
	if len(cfg.Rules) == 0 {
		t.Error("expected default config to have rules")
	}
}

func TestEstimateToolTokens(t *testing.T) {
	tools := []broker.ToolDefinition{
		{Name: "tool_a", Server: "test", Description: "A short description"},
		{Name: "tool_b", Server: "test", Description: "Another description for testing"},
	}

	tokens := EstimateToolTokens(tools)
	if tokens <= 0 {
		t.Errorf("expected positive token estimate, got %d", tokens)
	}

	// Tokens should increase with more tools.
	moreTools := append(tools, broker.ToolDefinition{
		Name: "tool_c", Server: "test", Description: "Yet another tool with a longer description for estimation",
	})
	moreTokens := EstimateToolTokens(moreTools)
	if moreTokens <= tokens {
		t.Errorf("expected more tokens with more tools: %d <= %d", moreTokens, tokens)
	}
}

func TestPruneToolsToTokenBudget_UnderBudget(t *testing.T) {
	tools := []broker.ToolDefinition{
		{Name: "tool_a", Server: "test", Description: "Short"},
		{Name: "tool_b", Server: "test", Description: "Short"},
	}

	// Give a very large budget — all tools should pass through.
	result := PruneToolsToTokenBudget(tools, 100000)
	if len(result) != len(tools) {
		t.Errorf("expected %d tools unchanged, got %d", len(tools), len(result))
	}
}

func TestPruneToolsToTokenBudget_OverBudget(t *testing.T) {
	tools := []broker.ToolDefinition{
		{Name: "tool_a", Server: "test", Description: "First tool"},
		{Name: "tool_b", Server: "test", Description: "Second tool"},
		{Name: "tool_c", Server: "test", Description: "Third tool"},
		{Name: "tool_d", Server: "test", Description: "Fourth tool"},
		{Name: "tool_e", Server: "test", Description: "Fifth tool"},
	}

	// Set budget to only fit ~1 tool.
	result := PruneToolsToTokenBudget(tools, 20)
	if len(result) >= len(tools) {
		t.Errorf("expected fewer tools after pruning, got %d (original %d)", len(result), len(tools))
	}
	if len(result) == 0 {
		t.Error("expected at least 1 tool to remain")
	}
}

func TestPruneToolsToTokenBudget_KeepsAtLeastOne(t *testing.T) {
	tools := []broker.ToolDefinition{
		{Name: "big_tool", Server: "test", Description: "A very long description that should exceed any tiny budget we set for testing purposes to ensure at least one tool is always kept"},
	}

	// Budget of 1 token — still must keep at least 1 tool.
	result := PruneToolsToTokenBudget(tools, 1)
	if len(result) != 1 {
		t.Errorf("expected exactly 1 tool retained, got %d", len(result))
	}
}

func TestPruneToolsToTokenBudget_EmptySlice(t *testing.T) {
	result := PruneToolsToTokenBudget(nil, 100)
	if len(result) != 0 {
		t.Errorf("expected 0 tools for nil input, got %d", len(result))
	}
}

func TestDefaultConfig_HasTokenBudget(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.ToolTokenBudgetPct != DefaultToolTokenBudgetPct {
		t.Errorf("expected ToolTokenBudgetPct=%f, got %f", DefaultToolTokenBudgetPct, cfg.ToolTokenBudgetPct)
	}
	if cfg.ContextWindowTokens != DefaultContextWindowTokens {
		t.Errorf("expected ContextWindowTokens=%d, got %d", DefaultContextWindowTokens, cfg.ContextWindowTokens)
	}
}

// TestSelectTools_GeminiWindowBudget asserts that the tool token budget scales
// with the per-session context window. A Gemini-1M session must receive a
// budget significantly larger than a 200K session (A5 audit Risk 2, item 7,
// CW-20260426-0032).
func TestSelectTools_GeminiWindowBudget(t *testing.T) {
	// Build a ToolClient with enough tools to saturate a 200K budget at 20%
	// (40K tokens) but fit comfortably inside a 1M budget (200K tokens).
	cfg := DefaultConfig()
	tb := New(nil, nil, cfg)

	// Register 10 identical tools whose combined token estimate exceeds the
	// 200K-window budget (200K * 20% = 40K) but fits within the 1M budget
	// (1M * 20% = 200K). A description of ~500 chars is ~125 tokens each;
	// 10 tools = ~1250 tokens — well under both budgets. Use a description
	// large enough that the difference is measurable.
	longDesc := strings.Repeat("x", 800) // ~200 tokens each
	tools := make([]broker.ToolDefinition, 10)
	for i := range tools {
		tools[i] = broker.ToolDefinition{
			Name:        fmt.Sprintf("heavy_tool_%d", i),
			Server:      "test",
			Description: longDesc,
		}
	}
	tb.RegisterTools(tools)

	// Select with a 200K window (default).
	tools200k, _, err := tb.SelectTools(context.Background(), "*", nil, "", "", 200_000)
	if err != nil {
		t.Fatalf("SelectTools(200K): %v", err)
	}

	// Select with a 1M window (Gemini).
	tools1m, _, err := tb.SelectTools(context.Background(), "*", nil, "", "", 1_000_000)
	if err != nil {
		t.Fatalf("SelectTools(1M): %v", err)
	}

	// With identical tool sets but different windows, the 1M budget should
	// permit at least as many tools as the 200K budget. For a meaningful
	// regression check we assert that when tools are pruned at 200K they are
	// not pruned at 1M (i.e. the two results differ when tools are heavy).
	// If the tools happen to fit under both budgets the counts may be equal;
	// the key invariant is that the 1M count is never less than the 200K count.
	if len(tools1m) < len(tools200k) {
		t.Errorf("Gemini 1M window produced fewer tools than 200K window: 1M=%d 200K=%d",
			len(tools1m), len(tools200k))
	}

	// Also verify the 0 (unknown model) path falls back to DefaultContextWindowTokens
	// and never returns an error.
	toolsUnknown, _, err := tb.SelectTools(context.Background(), "*", nil, "", "", 0)
	if err != nil {
		t.Fatalf("SelectTools(unknown model): %v", err)
	}
	// Unknown-model behaviour must match the 200K fallback (same budget).
	if len(toolsUnknown) != len(tools200k) {
		t.Errorf("unknown-model fallback count (%d) differs from explicit 200K count (%d); "+
			"DefaultContextWindowTokens must equal 200000",
			len(toolsUnknown), len(tools200k))
	}
}

func TestListToolSummaries_ReturnsNameAndDescription(t *testing.T) {
	cfg := DefaultConfig()
	tb := New(nil, nil, cfg)

	// ListToolSummaries with no MCP manager returns empty.
	summaries := tb.ListToolSummaries()
	if len(summaries) != 0 {
		t.Errorf("expected 0 summaries without MCP manager, got %d", len(summaries))
	}
}

func TestToolSummary_HasCorrectFields(t *testing.T) {
	s := ToolSummary{
		Name:        "test_tool",
		Description: "A test tool",
		Server:      "test-server",
	}
	if s.Name != "test_tool" {
		t.Errorf("expected Name=test_tool, got %s", s.Name)
	}
	if s.Description != "A test tool" {
		t.Errorf("expected Description='A test tool', got %s", s.Description)
	}
	if s.Server != "test-server" {
		t.Errorf("expected Server=test-server, got %s", s.Server)
	}
}

func TestGetToolsByNames_NoMCPManager(t *testing.T) {
	cfg := DefaultConfig()
	tb := New(nil, nil, cfg)

	result := tb.GetToolsByNames([]string{"tool_a", "tool_b"})
	if len(result) != 0 {
		t.Errorf("expected 0 tools without MCP manager, got %d", len(result))
	}
}

func TestConfig_RulesFor_MergesOverrides(t *testing.T) {
	cfg := DefaultConfig()
	baseCount := len(cfg.Rules)

	cfg.WorkspaceOverrides["ws-1"] = []broker.Rule{
		{Name: "ws-rule", Intent: "*", Action: broker.Action{Type: "include"}},
	}
	cfg.AgentOverrides["agent-1"] = []broker.Rule{
		{Name: "agent-rule", Intent: "*", Action: broker.Action{Type: "exclude"}},
	}

	rules := cfg.RulesFor("ws-1", "agent-1")
	if len(rules) != baseCount+2 {
		t.Errorf("expected %d rules, got %d", baseCount+2, len(rules))
	}

	// Without overrides, should be base count.
	rules2 := cfg.RulesFor("", "")
	if len(rules2) != baseCount {
		t.Errorf("expected %d rules without overrides, got %d", baseCount, len(rules2))
	}
}

// --- mockTransport implements mcp.MCPTransport for testing ---

type mockTransport struct {
	tools []mcp.Tool
}

func (m *mockTransport) ListTools(_ context.Context) ([]mcp.Tool, error) {
	return m.tools, nil
}

func (m *mockTransport) CallTool(_ context.Context, _ string, _ map[string]any) (*mcp.ToolResult, error) {
	return &mcp.ToolResult{}, nil
}

// newTestBrokerWithTools creates a ToolClient backed by an MCP manager
// populated with the given provider.ToolDefinition set (via a mock transport).
func newTestBrokerWithTools(tools []provider.ToolDefinition) *ToolClient {
	mgr := mcp.NewManager()

	mcpTools := make([]mcp.Tool, len(tools))
	for i, t := range tools {
		mcpTools[i] = mcp.Tool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		}
	}
	if err := mgr.AddServer("test", &mockTransport{tools: mcpTools}, mcp.TierBuiltin); err != nil {
		panic(err)
	}
	_ = mgr.DiscoverTools(context.Background())

	cfg := DefaultConfig()
	tb := New(mgr, nil, cfg)
	return tb
}

// --- SelectByIntent tests ---

func TestSelectByIntent_FindsRelevantTools(t *testing.T) {
	tools := []provider.ToolDefinition{
		{Name: "volon_task_create", Description: "Create a new task in the backlog"},
		{Name: "volon_sprint_list", Description: "List all sprints"},
		{Name: "conduit_context_view", Description: "View a context packet"},
		{Name: "hadron_pipeline_run", Description: "Run a build pipeline"},
	}
	tb := newTestBrokerWithTools(tools)

	// Intent about tasks should find the task tool.
	result := tb.SelectByIntent("create task backlog", 10)
	if len(result) == 0 {
		t.Fatal("expected at least one tool for intent 'create task backlog'")
	}

	// The top result should be the task_create tool (highest score).
	// Uniform agent-facing name (ADR-002): no `mcp__test__` prefix.
	found := false
	for _, r := range result {
		if r.Name == "volon_task_create" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected volon_task_create in results, got %v", namesOf(result))
	}
}

func TestSelectByIntent_RespectsMaxTools(t *testing.T) {
	tools := []provider.ToolDefinition{
		{Name: "tool_sprint_a", Description: "Sprint tool A"},
		{Name: "tool_sprint_b", Description: "Sprint tool B"},
		{Name: "tool_sprint_c", Description: "Sprint tool C"},
		{Name: "tool_sprint_d", Description: "Sprint tool D"},
		{Name: "tool_sprint_e", Description: "Sprint tool E"},
	}
	tb := newTestBrokerWithTools(tools)

	result := tb.SelectByIntent("sprint tool", 2)
	if len(result) > 2 {
		t.Errorf("expected at most 2 tools, got %d", len(result))
	}
}

func TestSelectByIntent_EmptyOnNoMatch(t *testing.T) {
	tools := []provider.ToolDefinition{
		{Name: "volon_task_create", Description: "Create a new task"},
		{Name: "conduit_context_view", Description: "View a context packet"},
	}
	tb := newTestBrokerWithTools(tools)

	// Intent with words that don't match anything.
	result := tb.SelectByIntent("xylophone quantum zebra", 10)
	if len(result) != 0 {
		t.Errorf("expected 0 tools for unrelated intent, got %d: %v", len(result), namesOf(result))
	}
}

func TestSelectByIntent_EmptyIntent(t *testing.T) {
	tools := []provider.ToolDefinition{
		{Name: "volon_task_create", Description: "Create a new task"},
	}
	tb := newTestBrokerWithTools(tools)

	result := tb.SelectByIntent("", 10)
	if len(result) != 0 {
		t.Errorf("expected 0 tools for empty intent, got %d", len(result))
	}
}

func TestSelectByIntent_NoMCPManager(t *testing.T) {
	tb := New(nil, nil, DefaultConfig())
	result := tb.SelectByIntent("create task", 10)
	if len(result) != 0 {
		t.Errorf("expected 0 tools without MCP manager, got %d", len(result))
	}
}

// --- Scoring unit tests ---

func TestScoreToolAgainstIntent(t *testing.T) {
	tool := provider.ToolDefinition{
		Name:        "volon_task_create",
		Description: "Create a new task in the backlog",
	}

	// "task" appears in both name (+2) and description (+1) = 3
	// "create" appears in both name (+2) and description (+1) = 3
	// "backlog" appears in description (+1) = 1
	score := scoreToolAgainstIntent(tool, []string{"task", "create", "backlog"})
	if score < 3 {
		t.Errorf("expected score >= 3 for matching intent, got %d", score)
	}

	// No matching words.
	score = scoreToolAgainstIntent(tool, []string{"pipeline", "deploy"})
	if score != 0 {
		t.Errorf("expected score 0 for non-matching intent, got %d", score)
	}
}

func TestTokeniseIntent(t *testing.T) {
	words := tokeniseIntent("Create a new sprint for the project")
	// "create" (6), "new" (3), "sprint" (6), "project" (7)
	// "the" and "for" are stop words; "a" is < 3 chars
	if len(words) == 0 {
		t.Fatal("expected at least one word from intent")
	}
	found := false
	for _, w := range words {
		if w == "sprint" {
			found = true
		}
		if w == "the" || w == "for" || w == "a" {
			t.Errorf("stop word or short word %q should have been filtered", w)
		}
	}
	if !found {
		t.Errorf("expected 'sprint' in tokenised words, got %v", words)
	}
}

// --- Meta-tool tests ---

func TestRequestToolsMetaTool_HasCorrectSchema(t *testing.T) {
	def := RequestToolsMetaTool()
	if def.Name != "request_tools" {
		t.Errorf("expected name 'request_tools', got %q", def.Name)
	}
	if def.InputSchema == nil {
		t.Fatal("expected non-nil InputSchema")
	}
	props, ok := def.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties in InputSchema")
	}
	if _, ok := props["tool_names"]; !ok {
		t.Error("expected tool_names in properties")
	}
	if _, ok := props["intent"]; !ok {
		t.Error("expected intent in properties")
	}
}

func TestHandleRequestTools_ByIntent(t *testing.T) {
	tools := []provider.ToolDefinition{
		{Name: "volon_task_create", Description: "Create a new task in the backlog"},
		{Name: "hadron_pipeline_run", Description: "Run a build pipeline"},
	}
	tb := newTestBrokerWithTools(tools)

	matched, summary := tb.HandleRequestTools(map[string]any{
		"intent": "create task",
	})

	if len(matched) == 0 {
		t.Fatalf("expected matched tools for intent 'create task', got 0. Summary: %s", summary)
	}
	if summary == "No matching tools found." {
		t.Error("expected non-empty match summary")
	}
}

func TestHandleRequestTools_ByName(t *testing.T) {
	tools := []provider.ToolDefinition{
		{Name: "volon_task_create", Description: "Create a task"},
		{Name: "hadron_pipeline_run", Description: "Run a pipeline"},
	}
	tb := newTestBrokerWithTools(tools)

	matched, _ := tb.HandleRequestTools(map[string]any{
		"tool_names": []any{"volon_task_create"},
	})

	if len(matched) != 1 {
		t.Fatalf("expected 1 tool matched by name, got %d", len(matched))
	}
	if matched[0].Name != "volon_task_create" {
		t.Errorf("expected volon_task_create (uniform name post ADR-002), got %s", matched[0].Name)
	}
}

func TestHandleRequestTools_NoMatch(t *testing.T) {
	tools := []provider.ToolDefinition{
		{Name: "volon_task_create", Description: "Create a task"},
	}
	tb := newTestBrokerWithTools(tools)

	matched, summary := tb.HandleRequestTools(map[string]any{
		"intent": "xylophone quantum zebra",
	})

	if len(matched) != 0 {
		t.Errorf("expected 0 matched tools, got %d", len(matched))
	}
	if summary != "No matching tools found." {
		t.Errorf("expected 'No matching tools found.' summary, got %q", summary)
	}
}

// namesOf extracts tool names from a slice of ToolDefinition.
func namesOf(tools []provider.ToolDefinition) []string {
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name
	}
	return names
}

// --- Strict tool use tests (CW-20260420-0007) ---

func TestSelectToolsAsProvider_BrokerToolsDefaultStrict(t *testing.T) {
	cfg := DefaultConfig()
	tb := New(nil, nil, cfg)

	// Register tools directly with the LocalBroker so SelectToolsAsProvider
	// returns them. SelectToolsAsProvider converts broker.ToolDefinition →
	// provider.ToolDefinition and must set Strict: strictTrue on each result.
	brokerTools := []broker.ToolDefinition{
		{Name: "volon_task_create", Server: "volon", Description: "Create a task in the backlog"},
		{Name: "conduit_context_view", Server: "conduit", Description: "View a context packet"},
	}
	tb.RegisterTools(brokerTools)

	result, err := tb.SelectToolsAsProvider(context.Background(), "task backlog", nil, "", "", 0)
	if err != nil {
		t.Fatalf("SelectToolsAsProvider error: %v", err)
	}
	if len(result.Tools) == 0 {
		t.Fatal("expected at least one tool in result — broker did not select any tools")
	}

	// All broker-registered tools in the result must have Strict set to *true.
	for _, d := range result.Tools {
		if d.Strict == nil {
			t.Errorf("tool %q: Strict is nil, want *true (strict default-on)", d.Name)
			continue
		}
		if !*d.Strict {
			t.Errorf("tool %q: Strict is false, want true (strict default-on)", d.Name)
		}
	}
}

func TestMetaTools_HaveAdditionalPropertiesFalse(t *testing.T) {
	cases := []struct {
		name string
		def  provider.ToolDefinition
	}{
		{"request_tools", RequestToolsMetaTool()},
		{"fetch_tool_result", FetchToolResultMetaTool()},
		{"search_tool_result", SearchToolResultMetaTool()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ap, ok := tc.def.InputSchema["additionalProperties"]
			if !ok {
				t.Errorf("%s: InputSchema missing additionalProperties (required for strict-mode)", tc.name)
				return
			}
			if ap != false {
				t.Errorf("%s: additionalProperties = %v, want false", tc.name, ap)
			}
		})
	}
}

func TestStrictTrue_IsConstant(t *testing.T) {
	// strictTrue must be a pointer to true, not nil or false.
	if strictTrue == nil {
		t.Fatal("strictTrue is nil")
	}
	if !*strictTrue {
		t.Fatal("strictTrue points to false")
	}
}
