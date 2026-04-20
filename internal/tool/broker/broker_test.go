package broker_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/tool"
	"github.com/hollis-labs/nanite/internal/tool/broker"
	"github.com/hollis-labs/nanite/internal/tool/enrichment"
)

// stubEnricher is a map-backed Enricher for tests.
type stubEnricher map[string]enrichment.Hints

func (s stubEnricher) LookupByToolName(_ context.Context, name string) (enrichment.Hints, bool, error) {
	h, ok := s[name]
	return h, ok, nil
}

// testTool creates a simple tool for testing.
func testTool(name, category string, tags ...string) tool.Tool {
	opts := []tool.ToolOption{
		tool.WithCategory(category),
		tool.WithTags(tags...),
		tool.WithReadOnly(category == tool.CategorySearch),
	}
	return tool.NewTool(name, "test tool: "+name, opts...)
}

// testRegistry returns a registry with a standard set of test tools.
func testRegistry() *broker.Registry {
	r := broker.NewRegistry()
	r.Register(testTool("dev_read", tool.CategoryCoreIO, "io", "file"))
	r.Register(testTool("dev_write", tool.CategoryCoreIO, "io", "file"))
	r.Register(testTool("dev_edit", tool.CategoryCoreIO, "io", "file"))
	r.Register(testTool("dev_grep", tool.CategorySearch, "search", "file"))
	r.Register(testTool("dev_glob", tool.CategorySearch, "search", "file"))
	r.Register(testTool("web_fetch", tool.CategorySearch, "search", "web"))
	r.Register(testTool("web_search", tool.CategorySearch, "search", "web"))
	r.Register(testTool("engine_task_create", tool.CategoryAgent, "task"))
	r.Register(testTool("engine_sprint_list", tool.CategoryAgent, "task", "sprint"))
	r.Register(testTool("session_create", tool.CategorySession, "session"))
	r.Register(testTool("memory_read", tool.CategoryContext, "memory"))
	r.Register(testTool("enter_plan", tool.CategoryMode, "mode"))
	r.Register(testTool("shell", tool.CategoryCoreIO, "io"))
	r.Register(testTool("send_message", tool.CategoryAgent, "agent"))
	return r
}

// --- Registry tests ---

func TestRegistry_BasicOps(t *testing.T) {
	r := broker.NewRegistry()
	if r.Count() != 0 {
		t.Errorf("Count() = %d, want 0", r.Count())
	}

	r.Register(testTool("a", tool.CategoryCoreIO))
	r.Register(testTool("b", tool.CategorySearch))
	if r.Count() != 2 {
		t.Errorf("Count() = %d, want 2", r.Count())
	}

	if r.Get("a") == nil {
		t.Error("Get(a) returned nil")
	}
	if r.Get("nonexistent") != nil {
		t.Error("Get(nonexistent) should be nil")
	}

	all := r.All()
	if len(all) != 2 || all[0].Name() != "a" || all[1].Name() != "b" {
		t.Errorf("All() = %v", toolNameSlice(all))
	}
}

func TestRegistry_ByCategory(t *testing.T) {
	r := testRegistry()
	coreIO := r.ByCategory(tool.CategoryCoreIO)
	if len(coreIO) != 4 { // dev_read, dev_write, dev_edit, shell
		t.Errorf("ByCategory(core-io) = %d tools, want 4", len(coreIO))
	}

	search := r.ByCategory(tool.CategorySearch)
	if len(search) != 4 { // dev_grep, dev_glob, web_fetch, web_search
		t.Errorf("ByCategory(search) = %d tools, want 4", len(search))
	}
}

func TestRegistry_ByTag(t *testing.T) {
	r := testRegistry()
	webTools := r.ByTag("web")
	if len(webTools) != 2 {
		t.Errorf("ByTag(web) = %d tools, want 2", len(webTools))
	}
}

func TestRegistry_GetByNames(t *testing.T) {
	r := testRegistry()
	tools := r.GetByNames([]string{"dev_read", "nonexistent", "shell"})
	if len(tools) != 2 {
		t.Errorf("GetByNames() = %d tools, want 2", len(tools))
	}
}

func TestRegistry_Replace(t *testing.T) {
	r := broker.NewRegistry()
	r.Register(testTool("a", tool.CategoryCoreIO))
	r.Register(testTool("a", tool.CategorySearch)) // replace
	if r.Count() != 1 {
		t.Errorf("Count() = %d after replace, want 1", r.Count())
	}
	if r.Get("a").Category() != tool.CategorySearch {
		t.Errorf("replaced tool has wrong category")
	}
}

