# Rebuild `plan-review` as a `list-card` + `confirmation-card` composition

**Phase:** 6
**Status:** not-started
**Depends on:** none
**Touches:** `libs/go-envelopes/manifest/envelopes.yaml` (remove standalone `plan-review` entry), `ui/src/components/chat/envelopes/PlanReviewCard.tsx` (retire or fold into composed primitives), plan-creation call site (not yet traced — locate during implementation, see Context)

## Context

Architecture doc `08-cards.md`: *"`plan-review` → `list-card` + `confirmation-card`."* Decision log §36 same framing.

### Current implementation, verified — and one real gap left for this task to close

`PlanReviewCard.tsx` — `data: {plan_id, title, description?, status, steps: [{id, title}]}`. Same live-refetch shape as `todo-list`: re-fetches via `useQuery(['plans', data.plan_id], () => api.getPlan(...))`, with `useApprovePlan`/`useRejectPlan`/`useTogglePlanStep` mutations and a real interactive footer (Approve/Reject/Request changes). Referenced from `internal/subagent/service.go` (comments at lines ~1309, ~1527 describe the wire shape) but **the actual emitter call site was not traced during planning research** — a worker must grep `CreatePlan`/plan-creation call sites directly to find where `{"type":"plan-review",...}` is actually constructed, rather than assuming `subagent/service.go` is the emitter just because it references the shape in a comment.

## What to do

1. Locate the real `plan-review` emitter (grep plan-creation logic — likely wherever `todo`/`plan` records get created, possibly `internal/service/` or a dedicated planning package) before starting the rebuild — this task's own Context couldn't confirm it, don't assume.
2. Design the composition: `list-card` for the step list (with per-step toggle, mirroring `useTogglePlanStep`), `confirmation-card` for the Approve/Reject/Request-changes footer, both driven by the same live `useQuery(['plans', plan_id])` re-fetch pattern `PlanReviewCard.tsx` already uses.
3. Update the manifest to remove the standalone `plan-review` entry; regenerate `ui/src/generated/plugin-envelopes.ts`.
4. Update the real emitter (found in step 1) to produce the composed shape.
5. Verify approve/reject/toggle-step behavior is preserved identically through the composed card.

## Done means

- `plan-review` no longer exists as a standalone manifest entry; its behavior is fully reproduced via `list-card` + `confirmation-card` composition.
- A real plan review still renders, and approve/reject/toggle-step all work correctly in a live chat session.
- `cd ui && npm run build` passes; `node scripts/generate-plugin-imports.mjs --check` passes.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
