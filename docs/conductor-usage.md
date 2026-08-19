# Conductor — usage guide

**Status:** shipped (`EP-20260816-0004`, merged in [PR #258](https://github.com/hollis-labs/nanite/pull/258)), live on `nanite-api-service` as of 2026-08-16. This doc is a practical "how do I use it" companion to the architecture reference at `docs/architecture/conductor-console-design.md` and the full operating contract in `.nanite/agents/conductor.md` — read those two for the *why* and the *exact rules*; this doc is the *how to drive it* version, written for manual testing.

## What Conductor is

A single chat-facing durable agent (`harness` class, recipe id `conductor`, profile slug `conductor`) that sits across everything you have running at once. It does not do engineering work, does not sequence a project's task order, and does not reimplement cross-app plumbing — it **delegates, relays, captures, and surfaces**:

- **Delegates** per-project work to Torque + the existing `orchestrator` profile, and cross-app work to Tether/mux (`mux_session_*`/`mux_message_*`/`tether_registry_*`).
- **Relays** a delegated session's outcome as a distilled `report-card` summary + link, never a raw transcript.
- **Captures** notes/decisions to Vanta (`memory_write`), or to a named app's own store when one exists (currently: Nil).
- **Surfaces** pending approvals/checkpoints and peer messages without you having to ask.

It deliberately does **not** have `subagent_spawn`/`workflow_run` — if it ever seems to be trying to do work itself rather than delegate it, that's a bug, not a feature.

## Starting a session

Conductor is a durable-agent **recipe** (id `conductor`, kind `conductor`, shown as **"Conductor"** in the recipe-kind list), the same mechanism used for `orchestrator`/`planner`/`reviewer`. From the chat UI's durable-agent / "new agent" flow:

1. Pick the **Conductor** recipe.
2. The recipe's `ProfileRule` is `operator_selected`, so you'll be asked to pick the agent profile explicitly — choose **`conductor`** (`.nanite/agents/conductor.md`).
3. Provider defaults to `anthropic` with no fixed model (uses your session default); runtime kind `api` (chat-driven, not a CLI/PTY harness).
4. Optional recipe inputs worth filling in for a real test: **work root** (defaults `~/dev`), **project roster notes** (which Torque projects / Tether-registered apps you want Conductor tracking), **wake prompt** (kickoff instructions for first boot, e.g. "check in on nanite and fragments-engine").

Once created, talk to it like any other chat session.

## What to expect it to do

### Routine status — pull, not push
Ask "what's pending?" or "how's `<project>` doing?" and expect a digest, not a firehose — it checks `torque_task_list`/`torque_task_get`/`mux_session_get` on request rather than narrating every state change proactively.

### Approvals and blockers — push
A pending Torque checkpoint, a `torque_sprint_approve` decision, or a message from another durable agent (`message_send`/A2A) should reach you without asking — this rides on the platform's default `message_wake_policy: auto_summarize` (`CW-20260816-0065`), which proactively wakes Conductor's session when a peer sends it something.

### Distilled output, not transcripts
When Conductor relays a completed delegated session, expect a `report-card` (title, a couple of metrics, a one/two-sentence summary) with a **`session_link`** — either a real clickable URL or a plain-text identifier (e.g. a Torque task id) when no direct link exists yet. If you ever see a raw tool-output dump instead, that's a regression worth flagging.

## Deterministic reflex triggers — RETIRED (correction + status)

**As of `TASKS/phase-4/03-migrate-promptrouter-to-reflexes.md`, this mechanism no longer runs.** `internal/promptrouter` (the phrase-match router this section originally described) is deleted in full, and its `~/.nanite/reflexes/*.yaml` user-override loader — the mechanism that read `~/.nanite/reflexes/conductor-*.yaml` — is retired with it. The six `conductor-*.yaml` files themselves still exist on disk (this migration did not delete any operator-owned file outside the repo) but nothing loads them anymore.

**Correcting the record on what this mechanism actually did**, since the sentence above ("keep Conductor from being force-dispatched") turned out to describe a wiring the code didn't actually have: `~/.nanite/reflexes/*.yaml` overrides only ever fed `internal/mcp/self_tools_dispatch.go`'s `callExecuteTask` — the reflex matcher that runs **inside** a `task_execute` call, after the Chat/Conductor LLM has already decided to dispatch. Nothing in that call site could have prevented `task_execute` from being invoked in the first place; that decision was always the LLM's own tool choice (or, before it was retired, the upstream agent-broker's Rule 1 — a different call site the conductor YAML files' own header comments named as their intended target, which they were never actually wired into). Since every one of the six conductor reflexes left `resolves_to.profile` empty (by design — see the retired files' own comments), a match at the wired call site never overrode the dispatch target either. **The practical effect of a conductor reflex match was therefore limited to an audit-log row in `playbook_match_log`** (and a since-cut session-mode-bus signal — Modes were cut in full, `TASKS/phase-0/21-cut-modes.md`) — not a routing guarantee. Conductor's actual behavior (staying in chat and calling `memory_write`/`torque_task_create`/`torque_task_checkpoint_respond` directly for these phrasings, per `.nanite/agents/conductor.md`) was always driven by its system prompt, same as this doc's own next sentence already said — so retiring this mechanism is not expected to change Conductor's observable behavior for these phrasings, only to drop the (already-limited) audit tagging.

**What replaces it:** reflexes are DB-authoritative now (`agent_reflexes` table, CRUD via `internal/api/reflexes.go`). The new `dispatch_to_agent` action kind these conductor entries would migrate onto is a positive "route TO this agent" primitive — it has no representation for "stay in chat, don't dispatch" (the semantic these six entries actually wanted), so they were not mechanically migrated; see the migration task's Work Log for the full reasoning. If deterministic audit tagging for these phrasings is wanted again, it would need a new reflex action kind (or a dedicated `event_log` write) — not built as part of this migration.

The table below is preserved for historical reference (what the six retired conductor override files covered) — it does not describe live behavior.

| Trigger phrases (examples) | What it was for |
|---|---|
| "note for Nil: ...", "note to self", "capture a note for ..." | Note capture (see below for per-target routing) |
| "approve the checkpoint", "sign off on this", "reject the checkpoint" | Approval/checkpoint phrasing — Conductor acts on the verdict directly (`torque_task_checkpoint_respond`, `torque_sprint_approve`) |
| "drop the results in ...", "research this and drop it in ...", "file a task with the results" | Non-linear research-and-file — routes to `torque_task_create`, not an inline synchronous dispatch |
| "dispatch this to ...", "queue this up for ...", "hand this off to ..." | Dispatch-to-project — routes to Torque task creation for the target project's `orchestrator` to pick up |

Full phrase lists still live in `~/.nanite/reflexes/conductor-*.yaml` (unloaded); the schema they used is documented (retired) in `docs/promptrouter-catalog.md`.

## Note capture, by target

| You say | What happens |
|---|---|
| "note for Nil: ..." | Calls `nil_create_item` (`kind: "note"`) — a real Nil vault item. **Currently inert**: `nil-mcp` isn't yet registered as an MCP upstream (`CW-20260816-0091`, follow-up filed), so expect Conductor to report the tool call failed rather than silently falling back to Vanta. |
| "note for FE" / "note for Fragments Engine" | Falls back to `memory_write` (Vanta) — Fragments Engine has no note/fragment-creation MCP tool at all (confirmed investigation, `CW-20260816-0070`). Conductor should tell you explicitly it landed in Vanta, not FE. |
| "note to self" / anything unaddressed | `memory_write` (Vanta), as expected. |

## Known limitations (not bugs — tracked follow-ups)

- **Nil note-capture is wired but not reachable yet** — `CW-20260816-0091`. Infra-side (Cerberus/mux catalog) registration still needed.
- **Fragments Engine has no note-capture surface at all** — would need app-side FE work, out of scope for Nanite.
- **No dedicated queue/approvals UI panel** — conversational only for v1, by design (`docs/architecture/conductor-console-design.md`'s non-goals).
- **`CW-20260816-0072`** (deterministic cross-project dispatch-timing rules, e.g. "don't start project B until project A's worker finishes") is explicitly deferred to a future pass, not built yet — Conductor doesn't yet enforce any ordering across projects beyond what you tell it.

## Suggested manual test pass

1. **Boot** — create a Conductor session per "Starting a session" above; confirm it introduces itself as a delegator, not a worker.
2. **Routine pull** — ask "what's pending?" with no other setup; expect either an empty/quiet digest or a summary of real Torque/mux state, not silence or a crash.
3. **Dispatch-to-project** — say something like "dispatch this to nanite: fix the X bug" and confirm a Torque task gets created (`torque_task_create`) rather than Conductor trying to fix it inline.
4. **Research-and-file** — "research Y and drop the results in a task" — confirm it creates a task rather than doing the research itself synchronously.
5. **Approval surfacing** — create a Torque checkpoint on some task and confirm Conductor proactively raises it (or surfaces it on the next "what's pending?").
6. **Note capture (unaddressed)** — "note to self: ..." — confirm it lands in Vanta (`memory_recall` it back in a later turn).
7. **Note capture (Nil)** — "note for Nil: ..." — expect a clear failure message (tool unreachable), not a silent Vanta fallback pretending it worked.
8. **Note capture (FE)** — "note for FE: ..." — expect an explicit "this went to Vanta, not FE" callout.
9. **Delegated-work relay** — trigger or wait for a real per-project completion (e.g. an Orchestrator-run task finishing) and confirm the relay is a `report-card` with a `session_link`, not a transcript paste.
10. **Cross-app peer wake** — from another durable agent, `message_send` to the Conductor session and confirm it wakes proactively rather than sitting until polled.

## Where to look for more

- Architecture/design rationale: `docs/architecture/conductor-console-design.md`
- Full operating contract (system prompt): `.nanite/agents/conductor.md`
- Reflex phrase catalog conventions: `docs/promptrouter-catalog.md`, `docs/promptrouter-authoring.md`
- Torque epic: `EP-20260816-0004` (tasks `CW-20260816-0062`, `CW-20260816-0065` through `-0070`, currently in `review` pending manual close)
- Follow-ups: `CW-20260816-0088` (resolver duplication), `CW-20260816-0089` (audit-log ambiguity), `CW-20260816-0090` (go-envelopes tagged release), `CW-20260816-0091` (Nil MCP registration)
