# Audit fs/terminal proxying requirement per ACP agent

**Phase:** 5 — Verification & hardening (`TASKS/agent-host-acp`)
**Status:** not-started
**Depends on:** `09`, `10` (native adapters); `13`, `14`, `15` (bridge adapters, if not
deferred) — needs real, running ACP adapters to audit against, not a paper exercise.
**Touches:** none expected (audit-only task) unless a real requirement is found — in which
case this task's Done means is to scope the fix as new task(s), not build it inline. Repo:
`libs/go-agent-wrapper` + Nanite (whichever a real finding implicates).

## Context

`docs/engineering/architecture/17-acp.md`'s "Known limitations" section names this as a real,
open, per-agent unknown — not a settled no: "ACP lets the *agent* ask the *client* to do
filesystem/terminal work on its behalf (`session/request_permission`... `fs/read_text_file`/
`fs/write_text_file`, `terminal/*`)... Tether declined all of this MVP because it was safe to:
Claude and Codex already do their own fs/terminal work internally regardless of what any
client offers, so no request was ever coming. Nanite's host sits on the *other* side of this
exchange — as the client driving someone else's agent implementation... and it's genuinely
unverified whether those agents behave the same way Claude/Codex do... or actually expect the
client to serve them (in which case the host would need a small in-process fs/terminal
server, scoped to the existing sandbox boundary, to keep that agent fully functional)."

The doc is explicit this "has to be checked against each real agent's behavior at
implementation time, not assumed — if it turns out to be required, it's meaningfully more
scope than 'plug in an ACP client,' worth flagging as a possible hidden cost rather than
folding silently into the adapter work." This task exists specifically to do that check, now
that Phases 3-4 have real, running ACP adapters (OpenCode, Copilot CLI, and whichever of
Claude/Codex/Pi's bridges landed) to observe.

## What to do

1. For each real ACP adapter built in Phases 3-4 (OpenCode, Copilot CLI, and any bridge-
   driven agents that landed), run a real session that would plausibly trigger a
   filesystem or terminal operation (e.g. asking the agent to read/write a file, or run a
   shell command) and observe: does the agent do this work itself (same as Claude/Codex's
   confirmed native behavior), or does it send `fs/read_text_file`/`fs/write_text_file`/
   `terminal/*`/`session/request_permission` requests back to the client (Nanite's host),
   expecting them to be served?
2. If every audited agent does its own fs/terminal work (matching Claude/Codex's behavior) —
   this task is done, no code needed. Document the finding.
3. If any agent genuinely expects the client to serve these requests: **do not build the
   in-process fs/terminal server as part of this task.** Per this project's own "surface
   discoveries, don't silently expand scope" discipline (matching the pattern documented
   throughout `TASKS/ESCALATIONS.md` for real scope-growth findings), write up a new,
   properly-scoped task file describing exactly what's needed (a small in-process fs/terminal
   server scoped to the existing sandbox boundary, per 17-acp.md's own suggested shape) and
   flag it to the operator as new, real scope this batch didn't originally account for — this
   is meaningfully bigger than "plug in an ACP client," per the doc's own framing.

## Done means

- Every real ACP adapter built in Phases 3-4 has been checked against real session behavior
  for fs/terminal proxying requests, not assumed either way.
- If none require it: documented finding, no code changes.
- If any do: a new, explicitly-scoped follow-up task file exists, and the operator has been
  flagged that this batch surfaced real additional scope — not silently absorbed into an
  existing task's Work Log.
