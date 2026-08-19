# Build the interactive-table-with-row-actions primitive

**Phase:** 5
**Status:** not-started
**Depends on:** none
**Touches:** `libs/go-envelopes/manifest/schemas/table-card.schema.json` (currently `additionalProperties: false` at root and column level — needs a real schema change, not just a frontend addition), `ui/src/components/chat/envelopes/primitives/TableCard.tsx`, `internal/chat/envelope_handler.go` (`ResponseHandler` registry — the reuse target, see Context), `internal/api/envelopes.go` (`handleEnvelopeRespond` — confirm no change needed, generic already)

## Context

Architecture doc `08-cards.md`: *"Interactive tables with row-level actions are being built as a first-class, provider-agnostic primitive extension to `table-card`... previously proven feasible as a one-off plugin implementation, now being generalized."* Decision log §37: *"design fresh rather than porting the old plugin-specific implementation."*

### No prior implementation exists in this repo — confirmed, reinforces "design fresh"

Grepped `internal/`, `ui/src/`, and git history for table/action/row-action terms and any Torque reference — zero hits, no prior plugin implementation reachable from this repo. Decision log §37's "previously tested and working as a one-off plugin implementation" almost certainly refers to something in a sibling app's (Torque's) own codebase or an already-deleted branch, not anything portable from here. This reinforces the decision log's own instruction to design fresh — there is nothing to port even if porting were preferred.

### Base primitive and response-routing mechanism, both real and ready to extend

`table-card` (`ui/src/components/chat/envelopes/primitives/TableCard.tsx` — sortable columns, `data: {title?, columns: [{key,label,sortable?}], rows: Record<string,primitive>[], caption?}`) is the base to extend. Its schema (`libs/go-envelopes/manifest/schemas/table-card.schema.json`) is strict (`additionalProperties: false` on both root and column objects) — adding row/column actions requires a real schema change, not just new frontend rendering logic.

**Response-routing is already fully generic, not `approval-card`-specific** — confirmed: `POST /api/envelopes/{id}/respond` (`internal/api/api.go:498` → `handleEnvelopeRespond`, `internal/api/envelopes.go:27`) looks up the envelope instance by ID, validates the response `kind` matches the stored `envelope_type`, and dispatches via `chat.LookupResponseHandler(envelopeType)` — a plain `map[string]ResponseHandler` registry (`internal/chat/envelope_handler.go:36-68`) any envelope type can bind into via `chat.RegisterResponseHandler(type, handler)`. **This task needs no new transport mechanism** — just (a) a `ResponseHandler` registered for the new interactive-table type(s), and (b) `EnvelopeInstance` creation on emit (same as `subagent-spawn-approval`/`question-form` already do), so a response has an addressable `id`.

## What to do

1. Design the `actions` capability on `table-card`'s schema: per-row and/or per-column, schema-validated action definitions (an action ID, label, and confirmation requirement at minimum) — reference Adaptive Cards' `Action.*` element model as a design touchstone per the architecture doc, without copying it wholesale; fit it to this codebase's existing `props`/discriminator conventions.
2. Extend `table-card.schema.json` to allow the new `actions` field (root and/or column level, per the design in step 1) — this is a real, deliberate schema change to a currently-strict (`additionalProperties: false`) schema, not an additive no-op.
3. Extend `TableCard.tsx` to render row/column action buttons and POST a response via the existing `/api/envelopes/{id}/respond` mechanism.
4. Register a `ResponseHandler` for the new action-response shape, following the exact registration pattern `subagent-spawn-approval`'s handler already uses.
5. Wire `EnvelopeInstance` creation on emit for any plugin/backend code that wants to use this primitive, so responses have an addressable envelope ID.
6. Build a real, working example (a test plugin or a genuine first consumer, if one exists in this pass's scope) demonstrating a real row action round-trip.

## Done means

- `table-card`'s schema supports a real, schema-validated `actions` capability at row and/or column level.
- A real interactive table, with at least one working row action, has been exercised end to end in a live session (emit → render → click → respond → backend handles the response) — not just a static rendering test.
- `cd ui && npm run build` passes; `node scripts/generate-plugin-imports.mjs --check` passes.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
