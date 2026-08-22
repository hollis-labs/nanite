# Coding Standards

Stub — real content added as it's actually established, not invented wholesale. What's genuinely observable from this codebase's own conventions today:

- **Comments explain WHY, not WHAT.** The strongest doc comments in this codebase cite a ticket, a specific historical bug, or a non-obvious constraint (e.g. "removed after causing a distinct '0 seconds' bug class") rather than restating what the code does. Follow that pattern.
- **Check the glossary before introducing a new term.** This codebase has a real, repeated, expensive history of naming collisions. Reuse existing vocabulary or pick something genuinely distinct — see `../GLOSSARY.md`.
- **Prefer a typed field over a string convention checked in multiple places.** Several of this review's worst bugs trace back to a decision being encoded as a string pattern (a `pty-` prefix, a hardcoded model string) matched independently at multiple call sites that could drift out of sync. A single typed field, checked once, is worth the migration.
- **Real foreign keys over free-text strings for anything referencing another entity.** Tool names, skill names, model IDs — the pattern of "a string that's supposed to match something else, with no enforced relationship" has been the direct cause of most of the tool-name-typo and stale-config bug classes found in this codebase's history.

## Standards from the 2026-08-21 Go quality audit

Six standards adopted verbatim from `nanite-audit-triage-remediation-planning-guide.md` §4, Wave 7, "Standards to add/confirm." The guide lives outside this repo (in the operator's inbox), so this section is self-contained — the full quoted text is below, not just a pointer to the source. Each standard was derived from a real, repeating defect class this audit found recurring in this codebase, not proposed as a generic best-practice import; the full traceability behind each one is aggregated in `TASKS/audit-remediation/PREVENTION.md`.

### Production Reachability

> A feature is not done until its production entry point, wiring, invocation, and observable behavior are proven.

**Why:** six fully-built features — grounding memory recall, the Hadron context gate, team semantic routing, the tool builder/YAML architecture, reasoning-augmented tool selection, and the curated tool knowledge matcher — passed their unit tests while nothing checked whether they were ever wired to a reachable production entry point. See `TASKS/audit-remediation/09-production-islands/`.

### Security/Correctness Migration Completeness

> When a security or correctness fix replaces a primitive or pipeline, enumerate and verify every production caller of the superseded implementation.

**Why:** a fail-closed CLI plugin-install pipeline landed while the API handler was never migrated off the older, weaker pipeline it was meant to replace — the same "newer, correct implementation coexisting with an older, stale one" shape recurred elsewhere too. Nothing in the process required enumerating every caller of the primitive being replaced. See `TASKS/audit-remediation/01-plugin-install-convergence/` and `03-agent-slug-traversal/`.

### Semantic Duplication

> Duplicating syntax is a maintainability concern. Duplicating a semantic rule is a correctness concern.

**Why:** the single largest finding category in this audit — 26 duplication findings across all 16 tasks in one folder — with no mechanism distinguishing harmless textual duplication (boilerplate) from a genuinely divergent copy of the same semantic rule. See `TASKS/audit-remediation/11-semantic-duplication-migration-drift/`.

### Lifecycle Ownership

> Every goroutine/background worker/resource has an explicit owner and shutdown path; partial construction cleans up already-started resources.

**Why:** `go vet` flagged the container reaper's leaking cancel funcs directly and the finding shipped anyway — a signal existed and wasn't gating — while roughly 18 further untracked background-goroutine sites and API tests that create containers without ever shutting them down showed the same missing-owner shape. See `TASKS/audit-remediation/04-container-reaper-lifecycle/`.

### Trust-Boundary Paths

> Values influenced by external callers, agents, plugins, catalogs, or persisted untrusted state must not become filesystem paths without canonical validation/confinement.

**Why:** two confinement mechanisms coexist in this codebase with no rule for which applies where, and the one lint rule that would have caught this audit's most severe finding (an unconfined path-traversal write in an HTTP handler) was scoped, by its own path exclusion, away from exactly the package the bug lived in. See `TASKS/audit-remediation/02-linux-sandbox-fail-open/`, `03-agent-slug-traversal/`, and `08-remaining-security-hardening/`.

### Silent Security Degradation

> A required security boundary must not silently degrade while reporting success.

**Why:** the Linux sandbox falls back to unisolated execution while reporting success when its isolation binary is absent, and plugin signature verification is skipped with no log line at all when no public key is configured — both share one shape: the degraded path is the `else` of a condition that's false by default out of the box. See `TASKS/audit-remediation/02-linux-sandbox-fail-open/`.

## Not yet documented

Formatting/linting conventions beyond `gofmt`/standard Go tooling, error-handling conventions beyond what the two security-boundary standards above already partially cover, package-naming conventions beyond the `agent/*`/`prompt/*`-style grouping already established, a real style guide for the frontend.
