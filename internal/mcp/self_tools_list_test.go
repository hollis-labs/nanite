package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/dispatch"
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
		if d.Name == "nanite_tool_list" {
			found = true
			if d.InputSchema == nil {
				t.Fatal("nanite_tool_list missing InputSchema")
			}
			// filter is optional — schema must NOT mark it required.
			if reqd, ok := d.InputSchema["required"].([]string); ok && len(reqd) > 0 {
				t.Errorf("nanite_tool_list must have no required fields, got %v", reqd)
			}
			break
		}
	}
	if !found {
		t.Fatal("nanite_tool_list not in selfToolDefinitions()")
	}

	st := newSelfTools(t)
	res, err := st.CallTool(context.Background(), "nanite_tool_list", map[string]any{})
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
	// Spot-check: the surface includes nanite_tool_describe (sibling
	// discovery primitive) and nanite_tool_list itself.
	saw := map[string]string{}
	for _, t := range out.Tools {
		saw[t.Name] = t.Summary
	}
	if _, ok := saw["nanite_tool_describe"]; !ok {
		t.Error("inventory missing nanite_tool_describe")
	}
	if _, ok := saw["nanite_tool_list"]; !ok {
		t.Error("inventory missing nanite_tool_list (self-include)")
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
// guessed `nanite_reminder_create` instead of `nanite_set_reminder`.
func TestNaniteToolList_FilterNarrowsByNameAndSummary(t *testing.T) {
	st := newSelfTools(t)
	// Substring match in NAME — `set_reminder`.
	res, err := st.CallTool(context.Background(), "nanite_tool_list", map[string]any{
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
		t.Fatal("expected ≥1 match for filter=reminder (nanite_set_reminder must surface)")
	}
	sawSetReminder := false
	for _, tool := range out.Tools {
		if tool.Name == "nanite_set_reminder" {
			sawSetReminder = true
		}
		// Every survivor must contain the filter token in name OR summary.
		if !strings.Contains(strings.ToLower(tool.Name), "reminder") &&
			!strings.Contains(strings.ToLower(tool.Summary), "reminder") {
			t.Errorf("filter leaked tool %q without 'reminder' in name or summary (summary=%q)", tool.Name, tool.Summary)
		}
	}
	if !sawSetReminder {
		t.Error("filter=reminder must surface nanite_set_reminder (the c120 motivating case)")
	}

	// Case-insensitive: "REMINDER" should match the same set as "reminder".
	resUC, err := st.CallTool(context.Background(), "nanite_tool_list", map[string]any{
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
	res, err := st.CallTool(context.Background(), "nanite_tool_list", map[string]any{
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
	// "skill" appears in nanite_create_skill / list / update / delete
	// names directly — pick a token that's likely only in summaries.
	// The remember tool's first sentence mentions "lesson"; no tool is
	// named "lesson".
	res, _ := st.CallTool(context.Background(), "nanite_tool_list", map[string]any{
		"filter": "lesson",
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
		t.Fatal("expected at least one tool whose summary contains 'lesson' (e.g. nanite_remember)")
	}
	// At least one survivor's name must NOT contain "lesson" — proving
	// the summary-side match path fired.
	matchedViaSummary := false
	for _, tool := range out.Tools {
		if !strings.Contains(strings.ToLower(tool.Name), "lesson") {
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
	res, err := st.CallTool(context.Background(), "nanite_tool_list", map[string]any{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	body := res.Content[0].Text
	t.Logf("nanite_tool_list unfiltered: %d bytes", len(body))

	// Filtered "reminder" — should be small.
	resF, _ := st.CallTool(context.Background(), "nanite_tool_list", map[string]any{
		"filter": "reminder",
	})
	t.Logf("nanite_tool_list filter=\"reminder\": %d bytes", len(resF.Content[0].Text))
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
// the agent burned 6 list calls trying to find `nanite_memory_recall`
// (registered on `nanite-memory`) and concluded it didn't exist.
func TestNaniteToolList_CrossServerEnumeration(t *testing.T) {
	st := newSelfTools(t)
	// Stub an inventory that looks like the real composite surface:
	// a self-tool, two memory tools, and one off-surface tool that
	// must NOT survive the chat-surface filter.
	st.Inventory = &stubInventoryLookup{
		tools: []provider.ToolDefinition{
			{
				Name:        "nanite_memory_recall",
				Description: "Recall memories relevant to the current turn from the durable Vanta substrate.",
			},
			{
				Name:        "nanite_memory_save",
				Description: "Save a memory for future sessions.",
			},
			{
				// Self-tool advertised via the manager too — the dedup
				// guard in gatherInventory should keep this single-entry.
				Name:        "nanite_remember",
				Description: "Persist a one-sentence lesson to durable memory.",
			},
			{
				// Off-surface (Worker tool, dev_*) — must be filtered out.
				Name:        "dev_read",
				Description: "Read a file from the developer-mode allowed paths.",
			},
		},
	}

	res, err := st.CallTool(context.Background(), "nanite_tool_list", map[string]any{
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

	// nanite_memory_recall MUST surface — the c121 motivating case.
	// It's in dispatch.ChatToolSurface as a literal entry.
	if !saw["nanite_memory_recall"] {
		t.Error("filter=memory must surface nanite_memory_recall (the cross-server bug fix)")
	}
	// nanite_memory_save is on `nanite-memory` server but NOT on the
	// Chat surface (ChatToolSurface lists nanite_memory_recall as a
	// literal — there is no `nanite_memory_` prefix). The chat-surface
	// filter must drop it; the agent can't invoke it from chat anyway.
	if saw["nanite_memory_save"] {
		t.Error("nanite_memory_save is off-surface; chat-surface filter must drop it")
	}
	// Sibling self-tool with `memory` in summary should surface
	// (nanite_remember mentions "durable memory").
	if !saw["nanite_remember"] {
		t.Error("filter=memory must surface nanite_remember (summary contains 'memory')")
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
				Name:        "nanite_tool_list",
				Description: "Stub description from the manager that should win on dedup.",
			},
		},
	}

	res, err := st.CallTool(context.Background(), "nanite_tool_list", map[string]any{
		"filter": "nanite_tool_list",
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
		if tool.Name == "nanite_tool_list" {
			hits++
		}
	}
	if hits != 1 {
		t.Errorf("nanite_tool_list appeared %d times; want exactly 1 (dedup across sources)", hits)
	}
}

// TestNaniteToolList_FiltersOffSurfaceTools is the negative half of the
// CW-20260501-0001 fix: tools published by the manager that are NOT on
// the Chat agent's static surface (dispatch.IsChatSurfaceTool == false)
// must be omitted from the list. Off-surface tools the agent can't
// actually invoke would be a misleading discovery primitive.
func TestNaniteToolList_FiltersOffSurfaceTools(t *testing.T) {
	st := newSelfTools(t)
	st.Inventory = &stubInventoryLookup{
		tools: []provider.ToolDefinition{
			{
				Name:        "dev_read",
				Description: "Read a file. Worker-only.",
			},
			{
				Name:        "dev_bash",
				Description: "Run a bash command. Worker-only.",
			},
			{
				Name:        "memory_write",
				Description: "Mux memory_write — third-party uniform name. Worker-only.",
			},
			{
				// On-surface — the control case.
				Name:        "nanite_memory_recall",
				Description: "Recall memories from Vanta.",
			},
		},
	}

	res, err := st.CallTool(context.Background(), "nanite_tool_list", map[string]any{})
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

	for _, tool := range out.Tools {
		switch tool.Name {
		case "dev_read", "dev_bash", "memory_write":
			t.Errorf("off-surface tool %q leaked into nanite_tool_list output", tool.Name)
		}
	}

	// Sanity check: the on-surface stub tool DID survive.
	saw := false
	for _, tool := range out.Tools {
		if tool.Name == "nanite_memory_recall" {
			saw = true
			break
		}
	}
	if !saw {
		t.Error("expected nanite_memory_recall to survive the chat-surface filter")
	}
}

// TestNaniteToolList_CrossServerSizeMeasurement documents the actual
// payload size when the cross-server inventory is wired (the realistic
// production shape). Used by CW-20260501-0001 to verify the fix doesn't
// blow past the chat-surface ceiling tracked in CW-20260430-0007. The
// test does NOT fail on size — see TestNaniteToolList_UnfilteredSize.
func TestNaniteToolList_CrossServerSizeMeasurement(t *testing.T) {
	st := newSelfTools(t)
	// Approximate the production composite: self surface + memory tools
	// + a couple plugin tools. All on-surface (filtered to chat surface).
	st.Inventory = &stubInventoryLookup{
		tools: []provider.ToolDefinition{
			{
				Name:        "nanite_memory_recall",
				Description: "Recall memories relevant to the current turn from durable storage.",
			},
		},
	}
	res, err := st.CallTool(context.Background(), "nanite_tool_list", map[string]any{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	body := res.Content[0].Text
	t.Logf("nanite_tool_list cross-server unfiltered: %d bytes", len(body))

	resF, _ := st.CallTool(context.Background(), "nanite_tool_list", map[string]any{
		"filter": "memory",
	})
	t.Logf("nanite_tool_list cross-server filter=\"memory\": %d bytes", len(resF.Content[0].Text))
}

// TestNaniteToolList_WorkerSurfaceSeesOffChatTools is the positive half
// of the CW-20260501-0012 fix: when the caller's dispatch role is
// RoleWorker, off-chat-surface tools (dev_*, general_*, code_*, third-
// party MCP) MUST surface in the discovery primitive. Worker agents have
// profile-owned permissions, not a dispatch-side allow-list — filtering
// them through IsChatSurfaceTool would replicate the CW-20260501-0001
// bug for the Worker role.
func TestNaniteToolList_WorkerSurfaceSeesOffChatTools(t *testing.T) {
	st := newSelfTools(t)
	st.Inventory = &stubInventoryLookup{
		tools: []provider.ToolDefinition{
			{
				Name:        "dev_read",
				Description: "Read a file from the developer-mode allowed paths.",
			},
			{
				Name:        "dev_bash",
				Description: "Run a bash command.",
			},
			{
				Name:        "general_web_fetch",
				Description: "Fetch a URL and return the response body.",
			},
			{
				Name:        "memory_write",
				Description: "Write a memory to the durable substrate.",
			},
			{
				// On-chat-surface — should always survive.
				Name:        "nanite_memory_recall",
				Description: "Recall memories from Vanta.",
			},
		},
	}

	// Stamp Worker role on ctx — this is what executeToolBatch does for
	// non-chat-role agents.
	ctx := WithCallerRole(context.Background(), dispatch.RoleWorker)

	res, err := st.callToolList(ctx, map[string]any{})
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

	// Worker MUST see off-chat-surface tools — that's the whole point.
	for _, name := range []string{"dev_read", "dev_bash", "general_web_fetch", "memory_write"} {
		if !saw[name] {
			t.Errorf("Worker surface missing off-chat tool %q (CW-0012 regression — surface filter still active)", name)
		}
	}
	// On-chat-surface tools also survive (worker is a superset of chat
	// for discovery purposes).
	if !saw["nanite_memory_recall"] {
		t.Error("Worker surface missing nanite_memory_recall")
	}
}

// TestNaniteToolList_ChatSurfaceFiltersOffChatTools is the negative half
// of CW-20260501-0012: when the caller's dispatch role is RoleChat,
// off-chat-surface tools MUST be filtered out (the pre-CW-0012
// behavior is preserved). This pins CW-20260501-0001's contract for
// the chat surface specifically.
func TestNaniteToolList_ChatSurfaceFiltersOffChatTools(t *testing.T) {
	st := newSelfTools(t)
	st.Inventory = &stubInventoryLookup{
		tools: []provider.ToolDefinition{
			{
				Name:        "dev_read",
				Description: "Read a file. Worker-only.",
			},
			{
				Name:        "general_web_fetch",
				Description: "Fetch a URL.",
			},
			{
				Name:        "memory_write",
				Description: "Mux memory_write — third-party uniform name.",
			},
			{
				Name:        "nanite_memory_recall",
				Description: "Recall memories from Vanta.",
			},
		},
	}

	ctx := WithCallerRole(context.Background(), dispatch.RoleChat)
	res, err := st.callToolList(ctx, map[string]any{})
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

	for _, tool := range out.Tools {
		switch tool.Name {
		case "dev_read", "general_web_fetch", "memory_write":
			t.Errorf("RoleChat surface leaked off-chat tool %q (CW-0001 regression)", tool.Name)
		}
	}
	saw := false
	for _, tool := range out.Tools {
		if tool.Name == "nanite_memory_recall" {
			saw = true
		}
	}
	if !saw {
		t.Error("expected nanite_memory_recall to survive the chat-surface filter")
	}
}

// TestNaniteToolList_PlannerSurfaceMatchesWorker pins that RolePlanner
// uses the same no-filter discovery as RoleWorker. Planner agents
// (like Worker) have profile-owned permissions, so the discovery
// primitive must reflect that (no dispatch-side allow-list).
func TestNaniteToolList_PlannerSurfaceMatchesWorker(t *testing.T) {
	makeST := func() *SelfToolsTransport {
		st := newSelfTools(t)
		st.Inventory = &stubInventoryLookup{
			tools: []provider.ToolDefinition{
				{Name: "dev_read", Description: "Read a file."},
				{Name: "memory_write", Description: "Write a memory."},
				{Name: "nanite_memory_recall", Description: "Recall memories from Vanta."},
			},
		}
		return st
	}

	collectNames := func(ctx context.Context, st *SelfToolsTransport) map[string]bool {
		res, err := st.callToolList(ctx, map[string]any{})
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		var out struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		}
		if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		set := map[string]bool{}
		for _, tool := range out.Tools {
			set[tool.Name] = true
		}
		return set
	}

	worker := collectNames(WithCallerRole(context.Background(), dispatch.RoleWorker), makeST())
	planner := collectNames(WithCallerRole(context.Background(), dispatch.RolePlanner), makeST())

	if len(worker) != len(planner) {
		t.Fatalf("worker (%d) and planner (%d) surfaces differ in size", len(worker), len(planner))
	}
	for name := range worker {
		if !planner[name] {
			t.Errorf("planner surface missing %q (worker had it) — Worker/Planner must share the discovery view", name)
		}
	}
}

// TestNaniteToolList_UnstampedContextFallsBackToChatSurface verifies the
// safe default: when ctx carries no caller role (e.g. test paths that
// don't go through executeToolBatch, or early-init code), the discovery
// primitive falls back to the chat-surface filter — preserving
// CW-20260501-0001's behavior for any unstamped call site.
func TestNaniteToolList_UnstampedContextFallsBackToChatSurface(t *testing.T) {
	st := newSelfTools(t)
	st.Inventory = &stubInventoryLookup{
		tools: []provider.ToolDefinition{
			{Name: "dev_read", Description: "Off-chat tool."},
			{Name: "nanite_memory_recall", Description: "On-chat tool."},
		},
	}

	// bare context — no WithCallerRole stamping.
	res, err := st.callToolList(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	var out struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	_ = json.Unmarshal([]byte(res.Content[0].Text), &out)

	for _, tool := range out.Tools {
		if tool.Name == "dev_read" {
			t.Error("unstamped ctx must fall back to chat-surface filter; dev_read leaked")
		}
	}
	saw := false
	for _, tool := range out.Tools {
		if tool.Name == "nanite_memory_recall" {
			saw = true
		}
	}
	if !saw {
		t.Error("unstamped ctx still filters to chat surface; nanite_memory_recall should survive")
	}
}

// TestNaniteToolList_WorkerSurfaceSizeMeasurement documents the actual
// payload size on the Worker surface (CW-20260501-0012 acceptance
// criterion). Worker surface includes off-chat-surface tools so payload
// is expected to be larger than chat. Logged via t.Logf, not a hard
// failure — same posture as TestNaniteToolList_UnfilteredSize.
func TestNaniteToolList_WorkerSurfaceSizeMeasurement(t *testing.T) {
	st := newSelfTools(t)
	// Approximate a realistic worker inventory: self surface + memory
	// tools + dev_*, general_*, code_* builtins, plus a couple plugin
	// MCP tools.
	st.Inventory = &stubInventoryLookup{
		tools: []provider.ToolDefinition{
			{Name: "nanite_memory_recall", Description: "Recall memories from Vanta."},
			{Name: "memory_write", Description: "Write a memory to the durable substrate."},
			{Name: "memory_get", Description: "Get a memory by key."},
			{Name: "dev_read", Description: "Read a file from the developer-mode allowed paths."},
			{Name: "dev_write", Description: "Write a file."},
			{Name: "dev_edit", Description: "Edit a file via search/replace."},
			{Name: "dev_grep", Description: "Search for a pattern across files."},
			{Name: "dev_glob", Description: "Glob files matching a pattern."},
			{Name: "dev_bash", Description: "Run a bash command."},
			{Name: "general_web_fetch", Description: "Fetch a URL and return the response body."},
			{Name: "general_json_parse", Description: "Parse a JSON string into a structured value."},
			{Name: "general_datetime", Description: "Return the current date and time."},
			{Name: "nanite_code_execute", Description: "Execute code in a sandboxed environment."},
			{Name: "task_create", Description: "Create a task in the work tracker."},
			{Name: "task_list", Description: "List tasks in the work tracker."},
		},
	}

	chatCtx := WithCallerRole(context.Background(), dispatch.RoleChat)
	chatRes, _ := st.callToolList(chatCtx, map[string]any{})
	t.Logf("nanite_tool_list RoleChat unfiltered: %d bytes (count: chat-surface filtered)", len(chatRes.Content[0].Text))

	workerCtx := WithCallerRole(context.Background(), dispatch.RoleWorker)
	workerRes, _ := st.callToolList(workerCtx, map[string]any{})
	t.Logf("nanite_tool_list RoleWorker unfiltered: %d bytes (count: cross-server inventory)", len(workerRes.Content[0].Text))

	if len(workerRes.Content[0].Text) <= len(chatRes.Content[0].Text) {
		t.Logf("note: Worker payload (%d) not larger than Chat payload (%d) — usually means the stub inventory contains few off-chat tools",
			len(workerRes.Content[0].Text), len(chatRes.Content[0].Text))
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
