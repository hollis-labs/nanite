package mcp

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// goldenExampleFiles embeds the per-tool golden examples so the runtime
// describe handler does not depend on a filesystem layout outside the
// binary. Files live at internal/mcp/examples/<tool_name>.json with shape:
//
//	[{"title": string, "args": object, "result"?: any, "notes"?: string}]
//
// CW-20260429-0005 (A1 — self-healing tool surface, Layer 1 discovery).
//
//go:embed examples/*.json
var goldenExampleFiles embed.FS

// goldenExample is the on-disk shape for a single example entry.
type goldenExample struct {
	Title  string         `json:"title"`
	Args   map[string]any `json:"args"`
	Result any            `json:"result,omitempty"`
	Notes  string         `json:"notes,omitempty"`
}

// loadGoldenExamples reads internal/mcp/examples/<toolName>.json and
// returns the parsed entries. Returns (nil, nil) when the file does not
// exist — examples are best-effort, the handler still describes the tool
// without them. Returns a non-nil error only on a malformed file.
func loadGoldenExamples(toolName string) ([]goldenExample, error) {
	path := "examples/" + toolName + ".json"
	raw, err := goldenExampleFiles.ReadFile(path)
	if err != nil {
		// fs.ErrNotExist or any other read error — treat as "no examples
		// available" so the describe call still succeeds. The caller
		// distinguishes "tool not found" from "tool exists, no examples".
		return nil, nil
	}
	var out []goldenExample
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse examples for %q: %w", toolName, err)
	}
	return out, nil
}

// naniteToolDescribeDefinition is the self-tool that returns a tool's
// contract on demand: schema + golden examples + related tools/skills.
// It exists so an agent can introspect any internal tool when uncertain
// about input shape, instead of repeatedly failing the call. The system
// prompt nudges agents toward this tool.
//
// CW-20260429-0005 (A1 — Layer 1 of the self-healing tool surface lens).
func naniteToolDescribeDefinition() Tool {
	return Tool{
		Name: "nanite_tool_describe",
		Description: "Return a tool's contract on demand: description, input schema, golden examples, and related tools/skills.\n\n" +
			"**When to use:** When you are about to call an internal tool and you are unsure about its input shape — call this FIRST. It returns the schema plus 1-3 golden examples. Cheaper than failing the real call repeatedly.\n\n" +
			"**When NOT to use:** Skip this when you have already called the tool successfully in the same session, or when the tool is from a third-party MCP server (this only describes nanite_* self-tools at v1).\n\n" +
			"**Output shape:** {name, description, input_schema, examples: [{title, args, result?, notes?}], related_tools?: [string], related_skills?: [string]}.\n\n" +
			"**Unknown tool name:** Returns a structured error with `closest_matches` (Levenshtein) so you can correct typos in one round-trip.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "The exact tool name to describe, e.g. \"nanite_show_card\" or \"nanite_todo_create\".",
				},
			},
			"required": []string{"name"},
		},
	}
}

