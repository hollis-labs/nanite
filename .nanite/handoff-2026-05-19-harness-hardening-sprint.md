# Handoff — Nanite harness-hardening debug arc & sprint orchestration

**Written:** 2026-05-19 · **Read this first after compaction.** Then re-check
`git status` / `git log -1` and `cerberus resource status nanite-api-service`
to confirm the "current state" below still holds.

---

## What this is

A long debug/audit arc of the **Nanite chat harness** (2026-05-18 → 05-19),
run by dogfooding Nanite + Torque to build new apps (Glyph, Tether work, the
DAR spike). It surfaced a cluster of harness rough edges, all now captured as
a Torque sprint. **Next step: orchestrate that sprint.**

**Orchestration model going forward:** this session coordinates/oversees; the
operator boots a **second session** that acts as the Torque **orchestrator** and
actually dispatches + runs the sprint's subagent tasks.

---

## THE SPRINT — `SP-20260518-0013`

"Nanite harness hardening — full-time-chat blockers" · project `PRJ-20260417-0002`
(nanite) · **17 tasks** · all `kind=agent`, `executor=cli`,
`agent_profile=implementer`, `working_dir=/Users/chrispian/dev/hollis-labs/apps/nanite`,
`status=backlog`, `manual=true`.

**Wave 1 — Subagent execution** (do in order; 0073 is load-bearing)
1. `CW-20260519-0073` — wall-clock → heartbeat/inactivity liveness *(keystone)*
2. `CW-20260517-0036` — realistic + configurable run budget
3. `CW-20260519-0071` — partial-result capture on deadline/failure
4. `CW-20260519-0074` — run status taxonomy (cancelled/over-budget/stalled/failed)
5. `CW-20260519-0067` — output-presence gate before `completed`
6. `CW-20260519-0068` — progress-heartbeat narration

**Wave 2 — Visibility / SSE seam**
7. `CW-20260518-0074` — CLI replies vanish on reload
8. `CW-20260518-0084` — FE indicator when a turn is killed by a restart
9. `CW-20260519-0066` — subagent envelopes (approval cards) bubble to parent

**Wave 3 — Runtime hygiene / identity**
10. `CW-20260518-0085` — orphan/reaper sweep reconciles runtime rows
11. `CW-20260519-0051` — AskUserQuestion capture for CLI sessions
12. `CW-20260519-0063` — cross-session chat search
13. `CW-20260519-0064` — MEMORY.md boot signpost

**Wave 4 — Tool ergonomics**
14. `CW-20260519-0115` — audit & rework count-based limits (0073-class first; cheap)
15. `CW-20260519-0116` — bulk create/update across MCP/API/CLI
16. `CW-20260519-0117` — explore generic intent-routed tools + batch primitive *(design spike)*

**Stretch (sequence last):** `CW-20260519-0075` — subagent checkpoint/resume + retry.

**Engineering reference (non-dispatchable):** `CW-20260519-0076` — file:line map
for Wave 1, audit doc attached as artifact. **Read it before touching Wave 1.**

### How to start the sprint
Tasks are `backlog` / `manual=true` — nothing auto-dispatches.
1. Orchestrator reviews → triages `backlog`→`todo`.
2. `torque_sprint_start SP-20260518-0013` bulk-promotes `manual=true`→`false`.
3. Walk Wave 1 in order — **0073 first** (fixing the timeout *number* without the
   *signal* re-hits the wall). 0117 is a spike, gates nothing, can run parallel.
Follow `/Users/chrispian/dev/agent-os/docs/torque-agent-guide` for the run flow.

---

## SHIPPED this arc — done, do NOT redo

- **go-providers v0.22.0** (PR #24, merged) — `CodexAdapter.WritableRoots` +
  `ClaudeAdapter.AdditionalDirectories`.
- **nanite #210** (merged) — CLI writable-roots threading (`dev_tools_allowed_paths`
  → codex `[sandbox_workspace_write]` / claude `additionalDirectories`).
- **nanite #212** (merged) — handoff tools resolve session id from dispatch
  context; scratchpad tools return a clear error instead of `unknown tool`.
- **Clockwork→Torque migration** — recovered the c248 agent's orphaned tasks into
  Torque plan `CW-20260518-0077` + children. Old `clockwork.db` deleted.

## LIVE config changes — DB/file state, NOT in git (re-apply if the DB resets)

- `mcp_servers` — "Clockwork Manifold" row deleted. "Agent Mux" row fixed:
  `args=["mcp","--proxy","--servers","torque,tesseract","--token","local-dev","--scopes","session.write,message.write"]`,
  `env_allowlist=["PATH","HOME","USER","TMPDIR"]`, `trust_tier=plugin_stdio`.
  (Result: Agent Mux discovers ~180 tools; catalog ~257.)
- `user_settings.tool_per_turn_cap` — `10 → 100` (interim; CW-20260519-0115 is the real fix).
- `~/.config/nanite/config.yaml` — added `dev_tools_allowed_paths: [~/dev]`.

## Current state (verify after compaction)

- **nanite** — `main` @ `2e42a1a`, clean (only the usual untracked: `.claude/libs`
  symlink, `.nanite/handoff-*.md`, `internal/api/data/`).
- **go-providers** — `main` @ `90b9a69`, clean.
- **Service** — `nanite-api-service` running (last reload: the Agent Mux MCP fix).
  Deploy recipe: `cerberus resource deploy nanite-api-service` then
  `cerberus resource reload nanite-api-service` (use the CLI — the MCP tool
  times out on long builds). A reload cold-boots active chat sessions.