// --- Layer 1: Explicit ---

func TestBroker_Layer1_ExplicitTools(t *testing.T) {
	reg := testRegistry()
	b := broker.New(reg, nil)

	sel := b.Select(context.Background(), broker.IntentSignals{
		ExplicitTools: []string{"dev_read", "dev_write"},
	})

	if sel.LayerReached != "explicit" {
		t.Errorf("LayerReached = %q, want explicit", sel.LayerReached)
	}
	if len(sel.Tools) != 2 {
		t.Errorf("got %d tools, want 2", len(sel.Tools))
	}
	if sel.Intent != "caller-specified" {
		t.Errorf("Intent = %q", sel.Intent)
	}
}

func TestBroker_Layer1_SkillTools(t *testing.T) {
	reg := testRegistry()
	b := broker.New(reg, nil)

	sel := b.Select(context.Background(), broker.IntentSignals{
		SkillTools: []string{"dev_grep", "web_fetch"},
	})

	if sel.LayerReached != "explicit" {
		t.Errorf("LayerReached = %q, want explicit", sel.LayerReached)
	}
	if sel.Intent != "skill-binding" {
		t.Errorf("Intent = %q", sel.Intent)
	}
	if len(sel.Tools) != 2 {
		t.Errorf("got %d tools, want 2", len(sel.Tools))
	}
}

func TestBroker_Layer1_AgentToolSet(t *testing.T) {
	reg := testRegistry()
	b := broker.New(reg, nil)

	sel := b.Select(context.Background(), broker.IntentSignals{
		AgentToolSet: []string{"engine_task_create", "engine_sprint_list"},
	})

	if sel.LayerReached != "explicit" {
		t.Errorf("LayerReached = %q", sel.LayerReached)
	}
	if sel.Intent != "agent-config" {
		t.Errorf("Intent = %q", sel.Intent)
	}
}

func TestBroker_Layer1_Priority(t *testing.T) {
	reg := testRegistry()
	b := broker.New(reg, nil)

	// When both explicit and skill are set, explicit wins.
	sel := b.Select(context.Background(), broker.IntentSignals{
		ExplicitTools: []string{"shell"},
		SkillTools:    []string{"web_fetch"},
		AgentToolSet:  []string{"dev_read"},
	})

	if sel.Intent != "caller-specified" {
		t.Errorf("Expected explicit to win, got Intent = %q", sel.Intent)
	}
	if len(sel.Tools) != 1 || sel.Tools[0].Name() != "shell" {
		t.Errorf("Expected [shell], got %v", toolNameSlice(sel.Tools))
	}
}

// --- Layer 2: Rules ---

func TestBroker_Layer2_RuleMatch(t *testing.T) {
	reg := testRegistry()
	rules := &broker.RuleSet{
		Rules: []broker.Rule{
			{Match: "dev_*", Intent: []string{"code", "file"}, Priority: 10},
			{Match: "web_*", Intent: []string{"research"}, Priority: 5},
		},
		AlwaysAvailable: []string{"shell"},
		Presets:         map[string]broker.Preset{},
	}
	b := broker.New(reg, rules)

	sel := b.Select(context.Background(), broker.IntentSignals{
		MessageKeywords: []string{"code", "edit"},
	})

	if sel.LayerReached != "rules" {
		t.Errorf("LayerReached = %q, want rules", sel.LayerReached)
	}

	// Should have dev_* tools + shell (always available).
	names := toolNameSlice(sel.Tools)
	hasShell := false
	hasDevRead := false
	for _, n := range names {
		if n == "shell" {
			hasShell = true
		}
		if n == "dev_read" {
			hasDevRead = true
		}
	}
	if !hasShell {
		t.Error("expected shell in always_available")
	}
	if !hasDevRead {
		t.Error("expected dev_read from rule match")
	}
}

func TestBroker_Layer2_Preset(t *testing.T) {
	reg := testRegistry()
	rules := &broker.RuleSet{
		Presets: map[string]broker.Preset{
			"code": {Tools: []string{"dev_read", "dev_write", "dev_edit", "dev_grep", "dev_glob"}},
		},
	}
	b := broker.New(reg, rules)

	sel := b.Select(context.Background(), broker.IntentSignals{
		SessionMode: "code",
	})

	if sel.LayerReached != "rules" {
		t.Errorf("LayerReached = %q, want rules", sel.LayerReached)
	}
	if sel.Intent != "preset:code" {
		t.Errorf("Intent = %q, want preset:code", sel.Intent)
	}
	if len(sel.Tools) != 5 {
		t.Errorf("got %d tools, want 5", len(sel.Tools))
	}
}

