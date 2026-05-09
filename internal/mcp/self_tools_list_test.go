package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/go-providers/provider"
)

// stubInventoryLookup is a minimal ToolInventoryLookup stub used to
// prove the cross-server enumeration path is exercised by callToolList
// (CW-20260501-0001). Returns a fixed slice of tool definitions.
type stubInventoryLookup struct {
	tools []provider.ToolDefinition
}

func (s *stubInventoryLookup) GetAllToolsUnfiltered() []provider.ToolDefinition {
	return s.tools
}

// TestNaniteToolList_RegistrationAndShape verifies the self-tool is
// registered in selfToolDefinitions(), is wired through the dispatch
// table, and returns the documented {tools, count} shape on a
// no-args call. (SP6, CW-20260430-0006.)
func TestNaniteToolList_RegistrationAndShape(t *testing.T) {
	defs := selfToolDefinitions()
	var found bool
	for _, d := range defs {
		if d.Name == "tool_list" {
			found = true
			if d.InputSchema == nil {
				t.Fatal("tool_list missing InputSchema")
			}
			// filter is optional — schema must NOT mark it required.
			if reqd, ok := d.InputSchema["required"].([]string); ok && len(reqd) > 0 {
				t.Errorf("tool_list must have no required fields, got %v", reqd)
			}
			break
		}
	}
	if !found {
		t.Fatal("tool_list not in selfToolDefinitions()")
	}

	st := newSelfTools(t)
	res, err := st.CallTool(context.Background(), "tool_list", map[string]any{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content[0].Text)
	}
	var out struct {
		Tools []struct {
			Name    string `json:"name"`
			Summary string `json:"summary"`
		} `json:"tools"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Count != len(out.Tools) {
		t.Errorf("count %d != len(tools) %d", out.Count, len(out.Tools))
	}
	if out.Count == 0 {
		t.Fatal("expected non-empty tool inventory")
	}
	// Spot-check: the surface includes tool_describe (sibling
	// discovery primitive) and tool_list itself.
	saw := map[string]string{}
	for _, t := range out.Tools {
		saw[t.Name] = t.Summary
	}
	if _, ok := saw["tool_describe"]; !ok {
		t.Error("inventory missing tool_describe")
	}
	if _, ok := saw["tool_list"]; !ok {
		t.Error("inventory missing tool_list (self-include)")
	}
	// Every entry must carry a non-empty summary — a missing summary
	// defeats the purpose of the cheap-discovery primitive.
	for name, summ := range saw {
		if summ == "" {
			t.Errorf("tool %q has empty summary", name)
		}
		if len(summ) > summaryMaxBytes {
			t.Errorf("tool %q summary exceeds %d bytes (got %d): %q", name, summaryMaxBytes, len(summ), summ)
		}
	}
}

// TestNaniteToolList_FilterNarrowsByNameAndSummary asserts the filter
// is case-insensitive and matches against BOTH the tool name and its
// summary. The "reminder" filter is the canonical c120 case — agent
// guessed `nanite_reminder_create` instead of `reminder_set`.
func TestNaniteToolList_FilterNarrowsByNameAndSummary(t *testing.T) {
	st := newSelfTools(t)
	// Substring match in NAME — `set_reminder`.
	res, err := st.CallTool(context.Background(), "tool_list", map[string]any{
		"filter": "reminder",
	})
	if err != nil {
		t.Fatalf("filter call: %v", err)
	}
	if res.IsError {
		t.Fatalf("filter call returned error: %s", res.Content[0].Text)
	}
	var out struct {
		Tools []struct {
			Name    string `json:"name"`
			Summary string `json:"summary"`
		} `json:"tools"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Count == 0 {
		t.Fatal("expected ≥1 match for filter=reminder (reminder_set must surface)")
	}
	sawSetReminder := false
	for _, tool := range out.Tools {
		if tool.Name == "reminder_set" {
			sawSetReminder = true
		}
		// Every survivor must contain the filter token in name OR summary.
		if !strings.Contains(strings.ToLower(tool.Name), "reminder") &&
			!strings.Contains(strings.ToLower(tool.Summary), "reminder") {
			t.Errorf("filter leaked tool %q without 'reminder' in name or summary (summary=%q)", tool.Name, tool.Summary)
		}
	}
	if !sawSetReminder {
		t.Error("filter=reminder must surface reminder_set (the c120 motivating case)")
	}

	// Case-insensitive: "REMINDER" should match the same set as "reminder".
	resUC, err := st.CallTool(context.Background(), "tool_list", map[string]any{
		"filter": "REMINDER",
	})
	if err != nil {
		t.Fatalf("uppercase filter call: %v", err)
	}
	var outUC struct {
		Count int `json:"count"`
	}
	_ = json.Unmarshal([]byte(resUC.Content[0].Text), &outUC)
	if outUC.Count != out.Count {
		t.Errorf("case-insensitive filter produced different count: lower=%d upper=%d", out.Count, outUC.Count)
	}
}

// TestNaniteToolList_FilterNotFoundReturnsEmpty verifies the not-found
// path returns count:0 with an empty list — NOT a structured error.
// The agent reading the response decides whether to widen the filter.
func TestNaniteToolList_FilterNotFoundReturnsEmpty(t *testing.T) {
	st := newSelfTools(t)
	res, err := st.CallTool(context.Background(), "tool_list", map[string]any{
		"filter": "this_substring_appears_in_no_tool_name_or_summary_zzz",
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.IsError {
		t.Fatalf("filter-not-found must NOT be an error result, got: %s", res.Content[0].Text)
	}
	var out struct {
		Tools []json.RawMessage `json:"tools"`
		Count int               `json:"count"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Count != 0 {
		t.Errorf("expected count=0, got %d", out.Count)
	}
	if len(out.Tools) != 0 {
		t.Errorf("expected empty tools list, got %d entries", len(out.Tools))
	}
}

// TestNaniteToolList_FilterMatchesSummaryNotJustName covers the half of
// the contract that's easy to forget: the filter applies to BOTH name
// AND summary. We synthesize a search that's plausibly only in summary
// text (not in any tool's name) and assert at least one match.
func TestNaniteToolList_FilterMatchesSummaryNotJustName(t *testing.T) {
	st := newSelfTools(t)
	// Pick a token that appears in summary text but is NOT part of any
	// tool name. "durable" appears in lesson_capture's "persist ... to
	// durable memory" and in handoff_stash; no tool is named "durable".
	res, _ := st.CallTool(context.Background(), "tool_list", map[string]any{
		"filter": "durable",
	})
	var out struct {
		Tools []struct {
			Name    string `json:"name"`
			Summary string `json:"summary"`
		} `json:"tools"`
		Count int `json:"count"`
	}
	_ = json.Unmarshal([]byte(res.Content[0].Text), &out)
	if out.Count == 0 {
		t.Fatal("expected at least one tool whose summary contains 'durable'")
	}
	// At least one survivor's name must NOT contain "durable" — proving
	// the summary-side match path fired.
	matchedViaSummary := false
	for _, tool := range out.Tools {
		if !strings.Contains(strings.ToLower(tool.Name), "durable") {
			matchedViaSummary = true
			break
		}
	}
	if !matchedViaSummary {
		t.Error("filter must match summary text, not just tool names")
	}
}

// TestNaniteToolList_UnfilteredSize documents the actual unfiltered
// payload size. The ticket targets ≤2 KB; if we exceed that, the report
// flags the deviation. The test does NOT fail on size — a mechanical
// size cap would force ratcheting summaries down across every PR that
// adds a tool. Use the test output (-v) to read the size.
func TestNaniteToolList_UnfilteredSize(t *testing.T) {
	st := newSelfTools(t)
	res, err := st.CallTool(context.Background(), "tool_list", map[string]any{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	body := res.Content[0].Text
	t.Logf("tool_list unfiltered: %d bytes", len(body))

	// Filtered "reminder" — should be small.
	resF, _ := st.CallTool(context.Background(), "tool_list", map[string]any{
		"filter": "reminder",
	})
	t.Logf("tool_list filter=\"reminder\": %d bytes", len(resF.Content[0].Text))
}

// TestFirstSentenceSummary covers the summary-extraction strategy at
// the unit level: end with period, double newline boundary, hard byte
// cap, and the trailing-period strip.
func TestFirstSentenceSummary(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "simple sentence with trailing period",
			in:   "Create a new skill.",
			want: "Create a new skill",
		},
		{
			name: "first of two sentences",
			in:   "Create a new skill. Then list it.",
			want: "Create a new skill",
		},
		{
			name: "double-newline paragraph break",
			in:   "Create a new skill\n\n**When to use:** ...",
			want: "Create a new skill",
		},
		{
			name: "single-newline soft break",
			in:   "Create a new skill\nfoo bar",
			want: "Create a new skill",
		},
		{
			name: "no break, capped at 80 chars",
			in:   "this is a very long single-sentence description without any clean sentence boundary inside it whatsoever",
			want: "this is a very long single-sentence description without any clean sentence bound",
		},
		{
			name: "empty input",
			in:   "",
			want: "",
		},
		{
			name: "leading whitespace trimmed",
			in:   "   Create a new skill.",
			want: "Create a new skill",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := firstSentenceSummary(c.in)
			if got != c.want {
				t.Errorf("firstSentenceSummary(%q)\n got: %q\nwant: %q", c.in, got, c.want)
			}
			if len(got) > summaryMaxBytes {
				t.Errorf("summary exceeds cap: %d bytes", len(got))
			}
		})
	}
}

// TestNaniteToolList_CrossServerEnumeration is the regression test for
// CW-20260501-0001: with a wired ToolInventoryLookup the discovery
// primitive must surface tools registered on sibling MCP servers, not
// just the in-process self-tools. The motivating bug was c121, where
// the agent burned 6 list calls trying to find `memory_recall`
// (registered on `nanite-memory`) and concluded it didn't exist.
func TestNaniteToolList_CrossServerEnumeration(t *testing.T) {
	st := newSelfTools(t)
	st.Inventory = &stubInventoryLookup{
		tools: []provider.ToolDefinition{
			{
				Name:        "memory_recall",
				Description: "Recall memories relevant to the current turn from the durable Vanta substrate.",
			},
			{
				Name:        "memory_write",
				Description: "Save a memory for future sessions.",
			},
			{
				// Self-tool advertised via the manager too — the dedup
				// guard in gatherInventory should keep this single-entry.
				Name:        "lesson_capture",
				Description: "Persist a one-sentence lesson to durable memory.",
			},
		},
	}

	res, err := st.CallTool(context.Background(), "tool_list", map[string]any{
		"filter": "memory",
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content[0].Text)
	}

	var out struct {
		Tools []struct {
			Name    string `json:"name"`
			Summary string `json:"summary"`
		} `json:"tools"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}

	saw := map[string]bool{}
	for _, tool := range out.Tools {
		saw[tool.Name] = true
	}

	// memory_recall MUST surface — the c121 motivating case.
	if !saw["memory_recall"] {
		t.Error("filter=memory must surface memory_recall (the cross-server bug fix)")
	}
	// memory_write matches filter=memory by name and surfaces too —
	// discovery shows the full inventory regardless of caller role.
	if !saw["memory_write"] {
		t.Error("filter=memory must surface memory_write (full cross-server inventory)")
	}
	// Sibling self-tool with `memory` in summary should surface
	// (lesson_capture mentions "durable memory").
	if !saw["lesson_capture"] {
		t.Error("filter=memory must surface lesson_capture (summary contains 'memory')")
	}
}

// TestNaniteToolList_DedupesAcrossSources verifies the same tool name
// appearing in BOTH the manager inventory and selfToolDefinitions is
// emitted exactly once. The manager-fed entry wins (added first), so
// the description used is the one the manager sees.
func TestNaniteToolList_DedupesAcrossSources(t *testing.T) {
	st := newSelfTools(t)
	st.Inventory = &stubInventoryLookup{
		tools: []provider.ToolDefinition{
			{
				Name:        "tool_list",
				Description: "Stub description from the manager that should win on dedup.",
			},
		},
	}

	res, err := st.CallTool(context.Background(), "tool_list", map[string]any{
		"filter": "tool_list",
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	var out struct {
		Tools []struct {
			Name    string `json:"name"`
			Summary string `json:"summary"`
		} `json:"tools"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}

	hits := 0
	for _, tool := range out.Tools {
		if tool.Name == "tool_list" {
			hits++
		}
	}
	if hits != 1 {
		t.Errorf("tool_list appeared %d times; want exactly 1 (dedup across sources)", hits)
	}
}

// TestNaniteToolList_CrossServerSizeMeasurement documents the actual
// payload size when the cross-server inventory is wired (the realistic
// production shape). The test does NOT fail on size — see
// TestNaniteToolList_UnfilteredSize.
func TestNaniteToolList_CrossServerSizeMeasurement(t *testing.T) {
	st := newSelfTools(t)
	st.Inventory = &stubInventoryLookup{
		tools: []provider.ToolDefinition{
			{
				Name:        "memory_recall",
				Description: "Recall memories relevant to the current turn from durable storage.",
			},
		},
	}
	res, err := st.CallTool(context.Background(), "tool_list", map[string]any{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	body := res.Content[0].Text
	t.Logf("tool_list cross-server unfiltered: %d bytes", len(body))

	resF, _ := st.CallTool(context.Background(), "tool_list", map[string]any{
		"filter": "memory",
	})
	t.Logf("tool_list cross-server filter=\"memory\": %d bytes", len(resF.Content[0].Text))
}

// TestNaniteToolList_FullInventoryIncludesNonNanitePrefixed pins the
// runtime-chat-surface-filter removal contract (CW for the surface
// filter, addressed in the runtime-filter PR): output is the FULL
// inventory regardless of name prefix. Earlier revisions filtered to
// `nanite_*` names on certain agent roles; that filter is gone, and
// without a positive test future code could quietly re-introduce it.
//
// The probe registers a non-nanite_*-prefixed tool (`dev_read`, the
// canonical dev-mode shell tool) on the manager-fed inventory and asserts
// it appears in unfiltered tool_list output. Reach for any caller
// to actually call dev_read is governed elsewhere (agent permissions +
// dev-mode gate); the catalog primitive must surface it regardless.
func TestNaniteToolList_FullInventoryIncludesNonNanitePrefixed(t *testing.T) {
	st := newSelfTools(t)
	st.Inventory = &stubInventoryLookup{
		tools: []provider.ToolDefinition{
			{
				Name:        "dev_read",
				Description: "Read a file from the workspace (dev-mode shell tool).",
			},
			{
				Name:        "web_fetch",
				Description: "Fetch a URL and return its body.",
			},
		},
	}

	// Unfiltered call — full inventory mode.
	res, err := st.CallTool(context.Background(), "tool_list", map[string]any{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content[0].Text)
	}

	var out struct {
		Tools []struct {
			Name    string `json:"name"`
			Summary string `json:"summary"`
		} `json:"tools"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}

	saw := map[string]bool{}
	for _, tool := range out.Tools {
		saw[tool.Name] = true
	}

	// Non-nanite_*-prefixed tools must appear — full inventory contract.
	if !saw["dev_read"] {
		t.Error("tool_list must surface non-nanite_*-prefixed tools (dev_read missing — runtime surface filter regression?)")
	}
	if !saw["web_fetch"] {
		t.Error("tool_list must surface non-nanite_*-prefixed tools (web_fetch missing — runtime surface filter regression?)")
	}
}

// TestSafeTruncate_RuneBoundary ensures we don't cut a multi-byte
// glyph in half on the byte cap path.
func TestSafeTruncate_RuneBoundary(t *testing.T) {
	// "naïve" — the 'ï' is two bytes (0xC3 0xAF). Cutting at byte 3 of
	// "naï" would land mid-rune; safeTruncate must back off to byte 2.
	in := "naïve"
	got := safeTruncate(in, 3)
	if got != "na" {
		t.Errorf("safeTruncate(%q, 3) = %q, want %q", in, got, "na")
	}
	if got2 := safeTruncate(in, 100); got2 != in {
		t.Errorf("safeTruncate(%q, 100) = %q, want unchanged", in, got2)
	}
}
