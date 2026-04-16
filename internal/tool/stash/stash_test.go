package stash

import (
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/go-providers/provider"
)

func def(name string) provider.ToolDefinition {
	return provider.ToolDefinition{Name: name, Description: "desc-" + name}
}

// staticCategorizer categorizes by a fixed map; unmatched names return "".
type staticCategorizer map[string]string

func (s staticCategorizer) Categorize(name string) string { return s[name] }

func newTestManager() *Manager {
	cat := staticCategorizer{
		"search_notes":  "search",
		"search_web":    "search",
		"shell":         "code-exec",
		"read_file":     "core-io",
		"write_file":    "core-io",
		"http_fetch":    "http",
	}
	m := NewManager(cat)
	m.now = func() time.Time { return time.Unix(1700000000, 0).UTC() }
	return m
}

func TestSelectionHash_OrderIndependent(t *testing.T) {
	a := []provider.ToolDefinition{def("b"), def("a"), def("c")}
	b := []provider.ToolDefinition{def("c"), def("a"), def("b")}
	if SelectionHash(a) != SelectionHash(b) {
		t.Fatalf("hash should be order-independent: %s vs %s", SelectionHash(a), SelectionHash(b))
	}
}

func TestSelectionHash_DifferentSets(t *testing.T) {
	a := []provider.ToolDefinition{def("a"), def("b")}
	b := []provider.ToolDefinition{def("a"), def("c")}
	if SelectionHash(a) == SelectionHash(b) {
		t.Fatalf("different selections must produce different hashes")
	}
}

func TestSelectionHash_Empty(t *testing.T) {
	if SelectionHash(nil) != "empty" {
		t.Fatalf("empty selection should hash to sentinel")
	}
}

func TestManager_BuildCategorizesAndStores(t *testing.T) {
	m := newTestManager()
	selected := []provider.ToolDefinition{def("shell"), def("read_file"), def("write_file"), def("search_web"), def("mystery_plugin_tool")}

	s := m.Build("sess-1", selected)

	if s.SessionID != "sess-1" {
		t.Fatalf("wrong session id: %q", s.SessionID)
	}
	if len(s.FullDefs) != 5 {
		t.Fatalf("expected 5 defs, got %d", len(s.FullDefs))
	}
	if s.CategoryOf["mystery_plugin_tool"] != CategoryOther {
		t.Fatalf("unknown tool should fall into %q, got %q", CategoryOther, s.CategoryOf["mystery_plugin_tool"])
	}
	if got := s.Categories["core-io"]; len(got) != 2 || got[0] != "read_file" || got[1] != "write_file" {
		t.Fatalf("core-io bucket wrong: %v", got)
	}
	if got := s.Categories["search"]; len(got) != 1 || got[0] != "search_web" {
		t.Fatalf("search bucket wrong: %v", got)
	}
	if s.SelectionHash != SelectionHash(selected) {
		t.Fatalf("selection hash mismatch")
	}
	if s.BuiltAt.IsZero() {
		t.Fatalf("BuiltAt should be set")
	}
}

func TestManager_GetReturnsNilOnHashMismatch(t *testing.T) {
	m := newTestManager()
	initial := []provider.ToolDefinition{def("shell"), def("read_file")}
	m.Build("sess-1", initial)

	// Same selection — cache hit.
	if m.Get("sess-1", initial) == nil {
		t.Fatalf("expected cache hit on identical selection")
	}

	// Different selection — miss.
	changed := []provider.ToolDefinition{def("shell"), def("write_file")}
	if m.Get("sess-1", changed) != nil {
		t.Fatalf("expected cache miss on selection change")
	}
}

func TestManager_GetOrBuildRebuildsOnHashMismatch(t *testing.T) {
	m := newTestManager()
	initial := []provider.ToolDefinition{def("shell")}
	first := m.GetOrBuild("sess-1", initial)

	changed := []provider.ToolDefinition{def("shell"), def("read_file")}
	second := m.GetOrBuild("sess-1", changed)

	if first.SelectionHash == second.SelectionHash {
		t.Fatalf("rebuild should produce different hash")
	}
	if len(second.FullDefs) != 2 {
		t.Fatalf("rebuilt stash should have 2 defs, got %d", len(second.FullDefs))
	}
	// Original stash object remains valid (immutable after construction).
	if len(first.FullDefs) != 1 {
		t.Fatalf("first stash should still have 1 def, got %d", len(first.FullDefs))
	}
}

func TestManager_GetOrBuildReusesOnIdenticalSelection(t *testing.T) {
	m := newTestManager()
	selected := []provider.ToolDefinition{def("shell"), def("search_web")}

	first := m.GetOrBuild("sess-1", selected)
	second := m.GetOrBuild("sess-1", selected)

	if first != second {
		t.Fatalf("identical selection should return the same stash pointer")
	}
}

