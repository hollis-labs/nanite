# `SelfToolsTransport` — capability-domain map and dispatch-direction decision

**Phase:** Audit remediation — Wave 5 (architectural concentration)
**Status:** not-started
**Depends on:** none (self-contained planning task, independent of `01-chatserviceimpl-generateresponse-decomposition.md` — different package, different type, no shared code)
**Touches:** `internal/selftools/self_tools_transport.go` (`SelfToolsTransport` struct and `CallTool` switch), the other files implementing `SelfToolsTransport`'s handler methods in `internal/selftools/` (exact file list to be confirmed by the worker — the type's 81 methods are not all in one file). Read-only reference: `internal/toolclient/broker.go`, `internal/toolclient/intent.go`, `internal/toolclient/ranking.go`, `internal/toolclient/meta_tools.go` (`ToolClient` — a separate type, covered as the second finding in this same file, see below).
**requires_architect_decision:** true — per the remediation guide's §9 decision queue item 6 ("`SelfToolsTransport` decomposition boundaries").

## Context

### Findings addressed

- **GO-MCPTOOL-006** (**medium**, god-object, confidence high) — `SelfToolsTransport` (`internal/selftools/self_tools_transport.go:92`) is described by the audit as **"the strongest god-object candidate in this cluster"**: **31 fields**, **81 methods**, a single flat `CallTool` switch dispatching to **~62 tool names spanning ~15 largely-unrelated capability domains** (todo/plan, messaging, subagent-dispatch, background jobs, workflow execution, panels, python execution, elicitation, learning capture, builder wizard, reactions), implemented as real **2,431-line business logic**, not thin adapters. It fails the guide's "wire vs. implement" test the same way `chatServiceImpl` does (see `01-chatserviceimpl-generateresponse-decomposition.md`, §8.4).
- **GO-MCPTOOL-007** (informational, god-object, confidence high) — `ToolClient` (`internal/toolclient/broker.go:21`, with methods spread across `broker.go`, `intent.go`, `ranking.go`, `meta_tools.go`) has **26 methods across 4 files** backing **3 self-described responsibilities**: selection, permissions, and execution. Lower severity than `SelfToolsTransport` — no unrelated concerns mixed in, methods are genuinely tool-selection-adjacent. Recorded for the architect's holistic pass, not flagged as confirmed drift.

**`ToolClient` and `SelfToolsTransport` are different types in different packages** (`internal/toolclient` vs. `internal/selftools`) with no direct code-sharing relationship described by the audit — do not conflate them. They are kept as two clearly-separated subsections in this one task file because both are god-object-shaped findings from the same audit cluster (§8.7), not because they are architecturally related.

Both findings are evidenced in `docs/audits/2026-08-21-go-quality/REPORT.md` §8.7 and `docs/audits/2026-08-21-go-quality/findings.json` (ids `GO-MCPTOOL-006`, `GO-MCPTOOL-007`).

---

## Part A — `SelfToolsTransport` (GO-MCPTOOL-006)

### Root cause

`SelfToolsTransport` is the concrete `mcp.Transport` implementation backing Nanite's first-party self-tools MCP server. As new self-tool capabilities were added over time (todo/plan management, agent messaging, subagent dispatch, background jobs, workflow execution, panel control, sandboxed python execution, elicitation, learning capture, the builder wizard, reactions), each was implemented as one or more methods directly on `SelfToolsTransport` and wired into one flat `switch name { case "...": ... }` in `CallTool`, rather than being delegated to a narrower, domain-scoped collaborator. The audit's cohesion review of this cluster explicitly checked for duplicate tool-routing (a different concern) and found none — this finding is specifically about the breadth of unrelated domains implemented directly on one type, not about routing correctness.

### Current behavior

