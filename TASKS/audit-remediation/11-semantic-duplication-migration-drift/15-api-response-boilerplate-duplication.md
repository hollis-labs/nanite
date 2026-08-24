# `internal/api` response/decode boilerplate cleanup — bypassed shared helper, plus CRUD-family duplication

**Phase:** Wave 6 — Semantic duplication / migration drift
**Status:** reviewed
**Depends on:** none
**Touches:** `internal/api/api.go` (`API`, `a.decode`), `internal/api/catalog.go` (`catalogState`), `internal/api/plugins.go` (`pluginManagerState`), `internal/api/provider_manage.go`, `internal/api/plugin_config.go`, `internal/api/agent_capabilities.go`.

`requires_architect_decision: false` — both bundled findings are low-priority mechanical cleanup with no semantic-divergence risk.

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 6 — semantic duplication and migration drift · **Dispatch unit:** `W6b`
> - **Depends on:** `01/01`, `08/09`, `08/10`
> - **Blocks:** none
> - **Parallel-safe with:** **wants `internal/api` to itself**
> - **Gated on:** none
> - **requires_security_review:** false · **requires_regression_test:** true

## Context

### Findings addressed
- `GO-API-006` — severity low, confidence high. `docs/audits/2026-08-21-go-quality/REPORT.md` §8.5; `docs/audits/2026-08-21-go-quality/findings.json` id `GO-API-006`.
- `GO-API-009` — severity **informational**, confidence high. `docs/audits/2026-08-21-go-quality/REPORT.md` §8.5; `docs/audits/2026-08-21-go-quality/findings.json` id `GO-API-009`.

Bundled into one task because both are `internal/api` duplication findings from the same package review, both are low-priority/mechanical, and neither carries semantic-divergence risk — but they are two structurally different problems within the file, kept as separate sub-sections below.

### GO-API-006 — 3 `*API`-receiver call sites bypass the existing shared `a.decode` helper

**Root cause:** `internal/api` has 3 parallel handler-state types — `API`, `catalogState`, `pluginManagerState` — each with its own response/decode boilerplate. The audit's own §8.5 cohesion verdict is explicit that **2 of these 3 duplications are architecturally justified**: `catalogState` and `pluginManagerState` are genuinely different receiver types from `API`, so their having their own decode/response boilerplate is a defensible, non-drifting design choice, not a bug. The actual finding is narrower: **3 call sites that already have a receiver of type `*API`** — specifically in `internal/api/provider_manage.go` and `internal/api/plugin_config.go` — **bypass the existing shared `a.decode` helper for no apparent reason**, hand-rolling their own decode logic instead of calling the helper that's already sitting right there on their own receiver type. This is classification **(1) textual-only boilerplate**, and unlike GO-API-004 (a related-but-separate `internal/api` finding about schedule/settings validation duplication, tracked elsewhere), no behavioral inconsistency was found riding on this specific duplication — it's a pure "why isn't this using the helper it already has access to" question.

**Current behavior:** the audit's evidence array is empty; locate the 3 bypassing call sites via `grep -n 'json.NewDecoder\|json.Unmarshal' internal/api/provider_manage.go internal/api/plugin_config.go` (or however `a.decode` itself is implemented — read `a.decode`'s definition in `internal/api/api.go` first to know what pattern to search for) before starting.

### GO-API-009 — 7 `dupl`-flagged pairs within `agent_capabilities.go`'s 4 parallel CRUD families

**Root cause:** `internal/api/agent_capabilities.go` implements 4 parallel CRUD families — known-tools, known-skills, procedures, knowledge-seeds — and the `dupl` tool confirmed 7 flagged pairs of near-identical code within them. This is mechanical, low-risk, and the audit itself frames the fix as **"a judgment call on whether a generic helper is worth the abstraction cost; not required."**

**Current behavior:** the audit's evidence array is empty beyond the file name and the count (7 pairs); re-run `dupl` against `internal/api/agent_capabilities.go` (or read the raw audit log if still present in the repo) to get the current, specific pair locations before deciding whether/how to consolidate.

### Desired invariant

For GO-API-006: every `*API`-receiver handler in `internal/api` that needs to decode a request body uses the existing shared `a.decode` helper — no `*API`-receiver call site hand-rolls its own equivalent. For GO-API-009: at the implementer/reviewer's judgment, either the 4 CRUD families share more logic via a generic helper, or the current per-family duplication is explicitly accepted as a reasonable tradeoff (both are valid closing states for this specific finding, per the audit's own "not required" framing).

## What to do

