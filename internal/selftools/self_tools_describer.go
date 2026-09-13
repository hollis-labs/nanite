// self_tools_describer.go — per-call description Describers for the
// initial adopter set in SP-20260512-0008 W1B (CW-20260512-0105):
// task_execute, tool_list, skill_list.
//
// Each Describer renders the tool's description with the caller's
// identity + dispatch allowlist threaded in. The static fallback
// description (selfToolDefinitions in self_tools.go) is what the LLM
// sees if no Describer is registered for the tool — the registrations
// here are the opt-in upgrade.
//
// Downstream consumer: W2A Agent Broker (CW-20260512-0107) populates
// CallerAgent.DispatchAllowlist from the parent agent's role-derived
// allowlist. Until that ticket merges, DispatchAllowlist is empty for
// every caller and the Describers degrade gracefully — they render a
// baseline description without role enumeration (still useful as the
// Describe hook is exercised, just without the per-caller variation).
//
// The two-caller acceptance test (describer_test.go in toolclient) does
// not depend on W2A: it constructs CallerAgent directly with synthetic
// DispatchAllowlist values so the Describer's behavior is verifiable
// in isolation from the W2A wiring.

package selftools

import (
	"context"
	"sort"
	"strings"

	"github.com/hollis-labs/nanite/internal/describer"
)

// taskExecuteBaseDescription is the canonical static body of the
// task_execute tool description — referenced from BOTH the
// registration-time tool definition in selfToolDefinitions() AND the
// per-call describeTaskExecute Describer below. Single source of truth:
// any edit lands in both surfaces automatically. The Describer appends
// a "Dispatchable roles:" section when the caller's DispatchAllowlist
// is non-empty; an empty allowlist yields this baseline unchanged.
const taskExecuteBaseDescription = "Dispatch a task to a Worker or Planner role agent. The Chat agent (harness) calls this when the user's request needs concrete execution — file edits, tool runs, code work, planning — instead of a direct conversational reply.\n\n" +
	"**When to use:** When the user asks for any work that requires tool calls beyond Chat's static surface (todos / plans / scratchpad / messaging / narration / executeTask itself). Examples: \"fix the bug\", \"audit X\", \"refactor Y\", \"build Z\".\n\n" +
	"**When NOT to use:** Trivial conversational replies (\"thanks\", \"what does X mean\"). The Chat harness handles those directly without dispatch.\n\n" +
	"**Behavior:** ScopeTier classifies the request, selects a Role (Worker for execution, Planner for open-scope breakdown), spawns the role agent with its own task-appropriate tool surface, captures the result, and returns it as a structured envelope. The Chat agent's context never sees raw worker output — only the envelope.\n\n" +
	"**Required context:** session_id (your current session) and message (the task to dispatch). parent_agent_id, provider, and timeout_seconds are optional overrides.\n\n" +
	"**Output shape:** A structured envelope JSON the harness relays to the user. The envelope's `type` describes the result shape (report-card, document-viewer, etc.).\n\n" +
	"**Static surface note:** This is the ONLY way the Chat harness dispatches work. Do not expect raw spawn / shell / file tools — those are not on Chat's surface."

// describeTaskExecute renders the task_execute description for the given
// caller. The base body is constant; when the caller has a non-empty
// DispatchAllowlist, a "Dispatchable roles:" section enumerates the
// permitted role slugs alphabetically so the agent can pick directly
// instead of inferring permission.
//
// Why enumerate here instead of in the schema? Anthropic tool schemas
// accept enum constraints on input fields, but task_execute's `message`
// arg is free-form text — the role is selected by ScopeTier on the
// server side. The right place to surface "which roles are reachable"
// is the description the agent reads before picking a tool.
func describeTaskExecute(_ context.Context, caller describer.CallerAgent) string {
	if len(caller.DispatchAllowlist) == 0 {
		// Fallback: empty allowlist → baseline description. Keeps the
		// surface stable for callers without explicit allowlist plumbing
		// (everyone, until W2A Agent Broker lands).
		return taskExecuteBaseDescription
	}
	roles := append([]string(nil), caller.DispatchAllowlist...)
	sort.Strings(roles)
	return taskExecuteBaseDescription +
		"\n\n**Dispatchable roles for this caller:** " + strings.Join(roles, ", ") +
		". Pass the role hint in `message` or let ScopeTier infer it; roles outside this list are not reachable from this caller."
}