func TestManager_Invalidate(t *testing.T) {
	m := newTestManager()
	selected := []provider.ToolDefinition{def("shell")}
	m.Build("sess-1", selected)

	if m.Get("sess-1", selected) == nil {
		t.Fatalf("expected hit before invalidation")
	}
	m.Invalidate("sess-1")
	if m.Get("sess-1", selected) != nil {
		t.Fatalf("expected miss after invalidation")
	}
}

func TestSummary_IncludesCategoryCountsAndTotal(t *testing.T) {
	m := newTestManager()
	selected := []provider.ToolDefinition{def("shell"), def("read_file"), def("write_file"), def("search_web")}
	s := m.Build("sess-1", selected)

	if !strings.Contains(s.SummaryText, "[4 tools available — pointer]") {
		t.Errorf("summary missing total line: %q", s.SummaryText)
	}
	if !strings.Contains(s.SummaryText, "code-exec(1)") {
		t.Errorf("summary missing code-exec count: %q", s.SummaryText)
	}
	if !strings.Contains(s.SummaryText, "core-io(2)") {
		t.Errorf("summary missing core-io count: %q", s.SummaryText)
	}
	if !strings.Contains(s.SummaryText, "/tools on") {
		t.Errorf("summary missing /tools escape hatch hint: %q", s.SummaryText)
	}
}

func TestSummary_EmptySelection(t *testing.T) {
	m := newTestManager()
	s := m.Build("sess-1", nil)
	if s.SummaryText != "" {
		t.Errorf("empty selection should produce empty summary, got %q", s.SummaryText)
	}
}

func TestSummary_BoundedSize(t *testing.T) {
	m := newTestManager()
	// 30 tools across known categories — summary must stay compact.
	selected := make([]provider.ToolDefinition, 0, 30)
	for i := 0; i < 10; i++ {
		selected = append(selected, def("shell_"+string(rune('a'+i))))
	}
	for i := 0; i < 10; i++ {
		selected = append(selected, def("read_file_"+string(rune('a'+i))))
	}
	for i := 0; i < 10; i++ {
		selected = append(selected, def("search_web_"+string(rune('a'+i))))
	}

	// Unknown tool names — all land in "other". Summary still bounded because it
	// only emits one line per category, not per tool.
	s := m.Build("sess-1", selected)

	// 4 cats × ~60 chars + header ≈ well under 1200 chars (< 300 tokens).
	if got := len(s.SummaryText); got > 1200 {
		t.Errorf("summary should be bounded by category count, got %d chars", got)
	}
}

func TestDefsForCategories(t *testing.T) {
	m := newTestManager()
	selected := []provider.ToolDefinition{def("shell"), def("read_file"), def("write_file"), def("search_web")}
	s := m.Build("sess-1", selected)

	defs := s.DefsForCategories([]string{"core-io"})
	if len(defs) != 2 {
		t.Fatalf("expected 2 core-io defs, got %d", len(defs))
	}
	// Sorted by name.
	if defs[0].Name != "read_file" || defs[1].Name != "write_file" {
		t.Fatalf("defs not sorted: %+v", defs)
	}

	multi := s.DefsForCategories([]string{"code-exec", "search"})
	if len(multi) != 2 {
		t.Fatalf("expected 2 defs across two cats, got %d", len(multi))
	}

	none := s.DefsForCategories([]string{"bogus"})
	if len(none) != 0 {
		t.Fatalf("unknown category should return no defs, got %d", len(none))
	}
}

func TestCategoriesList_Sorted(t *testing.T) {
	m := newTestManager()
	selected := []provider.ToolDefinition{def("shell"), def("read_file"), def("search_web"), def("http_fetch")}
	s := m.Build("sess-1", selected)

	cats := s.CategoriesList()
	for i := 1; i < len(cats); i++ {
		if cats[i-1] > cats[i] {
			t.Fatalf("categories not sorted: %v", cats)
		}
	}
}

func TestNewManager_NilCategorizerBucketsEverythingToOther(t *testing.T) {
	m := NewManager(nil)
	s := m.Build("sess-1", []provider.ToolDefinition{def("anything"), def("else")})
	if s.CategoryOf["anything"] != CategoryOther || s.CategoryOf["else"] != CategoryOther {
		t.Fatalf("nil categorizer should bucket everything to %q", CategoryOther)
	}
	if len(s.Categories[CategoryOther]) != 2 {
		t.Fatalf("expected 2 tools in %q, got %d", CategoryOther, len(s.Categories[CategoryOther]))
	}
}
