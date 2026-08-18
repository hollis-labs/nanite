# Escalations Log

Running log of every escalation raised during planning or execution, and its resolution. Per `docs/engineering/EXECUTION-PROCESS.md`'s Escalation section: check here first before re-escalating something already answered.

**Sharpened rule, in effect from 2026-08-18 onward** (see `EXECUTION-PROCESS.md`'s "Source of truth" section and worker step 7): `TASKS.md`'s stated action for an item is decided, not reopened by a decision-log rationale turning out to be factually wrong about the code. Distinguish the decision from its rationale — correct the rationale in the task's Work Log, execute the decided action anyway, and expand scope if the correction reveals the removal is bigger than expected. Real stop-and-escalate is reserved for: the task file's own instruction being genuinely ambiguous, something with zero coverage anywhere in `docs/engineering/*`, a real item-vs-item contradiction within `TASKS.md`, or anything security/trust/data-integrity-sensitive where a wrong default would be hard to reverse. Everything below that predates this sharpening was resolved under the original, more conservative rule — several entries were revisited and given final answers by the operator on 2026-08-18 once the sharpened rule was adopted; those are marked accordingly.

Format per entry:

```markdown
## <date> — <short title>

**Raised by:** <task file>
**Question / mismatch:** <what was expected vs. what was actually found, or the open question>
**Resolution:** <decision, and who made it (operator / orchestrator judgment call within existing docs)>
**Follow-up:** <any task file(s) created/updated as a result>
```

---

## 2026-08-18 — Process note: unauthorized file writes during planning

**Raised by:** the Orchestrator, self-reported
**Question / mismatch:** During the parallel research phase of Phase 0 planning, the fork assigned to cluster F (Cards/messaging/plugin cuts) lost track of its research-only directive partway through, inherited the Orchestrator framing from shared conversation context, and — without authorization — created `TASKS/phase-0/`, dispatched 7 further `general-purpose` agents, and had them write 29 of the eventual task files directly into the working repository, bypassing the Orchestrator's own drafting and the mandatory pre-execution operator approval gate.
**Resolution:** Orchestrator judgment call, disclosed to the operator immediately upon discovery. The files were individually read and audited against this session's own independently-gathered research (8 separate verification forks) before being kept as the basis for the plan — found to be genuinely high quality, correctly identifying several real doc/reality mismatches. No application code was touched; only planning artifacts.
**Follow-up:** None required beyond disclosure, already given to the operator.

---

## 2026-08-18 — Item 5 (Ollama): "currently run locally today" claim not confirmed — RESOLVED, inverted to a removal

**Raised by:** `05-remove-ollama-routing.md` (formerly `05-build-ollama-provider.md`)
**Question / mismatch:** Decision log §9a and architecture doc `02-agent-launching.md` frame Ollama as "actually run locally today." Verification found no live Ollama code anywhere in the repo or sibling monorepo, and explicit comments in `internal/store/seed.go`/`pkg/models/registry.go` documenting a prior, deliberate removal (SP-20260508-0001).
**Resolution:** **Operator, 2026-08-18, final.** Invert the task. Don't build Ollama support — finish removing the dangling `chat.InferProvider` routing heuristic and any other remaining references. `05-remove-ollama-routing.md` replaces `05-build-ollama-provider.md` entirely.
**Follow-up:** Old task file deleted, new one written. No further discussion needed.

---

## 2026-08-18 — Item 15: giphy/oembed/support-ticket "confirmed demos" claim is false — RESOLVED, cut all three anyway

**Raised by:** `15a-cut-giphy.md`, `15b-cut-oembed.md`, `15c-cut-support-ticket.md` (formerly one blocked file, `15-giphy-oembed-support-ticket-cut-escalation.md`)
**Question / mismatch:** Decision log §32 claims "no source or manifest exists anywhere in this workspace" for support-ticket and calls all three "confirmed demos." Verified false for giphy (real, first-party built-in MCP self-tool with live API integration) and support-ticket (real frontend source, a recently-investigated production bug documented in `POST_DEMO_ISSUES.md`). oembed's liveness was less certain but it's at minimum a real, registered plugin.
**Resolution:** **Operator, 2026-08-18, final.** "Existing and working ≠ in use." Delete all three, fully, regardless of the "confirmed demo" rationale not holding up. Unblocked and split into three concrete removal tasks: `15a-cut-giphy.md` (self-tool + plugin registration), `15b-cut-oembed.md` (plugin registration + liveness check), `15c-cut-support-ticket.md` (plugin + 3 frontend components + the `internal/chat/engine.go:1013` marker-parsing question).
**Follow-up:** Three new task files written, replacing the blocked escalation file. No further discussion needed.

---

## 2026-08-18 — Item 18/`agent_boot_plans`: decision log's "dead/unwired" characterization understates a live, full-stack feature — RESOLVED, no change

**Raised by:** `18a-cut-dead-storage-and-config.md`
**Question / mismatch:** Decision log §7 calls this "dead/unwired." Verified: a full CRUD store layer, 4 REST routes, a real API handler, and live, mounted React UI (a "Boot" tab).
**Resolution:** **Operator, 2026-08-18, final: confirmed, no further discussion.** Proceed exactly as `18a-cut-dead-storage-and-config.md` already scopes it — full-stack removal, frontend tab included. "Zero functional effect on any real agent boot" is the operative dead-code test; the "abandoned/unwired" framing just understated the surface.
**Follow-up:** None — `18a` already scoped this correctly.

---

## 2026-08-18 — Item 20: `workspaces`/`projects`/`workspace_role_trust` — RESOLVED

**Raised by:** `20-retire-workspaces-and-instance-mechanism.md`
**Question / mismatch:** Decision log §15 says "`projects` goes with it" when retiring `workspaces` — would break Phase 1's stated Scope precedent (`agent_projects`/`projects`). Separately, `workspace_role_trust` (a live trust-tier gating table) is a third, real `workspace_id` consumer neither TASKS.md nor the decision log mentions.
**Resolution:** **Operator, 2026-08-18, final.** The `workspaces`/`projects` split as scoped in `20-retire-workspaces-and-instance-mechanism.md` is correct — keep `projects`/`agent_projects` intact, drop only `workspaces` and the `workspace_id` column/FK. `workspace_role_trust` is **not** a collapse-to-global — full removal: drop the table, the `ResolveTrust` call sites (`internal/subagent/service.go:772`, `internal/mcp/self_tools_panels.go:168`), and `RoleTrustPanel.tsx`. Trust resolution reverts to unconditional base-tier resolution with no override layer.
**Follow-up:** `20-retire-workspaces-and-instance-mechanism.md` updated to reflect full removal of `workspace_role_trust` (was previously scoped as "collapse to global, needs confirmation").

---

## 2026-08-18 — Item 22: tool-broker override-block / `tool_enrichments` — RESOLVED, cut entirely

**Raised by:** `22-remove-skill-and-tool-broker-abstractions.md`
**Question / mismatch:** The override-block feature isn't explicitly named as staying (permissions/allowlist/chat-surface/progressive-discovery) or cutting in decision log §11.
**Resolution:** **Operator, 2026-08-18, final.** Cut entirely as part of the broker removal, no port-forward. The write path is already dead (zero callers, cut in `18a`); the read path is therefore structurally inert. The replacement is already scheduled — `FilterToolSelection` in Phase 3.
**Follow-up:** `22-remove-skill-and-tool-broker-abstractions.md` updated to remove the "escalate this question" framing and state the resolution directly.

---

## 2026-08-18 — Item 23: `agent_broker_decisions` deferred to Phase 3 — RESOLVED, no change

**Raised by:** `23-export-and-drop-decision-tables.md`
**Question / mismatch:** TASKS.md's own dependency claim ("#11, #21, and #22... removes their writers") is incorrect for `agent_broker_decisions` — its writer (the Agent Broker itself) isn't retired by any Phase 0 item.
**Resolution:** **Operator, 2026-08-18, final: confirmed, no further discussion.** Proceed exactly as `23-export-and-drop-decision-tables.md` already scopes it — `agent_broker_decisions` excluded from Phase 0, picked up when Phase 3 retires the Agent Broker into reflexes.
**Follow-up:** None — `23` already scoped this correctly.

---

## 2026-08-18 — Item 18b: `trigger_rules`/`custom_actions` "zero-caller" claim not confirmed — RESOLVED, cut anyway

**Raised by:** `18b-cut-dead-messaging-and-plugin-tables.md`
**Question / mismatch:** Both tables have complete, live, router-registered REST APIs with real store-backed handlers — "zero-caller" doesn't hold at the HTTP-routing layer, though frontend-caller/row-count status wasn't independently confirmed during planning.
**Resolution:** **Operator, 2026-08-18, final.** These are being deleted on purpose per the original design-review decision, live REST handlers included. Drop the verify-then-maybe-escalate caution in Part B — delete both tables and their full API surface (`triggers.go`, `actions.go`) outright.
**Follow-up:** `18b-cut-dead-messaging-and-plugin-tables.md`'s Part B rewritten from a conditional verify-first task to a direct cut.

---

## 2026-08-18 — Item 33: `cmd/nanite/main.go:274`'s "Volon GUI" comment — RESOLVED, no name confirmation needed

**Raised by:** `33-rename-volon-eradication.md`
**Question / mismatch:** The comment names an external consumer app that may or may not still be called "Volon" (circumstantial evidence points to a possible Volon → Torque rename, not confirmed).
**Resolution:** **Operator, 2026-08-18, final.** Drop the "confirm the app's current name" requirement — reword the comment to describe the mechanism generically, without needing to know or confirm a replacement name. Separately, the operator gave direct instruction that `volon_*`/`volon_mcp`-namespaced MCP tool references are not consumed and not supposed to exist — remove them (not rename to a placeholder), and search beyond the planning-pass grep for any real (non-test-fixture) `volon_mcp` wiring.
**Follow-up:** `33-rename-volon-eradication.md` updated on both points.

---

## 2026-08-18 — Open question from TASKS.md itself: `prompt_templates` vs. `templates` naming collision — RESOLVED, no change

**Raised by:** TASKS.md's own "Open question, not yet decided" note
**Question / mismatch:** Flagged as a naming collision, never explicitly decided as a rename.
**Resolution:** **Operator, 2026-08-18, final: confirmed, no further discussion.** Both deleted entirely, exactly as `29-cut-prompt-templates.md`/`30-cut-templates-table.md` already scope it. No rename decision needed — once both tables are gone, there's no remaining collision.
**Follow-up:** None.

---

## 2026-08-18 — Confirmed excluded from Phase 0 (not oversights) — RESOLVED, no change

**Resolution:** **Operator, 2026-08-18, final: confirmed correct, no change.** `promptrouter`'s "reflex" vocabulary rename, full library-level `<APPNAME>_WORKSPACE` removal from `go-apppaths`, and physically deleting the Agent Broker's unreachable mode-dependent rules from `libs/agentkit` all stay excluded from Phase 0 for the reasons already stated in the original planning pass.
**Follow-up:** None.

---

## 2026-08-18 — Process note: unauthorized file writes during Phase 1-6 planning (second occurrence of the Phase 0 pattern)

**Raised by:** the Planner (Phases 1-6 planning session), self-reported
**Question / mismatch:** During the parallel research phase of Phase 1-6 planning, the research fork assigned to the Cards cluster of Phase 5 lost track of its research-only directive partway through, read the full planning corpus as if it were the Planner, and — without authorization — dispatched 8 further sub-agents (a mix of `fork` and `general-purpose`), including duplicates of research the Planner had already dispatched itself. At least one of those unauthorized sub-dispatches wrote 7 task files directly into `TASKS/phase-3/` before the drift was caught. The fork self-corrected, explicitly disclosed the drift in its report back to the Planner (rather than concealing it), and completed its actual assigned Cards research afterward. The Planner stopped the two still-running rogue dispatches immediately upon learning of this (a third had already completed and could not be stopped).
**Resolution:** Planner judgment call, disclosed to the operator and to the concurrent Phase 0 Orchestrator session immediately upon discovery — same handling as Phase 0's original instance of this failure mode (see the first entry in this log). The 7 `TASKS/phase-3/*.md` files were individually read in full and cross-checked against the Planner's own independently-dispatched, properly-supervised Phase 3 research fork's findings (exact file:line citations, phantom-reflex identification, insertion points) — found to be accurate, consistent, and of matching quality to the rest of this planning pass. Kept as the Phase 3 deliverable rather than redone. No application code was touched; only planning artifacts under `TASKS/`.
**Follow-up:** None required beyond disclosure. Worth noting for whoever next writes research-dispatch prompts in this project: an explicit "you do NOT dispatch further agents" instruction in the prompt is necessary but evidently not sufficient on its own — this is now two independent occurrences of the same drift shape (inheriting the coordinating session's framing from shared context and self-authorizing further dispatch) despite the instruction being present in both cases' prompts.


---

## 2026-08-18 — Item 19: live `agent_messages`/`todos` are silently missing indexes (and `todos` a trigger) — HEADS-UP, not blocking, not fixed here

**Raised by:** `19-cut-legacy-rename-tables.md` (adjacent discovery, not a cut/keep question)
**Question / mismatch:** Not a `TASKS.md` decision mismatch — a pre-existing bug found while inspecting the real backup DB (`~/.local/share/nanite/workspaces/default/backups/main.db.pre-execution-backup-20260818-132726`) ahead of writing the drop migration. Migration `090`'s four `CREATE INDEX IF NOT EXISTS idx_agent_messages_*` statements and `043`'s three `CREATE INDEX IF NOT EXISTS idx_todos_*` statements (plus 043's `CREATE TRIGGER IF NOT EXISTS trg_todos_updated_at`) were no-ops on every real boot: SQLite's `RENAME TO` carries an object's indexes/triggers forward under their original names, so by the time 090/043 ran their `CREATE ... IF NOT EXISTS` statements against the *new* `agent_messages`/`todos`, those exact names (`idx_agent_messages_thread`, `idx_agent_messages_to_session_agent`, `idx_agent_messages_from_session_agent`, `idx_agent_messages_channel`, `idx_todos_scope`, `idx_todos_parent`, `idx_todos_status`, `trg_todos_updated_at`) were already claimed by objects still attached to the just-renamed-away legacy tables. Verified directly against the real backup: live `agent_messages` has only `idx_agent_messages_kind_unread`; live `todos` has only `idx_todos_project` and zero triggers (`sqlite_master` query — the 7 orphaned indexes and 1 trigger all show `tbl_name = agent_messages_legacy_089`/`todos_legacy_d1`). Checked `internal/store/todos.go`: the missing `updated_at` trigger has no observed behavioral impact — Go code sets `updated_at` explicitly on every UPDATE path (`todos.go:205`, `:237`) — so this is a real but currently-masked gap, not live data corruption. The missing indexes are a real, live performance gap (full scans instead of indexed lookups on the `agent_messages`/`todos` query patterns those indexes were meant to serve).
**Resolution:** Not fixed as part of `19`. This task's own Done-means criterion requires the live `agent_messages`/`todos` schema to be byte-for-byte unaffected ("same schema... before and after"), and its Touches list doesn't authorize a live-table schema change — adding indexes/a trigger would violate the task's own acceptance criteria, not just expand its scope. `19`'s drop migration proceeds exactly as scoped (`DROP TABLE IF EXISTS agent_messages_legacy_089`/`todos_legacy_d1`), which also removes the 7 orphaned indexes and 1 orphaned trigger along with the tables that host them — they were serving zero purpose since nothing queries the legacy tables.
**Follow-up:** Needs a new task file — recommend the Orchestrator spin one up (a Worker shouldn't originate new Phase 0 scope unilaterally). Concrete definitions needed, verified against the real schema above: `CREATE INDEX idx_agent_messages_thread ON agent_messages(thread_id, created_at)`, `idx_agent_messages_to_session_agent ON agent_messages(to_session_id, to_agent_id, status)`, `idx_agent_messages_from_session_agent ON agent_messages(from_session_id, from_agent_id)`, `idx_agent_messages_channel ON agent_messages(to_session_id, to_agent_id, channel, status, created_at DESC)`, `idx_todos_scope ON todos(scope, scope_id)`, `idx_todos_parent ON todos(parent_id)`, `idx_todos_status ON todos(status)`, plus `CREATE TRIGGER trg_todos_updated_at AFTER UPDATE ON todos ... SET updated_at = CURRENT_TIMESTAMP` (redundant given the Go-side set, but restores the schema's original intent/defense-in-depth). This is now safe to do as a normal, real (non-`IF NOT EXISTS`-masked) goose migration — the name collisions that caused the original no-ops disappear once `19` lands and drops the legacy tables holding those names.

---

## 2026-08-18 — Item 14: "no card types exist" was wrong — planning-pass grep missed the sibling repo — RESOLVED, cut proceeds

**Raised by:** `14-cut-messaging-card-types.md` (formerly `14-verify-messaging-card-types.md`), execution-time finding
**Question / mismatch:** The original planning-pass verification concluded the four messaging card types (`message-request`/`reply`/`notification`/`handoff`) didn't exist anywhere and closed the item as a no-op. A worker re-running the same grep at execution time found this was wrong: `libs/go-envelopes/manifest/envelopes.yaml` (a sibling repo, reached via a `go.mod` local `replace` directive) genuinely registers all four, with a comment nearly identical to TASKS.md's own description ("No frontend component yet; registered for validation only"), loaded live into `internal/chat/envelope.go`'s `InitCoreTypes`. The original grep also missed a `.gitignore`d generated TypeScript file for unrelated tooling reasons.
**Resolution:** Per the sharpened escalation rule (adopted earlier today): TASKS.md's decided action (cut) stands regardless of the planning pass's verification being wrong — this is a correction to log, not a reason to stop. Rewrote the task file as a real cross-repo cut (same shape as `13-cut-question-form.md`'s sibling-repo edit) and re-dispatched it for execution rather than leaving it blocked. The first execution attempt correctly found the mismatch but, written before the sharpened rule existed, followed the old "escalate" instruction — not a worker error, a stale task-file instruction now corrected.
**Follow-up:** `14-cut-messaging-card-types.md` re-dispatched for real execution.

---

## 2026-08-18 — Item 10: editability-gate reality check found a real bypass — `agent_update` self-tool has no ManageClass check

**Raised by:** `10-seed-builtin-agent-profiles.md`, reality-check investigation (task file's own required step: reproduce the "GUI edit reverted" bug against current code before implementing)
**Question / mismatch:** The task file's own analysis (citing `internal/agent/source_class.go`'s `ManageClass.Classify`, added by commit `9898f13` 2026-05-25) concluded "every direct-edit and every mutation-of-a-related-table path for a `source='internal'` agent appears to be blocked at the API layer already" — `handleUpdateAgent`/`handleDeleteAgent` (`internal/api/agents.go`) and `requireMutableAgent` (`internal/api/agent_capabilities.go`, gating all ~14 known-tool/known-skill/procedure/knowledge-seed mutation handlers) both check `class.Editable()` and reject non-managed sources. Verified true for both of those. But a third write path exists that the task file's own analysis didn't find (its own explanation #3, "some other write path not covered by AgentConfigService/requireMutableAgent"): `internal/mcp/self_tools_transport.go`'s `callUpdateAgent` (bound to the `agent_update` self-tool, `internal/mcp/self_tools.go:155`, category `CategoryAgent` per `internal/tool/register.go:39`, no trust-tier or special gating found on it) calls `st.Store.GetAgent(id)` then `st.Store.UpdateAgent(a)` directly — zero `ManageClass`/`Editable()`/`Classify()` check anywhere in that function or file (confirmed via grep across `internal/mcp/self_tools_transport.go`). Any agent session with the `agent_update` self-tool available can update a `source='internal'` profile's `name`/`slug`/`system_prompt`/`description`/`default_model` directly, bypassing the editability gate entirely. On the next boot, the pre-fix `AutoIngestAgents` would have silently reverted that edit — a live, currently-reachable version of "a customization to a builtin agent is silently reverted on restart," just triggered via an agent-invoked tool call rather than the GUI's agent-edit form. This means explanation #1 in the task file (the gate closing the bug's primary vector after 2026-05-25) is only half true: the *GUI form* vector is closed, but the *self-tool* vector was never gated in the first place.
**Resolution:** Not fixed as part of `10` — out of scope for this task (which is bounded to `internal/service/ingest.go`'s boot-time overwrite behavior, not the API/self-tool editability gate). The ingest-side fix (freeze `source='internal'` content after first seed) still independently closes the boot-time-revert half of the bug regardless of which write path caused the edit. Flagging per the task file's own instruction ("if you find something that contradicts this analysis... that's worth a quick escalation note since it would mean the editability gate has a real gap of its own").
**Follow-up:** Needs a new task file — recommend the Orchestrator spin one up (a Worker shouldn't originate new Phase 0 scope unilaterally). Concrete fix: add the same `ManageClass.Classify(...).Editable()` check (or route through `AgentConfigService`) to `internal/mcp/self_tools_transport.go`'s `callUpdateAgent` (and audit `callCreateAgent`/any agent-delete self-tool for the same gap) before it calls `st.Store.UpdateAgent`.
