# Cut the support-ticket plugin and its frontend components

**Phase:** 0
**Status:** not-started
**Depends on:** none
**Touches:** `ui/src/components/**/TicketFormCard.tsx` (delete), `ui/src/components/**/TicketConfirmationCard.tsx` (delete), `ui/src/components/**/TicketInitFlow.tsx` (delete), `plugins/repos.yaml` (remove the support-ticket plugin entry, if registered there — verify exact registration mechanism at implementation time), the five orphan card types this plugin registers (`kb-result`, `giphy-modal`, `resolution-capture`, `ticket-form`, `ticket-confirmation` — see Context on which of these actually belong to support-ticket vs. giphy), `internal/chat/engine.go:1013` (the marker-parsing code `POST_DEMO_ISSUES.md` identifies — confirm whether this is support-ticket-specific or shared parsing infrastructure before touching it), doc references (see Context)

## Context

TASKS.md Phase 0 item 15 names support-ticket as a "confirmed demo" to cut. Decision log §32 states "no source or manifest exists anywhere in this workspace" for it. **This is false, verified directly during planning.** Real frontend source exists in this repo: `TicketFormCard.tsx`, `TicketConfirmationCard.tsx`, `TicketInitFlow.tsx`. `POST_DEMO_ISSUES.md` documents a real, recently-investigated production bug in this exact feature — a marker-parsing regression caused by a submodule bump, with the relevant backend engine code identified at `internal/chat/engine.go:1013`. `docs/architecture/plugin-it-support.md` is a real architecture doc describing it as "a standalone Nanite plugin using the FE plugin SDK." This reads as a real, if imperfect, in-progress feature someone was actively debugging recently — not a demo with no source.

**Operator decision, 2026-08-18 — final.** Cut in full anyway. Per the sharpened `docs/engineering/EXECUTION-PROCESS.md` escalation rule, `TASKS.md`'s decided action stands even when the decision-log's rationale ("no source exists") is factually wrong — correct the record (this Context section is that correction) and execute the removal. This is explicitly a bigger removal than "delete dead code" (real frontend components, a real bug someone was fixing) — size and review it accordingly.

**Note on `internal/chat/engine.go:1013`**: `POST_DEMO_ISSUES.md` identifies this as the backend code involved in the marker-parsing bug. Before removing/modifying it, confirm whether it's support-ticket-specific parsing logic (safe to remove alongside the rest of this feature) or shared envelope/marker-parsing infrastructure that other Card types also depend on (in which case only the support-ticket-specific *caller* of it should be removed, not the shared code itself). This wasn't traced precisely enough during planning to say which with confidence — verify at implementation time, don't assume either way.

**Note on the five orphan card types**: TASKS.md's original item 15 wording groups `kb-result`, `giphy-modal`, `resolution-capture`, `ticket-form`, `ticket-confirmation` together as belonging to "the giphy/oembed/support-ticket plugins." Based on naming, `ticket-form`/`ticket-confirmation`/`resolution-capture` most plausibly belong to support-ticket and `giphy-modal` to giphy (see `15a-cut-giphy.md`) — `kb-result` (knowledge-base result) also plausibly belongs to support-ticket's help-desk domain, but confirm this mapping against the actual manifest registration at implementation time rather than assuming from naming alone. Coordinate with `15a-cut-giphy.md` so `giphy-modal` isn't removed twice or missed by both.

## What to do

1. Delete `TicketFormCard.tsx`, `TicketConfirmationCard.tsx`, `TicketInitFlow.tsx` and any other support-ticket-specific frontend files found during a broader search of `ui/src/` for this plugin's components.
2. Find and remove the plugin's registration (check `plugins/repos.yaml` and any manifest referenced by `docs/architecture/plugin-it-support.md`).
3. Remove `ticket-form`/`ticket-confirmation`/`resolution-capture`/`kb-result` (confirm each belongs to support-ticket, not oembed/giphy, before removing) from wherever card types get registered (the "`nanite-legacy` workaround" decision log §32 mentions — locate this mechanism at implementation time).
4. Investigate `internal/chat/engine.go:1013` per the Context note above — remove the support-ticket-specific caller/logic; leave shared parsing infrastructure alone if that's what it turns out to be.
5. Remove doc references: `docs/architecture/plugin-it-support.md`, `docs/tool-naming-audit.md` if it references support-ticket, `CLAUDE.md` if it references it, and `POST_DEMO_ISSUES.md` (decide whether to delete the now-resolved-by-removal bug entry or leave it as historical record — leaning toward leaving `POST_DEMO_ISSUES.md` as historical record of a real incident, but note the decision in the Work Log).
6. Grep the whole repo for `ticket`/`Ticket`/`support-ticket`/`kb-result`/`resolution-capture` (being careful not to false-positive on unrelated uses of "ticket" if any exist elsewhere in the codebase) after the cut to confirm no dangling references remain.
7. Run `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...`, and `cd ui && npm run build`.

## Done means

- `TicketFormCard.tsx`, `TicketConfirmationCard.tsx`, `TicketInitFlow.tsx` no longer exist.
- The support-ticket plugin is no longer registered.
- The four orphan card types confirmed to belong to support-ticket are removed (coordinated with `15a-cut-giphy.md` on `giphy-modal`).
- `internal/chat/engine.go:1013`'s support-ticket-specific involvement is resolved one way or the other, with the Work Log documenting which (removed alongside the feature, or confirmed shared infrastructure left in place).
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass; frontend build passes.
- No remaining references to the support-ticket plugin or its components, confirmed by grep.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
