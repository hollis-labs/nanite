package selftools

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/hollis-labs/nanite/internal/mcp"
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
func naniteToolDescribeDefinition() mcp.Tool {
	return mcp.Tool{
		Name: "tool_describe",
		Description: "Return a tool's contract on demand: description, input schema, golden examples, and related tools/skills.\n\n" +
			"**When to use:** When you're unsure about a tool's input shape, when you've never rendered a particular envelope `type` for `card_show`, or after a call fails with a schema-validation error. Cheap (registry + embed lookup, no LLM call) — prefer it to failing-and-retrying.\n\n" +
			"**When NOT to use:** Skip this when you have already called the tool successfully in the same session, or when the tool is from a third-party MCP server (this only describes built-in self-server tools at v1).\n\n" +
			"**Output shape:** {name, description, input_schema, examples: [{title, args, result?, notes?}], related_tools?: [string], related_skills?: [string]}.\n\n" +
			"**Unknown tool name:** Returns a structured error with `closest_matches` (Levenshtein) so you can correct typos in one round-trip.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "The exact tool name to describe, e.g. \"card_show\" or \"todo_create\".",
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
	"card_show": {
		relatedTools: []string{"panel_open"},
	},
	"todo_create": {
		relatedTools: []string{"todo_update", "todo_list", "plan_create"},
	},
	"todo_update": {
		relatedTools: []string{"todo_create", "todo_list"},
	},
	"todo_list": {
		relatedTools: []string{"todo_create", "todo_update"},
	},
	"plan_create": {
		relatedTools: []string{"plan_update", "plan_step_add", "plan_list", "plan_get", "todo_create"},
	},
	"plan_update": {
		relatedTools: []string{"plan_step_add", "plan_create", "plan_get"},
	},
	"plan_step_add": {
		relatedTools: []string{"plan_create", "plan_update", "plan_get"},
	},
	"plan_list": {
		relatedTools: []string{"plan_get", "plan_create"},
	},
	"plan_get": {
		relatedTools: []string{"plan_update", "plan_step_add", "plan_list"},
	},
	"plan_delete": {
		relatedTools: []string{"plan_list"},
	},
	"skill_create":  {relatedTools: []string{"skill_list", "skill_update", "skill_delete"}},
	"skill_list":    {relatedTools: []string{"skill_create", "skill_update"}},
	"skill_update":  {relatedTools: []string{"skill_list", "skill_delete"}},
	"skill_delete":  {relatedTools: []string{"skill_list"}},
	"agent_create":  {relatedTools: []string{"agent_list", "agent_update"}},
	"agent_list":    {relatedTools: []string{"agent_create", "agent_update"}},
	"agent_update":  {relatedTools: []string{"agent_list"}},
	"message_send":  {relatedTools: []string{"message_inbox", "message_thread", "message_ack"}},
	"message_inbox": {relatedTools: []string{"message_ack", "message_resolve", "message_thread"}},
	"subagent_spawn": {
		relatedTools: []string{"subagent_status", "subagent_cancel", "background_job"},
	},
	"background_job": {
		relatedTools: []string{"background_status", "background_cancel", "subagent_spawn"},
	},
	"reminder_set":  {relatedTools: []string{"context_pin", "context_unpin"}},
	"context_pin":   {relatedTools: []string{"reminder_set", "context_unpin"}},
	"context_unpin": {relatedTools: []string{"context_pin"}},
	"panel_open":    {relatedTools: []string{"panel_close", "signal_mode", "card_show"}},
	"panel_close":   {relatedTools: []string{"panel_open"}},
	"signal_mode":   {relatedTools: []string{"panel_open"}},
	"task_execute": {
		relatedTools: []string{"subagent_spawn", "background_job", "dispatch_executor"},
	},
	// CW-20260429-0036 (B2 closing piece) — dispatch_executor is the
	// agent-facing destination for the executor-handoff. Cross-reference
	// task_execute (the spawn-a-role-agent dispatch primitive — different
	// lane, sometimes confusable) and tool_describe (the agent reads the
	// describe payload to learn the contract).
	"dispatch_executor": {
		relatedTools: []string{"task_execute", "tool_describe"},
	},
	// D1 (CW-20260429-0009) — lesson_capture bundles with the layer 1/2
	// self-tools (describe + validate) because they form the
	// discover → validate → remember cluster the harness prompt teaches.
	"lesson_capture": {
		relatedTools: []string{"tool_validate", "tool_describe"},
	},
}

// callToolDescribe handles tool_describe. It looks up the tool by
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
func (st *SelfToolsTransport) callToolDescribe(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
	name := strArg(args, "name", "")
	if name == "" {
		return mcp.ErrorResult("name is required"), nil
	}

	// selfToolDefinitions() already includes tool_describe (see
	// internal/mcp/self_tools.go), so a self-introspective call falls
	// through the normal lookup path — no need to append it here.
	defs := selfToolDefinitions()

	var match *mcp.Tool
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
			"error":           "tool_not_found",
			"name":            name,
			"closest_matches": closest,
			"hint":            "Pick one of closest_matches and re-call tool_describe with that exact name. If none match, the tool is not on the v1 self-tool surface (this tool does not describe plugin-shipped or third-party MCP tools).",
		}
		out, _ := json.Marshal(payload)
		return mcp.ErrorResult(string(out)), nil
	}

	examples, err := loadGoldenExamples(match.Name)
	if err != nil {
		// Malformed example file is a server-side bug — surface it loudly
		// but still return the rest of the contract so the caller is
		// not stuck without a schema.
		return mcp.ErrorResult(fmt.Sprintf("describe %s: %v", match.Name, err)), nil
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
		return mcp.ErrorResult(fmt.Sprintf("describe %s: marshal: %v", match.Name, err)), nil
	}
	return mcp.TextResult(string(body)), nil
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