// describeRelations holds the curated cross-references for a tool. These
// are hand-written rather than derived from schemas so the surface stays
// useful (a derived list quickly devolves into "every tool with the same
// prefix"). Keep entries thin — chat-loop pressure rewards selectivity.
//
// Both lists are best-effort: nil/empty values are dropped from the
// describe result rather than rendered as empty arrays.
var describeRelations = map[string]struct {
	relatedTools  []string
	relatedSkills []string
}{
	"nanite_show_card": {
		relatedTools: []string{"nanite_giphy_search", "nanite_panel_open"},
	},
	"nanite_giphy_search": {
		relatedTools: []string{"nanite_show_card"},
	},
	"nanite_todo_create": {
		relatedTools: []string{"nanite_todo_update", "nanite_todo_list", "nanite_plan_create"},
	},
	"nanite_todo_update": {
		relatedTools: []string{"nanite_todo_create", "nanite_todo_list"},
	},
	"nanite_todo_list": {
		relatedTools: []string{"nanite_todo_create", "nanite_todo_update"},
	},
	"nanite_plan_create": {
		relatedTools: []string{"nanite_plan_update", "nanite_plan_step_add", "nanite_plan_list", "nanite_plan_get", "nanite_todo_create"},
	},
	"nanite_plan_update": {
		relatedTools: []string{"nanite_plan_step_add", "nanite_plan_create", "nanite_plan_get"},
	},
	"nanite_plan_step_add": {
		relatedTools: []string{"nanite_plan_create", "nanite_plan_update", "nanite_plan_get"},
	},
	"nanite_plan_list": {
		relatedTools: []string{"nanite_plan_get", "nanite_plan_create"},
	},
	"nanite_plan_get": {
		relatedTools: []string{"nanite_plan_update", "nanite_plan_step_add", "nanite_plan_list"},
	},
	"nanite_plan_delete": {
		relatedTools: []string{"nanite_plan_list"},
	},
	"nanite_create_skill":  {relatedTools: []string{"nanite_list_skills", "nanite_update_skill", "nanite_delete_skill"}},
	"nanite_list_skills":   {relatedTools: []string{"nanite_create_skill", "nanite_update_skill"}},
	"nanite_update_skill":  {relatedTools: []string{"nanite_list_skills", "nanite_delete_skill"}},
	"nanite_delete_skill":  {relatedTools: []string{"nanite_list_skills"}},
	"nanite_create_agent":  {relatedTools: []string{"nanite_list_agents", "nanite_update_agent"}},
	"nanite_list_agents":   {relatedTools: []string{"nanite_create_agent", "nanite_update_agent"}},
	"nanite_update_agent":  {relatedTools: []string{"nanite_list_agents"}},
	"nanite_message_send":  {relatedTools: []string{"nanite_message_inbox", "nanite_message_thread", "nanite_message_ack"}},
	"nanite_message_inbox": {relatedTools: []string{"nanite_message_ack", "nanite_message_resolve", "nanite_message_thread"}},
	"nanite_spawn_subagent": {
		relatedTools: []string{"nanite_subagent_status", "nanite_subagent_cancel", "nanite_background_job"},
	},
	"nanite_background_job": {
		relatedTools: []string{"nanite_background_status", "nanite_background_cancel", "nanite_spawn_subagent"},
	},
	"nanite_set_reminder": {relatedTools: []string{"nanite_pin", "nanite_unpin"}},
	"nanite_pin":          {relatedTools: []string{"nanite_set_reminder", "nanite_unpin"}},
	"nanite_unpin":        {relatedTools: []string{"nanite_pin"}},
	"nanite_panel_open":   {relatedTools: []string{"nanite_panel_close", "nanite_signal_mode", "nanite_show_card"}},
	"nanite_panel_close":  {relatedTools: []string{"nanite_panel_open"}},
	"nanite_signal_mode":  {relatedTools: []string{"nanite_panel_open"}},
	"nanite_execute_task": {
		relatedTools: []string{"nanite_spawn_subagent", "nanite_background_job"},
	},
	// D1 (CW-20260429-0009) — nanite_remember bundles with the layer 1/2
	// self-tools (describe + validate) because they form the
	// discover → validate → remember cluster the harness prompt teaches.
	"nanite_remember": {
		relatedTools: []string{"nanite_validate", "nanite_tool_describe"},
	},
}

