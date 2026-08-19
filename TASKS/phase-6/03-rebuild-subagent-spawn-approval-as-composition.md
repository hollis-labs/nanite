# Rebuild `subagent-spawn-approval` as an `approval-card` composition (larger rebuild than `todo-list`/`plan-review`)

**Phase:** 6
**Status:** not-started
**Depends on:** none
**Touches:** `libs/go-envelopes/manifest/envelopes.yaml` (remove standalone `subagent-spawn-approval` entry, or keep the type name but change its component wiring — see What to do), `ui/src/components/chat/envelopes/SubagentSpawnApprovalCard.tsx` (retire, folding its UI into/alongside `ApprovalCard`), `internal/subagent/service.go:848` (`svc.approver.Emit(ctx, run.ParentSessionID, "subagent-spawn-approval", payload)` — the real emitter), `internal/chat/envelope_response_subagent.go` (the registered response handler — `chat.RegisterResponseHandler("subagent-spawn-approval", ...)`, `internal/service/container.go:1231`), `internal/api/sessions.go` (a conditional at ~line 818 gating recovery-related handling on `inst.EnvelopeType != "subagent-spawn-approval" && ... != "elicitation-prompt"` — must not break)

## Context

Architecture doc `08-cards.md`: *"`subagent-spawn-approval` → `approval-card` + subagent-specific data via the existing `props` discriminator mechanism (already used for `approval-card`/`proposal-card`, just not applied consistently)."* Decision log §36 same framing.

### This is a materially bigger rebuild than `04`/`05` — verified, flag this sizing explicitly

Unlike `todo-list`/`plan-review` (thin wrappers over a live re-fetch, swappable for a composed primitive with the same shape), `subagent-spawn-approval` today does **not** reuse `ApprovalCard` at all — it is its own fully custom, self-contained component (`SubagentSpawnApprovalCard.tsx`, `envelope`-shaped prop, `data: {run_id, role, prompt, mode, parent_agent_id?, timeout_seconds?, inputs_json?, risk_level?}`): a risk-level badge, collapsible prompt/advanced sections, a reason textarea, and approve/reject buttons posting via the generic typed-response mechanism. "Approval-card + subagent-specific props" means **deleting a working, materially more elaborate bespoke component and re-implementing its UI (risk badge, collapsibles, reason field) inside or alongside `ApprovalCard`** — not a shape-preserving swap like `04`/`05`. Scope and estimate this task accordingly; do not treat it as the same size as the other two composition rebuilds.

### The `props` discriminator's real current mechanics — read before designing

`manifest.yaml` declares `props: approval`/`props: proposal` only for `approval-card`/`proposal-card` — but the actual generated frontend registry does **not** use those values as-is. A host-side `CORE_OVERRIDES` map (`scripts/generate-plugin-imports.mjs:38-47`) force-overrides `props` to `"envelope"` for `approval-card`, `proposal-card`, `confirmation-card`, `subagent-spawn-approval`, and `elicitation-prompt` — because interactive cards need the *whole* envelope wrapper (to read `prior_response` for post-reload hydration), which a stripped single-field `approval`/`proposal` prop would hide. Confirmed in `EnvelopeRenderer.tsx:189-212`: the `"approval"`/`"proposal"` prop-branches exist in the switch but are **currently unreachable** from both the core registry (always overridden to `"envelope"`) and dynamic plugin registrations. **Any new composed card needing response-routing + prior_response hydration should be wired via `props: "envelope"` + a `CORE_OVERRIDES` entry, matching the pattern the 5 existing interactive cards already use — not the manifest's own `approval`/`proposal` discriminator values, which are effectively vestigial today.**

### A real call site that must not break

`internal/api/sessions.go` (~line 818) gates recovery-related envelope handling on `inst.EnvelopeType != "subagent-spawn-approval" && inst.EnvelopeType != "elicitation-prompt"` — if this task changes the emitted `type` string, this comparison (and any other string-literal `"subagent-spawn-approval"` match) must be updated in lockstep. Grep for every literal occurrence of `"subagent-spawn-approval"` before finishing, not just the emitter/handler/component sites already known.

## What to do

1. Design the risk-badge/collapsible-sections/reason-field UI as an extension of `ApprovalCard` (via its `data`/`props` shape) rather than a bolt-on separate component — this is real UI design work, not a mechanical type swap.
2. Decide whether the manifest `type` string stays `subagent-spawn-approval` (same wire type, different component wiring underneath) or changes — if it changes, update every literal string match across the codebase (see the `sessions.go` gate above and any others found by grep).
3. Wire response-routing through `props: "envelope"` + a `CORE_OVERRIDES` entry, matching the existing interactive-card pattern (not the vestigial manifest `props` value).
4. Update `internal/subagent/service.go:848`'s emitter and `envelope_response_subagent.go`'s response handler registration to match whatever `type`/shape decision was made.
5. Verify the full approve/reject round-trip, prior-response hydration after a page reload, and the `sessions.go:818` recovery-gating conditional all still work correctly post-rebuild.

## Done means

- `subagent-spawn-approval`'s full current UI (risk badge, collapsible sections, reason field, approve/reject) is reproduced through `ApprovalCard` + composition, not lost in the rebuild.
- Every literal `"subagent-spawn-approval"` string match across the codebase is updated consistently (or confirmed unchanged, if the type string itself doesn't change).
- Prior-response hydration after a page reload works correctly (regression check — this is the specific thing `props: "envelope"` exists to preserve).
- `cd ui && npm run build` passes; `node scripts/generate-plugin-imports.mjs --check` passes.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- Verified end to end in a real session: a real subagent spawn approval request, approved and rejected paths both exercised.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