// toolListBaseDescription is the canonical description for tool_list,
// referenced from BOTH the registration-time naniteToolListDefinition()
// AND the per-call describeToolList Describer below. Single source of
// truth: any edit lands in both surfaces automatically.
const toolListBaseDescription = "Lists registered built-in and connected MCP tools by name and one-line summary. Returns the full inventory regardless of caller — actual reachability for any specific tool is governed by agent permissions, the dev-mode gate, and project/session policy, not by this output.\n\n" +
	"**Contract:** input `{filter?: string}` (optional case-insensitive substring matched against BOTH name and summary). Output `{tools: [{name, summary}], count}`.\n\n" +
	"**When to use:** Browse the catalog when you're not sure which tool to reach for, or confirm a tool name exists before calling it. Use `tool_describe` next for the full schema of a specific tool, and `request_tools` to load a tool for use in the current turn.\n\n" +
	"**Example:** `tool_list({filter:\"reminder\"}) → {tools:[{name:\"reminder_set\", summary:\"Schedule a reminder for the user at a specific time.\"}], count:1}`.\n\n" +
	"**See also:** `tool_describe(name=\"<tool>\")` for the full contract (schema, golden examples, related tools) of any single tool returned here."

// describeToolList renders tool_list's description per caller. When the
// caller has a Slug, a one-line note clarifies that the inventory
// returned is unfiltered — actual reachability still depends on the
// caller's per-agent permission set. This addresses the c120-class
// confusion where the LLM read "full inventory" and concluded every
// listed tool was reachable; the per-caller render makes the gap
// explicit at the point of decision.
func describeToolList(_ context.Context, caller describer.CallerAgent) string {
	if caller.Slug == "" {
		return toolListBaseDescription
	}
	return toolListBaseDescription +
		"\n\n**Caller context:** you are agent `" + caller.Slug +
		"` — the inventory below is the full registry; use `tool_describe` for its schema and `request_tools` to load it subject to your grants."
}

// skillListBaseDescription is the canonical description for skill_list,
// referenced from the registration-time selfToolDefinitions() entry.
// The describeSkillList Describer below currently falls through
// (returns "") so the static description is what reaches the LLM — but
// the constant is the single source of truth for when a future sprint
// opts the Describer in to a per-caller variant; the registration site
// will pick up the same base automatically.
const skillListBaseDescription = "List all skills, optionally filtered by category.\n\n" +
	"**When to use:** When the user asks what skills are available, or before creating a skill to check for duplicates.\n\n" +
	"**Output shape:** Text list of skills with name, slug, category, and description. Empty list if none match the filter."

// describeSkillList renders skill_list's description per caller. v1
// returns the base description unchanged — skills are not currently
// scoped per caller in a way the description needs to advertise. The
// Describer is wired now (per ticket spec — initial adopters list)
// so W2A and later sprints have a registered hook to extend; the
// fall-through-on-empty contract in RenderDescriptions means the
// agent surface is unaffected today.
func describeSkillList(_ context.Context, _ describer.CallerAgent) string {
	// Explicit fall-through: empty string signals "use static
	// description." Equivalent to not registering at all for v1, but
	// the registration is kept so siblings can extend without touching
	// the wiring graph in cmd/nanite/main.go again.
	return ""
}

// RegisterSelfToolDescribers attaches the per-call Describers for the
// initial adopter set (task_execute, tool_list, skill_list) to the
// supplied DescriberRegistry. Called once at startup from
// cmd/nanite/main.go after the ToolClient is constructed. Safe to call
// multiple times — Register is last-write-wins by design.
//
// Adding a new opt-in Describer for an additional self-tool: add the
// renderer function above and append a Register call here. No other
// wiring changes are needed.
func RegisterSelfToolDescribers(reg *describer.Registry) {
	if reg == nil {
		return
	}
	reg.Register("task_execute", describer.Func(describeTaskExecute))
	reg.Register("tool_list", describer.Func(describeToolList))
	reg.Register("skill_list", describer.Func(describeSkillList))
}
