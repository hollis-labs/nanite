package mcpserver

import gmcpserver "github.com/hollis-labs/go-mcp/server"

// toolAnnotations is the MCP tool-annotation table for every self/dev tool
// this server registers. go-mcp/server's RegisterTool makes the four hint
// fields mandatory -- no tool may register without stating them -- and
// nothing in this codebase populated them before this table existed: dev
// and self tools are hand-built Go structs, never round-tripped through an
// MCP wire response the way a remote-discovered server's tools are (see
// Manager.ToolBehavior / convertSDKTool in internal/mcp, which populate
// Tool.Annotations only for that separate, already-correct path).
//
// Every entry below was assigned by reading its handler, not inferred from
// its name -- CW-20260918-0017's migration audit (the tool_get/skill_get
// name-vs-behavior mismatch it turned up is exactly the kind of bug a
// name-based inference would have produced). A handful of tools carry an
// explicit idempotency claim in their own doc comment (tool_validate,
// message_ack, subagent_cancel, background_cancel, scratchpad_clear,
// lesson_capture); those are cited inline. Everything else is a read of
// what the handler actually does.
//
// A name absent from this table gets safeAnnotationsDefault (fully
// mutating, non-idempotent, open-world) rather than a permissive default:
// per condmcp.Tool.Annotations' own doc comment, an approval gate keys off
// these hints, and "the way it fails is letting a write through" -- an
// unrecognized tool should look dangerous until someone adds a real entry,
// not slip through as read-only by omission.
var toolAnnotations = map[string]gmcpserver.ToolAnnotations{
	// --- dev transport (internal/mcp/dev_tools.go) ---
	"dev_read": {ReadOnlyHint: true, IdempotentHint: true},
	"dev_grep": {ReadOnlyHint: true, IdempotentHint: true},
	"dev_glob": {ReadOnlyHint: true, IdempotentHint: true},
	// Overwrites file content unconditionally.
	"dev_write": {DestructiveHint: true, IdempotentHint: true},
	// Find/replace: a repeat call fails once old_string is gone, so not
	// idempotent.
	"dev_edit": {DestructiveHint: true},
	// Arbitrary shell execution.
	"dev_bash": {DestructiveHint: true, OpenWorldHint: true},

	// --- self transport: discovery / meta ---
	"tool_describe": {ReadOnlyHint: true, IdempotentHint: true},
	"tool_list":     {ReadOnlyHint: true, IdempotentHint: true},
	// Doc comment: "read-only and side-effect-free: it never invokes the
	// named tool, never persists".
	"tool_validate": {ReadOnlyHint: true, IdempotentHint: true},
	"whoami":        {ReadOnlyHint: true, IdempotentHint: true},
	"procedure_get": {ReadOnlyHint: true, IdempotentHint: true},

	// --- skills ---
	"skill_list": {ReadOnlyHint: true, IdempotentHint: true},
	// Deletes both the catalog row and vendored files; irreversible.
	"skill_delete": {DestructiveHint: true},
	// Despite the name, can delegate to a subagent turn and execute an
	// inline `!`cmd`` marker in the resulting text -- not a plain getter.
	"skill_get": {OpenWorldHint: true},

	// --- agent profiles ---
	"agent_create": {},
	"agent_list":   {ReadOnlyHint: true, IdempotentHint: true},
	// Partial field update; same args repeated yield the same end state.
	"agent_update": {IdempotentHint: true},
	// Pure read: resolves and composes, never writes.
	"agent_source_resolve": {ReadOnlyHint: true, IdempotentHint: true},

	// --- agent workflows (MCP callback surface) ---
	"workflow_execute_llm_step": {OpenWorldHint: true},
	// Generic tool passthrough -- real read/write-ness depends on the
	// wrapped tool, so this stays conservative.
	"workflow_execute_tool_step": {OpenWorldHint: true},
	"workflow_verify_step":       {OpenWorldHint: true},
	"workflow_run":               {OpenWorldHint: true},

	// --- cross-app / presentation ---
	// Same page+filters twice is the same nav state.
	"engine_navigate": {IdempotentHint: true},
	"engine_refresh":  {IdempotentHint: true},
	// Builds/validates JSON and returns text; no store write, though it
	// drives UI panel routing as a side effect.
	"card_show":   {IdempotentHint: true},
	"panel_open":  {IdempotentHint: true},
	"panel_close": {IdempotentHint: true},
	"signal_mode": {IdempotentHint: true},

	// --- builder wizard ---
	"builder_start": {},
	// Final step creates a real agent/skill entity; a repeat call after
	// completion errors rather than repeating.
	"builder_step": {},

	// --- todo / plan ---
	"todo_create": {},
	"todo_update": {IdempotentHint: true},
	"todo_list":   {ReadOnlyHint: true, IdempotentHint: true},
	"plan_create": {},
	"plan_update": {IdempotentHint: true},
	// Appends steps; a repeat call appends duplicates.
	"plan_step_add": {},
	"plan_list":     {ReadOnlyHint: true, IdempotentHint: true},
	"plan_get":      {ReadOnlyHint: true, IdempotentHint: true},
	// "including all its steps. Irreversible."
	"plan_delete": {DestructiveHint: true},

	// --- install ---
	// force=true overwrites user-modified files; without force it reports
	// unchanged/skipped, but the tool as a whole is destructive-capable.
	"install_home": {DestructiveHint: true, IdempotentHint: true},
	// Scaffolds .nanite/NANITE.md in a project dir; adopt-mode is
	// idempotent-ish.
	"install_project": {IdempotentHint: true},
	// Currently a stub ("install diff not yet implemented") -- no
	// filesystem access at all today. Revisit once dry-run lands.
	"install_diff": {ReadOnlyHint: true, IdempotentHint: true},

	// --- messaging / handoff ---
	"message_send":   {},
	"message_inbox":  {ReadOnlyHint: true, IdempotentHint: true},
	"message_thread": {ReadOnlyHint: true, IdempotentHint: true},
	// Doc comment: "Idempotent — safe to call multiple times."
	"message_ack":      {IdempotentHint: true},
	"message_resolve":  {IdempotentHint: true},
	"message_catch_up": {ReadOnlyHint: true, IdempotentHint: true},
	"handoff_request":  {},
	"handoff_approve":  {},
	"handoff_reject":   {},
	// Always mints a new uuid per call, even for identical payloads.
	"handoff_stash":           {},
	"handoff_pointers_expand": {ReadOnlyHint: true, IdempotentHint: true},

	// --- subagent / dispatch / background ---
	"subagent_spawn":  {OpenWorldHint: true},
	"subagent_status": {ReadOnlyHint: true, IdempotentHint: true},
	// Doc comment: "Idempotent — calling on an already-terminal run is a
	// no-op."
	"subagent_cancel":     {IdempotentHint: true},
	"subagent_role_audit": {ReadOnlyHint: true, IdempotentHint: true},
	// Runs an arbitrary shell command asynchronously.
	"background_job":    {DestructiveHint: true, OpenWorldHint: true},
	"background_status": {ReadOnlyHint: true, IdempotentHint: true},
	// Doc comment: "Idempotent — calling on an already-terminal job is a
	// no-op."
	"background_cancel": {IdempotentHint: true},
	"task_execute":      {OpenWorldHint: true},
	// Routes to an in-process executor; not guaranteed side-effect-free for
	// future executor kinds, so this stays conservative.
	"dispatch_executor": {},

	// --- scratchpad ---
	"scratchpad_write": {IdempotentHint: true},
	"scratchpad_read":  {ReadOnlyHint: true, IdempotentHint: true},
	// Doc comment: returns {"cleared": false} when the key is absent --
	// idempotent, not an error.
	"scratchpad_clear": {IdempotentHint: true},

	// --- reminders / pins ---
	"reminder_set": {},
	"context_pin":  {},
	// Delete-by-id; ordinarily idempotent at the SQL layer.
	"context_unpin": {DestructiveHint: true, IdempotentHint: true},

	// --- learning / scheduling / chat ---
	// Doc comment: idempotent on the (scope, subject, hint) triple --
	// re-writing the same lesson updates rather than duplicates.
	"lesson_capture":  {IdempotentHint: true},
	"schedule_create": {},
	// Persists nothing itself, but can fire a reaction against an
	// operator-configured endpoint.
	"task_update_report": {OpenWorldHint: true},
	"chat_search":        {ReadOnlyHint: true, IdempotentHint: true},
	"chat_get":           {ReadOnlyHint: true, IdempotentHint: true},
	// Sandboxed script exec whose only I/O is tool_call() into the full
	// internal tool registry -- arbitrary downstream mutation, gated by the
	// permission engine.
	"python_run": {OpenWorldHint: true},
}

// safeAnnotationsDefault is applied to any tool name absent from
// toolAnnotations -- see the package doc comment above for why that means
// "assume it's dangerous", not "assume it's safe".
var safeAnnotationsDefault = gmcpserver.ToolAnnotations{
	DestructiveHint: true,
	OpenWorldHint:   true,
}

// annotationsFor looks up name's registered hints, falling back to
// safeAnnotationsDefault for anything not yet audited into the table above.
func annotationsFor(name string) gmcpserver.ToolAnnotations {
	if a, ok := toolAnnotations[name]; ok {
		return a
	}
	return safeAnnotationsDefault
}