---

## HOW TO DEBUG / AUDIT A CHAT SESSION

**The DB** (XDG path, post go-apppaths migration — NOT `./nanite.db`):
`~/.local/share/nanite/workspaces/default/main.db`

**Find a session:**
`sqlite3 main.db "SELECT id,short_code,provider,model,status,message_count,created_at,last_activity FROM sessions WHERE short_code='cNNN';"`

**Key tables:**
- `messages` — `content` is `{"v":1,"text":...}` JSON; the **`tool_calls` array**
  inside that JSON has `name`/`status`/`error_reason` per call; `envelope` column
  carries structured cards (plan-review, approval, etc.); `metadata`.
- `session_events` — `pty_turn_start` / `pty_turn_complete` (CLI sessions only;
  anthropic in-process turns don't emit these).
- `execution_metrics` — per turn: `duration_ms`, `context_tokens`, `output_tokens`,
  `cache_read_tokens`, `tool_calls`, `tool_iterations`, `stop_reason`, `error`,
  `is_utility` (autoTitle/autoTags are `is_utility=1`).
- `subagent_runs` — `parent_session_id`/`child_session_id`, `role`, `mode`,
  `status`, `error`, `timeout_seconds`.
- `agent_runtime` — `state`, `pid`, `workdir` (id ≈ session id for chat).
- `handoff_stashes`, `mcp_servers`, `user_settings`.

**"Turn completed cleanly"** = matching `pty_turn_start`/`complete` (CLI) +
`execution_metrics.stop_reason=end_turn` + empty `error`.

**Service logs / harness telemetry:** `/Users/chrispian/.cerberus/apps/nanite/nanite-api-service/logs/stderr.log`
(grep it) or `cerberus resource logs nanite-api-service --stream stderr`. Useful lines:
`pre-loop classification` (`scope_tier`, `execution_pattern`, `tools_available`),
`agent-broker decision` (reflex routing), `strategy decision` (`approach`,
`reflex_match_id`), `chat-loop-diag` (`loop start`/`iter start`/`loop exit`,
`tool_per_turn_cap`, `ctx_deadline_remaining_s`), `request_build` (`tools_tokens`,
context-token breakdown).

**Hallucination check — verify, don't trust:** cross-check the agent's claimed
actions against (a) the message's actual `tool_calls` array and (b) the real
backend — e.g. `torque_task_get <id>` to confirm a task it claims to have created
actually exists. (Seen this arc: a pre-fix agent claimed a Torque task it never
created — it had called the wrong tool.)

**Boot dirs:** `/var/folders/.../T/nanite-boot-{provider}-{id}-r0-*` — read
`.mcp.json` / `config.toml` / `boot.md` there.

---

## THE AUDIT-DOC LENS / TEMPLATE

Audit write-ups go to `~/dev/chrispian/inbox/nanite-<topic>-<date>.md`.

**Structure:** header (what/scope) → the session(s) → **verdict** → performance
metrics table → per-dimension sections (context usage, continuity, tool use,
reflexes/classification, alignment, subagents) → **hallucination check** (claims
verified vs DB/Torque) → **findings logged** (table, each cross-referenced to a
Torque task) → **observations / blog-content ideas** → **pointers** (session ids,
turn ids, task ids, file:line refs).

**Lens:** metrics-grounded; `file:line` / id-specific; verify-don't-trust; name
what is NOT working plainly; always include blog/content angles; one section per
dimension the operator named.

**Use as templates (most representative first):**
- `~/dev/chrispian/inbox/nanite-c256-harness-audit-followup-2026-05-19.md`
- `~/dev/chrispian/inbox/nanite-c267-c270-torque-task-creation-audit-2026-05-19.md`
- `~/dev/chrispian/inbox/nanite-subagent-timeout-audit-2026-05-19.md` (file:line audit)
- `~/dev/chrispian/inbox/nanite-c256-harness-audit-2026-05-18.md`
- `~/dev/chrispian/inbox/nanite-claude-cli-mode-rough-edges-2026-05-18.md`

---

## TORQUE AGENT GUIDE

**`/Users/chrispian/dev/agent-os/docs/torque-agent-guide`** — how to prepare,
run, and orchestrate Torque tasks. Read `README.md`,
`preparing-and-running-tasks.md`, `orchestrator-runs.md`, `troubleshooting.md`.
Today's run logs are under `logs/`; the run-log template is at
`templates/run-log-template.md` (every orchestrated task writes a run-log +
reflection there). The orchestrator session should read this guide before
starting `SP-20260518-0013`.

---

## OPEN THREADS / not in the sprint

- `CW-20260519-0069` / `0070` — messaging address self-discovery + federation
  bring-up (project `PRJ-20260416-0001`, the messaging program — separate sprint).
- `CW-20260519-0102` / `0104` / `0106` — MCP settings-health UI, chat-agent
  fabrication guard, MCP-callstack review. Filed `backlog`, **not** folded into
  `SP-20260518-0013` — fold in or leave per operator call.
- The DAR architect work (sessions c252/c254/c256) produced the doc package in
  `~/dev/hollis-labs/inbox/dar/` and Torque plan `CW-20260518-0042` (Track C spike).
- Boot-profile sessions c267 (Glyph) / c270 (Tether) created their tasks/sprints
  successfully post-MCP-fix (verified, no hallucination) — see the c267/c270 audit.
