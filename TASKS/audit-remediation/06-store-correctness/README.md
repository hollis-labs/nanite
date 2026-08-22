# 06 — Store correctness

This group covers `internal/store`'s Wave 2 ("Correctness, lifecycle,
concurrency") findings. It sits in Wave 2 rather than Wave 1 because these are
correctness/error-handling defects internal to the persistence layer, not
release-blocking trust-boundary failures reachable from an external actor —
the remediation guide's own wave ordering puts trust-boundary work (plugin
install, sandbox, slug traversal) ahead of this kind of internal-correctness
gap. Within Wave 2, the guide's "Store correctness" section is explicit about
priority: fix `GO-STORE-003` (`DeleteAgentByID` silently swallowing every
`GetAgent` error, not just not-found) first, since it is the one **high**-severity
finding in `internal/store` with a real, traced production consequence — then
triage the remaining three lower-severity findings (`GO-STORE-004/005/006`)
as a group.

**Explicit scope fence, restated from the guide:** *"Do not turn this into a
repository-wide Store abstraction rewrite."* `internal/store` is a 349-method,
63-file gravitational package by design (one `*Store{DB, dbPath}` handle
backing ~25+ domain areas) — the guide's Wave 5 treats it as "a gravitational
package review, not a mandatory split," and both task files in this folder
inherit that constraint. Neither task narrows `*Store` into consumer-defined
interfaces, neither introduces a generic repository/DAO layer, and the
context-propagation question raised by `GO-STORE-005` is scoped as an
architect decision to be made and handed off — not implemented — precisely
so it can't balloon into that repo-wide rewrite by accident.

## Task files

- **`01-fix-deleteagentbyid-error-swallowing.md`** — `GO-STORE-003` (high).
  The priority fix: `DeleteAgentByID` (`internal/store/agents.go:1211-1221`)
  treats any `GetAgent` failure as "row doesn't exist," including transient
  DB errors, silently defeating the plugin-unload cascade-cleanup guarantee
  in `internal/plugin/agent_profiles.go`'s `SweepPluginAgentProfiles`. Clear
  bug fix, no architect decision needed.
- **`02-triage-store-context-and-transaction-gaps.md`** — `GO-STORE-004`
  (low, unchecked `json.Unmarshal` inconsistency), `GO-STORE-005` (medium,
  package-wide `context.Context` propagation gap — the one genuine architect
  decision in this folder, since backfilling `ctx` through `sessions.go`/
  `agents.go`'s 30+ methods is a real scope question), and `GO-STORE-006`
  (low/medium-confidence, a check-then-act gap in
  `SyncDurableAgentInstanceConfig` whose fix depends on a caller-concurrency
  trace the audit couldn't complete within budget — that trace is this
  sub-task's first step, not a deferred decision). One file, three
  independently-scoped sub-sections, per the guide's own instruction to
  triage these three together in a single review pass.
