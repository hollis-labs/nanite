# Cut `question-form` (Cards)

**Phase:** 0
**Status:** not-started
**Depends on:** none
**Touches:** `libs/go-envelopes/manifest/envelopes.yaml` (line ~57, sibling repo — see Context for cross-repo handling), `internal/service/chat_generate.go` (~lines 1837-1842, the special-cased `CreateEnvelopeInstance` gate), `ui/src/components/chat/envelopes/InterviewCard.tsx` (delete), `ui/src/generated/plugin-envelopes.ts` (regenerate via `node scripts/generate-plugin-imports.mjs` after the manifest edit lands), `ui/src/components/chat/ChatMessage.tsx` (lines ~21, 26 — drift-fix references), `internal/mcp/self_tools.go` (lines ~255, 267 — doc-only mentions), `internal/mcp/self_tools_dispatch_executor.go` (line ~31 — doc-only), `internal/mcp/self_tools_transport.go` (line ~809 — doc-only), `internal/envelope/validator.go` (line ~54 — doc-only)

## Context

TASKS.md Phase 0 item 13: "`question-form` (Cards) — special-cased in one specific place in the turn loop; bounded deletion, no dependency on the Cards primitive-composition work." Decision log §34: "Confirmed as a pre-envelope-system legacy attempt, not a real primitive — it already gets special-cased persistence handling in the core turn loop unlike every other envelope type (only `question-form` triggers a `CreateEnvelopeInstance` call from that path), which is itself evidence it doesn't fit the uniform model. Cut alongside the rebuild of what it was trying to do as a proper composed primitive if a real need for structured multi-field user input resurfaces." Architecture doc `08-cards.md`'s "Cut" section repeats this.

**Verified against real code (2026-08-18) — this is a real cut, but not a stub; the frontend component is a complete, working feature, not vestigial scaffolding.**

- **Backend type registration**: `libs/go-envelopes/manifest/envelopes.yaml` (line ~57) — the external `go-envelopes` module, checked out locally at `../../libs/go-envelopes` and referenced via a `replace` directive in `go.mod` (real GitHub repo `github.com/hollis-labs/go-envelopes`). This project's own `CLAUDE.md` documents this module as the manifest source of truth for core envelope types.
- **Frontend**: `ui/src/components/chat/envelopes/InterviewCard.tsx` is a complete, working React form component — not a stub — registered in `ui/src/generated/plugin-envelopes.ts` (lines ~67-75, lazy-loaded, `source: "core"`).
- **The turn-loop special case**, confirmed exactly as the decision log describes: `internal/service/chat_generate.go:1837-1842` — `if env.Type != "question-form" || env.ID != ""` gates a `CreateEnvelopeInstance` call that fires for no other envelope type. This is the concrete evidence cited for the cut — every other Card type flows through the uniform envelope-persistence path; `question-form` alone needed a special branch, which is exactly the kind of type-proliferation-without-composition this whole Cards redesign (see `08-cards.md`) exists to stop.
- **Doc-only mentions** (not registration, no functional effect either way): `internal/mcp/self_tools.go:255,267`, `internal/mcp/self_tools_dispatch_executor.go:31`, `internal/mcp/self_tools_transport.go:809`, `internal/envelope/validator.go:54` — these reference `question-form` in comments/doc strings, not in registration or dispatch logic. Update them to stop describing a type that no longer exists, but no behavior change is at stake.
- **Frontend drift-fix**: `ui/src/components/chat/ChatMessage.tsx:21,26` — references worth checking during implementation; confirm exactly what these lines do before editing (not fully traced in this planning pass).

**Cross-repo note, same pattern as `12-cut-agentconstraints-maxturns`'s envelope-manifest edit**: removing `question-form` from the manifest means editing `libs/go-envelopes/manifest/envelopes.yaml` in the sibling repo. Confirm with the Orchestrator how commits to that repo should land (separate PR vs. direct local commit in this session) before assuming a same-session edit-and-commit there is acceptable — this is a process question, not a code question.

## What to do

1. Remove the `question-form` entry from `libs/go-envelopes/manifest/envelopes.yaml` (sibling repo — confirm landing process with the Orchestrator first, per the cross-repo note above).
2. In `internal/service/chat_generate.go`, remove the `question-form`-special-cased `CreateEnvelopeInstance` gate (~lines 1837-1842). Confirm no other envelope type needs an equivalent path — if the generic envelope-persistence path already covers what this special case did for every other type, this should be a pure deletion, not a rewrite.
3. Delete `ui/src/components/chat/envelopes/InterviewCard.tsx`.
4. Regenerate `ui/src/generated/plugin-envelopes.ts` via `node scripts/generate-plugin-imports.mjs` (after step 1 lands in the sibling repo — the generator reads that repo's manifest) and confirm `node scripts/generate-plugin-imports.mjs --check` passes clean.
5. Update the doc-only mentions in `internal/mcp/self_tools.go`, `self_tools_dispatch_executor.go`, `self_tools_transport.go`, `internal/envelope/validator.go` to stop referencing `question-form` as if it still exists.
6. Check and update `ui/src/components/chat/ChatMessage.tsx:21,26` — trace what these lines actually reference before editing.
7. Grep the whole repo (Go and TypeScript) for `question-form`/`QuestionForm`/`InterviewCard` after the cut to confirm no dangling references remain.
8. Run `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...`, and `cd ui && npm run build`.

## Done means

- `question-form` no longer exists in the `go-envelopes` manifest, the backend turn loop, or the frontend component registry.
- `internal/service/chat_generate.go`'s envelope-persistence path has no `question-form`-specific branch — every envelope type (what remains of them) flows through the same uniform path.
- `InterviewCard.tsx` is deleted; `ui/src/generated/plugin-envelopes.ts` regenerates clean with no reference to it.
- If the sibling-repo edit (`libs/go-envelopes`) couldn't be completed in this session (e.g. it needs its own PR/review process), that's flagged explicitly in the Work Log — this task isn't fully "done" until that repo's manifest is actually updated, even if the nanite-side changes land first.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...`, and the frontend build all pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
