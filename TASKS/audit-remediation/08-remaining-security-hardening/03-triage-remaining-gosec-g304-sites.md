# Triage the remaining gosec G304 path-traversal-via-variable sites

**Phase:** Wave 3 — Remaining security hardening (guide §4; sequenced 2026-08-21 — see the sequencing block below)
**Status:** in-progress
**Depends on:** sequencing only — should be read alongside `TASKS/audit-remediation/03-agent-slug-traversal/01-canonical-slug-path-validation.md` (that folder was empty at the time this task was authored; the cross-reference below is written against the finding it's expected to cover). Not a hard blocking dependency — this task's own list-production step (Step 1) simply must exclude that task's scope rather than re-analyze it.
**Touches:** repo-wide read-only triage first; downstream code touches are **not yet known** — they depend entirely on Step 1's filtered list. Do not assume a package list before that list exists.
**Requires architect decision:** true (matches `findings.json`) — per the audit's own recommendation: "have the architect (or whoever owns the Phase-1-Wave-1 migration) walk the [filtered] list."

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 3 — remaining security hardening · **Dispatch unit:** `W3`
> - **Depends on:** `00/02` — this task's scope **is** the refreshed G304 count; the audited figure (68 production sites) is stale by 40 commits
> - **Blocks:** none
> - **Parallel-safe with:** `08/01`, `08/02`, `08/04`, `08/06`, `08/10`
> - **Gated on:** none
> - **requires_security_review:** true · **requires_regression_test:** true

## Findings addressed

- **GO-SEC-003** (medium severity, **medium confidence** — the audit's own confidence is explicitly moderate here; category security + architecture) — report §3 (main write-up), §3.1 (lint-triage-funnel cluster #1), extended by §8.6/§8.13 (per-package triage of the `internal/plugin` and `cmd/nanite` slices of this cluster).

## Context

**70** production gosec G304 (path-traversal-via-variable) findings exist outside the `pathsafe.ResolveUnder` forbidigo-tracked adoption scope (was **68** at the audited commit `8feeee5c`; refreshed at frozen HEAD `1d3bfd96` by `00/02` — see `docs/audits/2026-08-21-go-quality/raw-1d3bfd96/DELTA.md`, measured via `raw-1d3bfd96/golangci-baseline.json` filtered `FromLinter=gosec`, `G304`, non-`_test.go`, same methodology as the audit's own citation) — `.golangci.yml`'s `forbidigo` rules only enforce `ResolveUnder` adoption inside `internal/sandbox/`, `internal/mcp/`, `internal/service/install/`; none of these sites are in that path list. This is real, tracked-but-incomplete-migration debt, not a fresh discovery — the project already has the `pathsafe.ResolveUnder` primitive and an active adoption tracker; the tracker's scope just doesn't cover these sites yet.

**Cross-reference — do not re-task:** the `internal/agent/managed_files.go` (lines 151, 180 per the audit's sample) and `internal/agent/managed_section.go` (lines 33, 87) subset of these 68 sites is already covered — with a fully-traced exploit chain (**GO-AGENT-001**, high severity, high confidence, report §8.8) and its own dedicated fix — by `TASKS/audit-remediation/03-agent-slug-traversal/01-canonical-slug-path-validation.md` in this same batch's folder 03. This task's Step 1 output must exclude that file pair, citing this cross-reference, rather than re-analyzing them.

**Already-triaged and closed — record the verdict, don't re-open:** report §8.13's dedicated `cmd/nanite` gosec-cluster review traced every hit across `cmd/nanite/mcp_cmd.go`, `plugin_cmd.go`, `plugin_logs.go`, and `serve_autostart.go` (the audit's representative sample sites: `mcp_cmd.go:32`, `plugin_cmd.go:482,491`, `plugin_logs.go:22`, `serve_autostart.go:82,202`) back to their origin, and confirmed every one originates in `os.Args` (CLI positional/flag input) or a deterministic OS-derived state path — never a network or lower-privilege caller. Verdict: **"confirmed low real risk across the board."** Applying the guide's Wave 3 trust-classification instruction explicitly: these sites classify as **operator CLI-controlled** — the person supplying the untrusted-looking path variable is the same person who already has a shell on the machine running `nanite`. This task should record that verdict as the disposition for these specific `cmd/nanite` sites (**documentation only, no code change**) rather than re-flagging them as open work.

**Desired invariant (end-state, not necessarily this task's own full scope):** every production file-open reachable from GUI/API/MCP/agent-controlled input that involves a variable path is routed through `pathsafe.ResolveUnder` or an equivalently-reviewed confinement check; operator-CLI-only sites are an accepted, explicitly-documented exception to that invariant, not silently exempt by omission.

## What to do

**Step 1 (do this before any code decision) — produce the filtered list:**
`68 total sites` − `already-triaged cmd/nanite sites` (`mcp_cmd.go`, `plugin_cmd.go`, `plugin_logs.go`, `serve_autostart.go` — verdict: CLI-operator-trust, no code change needed, cite report §8.13) − `already-fixed/tasked internal/agent sites` (`managed_files.go` + `managed_section.go` — cite `TASKS/audit-remediation/03-agent-slug-traversal/01-canonical-slug-path-validation.md`).

Use `raw/golangci-baseline.json` (the audit's own cited source — filter `FromLinter=gosec`, rule `G304`, exclude `_test.go`) to regenerate the **complete** 68-site list mechanically. The audit only sampled and named 10 of the 68 by file; the remaining ~50-plus sites are not enumerated anywhere in this task-creation pass and must be pulled fresh from the raw lint output, not guessed or extrapolated.

**Step 2:** for each site in the filtered list, classify by trust boundary per the guide's Wave 3 taxonomy — `external unauthenticated / external authenticated / agent-controlled / plugin-catalog-controlled / operator CLI-controlled / OS-derived-internal`. The audit's own recommendation names this exact taxonomy as the required lens, not raw gosec severity.

**Step 3:** for every site classified above operator-CLI-controlled or OS-derived-internal (i.e., reachable from GUI/API/MCP/agent-controlled/plugin-catalog input), trace the actual call chain back to its origin (per the audit's own "suggested verification" note) before deciding whether `pathsafe.ResolveUnder` needs to be added. Do not add it reflexively to every hit — gosec's G304 rule fires on any variable-driven open regardless of whether the variable is already constrained by an earlier check; a meaningful fraction of the 68 may already be safe by construction, per the audit's own false-positive caveat.

**Step 4:** for sites confirmed to need the fix, apply `pathsafe.ResolveUnder` consistently (matching the existing `internal/sandbox`/`internal/mcp`/`internal/service/install` pattern), and consider whether `.golangci.yml`'s `forbidigo` path-list should be expanded to cover the newly-fixed package(s) — closing the tracking gap this finding is fundamentally about.

**All production callers:** not enumerable until Step 1's list exists — producing that list *is* the point of Step 1.

## Non-goals

Do not fix all 68 sites mechanically without individual trust-chain verification — the guide's own guardrail against "one task per lint occurrence" cuts both ways here: don't create 68 micro-fixes, but also don't apply one blanket fix to all 68 without checking each is actually reachable by untrusted input. Do not touch `internal/agent/managed_files.go`/`managed_section.go` (owned by folder 03's task) or the four already-triaged `cmd/nanite` files (documentation-only disposition here, no code change).

## Tests required

For any site where `pathsafe.ResolveUnder` is actually added: a traversal-rejection regression test mirroring the existing `pathsafe` test suite's own adversarial cases (dotdot-mid-path, symlink-escape, symlink-escape-subpath — report §8.12 confirms these are the existing patterns to mirror).

## Prevention

Expand `.golangci.yml`'s `forbidigo` path-list to cover any package fixed under this task, so future G304-shaped code in that package is caught at PR time rather than needing another repo-wide audit sweep to rediscover it.

## Verification

`gosec ./...` re-run after fixes, confirming the G304 count for fixed sites drops with no new regressions; `go test ./...` for touched packages.

## Risk / rollback

Risk depends entirely on which sites Step 3 confirms need fixing — likely low-risk per-site (`pathsafe.ResolveUnder` is a drop-in confinement wrapper, not a behavior change for already-valid paths), but this task's Step 1-3 output should size the real risk **before** any code lands, which is exactly why `requires_architect_decision` is `true` here.

## Done means

- [ ] Fresh, complete 68-site list pulled from `raw/golangci-baseline.json` (not just the audit's 10-site sample)
- [ ] List filtered to exclude the `internal/agent/managed_*` subset (cross-referenced, not re-analyzed) and the four already-triaged `cmd/nanite` files (documented verdict, not re-flagged)
- [ ] Remaining filtered list classified by trust boundary per the guide's taxonomy
- [ ] Each above-operator-CLI-trust site's call chain traced to origin
- [ ] Architect sign-off obtained on which sites get `pathsafe.ResolveUnder` applied
- [ ] Fixes + regression tests landed for confirmed sites; `forbidigo` tracker scope expanded if applicable

## Work log

### 2026-08-22 — mandatory pre-implementation checkpoint (Steps 1–3 only)

No production code, tests, or lint configuration were changed. The refreshed
70-site inventory, exclusions, per-site trust classification, origin traces,
proposed Step-4 footprint, and architect questions are recorded in
[`03-gosec-g304-triage-checkpoint.md`](03-gosec-g304-triage-checkpoint.md).
Step 4 remains blocked pending architect sign-off; this task is intentionally
left `in-progress`, not `implemented`.

## Review notes

<!-- Reviewer fills this in. -->
