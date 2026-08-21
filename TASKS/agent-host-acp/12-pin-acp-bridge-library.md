# Pin ACP bridge library(ies) for Claude/Codex/Pi

**Phase:** 4 — ACP bridge adapters for non-native agents (`TASKS/agent-host-acp`)
**Status:** reviewed — **resolved, operator sign-off recorded in `TASKS/ESCALATIONS.md`
(2026-08-21, "Task 12 resolved: ACP bridge library decision, explicit operator sign-off
recorded"). Decision: `agentclientprotocol/claude-agent-acp` (Claude),
`agentclientprotocol/codex-acp` (Codex), `svkozak/pi-acp` (Pi — pursued now, not deferred).
`beyond5959/acp-adapter` explicitly rejected (dormant 4+ months, source-verified Claude
backend does bare SIGKILL with no wire-level cancel, self-rated "Initial" Pi maturity). Real
interrupt/cancel capability was explicitly NOT the deciding criterion — the operator's own
framing: the real goal is ACP protocol uniformity for future config-based provider
extensibility with an honest per-adapter capabilities map, not immediate interrupt gain (which
Nanite doesn't have today anyway and isn't blocked on). The three per-provider bridges require
Node.js/npm at runtime for those three specific bridge-driven providers — an explicit,
reversible choice (the `acp.Client` interface already isolates callers from the concrete
bridge implementation), not a permanent stack commitment. Tasks `13`-`15` are unblocked.**
**Depends on:** `08` (ACP client abstraction — this task's decision needs to be evaluated
against a real target interface)
**Touches:** none yet (research/decision only). Repo: N/A until a decision is made.

---

> ### ⚠️ Escalation-gated — this is not a routine research task
>
> `docs/engineering/architecture/17-acp.md` states this explicitly, twice: "Which bridge(s)
> to pin is left undecided for now — **deliberately**, given how young and fast-moving this
> ecosystem is (the field itself widened mid-review: single-vendor bridges turned up that
> didn't exist in the first pass)." And: "Re-check library state and behavior at
> implementation time rather than trusting this review's snapshot."
>
> This is a genuine, unsettled design/library-selection decision, not a mechanical lookup —
> same posture as `TASKS/phase-5/06-make-http-middleware-plugin-extensible.md`
> (HTTP-middleware plugin extensibility), which stayed undispatched pending explicit operator
> input rather than defaulting to a guess. Follow that same discipline here: **do the research
> below, present findings + a recommendation, and stop for explicit operator sign-off before
> any adapter code is written** (tasks `13`-`15`). Log this task's findings and the eventual
> decision in `TASKS/ESCALATIONS.md`, following the standard entry format.

---

## Context

Three of the five providers 17-acp.md scopes for ACP support (Claude, Codex, Pi) have no
native ACP mode — the implementation tier for each is a third-party bridge library that
speaks ACP on the CLI's behalf. Per the doc's own validated (at review time) findings:

- `beyond5959/acp-adapter` — Go, embeddable, one library bridging Codex/Claude
  Code/Pi *together* (a single multi-provider dependency).
- `agentclientprotocol/claude-agent-acp` — wraps the official Claude Agent SDK, adopted
  under the ACP org itself, "updated within days" at the doc's last check.
- A `codex-acp` bridge — wraps Codex's own native `app-server` JSON-RPC.
- No confirmed single-provider bridge was named for Pi at review time.

The real criterion 17-acp.md names for evaluating any candidate: **whether a given bridge
actually wires ACP's `session/cancel` through to the provider's own native interrupt call,
rather than just acknowledging cancellation at the wire level and letting the turn finish
anyway** — the doc explicitly notes this is exactly what Tether's own current (stale) ACP-
server MVP does for cancellation, "evidence of an implementation gap in one specific hand-
rolled build, not proof of a protocol-level ceiling." This planning session's own research
(task `02`'s Context) independently confirmed that even Claude/Codex's own native `Stop()` in
`agentkit/agentsessions` today does *not* call any wire-level interrupt — so a bridge that
*does* wire `session/cancel` through to a real provider-native cancel would be a genuine
capability improvement over what Nanite has today, not just parity.

## What to do

1. Re-verify current state (as of dispatch time, not this doc's authoring date) of all
   candidate bridges named above, plus any that have appeared since (the doc explicitly warns
   the field "widened mid-review" once already) — maintenance activity, real production
   usage/stars/issues, and specifically: does `session/cancel` reach the provider's real
   native interrupt mechanism, or just acknowledge-and-let-finish? Test this directly against
   a real bridge instance if feasible, not just by reading its README/docs.
2. Evaluate the one-multi-provider-library vs. several-single-provider-libraries tradeoff
   explicitly — 17-acp.md frames this as "left undecided... which is the better bet."
   Consider: maintenance risk (one dependency vs. three), whether a single library's Claude/
   Codex/Pi implementations are equally mature or uneven, and how each affects task `08`'s
   ACP client abstraction (does a multi-provider library's own internal shape fit cleanly
   behind one Go interface, or would per-provider libraries fit more naturally?).
3. Present findings and a concrete recommendation. Log this task's research and the eventual
   decision in `TASKS/ESCALATIONS.md`, following the existing entry format (Raised by /
   Question-mismatch / Resolution / Follow-up) — this is exactly the shape of decision that
   log already exists to track (see the Phase 5 HTTP-middleware entry as the closest
   precedent).
4. **Stop and wait for explicit operator sign-off before dispatching `13`-`15`.** Do not
   default to a choice without it, unlike most of this project's other escalation categories
   (which default to a documented judgment call) — this one is explicitly named as needing
   real operator input given the ecosystem's immaturity.

## Done means

- A `TASKS/ESCALATIONS.md` entry exists recording this task's research and a clear
  recommendation.
- Explicit operator sign-off on which bridge(s) to pin (per-provider, and whether it's one
  multi-provider library or several) is recorded in that entry before any of `13`-`15` are
  dispatched.
- No adapter code has been written as part of this task.
