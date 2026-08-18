# Cut the four backend-only messaging card types

**Phase:** 0
**Status:** not-started
**Depends on:** none
**Touches:** `libs/go-envelopes/manifest/envelopes.yaml` (sibling repo, reached via a `go.mod` local `replace` directive — remove the `message-request`/`message-reply`/`message-notification`/`message-handoff` entries, ~lines 109-116), `internal/chat/envelope.go` (`InitCoreTypes` — confirm these four types disappear from the loaded set once the manifest entry is gone, no separate nanite-side registration expected), `ui/src/generated/plugin-envelopes.ts` (regenerate via `node scripts/generate-plugin-imports.mjs` after the manifest edit lands)

## Context — corrected, 2026-08-18, supersedes the original `14-verify-messaging-card-types.md`

TASKS.md Phase 0 item 14: "The four backend-only messaging card types (`message-request`/`reply`/`notification`/`handoff`) — no frontend component ever existed for them." Decision log §35: "backend-only, 'no frontend component yet' (unfinished scaffolding, not a deliberate omission)... and redundant with `agent_messages.kind`'s own CHECK constraint. Cut."

**The original planning-pass verification (recorded in the now-superseded `14-verify-messaging-card-types.md`) concluded these types didn't exist anywhere and treated the item as a no-op. That conclusion was wrong — a real execution-time check corrected it.** The original grep only searched the nanite tree itself; it missed two things: (1) `libs/go-envelopes/manifest/envelopes.yaml` — a **sibling repo**, reached only via `go.mod`'s local `replace` directive — genuinely registers all four types (~lines 109-116), with a comment that's a near-verbatim match for TASKS.md's own description: "No frontend component yet; registered for validation only." These four types are loaded live into `internal/chat/envelope.go`'s `InitCoreTypes` at startup today. (2) `ui/src/generated/plugin-envelopes.ts` also has a hit, but that path is `.gitignore`d and the original grep's tool silently respected that.

**Per the sharpened escalation rule** (`docs/engineering/EXECUTION-PROCESS.md`'s "Source of truth" section and worker step 7, adopted 2026-08-18): TASKS.md's decided action for this item — cut — stands regardless of the fact that the original planning pass's supporting verification was wrong. This is not a genuine stop-and-escalate case (no item-vs-item contradiction, nothing security/trust/data-integrity-sensitive, nothing genuinely ambiguous about what to do) — it's exactly the "correct the record, execute the decided action" scenario the sharpened rule exists for. `agent_messages.kind`'s CHECK constraint (migration `090`, the goose-renumbered former `089`) is confirmed a separate, real, unrelated mechanism (`internal/messaging`) and stays untouched.

## What to do

1. Remove the `message-request`/`message-reply`/`message-notification`/`message-handoff` entries from `libs/go-envelopes/manifest/envelopes.yaml` (sibling repo at `../../libs/go-envelopes` relative to this repo, or wherever the `replace` directive in `go.mod` actually points — confirm the path). If you can edit and commit there directly in this environment, do so. If you cannot (e.g. it needs a separate PR process you can't execute), do everything else in this task and clearly document in the Work Log exactly what's blocked and why — same handling as `13-cut-question-form.md`'s cross-repo note.
2. Confirm `internal/chat/envelope.go`'s `InitCoreTypes` no longer loads these four types once the manifest entry is gone (it should just fall out naturally — this file reads the manifest, it doesn't hardcode the four types separately, but verify this assumption rather than trust it).
3. Regenerate `ui/src/generated/plugin-envelopes.ts` via `node scripts/generate-plugin-imports.mjs` (after step 1 lands) and confirm `node scripts/generate-plugin-imports.mjs --check` passes clean.
4. Grep the whole repo (Go and TypeScript) for `message-request`/`message-reply`/`message-notification`/`message-handoff` after the cut to confirm no dangling references remain, including in `.gitignore`d generated files (don't rely on a `.gitignore`-respecting grep tool for this final check — use `grep` directly or pass `--no-ignore`).
5. Do NOT touch `agent_messages.kind`'s CHECK constraint or its real enum values (`request`/`reply`/`notification`/`handoff`/`subagent_result`) — confirmed separate, unrelated, real mechanism.
6. Run `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...`, and `cd ui && npm run build`.

## Done means

- The four card types no longer exist in the `go-envelopes` manifest (or the cross-repo edit is clearly documented as blocked, matching `13`'s handling).
- `internal/chat/envelope.go`'s loaded type set no longer includes them, verified directly (not assumed).
- `ui/src/generated/plugin-envelopes.ts` regenerates clean with no reference to any of the four.
- `agent_messages.kind`'s CHECK constraint and real enum values are unchanged.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass; frontend build passes.

## Work log

**2026-08-18 — first execution attempt (worker, worktree `agent-a443f7c0ab44c0b6a`):** Re-ran the planning-pass grep, found it was scoped only to the nanite tree and missed the sibling `go-envelopes` manifest and the `.gitignore`d generated TS file. Correctly identified both real hits and the near-verbatim comment match confirming these are the actual items TASKS.md/decision-log §35 describe. Treated this as a stop-and-escalate case per the task file's original wording (written before the sharpened escalation rule existed) and did not make the cut. No code was changed in that attempt.

**2026-08-18 — Orchestrator correction:** Per the sharpened escalation rule, this is not a genuine stop condition — TASKS.md's decided action stands. Rewrote this task file (renamed from `14-verify-messaging-card-types.md`) to reflect the real scope (a cross-repo cut, following the same pattern as `13-cut-question-form.md`) and re-dispatched for actual execution.

<Next worker fills in below: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
