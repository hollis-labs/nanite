# SSRF CIDR denylist duplication — cross-reference only, implemented in `08-remaining-security-hardening/`

**Phase:** Wave 6 — Semantic duplication / migration drift (cross-referenced from Wave 3)
**Status:** reviewed
**Depends on:** N/A — no implementation task lives in this file.
**Touches:** nothing directly; this file exists so the finding stays visible in this folder's classification table.

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 6 — semantic duplication and migration drift · **Dispatch unit:** `W6a`
> - **Depends on:** `08/01`
> - **Blocks:** none
> - **Parallel-safe with:** `11/01`, `11/02`, `11/06`, `11/09`
> - **Gated on:** AD-19
> - **requires_security_review:** true · **requires_regression_test:** false

> ## ⚠ RESOLVED ELSEWHERE — `GO-SEC4-007` reassigned to `08/09` (2026-08-22)
>
> This file correctly said its finding's real remediation lives in
> `08-remaining-security-hardening/`, but named no file because that folder was
> empty when this was written. **No such task was ever created, so
> `GO-SEC4-007` sat as an orphan** — flagged, dispositioned, and implemented by
> nothing — until the Wave 3 decision pass found it.
>
> It now belongs to
> `08-remaining-security-hardening/09-autocomplete-and-artifact-path-hardening.md`,
> because **AD-28** adds a third consumer of the duplicated CIDR denylist (a
> catalog-download SSRF guard) and therefore has to consolidate the set rather
> than copy it. Consolidation *is* the remediation.
>
> `findings.json`'s `task_file` for `GO-SEC4-007` now points at `08/09`. This
> file remains as the classification-table entry it was always meant to be, and
> maps to no finding — like `06/04` and `12/02`, that is by design, not an
> unmapped-finding error.

## Purpose of this file

**This is not an implementation task.** `GO-SEC4-007` — the duplicated SSRF CIDR denylist between `internal/sandbox/proxy.go` and `internal/mcp/general_tools.go` — is a real Wave 6 duplication finding thematically, but it is implemented as a security-hardening task in `TASKS/audit-remediation/08-remaining-security-hardening/`, alongside that folder's other trust-boundary findings (see that folder's own README once its task files are written — the exact filename for this specific finding was not available at the time this task-creation pass ran, since `08-remaining-security-hardening/` was empty when this folder was written). Per this batch's own "no finding should disappear merely because it was grouped" rule (remediation guide §4 Output C), this file's only job is to keep `GO-SEC4-007` visible in this folder's classification table (see this folder's `README.md`) even though its actual remediation work is tracked elsewhere.

**If you are looking to implement this fix, go to `TASKS/audit-remediation/08-remaining-security-hardening/` and find the task file covering `GO-SEC4-007` — do not duplicate implementation effort here.**

## Findings addressed
- `GO-SEC4-007` — severity medium, confidence high. `docs/audits/2026-08-21-go-quality/REPORT.md` §8.12; `docs/audits/2026-08-21-go-quality/findings.json` id `GO-SEC4-007`. `requires_architect_decision: false` per the finding record.

## Classification, for completeness

This finding sits at the intersection of two of this folder's classifications:

- **(4) migration drift** — the sandbox copy's own comment in `internal/sandbox/proxy.go` explicitly states the *intent* is parity: "so both network egress paths enforce the same policy." That comment is itself evidence the duplication was created deliberately, with the expectation the two lists would be kept in sync by hand — exactly the shape of migration/parity drift this batch's classification scheme names.
- **(1) textual boilerplate** — the CIDR list itself (loopback/RFC1918/CGNAT/link-local-IMDS/IPv6 ULA) is static data, not diverging logic; today the two copies are still identical.

The risk is not that the lists currently disagree (they don't, per the audit's evidence) but that **nothing enforces the parity the sandbox comment promises**. If one list is updated — e.g. a new cloud-metadata CIDR range is added to close a new SSRF bypass — and the other isn't, the two network-egress paths (`internal/sandbox/proxy.go`'s sandboxed subprocess proxy, and `internal/mcp/general_tools.go`'s `callWebFetch` SSRF defense) silently drift apart with no signal, defeating the parity the original author intended. The audit's own recommendation is minimal and mechanical: "Extract the shared CIDR set into one place both packages import, or add a test that asserts the two lists are identical."

## Why this lives in the security-hardening folder, not here

`08-remaining-security-hardening/` groups this finding with the audit's other Wave 3 trust-boundary findings (`GO-SVCCORE-004` A2A callback SSRF, `GO-MCPTOOL-008` symlink/TOCTOU escape, `GO-SEC-003` remaining variable-path sites, `GO-SEC4-003/004/008`, `GO-RUNTIME-002` auth/bind/TLS posture, dependency/toolchain updates) because the actual remediation work — extracting a shared CIDR constant or adding a parity test — is security-flavored and benefits from the same reviewer context as that folder's other SSRF/trust-boundary fixes, rather than being reviewed alongside this folder's mostly-mechanical duplication cleanup. This is purely a folder-organization choice, not a statement that the finding is less real or lower-priority than this folder's other items.

## Done means

- [x] Confirm `TASKS/audit-remediation/08-remaining-security-hardening/`'s task file covering `GO-SEC4-007` exists and cites this cross-reference (or, if that folder's task-authoring pass has not yet run, flag this to whoever authors it, so the finding isn't silently dropped between folders).
- [x] This file itself requires no further action — it is a pointer, not a task.

## Work log

- 2026-08-23: Orchestrator confirmation for Wave 6 preflight. `findings.json`
  maps `GO-SEC4-007` to
  `TASKS/audit-remediation/08-remaining-security-hardening/09-autocomplete-and-artifact-path-hardening.md`,
  disposition `remediate`, task_status `reviewed`. That task is itself
  `reviewed` and explicitly cites the reassignment from `11/07` plus the AD-28
  shared CIDR policy consolidation. No implementation remains in this pointer
  task.

## Review notes

N/A — see `08-remaining-security-hardening/09-autocomplete-and-artifact-path-hardening.md`
for the implementation review notes.