func TestBroker_Layer2_PlanModeReadOnly(t *testing.T) {
	reg := testRegistry()
	rules := &broker.RuleSet{
		Rules: []broker.Rule{
			{Match: "dev_*", Intent: []string{"code"}, Priority: 10},
		},
		Presets: map[string]broker.Preset{},
	}
	b := broker.New(reg, rules)

	sel := b.Select(context.Background(), broker.IntentSignals{
		SessionMode:     "plan",
		MessageKeywords: []string{"code"},
	})

	// In plan mode, only read-only dev_* tools should be selected.
	for _, t2 := range sel.Tools {
		if !t2.IsReadOnly(nil) && t2.Category() == tool.CategoryCoreIO {
			t.Errorf("plan mode included non-read-only tool: %s", t2.Name())
		}
	}
}

// --- Layer 3: Classifier ---

func TestBroker_Layer3_ClassifiesCode(t *testing.T) {
	reg := testRegistry()
	b := broker.New(reg, &broker.RuleSet{Presets: map[string]broker.Preset{}})

	sel := b.Select(context.Background(), broker.IntentSignals{
		MessageKeywords: []string{"edit", "fix", "bug", "refactor"},
	})

	if sel.LayerReached != "classifier" {
		t.Errorf("LayerReached = %q, want classifier", sel.LayerReached)
	}
	if sel.Intent != "classified:code" {
		t.Errorf("Intent = %q, want classified:code", sel.Intent)
	}

	// Should have core-io and search tools.
	hasCoreIO := false
	for _, tt := range sel.Tools {
		if tt.Category() == tool.CategoryCoreIO {
			hasCoreIO = true
		}
	}
	if !hasCoreIO {
		t.Error("expected core-io tools for code classification")
	}
}

func TestBroker_Layer3_ClassifiesResearch(t *testing.T) {
	reg := testRegistry()
	b := broker.New(reg, &broker.RuleSet{Presets: map[string]broker.Preset{}})

	sel := b.Select(context.Background(), broker.IntentSignals{
		MessageKeywords: []string{"search", "find", "documentation"},
	})

	if sel.LayerReached != "classifier" {
		t.Errorf("LayerReached = %q, want classifier", sel.LayerReached)
	}
}

func TestBroker_Layer3_SessionModeBoost(t *testing.T) {
	reg := testRegistry()
	rules := &broker.RuleSet{
		Presets: map[string]broker.Preset{
			"plan": {Tools: []string{"engine_task_create", "engine_sprint_list", "enter_plan"}},
		},
	}
	b := broker.New(reg, rules)

	// Session mode "plan" should strongly boost plan classification.
	sel := b.Select(context.Background(), broker.IntentSignals{
		SessionMode:     "plan",
		MessageKeywords: []string{"code"}, // weak competing signal
	})

	// Plan should win due to session mode boost (+5).
	if sel.Intent != "preset:plan" && sel.Intent != "classified:plan" {
		t.Errorf("Intent = %q, expected plan to win due to session mode", sel.Intent)
	}
}

// --- Fallback ---

func TestBroker_Fallback(t *testing.T) {
	reg := testRegistry()
	b := broker.New(reg, nil) // no rules at all

	sel := b.Select(context.Background(), broker.IntentSignals{})

	if sel.LayerReached != "fallback" {
		t.Errorf("LayerReached = %q, want fallback", sel.LayerReached)
	}
	if len(sel.Tools) == 0 {
		t.Error("fallback returned 0 tools")
	}
	if len(sel.Tools) > 5 {
		t.Errorf("fallback returned %d tools, want <= 5", len(sel.Tools))
	}
}

func TestBroker_Fallback_AlwaysAvailable(t *testing.T) {
	reg := testRegistry()
	rules := &broker.RuleSet{
		AlwaysAvailable: []string{"shell", "send_message"},
		Presets:         map[string]broker.Preset{},
	}
	b := broker.New(reg, rules)

	sel := b.Select(context.Background(), broker.IntentSignals{})

	if sel.LayerReached != "fallback" {
		t.Errorf("LayerReached = %q, want fallback", sel.LayerReached)
	}
	names := toolNameSlice(sel.Tools)
	hasShell := false
	for _, n := range names {
		if n == "shell" {
			hasShell = true
		}
	}
	if !hasShell {
		t.Errorf("fallback should include always_available tools, got %v", names)
	}
}

