package service

// Phase 4 item 07 (TASKS/phase-4/07-tool-concurrency-safety-classification.md):
// declaredToolConcurrencySafety is the curated, per-tool-NAME concurrency-
// safety table for every tool in Nanite's compiled-in catalog (the dev/
// general/code-exec/self-service MCP transports plus the S4a result-cache
// meta-tools). This is the "declared metadata, not inferred from its name"
// source architecture/03-steering.md's "Two correctness gaps carried into
// implementation" calls for. Each entry below was set by reading that
// tool's actual handler and description -- not by pattern-matching the
// name string (that was the old, now-removed mechanism; see GetToolMeta's
// doc comment in tool.go for the full account, including the real
// misclassifications this audit found).
//
// SyncKnownTools (known_tools_sync.go) reads this table once per boot, for
// every tool in the live catalog, and backfills known_tools.concurrency_safe
// via SetKnownToolConcurrencySafeIfUnset -- which only ever fills a NULL
// column, so a future operator override (direct DB edit, or a later admin
// UI) is never clobbered by a routine re-sync. GetToolMeta (tool.go) is the
// only runtime reader of the resulting known_tools value; it does not
// consult this table, or the tool's name, directly.
//
// Meaning of each value:
//
//	true  = safe to execute in parallel with other tool calls in the same
//	        turn: the call writes no shared mutable state (no DB row, no
//	        session/loopState field, no filesystem write, no subprocess/
//	        subagent dispatch), and its result does not depend on ordering
//	        relative to any other call in the same batch.
//	false = NOT safe: the tool mutates a database row, in-memory session
//	        state, the filesystem, or dispatches a subprocess/subagent --
//	        OR its real safety is genuinely input-dependent (dev_bash: `ls`
//	        is safe, `rm -rf` is not) and known_tools.concurrency_safe is a
//	        single static bool per tool name with no way to carry that
//	        per-call nuance. The conservative value (false) is deliberate
//	        in that case, not a placeholder pending a smarter mechanism --
//	        internal/tool/adapt.go's WithConcurrencySafeFunc *can* express
//	        per-input safety, but that mechanism is not wired into the live
//	        runtime (WrapExistingTools/WrapProviderDef have no production
//	        caller as of this task; see the Work Log), so it is not treated
//	        as a competing source of truth here.
//
// A tool absent from this table is left NULL by the backfill ("not yet
// classified" -- e.g. a brand-new MCP server tool discovered after this
// table was last reviewed). GetToolMeta's default for a NULL or missing
// known_tools row is false (fail closed) -- never a name guess.
var declaredToolConcurrencySafety = map[string]bool{
	// --- dev (internal/mcp/dev_tools.go) ---
	"dev_read":  true,
	"dev_grep":  true,
	"dev_glob":  true,
	"dev_write": false,
	"dev_edit":  false,
	"dev_bash":  false, // input-dependent; conservative static default — see doc comment above

	// --- general (internal/mcp/general_tools.go) — pure, deterministic,
	// side-effect-free reads/transforms. callThink (general_tools.go) is a
	// pure validate-and-echo with no persisted state.
	"web_fetch":     true,
	"json_parse":    true,
	"datetime":      true,
	"base64_encode": true,
	"base64_decode": true,
	"url_encode":    true,
	"url_decode":    true,
	"hash":          true,
	"math_eval":     true,
	"think":         true,

	// --- code-exec (internal/mcp/code_exec_tools.go) ---
	"code_execute": false, // arbitrary sandboxed code execution — side-effecting, non-deterministic

	// --- self-service (internal/mcp/self_tools*.go) ---
	"skill_create": false,
	"skill_update": false,
	"skill_delete": false,
	"skill_list":   true,

	"agent_create": false,
	"agent_update": false,
	"agent_list":   true,

	"engine_navigate": false, // UI navigation signal — order-sensitive
	"engine_refresh":  false, // UI refresh signal — order-sensitive
	"card_show":       false, // inserts a UI card into the chat stream — order-sensitive

	"builder_start": false,
	"builder_step":  false,

	"todo_create": false,
	"todo_update": false,
	"todo_list":   true,

	"plan_create":   false,
	"plan_update":   false,
	"plan_step_add": false,
	"plan_delete":   false,
	"plan_list":     true,
	"plan_get":      true,

	"install_home":    false, // writes files under ~/.nanite/
	"install_project": false, // writes/scaffolds files under a project dir
	"install_diff":    true,  // callInstallDiff is currently a read-only stub ("not yet implemented") that mutates nothing

	"message_send":     false,
	"message_ack":      false,
	"message_resolve":  false,
	"message_inbox":    true,
	"message_thread":   true,
	"message_catch_up": true,

	"handoff_request":         false,
	"handoff_approve":         false,
	"handoff_reject":          false,
	"handoff_stash":           false,
	"handoff_pointers_expand": true, // reads back a previously-stashed cache entry by key

	"subagent_spawn":      false,
	"subagent_cancel":     false,
	"subagent_status":     true,
	"subagent_role_audit": true, // read-only report/aggregate query

	"background_job":    false,
	"background_cancel": false,
	"background_status": true,

	"chat_search":   true,
	"chat_get":      true,
	"procedure_get": true,

	"panel_open":  false, // UI drawer signal — order-sensitive
	"panel_close": false, // UI drawer signal — order-sensitive
	"signal_mode": false, // UI mode signal — order-sensitive

	"reminder_set":  false,
	"context_pin":   false,
	"context_unpin": false,

	"tool_describe": true,
	"tool_list":     true,
	"tool_validate": true, // pre-flight schema check only — never invokes the target tool

	"dispatch_executor": false,
	"lesson_capture":    false,

	"whoami": true, // pure read of the caller's own identity, no side effects

	"workflow_execute_llm_step":  false,
	"workflow_execute_tool_step": false,
	"workflow_verify_step":       false, // mode=agent branch spawns a subagent — conservative for the whole tool
	"workflow_run":               false,

	"python_run": false,

	"task_execute": false,

	// scratchpad_* additionally get a hard per-call serial override in
	// chat_tool_executor.go's isScratchpadTool check (loopState access has
	// no mutex) regardless of what's declared here — declared false too,
	// so the two mechanisms agree rather than one silently overriding a
	// declared "true".
	"scratchpad_write": false,
	"scratchpad_read":  false,
	"scratchpad_clear": false,

	// --- S4a result-cache meta-tools (internal/toolclient/meta_tools.go) ---
	"request_tools":      false, // mutates the turn's progressive-discovery / loaded-tools state
	"fetch_tool_result":  true,  // pure byte-slice read of already-cached data
	"search_tool_result": true,  // pure regex read of already-cached data
}

// declaredConcurrencySafety looks up name in the curated table above.
// Returns (safe, true) when name has an explicit declared entry, or
// (false, false) when it does not (caller should treat this as "not yet
// classified", not as an implicit false declaration).
func declaredConcurrencySafety(name string) (bool, bool) {
	safe, ok := declaredToolConcurrencySafety[name]
	return safe, ok
}