`SelfToolsTransport` struct definition starts at `internal/selftools/self_tools_transport.go:92`; `CallTool` (`internal/selftools/self_tools_transport.go:419-420` for the method/switch opening) dispatches via a single flat `switch name` with cases including (sampled directly from source, not exhaustive — re-verify the full list against current source before treating any count as final, since new tool names may have been added since the audit's commit):

```
skill_list, skill_delete, agent_create, agent_list, agent_update,
workflow_execute_llm_step, workflow_execute_tool_step, workflow_verify_step, workflow_run,
engine_navigate, engine_refresh, card_show, tool_validate,
builder_start, builder_step,
todo_create, todo_update, todo_list,
plan_create, plan_update, plan_step_add, plan_list, plan_get, plan_delete,
install_home, install_project, install_diff,
message_send, message_inbox, message_thread, message_ack, message_resolve, message_catch_up,
handoff_request, handoff_approve, handoff_reject,
subagent_spawn, subagent_status, subagent_cancel, subagent_role_audit,
background_job, background_status, background_cancel,
task_execute, dispatch_executor,
chat_search, chat_get, procedure_get,
python_run,
panel_open, panel_close,
signal_mode,
reminder_set, context_pin, context_unpin,
tool_describe, tool_list,
lesson_capture,
handoff_stash, handoff_pointers_expand,
whoami,
scratchpad_write, scratchpad_read, scratchpad_clear
```

This is real behavior, not delegation: the audit's read confirms this is 2,431 lines of actual business logic living on `SelfToolsTransport`'s own methods, not thin pass-through adapters into some other owner. The 31-field/81-method counts are counted directly by the audit across all files implementing methods on the type (not just `self_tools_transport.go` itself — the type's methods are spread across multiple files in `internal/selftools/`, which the worker must enumerate as part of the capability-domain map below).

### Desired invariant

An architect (informed by this task's capability-domain map) has an explicit, written answer to: *"should `SelfToolsTransport` continue implementing every one of these ~15 domains' business logic directly, or should it become a thinner dispatcher that routes into narrower, domain-scoped capability owners it holds as collaborators?"* — with a stated rationale either way, not a default left unexamined.

### What to do

1. **Build a capability-domain map** — for each of the ~15 domains the audit names (todo/plan, messaging, subagent-dispatch, background jobs, workflow execution, panels, python execution, elicitation, learning capture, builder wizard, reactions — treat this as a starting classification, not a fixed list; re-derive the real domain groupings directly from the ~62 tool-name switch cases enumerated above, since several of the sampled cases above don't map cleanly onto the audit's named 15 and need their own bucket, e.g. `agent_create`/`agent_list`/`agent_update`, `skill_list`/`skill_delete`, `engine_navigate`/`engine_refresh`/`card_show`, `whoami`, `signal_mode`, `chat_search`/`chat_get`, `install_home`/`install_project`/`install_diff`, `reminder_set`/`context_pin`/`context_unpin`, `scratchpad_*`), record for each domain:
   - the tool names it owns (from the `CallTool` switch);
   - the `SelfToolsTransport` fields it reads/mutates (of the 31 total);
   - the methods implementing it (of the 81 total), and which file(s) they live in;
   - any shared/cross-domain state it touches (fields or helper methods used by more than one domain — these are the ones that make a clean split harder, and must be called out explicitly, not glossed over);
   - external dependencies (what other packages/types each domain's methods call into — e.g. does `subagent_spawn` reach into `internal/subagent`, does `python_run` reach a sandboxed executor, etc.);
   - a first-pass judgment on whether this domain looks cleanly extractable into its own capability-owner type, or is entangled enough with `SelfToolsTransport`'s other state that it isn't (be honest here — not every domain needs to resolve to "yes, extractable").
2. **Present the direction question explicitly**, informed by the map: does it make sense for `SelfToolsTransport` to become a thinner dispatcher holding narrower capability-owner collaborators (one per domain, or per cluster of related domains) and delegating `CallTool` into them, analogous to how `chatServiceImpl` holds `*StreamManager` rather than 6 raw `sync.Map` fields (see `01-chatserviceimpl-generateresponse-decomposition.md` for that precedent)? Or does the map show the domains are different enough in shape (some are a handful of lines, some are hundreds; some share real state, some don't) that a uniform "one owner per domain" split doesn't fit, and a different or partial grouping is more honest? Write the answer down as a recommendation for the architect, with the map as its evidence — do not silently default to "yes, split everything" or "no, leave it alone" without the map actually supporting the conclusion.
3. **Identify, if the direction is "yes, delegate to narrower owners,"** which 2-4 domains are the strongest first candidates for extraction (by size, by how self-contained their state is, by how little cross-domain entanglement the map found) — as a *candidate list for a follow-on task*, not as work to execute here.

### Non-goals

- **"Do not split solely to reduce field/method counts."** This is a direct quote from the remediation guide's §4 Wave 5 instruction for `SelfToolsTransport` specifically — the field/method counts (31/81) are the audit's signal that something is worth looking at, not the justification for any specific split. The capability-domain map's evidence of genuinely coherent, separable domains (or lack thereof) is the only valid basis for a splitting recommendation.
- Do not implement any extraction in this task. The capability-domain map and the direction recommendation are this task's deliverables; any actual code movement is follow-on work, scoped after architect sign-off.
- Do not assume all ~15 domains must resolve the same way — the map may legitimately show that some domains (e.g. `whoami`, `signal_mode` — likely tiny, single-method domains) aren't worth a dedicated owner type even if the overall direction answer is "yes, delegate," while a large domain like messaging or workflow execution clearly is.

### Tests required

- No new production tests are required by this task specifically (no production code changes), but if the capability-domain map's construction reveals any domain has notably thin or absent test coverage relative to its size/complexity, record that as a finding in the Work Log for a follow-on task to pick up — do not silently expand this task's scope to write that coverage.

### Prevention

- The capability-domain map itself becomes the reference document for any future engineer adding a new self-tool — it should make clear which domain a new tool name belongs to and (once the direction question is resolved) where new domain logic should actually live.

### Verification

```bash
go build ./internal/selftools/...
go vet ./internal/selftools/...
go test ./internal/selftools/...
```

Observable behavior required for PASS: the capability-domain map exists (inline in the Work Log or linked, per this task file's own note if too large to inline) and covers every tool name currently in the `CallTool` switch, re-verified against current source (the sampled list above may have drifted since this task was authored); the direction recommendation is written down with explicit reasoning tied to the map's findings, not asserted independent of it. No production code in `internal/selftools/` is modified by this task.

---

## Part B — `ToolClient` (GO-MCPTOOL-007)

### Root cause

`ToolClient` (`internal/toolclient/broker.go:21`) grew methods across 4 files (`broker.go` 795 lines, `intent.go` 119 lines, `ranking.go` 342 lines, `meta_tools.go` 194 lines — 1,450 lines total) as its 3 self-described responsibilities (tool selection, permissions, execution) each accumulated their own methods on the same type, rather than any one responsibility ballooning unexpectedly.

### Current behavior

26 methods total across the 4 files above. Unlike `SelfToolsTransport`, the audit found **no unrelated concerns mixed in** — every method is genuinely tool-selection-adjacent (selection, permissions, or execution), and the 4-file split already gives each responsibility its own file, which is itself a reasonable organizational signal (contrast with `SelfToolsTransport`, where ~15 domains are not separated even at the file level within `internal/selftools/`).

### Desired invariant

None required by this task specifically — this finding's own recommendation is **"no action implied; record for the architect's holistic pass."** The invariant this task establishes is simply that the finding is visibly tracked and not silently dropped, per the guide's disposition-tracking requirement (see this batch's own `FINDING-INDEX.md`).

### What to do

1. Record, in this task's Work Log, a short confirmation that `ToolClient`'s 3-responsibility/4-file/26-method shape has been reviewed against current source (re-count methods and files; note if the shape has changed materially since the audit's commit).
2. State explicitly that this finding requires no split, no extraction, and no further work from this task or any follow-on task — it is retained here purely as a recorded, non-actioned finding for the architect's future holistic pass across the codebase's god-object-shaped types, should one ever be scheduled.

### Non-goals

- Do not propose splitting `ToolClient`'s 3 responsibilities into 3 separate types. Nothing in the audit's evidence supports this — the finding is informational, and its own recommendation is explicitly "no action implied."
- Do not conflate this with `SelfToolsTransport`'s decomposition question (Part A) — they are unrelated types with unrelated recommendations, sharing only the same audit cluster and severity category (god-object).

### Tests required

- None. No production code changes.

### Prevention

- None needed beyond this task file's own record — the finding is closed by acknowledgment, not by a code or process change.

### Verification

No commands required for Part B specifically (no code touched); Part A's verification commands above cover the whole task file's `go build`/`go vet`/`go test` obligations.

---

## Risk / rollback

Zero production risk for both parts — this task produces a written map and two recommendations (a substantive one for `SelfToolsTransport`, a "no action" confirmation for `ToolClient`); no code is modified. Rollback is a plain file removal if needed.

## Done means

- [ ] Part A: a capability-domain map for `SelfToolsTransport` exists, covering every tool name in the current `CallTool` switch (re-verified against current source, not assumed from this task's sampled list), with fields/methods/shared-state/dependencies noted per domain.
- [ ] Part A: an explicit direction recommendation exists (delegate to narrower capability owners, or not), grounded in the map's evidence, with a stated rationale.
- [ ] Part A: if the direction is "delegate," 2-4 candidate domains for a first follow-on extraction are named, without any extraction being performed in this task.
- [ ] Part A: the "do not split solely to reduce field/method counts" instruction is explicitly honored — the recommendation's rationale references domain cohesion/coupling evidence, not raw counts.
- [ ] Part B: `ToolClient`'s shape is re-confirmed against current source and recorded as a closed, no-action finding in the Work Log.
- [ ] No production code in `internal/selftools/` or `internal/toolclient/` is modified by this task.
- [ ] An architect has reviewed and signed off on Part A's direction recommendation before any follow-on `SelfToolsTransport` extraction task is created or dispatched.
- [ ] `go build ./...`, `go vet ./...`, and `go test ./internal/selftools/... ./internal/toolclient/...` all pass clean.

## Work log

<!-- Worker fills in: the capability-domain map (inline or linked), the direction recommendation and its rationale, the ToolClient re-confirmation, any deviation from plan and why. -->

## Review notes

<!-- Reviewer fills in: pass/fail, what was independently re-verified (re-counted switch cases/fields/methods against current source, checked the map's domain groupings against the real code rather than trusting the worker's description). -->