// --- Rule matching ---

func TestMatchPattern(t *testing.T) {
	tests := []struct {
		pattern string
		name    string
		want    bool
	}{
		{"*", "anything", true},
		{"dev_*", "dev_read", true},
		{"dev_*", "web_fetch", false},
		{"mcp__engine__*", "mcp__engine__task_create", true},
		{"mcp__engine__*", "mcp__dev__read", false},
		{"shell", "shell", true},
		{"shell", "shell2", false},
	}

	for _, tc := range tests {
		got := broker.MatchPattern(tc.pattern, tc.name)
		if got != tc.want {
			t.Errorf("MatchPattern(%q, %q) = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
}

// --- Rule loading ---

func TestLoadRules(t *testing.T) {
	tmpDir := t.TempDir()
	naniteDir := filepath.Join(tmpDir, ".nanite")
	if err := os.MkdirAll(naniteDir, 0o755); err != nil {
		t.Fatal(err)
	}

	yamlContent := `tool_broker:
  rules:
    - match: "dev_*"
      intent: [code, file]
      priority: 10
    - match: "web_*"
      intent: [research]
      priority: 5
  presets:
    code:
      tools: [dev_read, dev_write, dev_edit, dev_grep, dev_glob]
    research:
      tools: [web_fetch, web_search, dev_read]
  always_available: [shell, send_message]
`
	if err := os.WriteFile(filepath.Join(naniteDir, "broker.yaml"), []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	rules := broker.LoadRules(tmpDir)

	if len(rules.Rules) != 2 {
		t.Errorf("got %d rules, want 2", len(rules.Rules))
	}
	if len(rules.Presets) != 2 {
		t.Errorf("got %d presets, want 2", len(rules.Presets))
	}
	if len(rules.AlwaysAvailable) != 2 {
		t.Errorf("got %d always_available, want 2", len(rules.AlwaysAvailable))
	}
}

// --- Selection has signals JSON ---

func TestBroker_SelectionSignals(t *testing.T) {
	reg := testRegistry()
	b := broker.New(reg, nil)

	sel := b.Select(context.Background(), broker.IntentSignals{
		ExplicitTools: []string{"shell"},
	})

	if sel.Signals == "" {
		t.Error("Signals should be non-empty JSON")
	}
}

// --- OverrideBlock (P1 ToolSurface) ---

func TestSelection_OverrideBlockPopulated(t *testing.T) {
	reg := testRegistry()
	enr := stubEnricher{
		"dev_read": {OutputShape: "returns file bytes"},
	}
	b := broker.New(reg, nil, broker.WithEnricher(enr))

	sel := b.Select(context.Background(), broker.IntentSignals{
		ExplicitTools: []string{"dev_read"},
	})

	if !strings.Contains(sel.OverrideBlock, "dev_read") {
		t.Errorf("expected OverrideBlock to mention dev_read, got: %q", sel.OverrideBlock)
	}
	if !strings.Contains(sel.OverrideBlock, "returns file bytes") {
		t.Errorf("expected OverrideBlock to contain hint, got: %q", sel.OverrideBlock)
	}
	if !strings.Contains(sel.OverrideBlock, "## Tool Overrides") {
		t.Errorf("expected OverrideBlock to contain header, got: %q", sel.OverrideBlock)
	}
}

func TestSelection_OverrideBlockEmptyWithoutEnricher(t *testing.T) {
	reg := testRegistry()
	b := broker.New(reg, nil) // no WithEnricher option → enricher is nil

	sel := b.Select(context.Background(), broker.IntentSignals{
		ExplicitTools: []string{"dev_read"},
	})

	if sel.OverrideBlock != "" {
		t.Errorf("expected empty OverrideBlock without enricher, got: %q", sel.OverrideBlock)
	}
}

func TestSelection_OverrideBlockEmptyWithoutEnrichedTools(t *testing.T) {
	reg := testRegistry()
	enr := stubEnricher{} // empty — no tool has enrichment
	b := broker.New(reg, nil, broker.WithEnricher(enr))

	sel := b.Select(context.Background(), broker.IntentSignals{
		ExplicitTools: []string{"dev_read"},
	})

	if sel.OverrideBlock != "" {
		t.Errorf("expected empty OverrideBlock without enriched tools, got: %q", sel.OverrideBlock)
	}
}

// --- helpers ---

func toolNameSlice(tools []tool.Tool) []string {
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name()
	}
	return names
}