### GO-API-006
1. Read `a.decode`'s definition in `internal/api/api.go` to confirm its exact signature and behavior.
2. Find the 3 `*API`-receiver call sites in `provider_manage.go` and `plugin_config.go` that bypass it, and confirm each is actually decoding the same kind of thing `a.decode` already handles (not some genuinely different decode shape that would explain the bypass) — read each site's surrounding code before assuming the bypass is unjustified.
3. Replace each bypassing call site with a call to `a.decode`, preserving exact existing behavior (error message wording, status codes on decode failure, etc. — these should not change unless `a.decode`'s existing behavior for those cases already differs meaningfully from the bypassed code, in which case flag this as a real behavioral question, not a silent change).

### GO-API-009
1. Re-run `dupl` (or read the existing raw audit log) against `internal/api/agent_capabilities.go` to get the current 7 pairs' exact locations.
2. Make the judgment call the audit explicitly defers: either extract a generic CRUD-family helper covering the shared shape across known-tools/known-skills/procedures/knowledge-seeds, or explicitly close this finding as "accepted, not worth the abstraction cost" with a one-line note recorded in Work log explaining the reasoning (e.g. if the 4 families are likely to diverge further in the near future, a generic helper would be premature abstraction working against that; if they're stable and unlikely to diverge, the helper is worth it).

### Non-goals
- Not touching `catalogState`'s or `pluginManagerState`'s own decode/response boilerplate — the audit explicitly judges these 2 as architecturally justified, not duplication to fix.
- Not addressing GO-API-004 (the related schedule/settings validation-in-transport-layer duplication) — that is a separate, already-tracked finding with its own disposition, not folded into this task.

## Tests required

- GO-API-006: existing tests for the 3 affected handlers (`provider_manage.go`, `plugin_config.go`) must pass unchanged after switching to `a.decode` — confirm decode-failure behavior (malformed JSON, wrong content type, etc.) is identical before and after, since this is the part most likely to silently change if `a.decode`'s error handling differs subtly from the bypassed code.
- GO-API-009: if a generic helper is extracted, existing tests for all 4 CRUD families must pass unchanged; if the finding is closed as "accepted," no new test is required.

## Prevention

GO-API-006: none needed beyond the fix — using the existing shared helper consistently prevents a 4th bypass from appearing in the future by making it the obvious, discoverable path (a code reviewer is more likely to catch "why isn't this using `a.decode`" once it's the established pattern everywhere else on the `*API` receiver). GO-API-009: not applicable if closed as accepted; if extracted, the generic helper itself is the prevention mechanism for a future 5th CRUD family being written as a 5th independent copy.

## Verification

```bash
go build ./internal/api/...
go vet ./internal/api/...
go test ./internal/api/... -run 'ProviderManage|PluginConfig|AgentCapabilities' -v
```

Observable behavior required for PASS: `go build`/`go vet`/`go test` all pass; the 3 `*API`-receiver bypass sites now use `a.decode`; the GO-API-009 judgment call is recorded in Work log either way (extracted-and-tested, or explicitly accepted-and-closed).

## Risk / rollback

Both findings are low risk. GO-API-006's main risk is a subtle behavioral change in decode-failure handling if `a.decode`'s existing behavior differs from the bypassed code in some edge case — mitigate by diffing behavior explicitly, not just confirming the happy path compiles. GO-API-009 carries essentially no risk if closed as accepted; low risk if a generic helper is extracted, mitigated by the existing per-family test coverage. Rollback for either is a per-file revert.

## Done means

- [x] All 3 `*API`-receiver bypass sites (GO-API-006) identified and switched to `a.decode`.
- [x] Decode-failure behavior confirmed identical before/after for all 3 switched sites.
- [x] GO-API-009's 7 pairs re-confirmed via a fresh `dupl` run or raw log read.
- [x] GO-API-009 judgment call made and recorded: generic helper extracted (with passing tests), or explicitly accepted and closed with reasoning.
- [x] `go build`, `go vet`, `go test ./internal/api/...` all pass.

## Work log

- 2026-08-24 worker: Confirmed `a.decode` in `internal/api/api.go` remains the shared `*API` helper at `api.go:607`, defers `r.Body.Close()`, then calls `json.NewDecoder(r.Body).Decode(v)`.
- 2026-08-24 worker: Confirmed the 3 bypasses were all ordinary request-body decodes with the same bad-request path: `handleUpdateProvider` and `handleSetProviderAPIKey` in `provider_manage.go`, plus `handleUpdatePluginConfig` in `plugin_config.go`. Switched all three to `a.decode` and preserved the existing `400` response wording: `"invalid JSON: "+err.Error()`.
- 2026-08-24 worker: Added `TestProviderManageAndPluginConfigMalformedJSONUsesSharedDecode` to cover malformed JSON for the three switched handlers. Wrong-content-type behavior is unchanged by inspection because neither the prior local decoders nor `a.decode` check `Content-Type`; they only decode the body.
- 2026-08-24 worker: Re-ran `dupl -plumbing internal/api/agent_capabilities.go`; current pairs are `24-44` with `180-200`, `180-200` with `362-382`, `362-382` with `510-530`, `510-530` with `24-44`, plus `146-165` with `328-347`, `328-347` with `476-495`, `476-495` with `639-658`, and `639-658` with `146-165`. Also confirmed the broader non-test package run (`find internal/api -maxdepth 1 -name '*.go' ! -name '*_test.go' | sort | dupl -files -plumbing`) reports 23 plumbing entries including these expected `agent_capabilities.go` CRUD-family entries.
- 2026-08-24 worker: Closed GO-API-009 as accepted/deferred, not extracted. The four CRUD families are similar at the transport shell but already carry family-specific behavior (known-skill bare-assignment upsert and grant-state preservation; knowledge-seed tag normalization and applied timestamp preservation; different names/request fields/errors). A generic helper would encode exceptions and increase coupling for an informational finding.
- 2026-08-24 worker: Verification passed: `go build ./internal/api/...`; `go vet ./internal/api/...`; `go test ./internal/api/... -run 'ProviderManage|PluginConfig|AgentCapabilities' -v`; `go build ./cmd/nanite/`; `go vet ./...`; `go test ./...`.

## Review notes

- 2026-08-24 fresh review PASS. Verified the three `*API` decode bypasses now
  use `a.decode` while preserving the existing `400` `invalid JSON: ...`
  response, with malformed JSON coverage for all three handlers. Verified
  `agent_capabilities.go` CRUD-family pairs were freshly re-confirmed and
  GO-API-009's accepted/deferred judgment is defensible for an informational
  finding; no broad helper or unrelated API scope was introduced.