// callToolDescribe handles nanite_tool_describe. It looks up the tool by
// exact name in selfToolDefinitions(), loads any embedded golden examples,
// and returns a single JSON object with the tool's contract. On miss it
// returns a structured "tool_not_found" payload with the three closest
// matches by Levenshtein distance — the agent can read those, pick one,
// and re-describe in a second round-trip without guessing.
//
// D1 (CW-20260429-0009) — when a LearningRecaller is wired, the result
// also includes a `prior_learnings` field with up to
// learnings.MaxRecallHints captured lessons for the named tool. This is
// the "lesson recall during similar tool selection" surface from the
// lens (Layer 4): the agent that calls describe to learn how to invoke
// a tool also reads past lessons inline, with no extra round-trip.
func (st *SelfToolsTransport) callToolDescribe(ctx context.Context, args map[string]any) (*ToolResult, error) {
	name := strArg(args, "name", "")
	if name == "" {
		return errorResult("name is required"), nil
	}

	// selfToolDefinitions() already includes nanite_tool_describe (see
	// internal/mcp/self_tools.go), so a self-introspective call falls
	// through the normal lookup path — no need to append it here.
	defs := selfToolDefinitions()

	var match *Tool
	names := make([]string, 0, len(defs))
	for i := range defs {
		names = append(names, defs[i].Name)
		if defs[i].Name == name {
			match = &defs[i]
		}
	}

	if match == nil {
		closest := closestToolNames(name, names, 3)
		payload := map[string]any{
			"error":            "tool_not_found",
			"name":             name,
			"closest_matches":  closest,
			"hint":             "Pick one of closest_matches and re-call nanite_tool_describe with that exact name. If none match, the tool is not on the v1 self-tool surface (this tool does not describe plugin-shipped or third-party MCP tools).",
		}
		out, _ := json.Marshal(payload)
		return errorResult(string(out)), nil
	}

	examples, err := loadGoldenExamples(match.Name)
	if err != nil {
		// Malformed example file is a server-side bug — surface it loudly
		// but still return the rest of the contract so the caller is
		// not stuck without a schema.
		return errorResult(fmt.Sprintf("describe %s: %v", match.Name, err)), nil
	}

	rels := describeRelations[match.Name]
	out := map[string]any{
		"name":         match.Name,
		"description":  match.Description,
		"input_schema": match.InputSchema,
	}
	if examples != nil {
		out["examples"] = examples
	} else {
		// Always include the field so callers can discriminate "tool exists
		// but no examples on file" from a malformed describe payload.
		out["examples"] = []goldenExample{}
	}
	if len(rels.relatedTools) > 0 {
		out["related_tools"] = rels.relatedTools
	}
	if len(rels.relatedSkills) > 0 {
		out["related_skills"] = rels.relatedSkills
	}

	// D1 (CW-20260429-0009): surface prior tool-use learnings inline.
	// Failing-open: nil/missing recaller, empty results, or any
	// error → the field is omitted. The agent's prompt already nudges
	// it to call describe before unfamiliar tools, so this layer is
	// load-bearing for the self-healing loop.
	//
	// Plumb the tool-call ctx through so cancellation/deadlines from
	// the parent request propagate. user_id is optional — empty falls
	// back to learnings.DefaultUserID inside RecallByToolName, so
	// single-user dogfood Just Works.
	userID := strArg(args, "user_id", "")
	if hints := st.RecallToolLearnings(ctx, userID, match.Name); len(hints) > 0 {
		surfaced := make([]map[string]any, 0, len(hints))
		for _, h := range hints {
			surfaced = append(surfaced, map[string]any{
				"hint":       h.Summary,
				"confidence": h.Confidence,
			})
		}
		out["prior_learnings"] = surfaced
	}

	body, err := json.Marshal(out)
	if err != nil {
		return errorResult(fmt.Sprintf("describe %s: marshal: %v", match.Name, err)), nil
	}
	return textResult(string(body)), nil
}

// closestToolNames returns up to limit names from candidates ranked by
// Levenshtein distance to query, ascending. Ties break by lexicographic
// order so the result is deterministic. An exact match would have come
// out at the top (distance 0) — but the caller has already excluded the
// exact-match path before calling this, so the worst case is "everything
// is far away" rather than "we recommend the same name back".
func closestToolNames(query string, candidates []string, limit int) []string {
	type scored struct {
		name string
		dist int
	}
	scoredList := make([]scored, 0, len(candidates))
	for _, c := range candidates {
		scoredList = append(scoredList, scored{name: c, dist: levenshtein(query, c)})
	}
	sort.Slice(scoredList, func(i, j int) bool {
		if scoredList[i].dist != scoredList[j].dist {
			return scoredList[i].dist < scoredList[j].dist
		}
		return scoredList[i].name < scoredList[j].name
	})
	if limit > len(scoredList) {
		limit = len(scoredList)
	}
	out := make([]string, limit)
	for i := 0; i < limit; i++ {
		out[i] = scoredList[i].name
	}
	return out
}

// levenshtein computes the classic edit-distance between two strings. Used
// to suggest closest tool names on miss. Comparison is case-insensitive
// because mistyped tool names ("Nanite_Show_Card") still benefit from the
// hint, and our tool names are ASCII-only so a simple ToLower normalisation
// is safe.
func levenshtein(a, b string) int {
	a = strings.ToLower(a)
	b = strings.ToLower(b)
	if a == b {
		return 0
	}
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	// Two-row DP. We only need the previous row to compute the next.
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := 0; j <= len(b); j++ {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			m := del
			if ins < m {
				m = ins
			}
			if sub < m {
				m = sub
			}
			curr[j] = m
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}
