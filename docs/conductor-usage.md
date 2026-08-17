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

## Deterministic reflex triggers

Certain phrasings are matched deterministically (no LLM call) before the turn reaches Conductor's own reasoning, per `internal/reflex/` + the user-level overrides in `~/.nanite/reflexes/conductor-*.yaml`. These don't change *what* Conductor does (that's still driven by its system prompt) — they tag the turn for the UI's mode indicator and the raw-vs-sent audit log (`playbook_match_log`, `CW-20260816-0068`), and critically, they keep Conductor from being force-dispatched to a synchronous worker/planner subagent the way a bare phrase like "research X" normally would be.

| Trigger phrases (examples) | What it's for |
|---|---|
| "note for Nil: ...", "note to self", "capture a note for ..." | Note capture (see below for per-target routing) |
| "approve the checkpoint", "sign off on this", "reject the checkpoint" | Approval/checkpoint phrasing — Conductor acts on the verdict directly (`torque_task_checkpoint_respond`, `torque_sprint_approve`) |
| "drop the results in ...", "research this and drop it in ...", "file a task with the results" | Non-linear research-and-file — routes to `torque_task_create`, not an inline synchronous dispatch |
| "dispatch this to ...", "queue this up for ...", "hand this off to ..." | Dispatch-to-project — routes to Torque task creation for the target project's `orchestrator` to pick up |

Full phrase lists live in `~/.nanite/reflexes/conductor-*.yaml`; documented in `docs/agent-reflex-catalog.md`.

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
- Reflex phrase catalog conventions: `docs/agent-reflex-catalog.md`, `docs/reflex-authoring.md`
- Torque epic: `EP-20260816-0004` (tasks `CW-20260816-0062`, `CW-20260816-0065` through `-0070`, currently in `review` pending manual close)
- Follow-ups: `CW-20260816-0088` (resolver duplication), `CW-20260816-0089` (audit-log ambiguity), `CW-20260816-0090` (go-envelopes tagged release), `CW-20260816-0091` (Nil MCP registration)
