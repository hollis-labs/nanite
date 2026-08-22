# Triage `internal/store`'s remaining error-handling, context-propagation, and transaction gaps

**Phase:** Wave 2 — Correctness, lifecycle, concurrency
**Status:** not-started
**Depends on:** none within this batch. Sequencing note only: this task is not blocked by `01-fix-deleteagentbyid-error-swallowing.md`, but the remediation guide's own "Store correctness" section lists `GO-STORE-003` first and this trio second — a planner may still choose to schedule `01` first for that reason, without it being a real code dependency.
**Touches:** `internal/store/plugin_settings.go` (`ListPluginSettings`), `internal/store/sessions.go` and `internal/store/agents.go` (package-wide method signatures — see GO-STORE-005 sub-section for the scope caveat), `internal/store/durable_agents.go` (`SyncDurableAgentInstanceConfig`). Three unrelated files/findings triaged in one pass per the remediation guide's explicit grouping — see Context.

## Context

The remediation guide's "Store correctness" section (Wave 2) is explicit: *"Prioritize `GO-STORE-003` ... Then triage `GO-STORE-004/005/006`. Do not turn this into a repository-wide Store abstraction rewrite."* This task is that triage. It is deliberately **not** a single uniform fix — the three findings below have almost nothing in common except living in the same package and being lower-priority than `GO-STORE-003`. They are grouped into one task file only because the guide groups them for a single review pass, not because they share a root cause. Each sub-section below is independently scoped, independently gated, and should be independently reviewable; a worker or reviewer should feel free to treat them as three small pieces of work executed in sequence within this one file, not one merged change.

### Findings addressed
- `GO-STORE-004` — severity low, confidence high. `docs/audits/2026-08-21-go-quality/REPORT.md` §8.1; `findings.json` id `GO-STORE-004`.
- `GO-STORE-005` — severity medium, confidence high. `docs/audits/2026-08-21-go-quality/REPORT.md` §8.1; `findings.json` id `GO-STORE-005`.
- `GO-STORE-006` — severity low, confidence medium. `docs/audits/2026-08-21-go-quality/REPORT.md` §8.1; `findings.json` id `GO-STORE-006`.

### Non-goals (applies to all three sub-sections)

- **Do not turn this into a repository-wide Store abstraction rewrite.** This is the remediation guide's own explicit instruction for this exact area (Wave 2, "Store correctness"). No new generic repository/DAO interface layer, no narrowing of `*Store` into per-domain consumer interfaces, no attempt to unify `internal/store`'s 349 methods onto one consistent transaction/context idiom in this task. `GO-STORE-001` (narrow consumer-defined interfaces) and the package-wide `context.Context` migration question raised by `GO-STORE-005` are architect-decision items tracked separately (see that sub-section) — this task triages the three named findings only, at the scope stated in each sub-section, and stops there.
- Do not expand scope to fix other `dupl`/lint findings noticed incidentally while in these files (e.g. `GO-STORE-007`'s scan-loop duplication is a separate, already-catalogued low-priority finding with its own disposition — leave it alone here).

---

## Sub-section A: GO-STORE-004 — `ListPluginSettings` unchecked `json.Unmarshal`, inconsistent with `GetPluginSettings`

`requires_architect_decision: false` — a clear, small, mechanical fix with no design ambiguity.

### Root cause

`ListPluginSettings` and `GetPluginSettings` both unmarshal the same two JSON columns (`settings`, `schema`) from the same `plugin_settings` table, but only `GetPluginSettings` checks the `json.Unmarshal` error. This is a straightforward oversight in one method that wasn't kept in sync with its sibling, not a deliberate design difference.

### Current behavior

`internal/store/plugin_settings.go:46-51`, `GetPluginSettings` (checked):

```go
if err := json.Unmarshal([]byte(settingsJSON), &ps.Settings); err != nil {
    return nil, fmt.Errorf("parse plugin settings: %w", err)
}
if err := json.Unmarshal([]byte(schemaJSON), &ps.Schema); err != nil {
    return nil, fmt.Errorf("parse plugin schema: %w", err)
}
```

`internal/store/plugin_settings.go:134-135`, `ListPluginSettings` (unchecked, identical operation on the same columns inside the row-scan loop):

```go
json.Unmarshal([]byte(settingsJSON), &ps.Settings)
json.Unmarshal([]byte(schemaJSON), &ps.Schema)
```

A malformed `settings`/`schema` row fails closed (returns an error) via `GetPluginSettings`, but silently zero-fills (`ps.Settings`/`ps.Schema` left at their zero value) via `ListPluginSettings` — inconsistent behavior for the identical data, depending only on which method the caller happened to use. The audit notes real-world risk is low (both columns are always written via `json.Marshal` internally elsewhere in this file, so a malformed row requires external tampering or a bug in the write path) — but the inconsistency itself is real and worth closing.

### Desired invariant

Malformed `settings`/`schema` JSON is handled the same way regardless of whether the caller used `GetPluginSettings` or `ListPluginSettings`.

### Proposed direction

Align `ListPluginSettings` with `GetPluginSettings`'s fail-closed behavior: check both `json.Unmarshal` errors inside the `rows.Next()` loop (`plugin_settings.go:124-137`) and return a wrapped error (mirroring `GetPluginSettings`'s `fmt.Errorf("parse plugin settings: %w", err)` / `"parse plugin schema"` message shapes, adapted to include the `plugin_id` for the offending row since `ListPluginSettings` iterates multiple rows) rather than continuing to build a zero-filled `PluginSettings` for that row. Confirm before implementing whether "fail the whole list on one bad row" vs. "skip the bad row and continue, logging a warning" is the right call for a list endpoint (a list is arguably worse off failing entirely over one bad row than a single get is) — the guide's template asks for consistency, not necessarily identical control flow; if the worker judges "skip and warn" is the better fit for a list method, document that reasoning in the Work Log rather than silently picking one.

### Tests required

- A test that inserts (or otherwise produces) a `plugin_settings` row with malformed `settings` or `schema` JSON and asserts `ListPluginSettings` now surfaces that malformation (either as a returned error, or — if "skip and warn" is chosen — asserts the malformed row is excluded from the result rather than silently zero-filled) instead of silently succeeding with zeroed fields.
- Confirm `GetPluginSettings`'s existing error-path test coverage (check current test files for one; add one if absent) still passes and is genuinely equivalent to the new `ListPluginSettings` coverage.

---

## Sub-section B: GO-STORE-005 — Inconsistent `context.Context` propagation across the package

`requires_architect_decision: true` for the backfill-scope decision described below. This is the one piece of this task file that is genuinely an architect decision, not implementation work — do not have a worker silently pick a scope.

### Root cause

`internal/store` migrated to context-aware DB calls (`s.DB.QueryContext`/`ExecContext`/`QueryRowContext` taking a `ctx context.Context` parameter) in some files but not others, and the migration was never completed package-wide. This reads as an incremental, partially-completed migration (some files converted, others not yet reached) rather than a uniformly-missing feature or a deliberate two-tier design.

### Current behavior

Package-wide: only 22 of 63 production files in `internal/store` reference `context.Context` at all. Plain `s.DB.Query`/`Exec` calls outnumber context-aware equivalents roughly 2:1 (231 vs. 116) across the package.

Worst offenders — the two hottest tables have **zero** context-taking methods:
- `internal/store/sessions.go` (1004 LOC, includes `ForkSession`) — no method takes `ctx`.
- `internal/store/agents.go` (1298 LOC, includes `CreateAgent`) — no method takes `ctx`.

Contrast: `internal/store/teams.go`'s 6 CRUD methods all take `ctx` and use the context-aware `*Context` DB call variants throughout.

### Why it matters

HTTP-request cancellation and agent-turn cancellation can never reach the DB layer for any of `sessions.go`'s or `agents.go`'s 30+ methods — a long-running or stuck query on either of the two hottest tables cannot be canceled by an upstream context deadline/cancel, regardless of how carefully the calling code above the store layer propagates its own context.

### Desired invariant (post-decision)

Once the architect decision below is made: callers with a live, cancelable context can propagate cancellation through to the DB layer for the paths the decision selects as in-scope; `internal/store`'s context-taking convention is documented so future methods added to `sessions.go`/`agents.go` (or any other still-uncontexted file) follow it consistently rather than perpetuating the split.

### Architect decision required

Whether and how far to backfill `ctx` through `sessions.go`/`agents.go` (and, if broader, the rest of the package's plain-`DB.Query` files) is a real scope question, not a small fix — this is not implementation work a worker should resolve unilaterally. Concretely, the architect must decide:

1. **Scope**: backfill only `sessions.go` + `agents.go` (the two files the audit flagged as worst-offenders and hottest tables), or the whole package (231 plain-call sites across 63 files)?
2. **Prioritization**: if partial, which specific methods are reachable from a cancelable request/agent-turn context today (i.e. where would ctx propagation actually change observable cancellation behavior) versus which are only ever called from background/non-cancelable code paths (where a ctx parameter would be plumbing with no behavioral payoff)?
3. **Migration mechanics**: signature-breaking change across 30+ methods on two of the largest files in the package touches every one of the package's high-fan-out callers (`internal/service` alone has 76 direct `*store.Store` references per `GO-STORE-001`) — does this land as one large mechanical PR, a series of smaller per-caller-group PRs, or is `ctx` added as a new parallel method (`*Ctx` suffix, mirroring a Go stdlib convention) that coexists with the non-ctx original rather than replacing call sites in place?
4. Confirm this is not "an intentional, not-yet-reached migration backlog" the store's actual owner already has a plan for, before treating it as urgent net-new work — the audit itself flags this as a live possibility (see false-positive note below) and the guide's Wave 0 instructs revalidating against current source/owner intent before scheduling.

### False-positive considerations (carried from the audit — do not silently discard)

"May be an intentional, not-yet-reached migration backlog rather than an oversight — worth confirming with the store's owner before treating as urgent." A planner picking this task up should confirm this with whoever owns `internal/store` before committing to a backfill scope.

### Proposed direction

Do not implement in this task. Once the architect decision above is made, split the selected scope into its own follow-on task file(s) — this triage task's job is to make the decision options and evidence legible, not to execute the backfill.

### Tests required (once scoped)

- Whatever scope is chosen, add at least one test demonstrating that a canceled/deadline-exceeded context passed through a newly-context-aware method actually aborts the in-flight query (not just that the method compiles with a `ctx` parameter it silently ignores) — a context parameter added without wiring it into the underlying `*Context` DB call is a plausible partial-implementation trap worth guarding against explicitly.

---

## Sub-section C: GO-STORE-006 — `SyncDurableAgentInstanceConfig` read-then-branch-then-write without a transaction

`requires_architect_decision: false` for the fix itself, **but this task's first step is a caller-concurrency trace, not an architect decision** — the audit explicitly could not complete this trace within its budget, and the fix's correct shape depends entirely on the trace's answer.

### Root cause

`SyncDurableAgentInstanceConfig` reads the existing row (`GetDurableAgentInstanceBySlug`), branches on what it found, and then writes (`UPDATE ... WHERE id = ?`) as three separate steps with no transaction wrapping them — a classic check-then-act gap if two calls for the same slug can race. Its siblings avoid this shape entirely by pushing the "still exists / still active" check into a single atomic `UPDATE ... WHERE id = ? AND status != 'archived'` followed by a `RowsAffected()` check, so there is nothing to race between the check and the write — the check *is* the write.

### Current behavior

`internal/store/durable_agents.go:301-367`, `SyncDurableAgentInstanceConfig`:

```go
func (s *Store) SyncDurableAgentInstanceConfig(inst *DurableAgentInstance) (*DurableAgentInstance, error) {
    if inst == nil {
        return nil, errors.New("SyncDurableAgentInstanceConfig: nil instance")
    }
    existing, err := s.GetDurableAgentInstanceBySlug(inst.Slug)
    if err == nil && existing != nil {
        // ... branches on inst.Status, inst.CreatedAt, etc., mutating `inst` in place ...
        _, err := s.DB.Exec(`UPDATE durable_agent_instances SET ... WHERE id = ?`, ..., existing.ID)
        // ...
    }
    if errors.Is(err, ErrDurableAgentInstanceNotFound) {
        // ... create path ...
    }
    // ...
}
```

No `Begin()`/`BeginTx()` wraps the read-decide-write sequence.

Compare siblings at `internal/store/durable_agents.go:369-434` — `SetDurableAgentInstanceStatus`, `SetDurableAgentInstanceLaunchState`, `ArchiveDurableAgentInstance` — each of which does a single `UPDATE ... WHERE id = ? AND status != 'archived'` and then checks `res.RowsAffected()`, returning `ErrDurableAgentInstanceNotFound` if zero rows were affected. There is no separate read step for these three; the `WHERE` clause and the `RowsAffected` check together make the whole operation atomic without needing an explicit transaction.

### Why it matters (conditional on the trace below)

If two `SyncDurableAgentInstanceConfig` calls for the same slug can run concurrently, the read-decide-write gap allows a classic check-then-act race: both calls read the same `existing` state, both branch the same way, and the second write can silently clobber fields the first write set (or vice versa), depending on interleaving — with no error surfaced to either caller.

### First step — not an architect decision, do this before proposing a fix

The audit explicitly did not trace `SyncDurableAgentInstanceConfig`'s callers for concurrency within its budget; confidence on this finding is medium *specifically* because "whether this is a live race (vs. a single-threaded reconciliation loop) is unverified." Before writing any fix:

1. Enumerate every production caller of `SyncDurableAgentInstanceConfig` (grep `internal/` for `SyncDurableAgentInstanceConfig(`).
2. For each caller, trace whether it can be invoked concurrently for the *same slug* — e.g. is it driven by a single-threaded reconciliation loop (one instance processed at a time, no concurrency possible), or can it be triggered by concurrent HTTP requests / concurrent agent-turn events / concurrent durable-agent lifecycle transitions for the same slug?
3. Record the trace's conclusion in the Work Log before proceeding — this determines whether a fix is needed at all, and if so, which of the two directions below applies.

### Desired invariant (conditional on the trace)

- If the trace finds concurrent same-slug calls are possible: the read-decide-write sequence must be atomic with respect to concurrent calls for the same slug — either wrapped in a transaction with appropriate isolation, or refactored to push the check into the `WHERE` clause the way the sibling methods already do.
- If the trace finds concurrent same-slug calls are not possible (e.g. genuinely single-threaded reconciliation): document that finding directly in this method's doc comment (mirroring how `ForkSession`/`CopyMessages` document their own deliberate non-transactional read-before-write ordering, noted in REPORT.md §8.1's "Reviewed and found healthy" section as "documented, not an oversight") so a future reader doesn't have to re-derive the same trace, and close this finding as accepted-risk/false-positive with that rationale rather than changing the code.

### Proposed direction (if the trace confirms a live race)

Prefer adopting the sibling idiom (atomic `UPDATE ... WHERE ... AND <condition>` + `RowsAffected()`) over wrapping in an explicit transaction, if the update-vs-create branch can be restructured to fit that shape — it is already the pattern this file uses elsewhere and requires no new transaction-lifecycle code. If the branch logic (which fields are conditionally preserved from `existing`, `internal/store/durable_agents.go:313-328`) is too stateful to express as a single `WHERE`-gated `UPDATE`, fall back to wrapping the full read-decide-write sequence in `s.DB.Begin()`/`tx.Commit()`/`defer tx.Rollback()`, matching the transaction-safety pattern the audit verified as correct at this package's 9 other `Begin()`/`BeginTx()` sites (REPORT.md §8.1, "Reviewed and found healthy").

### Tests required

- If the trace confirms a live race: a regression test that drives two concurrent `SyncDurableAgentInstanceConfig` calls for the same slug (e.g. via goroutines + a `sync.WaitGroup`, or `go test -race`) and asserts the final persisted state is coherent (not a torn/interleaved write) rather than merely "doesn't panic."
- If the trace finds no live race: no new concurrency test is required, but the accepted-risk rationale must be recorded in the doc comment per the Desired invariant above, and this must be checkable by a future reader without re-doing the trace.

---

## Dependencies

- None across the three sub-sections — they touch disjoint files and can be worked in any order, or in parallel by different people, despite living in one task file.
- Sub-section B produces a decision, not code — its output is input to a future, separately-scoped backfill task, not something this task file's own "Done means" can close by writing code.

## Prevention

- **A**: no new lint rule needed; `errcheck`/`go vet` would catch an unchecked `json.Unmarshal` if it were treated as a return-value-must-be-checked call — confirm current `errcheck` config actually flags this pattern (audit found it did not, since `GO-STORE-004` was found by manual read, not by a linter hit) and consider whether tightening `errcheck`'s scope to include this case is worth a separate `12-quality-ratchet-and-standards/` follow-up rather than doing it inline here.
- **B**: the guide's own "Lifecycle Ownership" and general Go-idiom standards apply once a scope is chosen; the real prevention mechanism is documenting the chosen ctx convention somewhere new methods on `internal/store` are expected to follow it (a package-level doc comment or a CONTRIBUTING-style note), so the split doesn't recur for the *next* new method added to `sessions.go`/`agents.go`.
- **C**: the sibling idiom (`UPDATE ... WHERE ... AND <condition>` + `RowsAffected`) is itself the prevention mechanism if adopted — it structurally cannot have this class of check-then-act gap. If the accepted-risk path is taken instead, the doc-comment rationale is the prevention (makes the trade-off legible to the next reader instead of requiring a re-trace).

## Verification

```bash
go build ./internal/store/...
go vet ./internal/store/...
go test ./internal/store/... -v -run 'TestListPluginSettings|TestSyncDurableAgentInstanceConfig'
go test -race ./internal/store/... -run TestSyncDurableAgentInstanceConfig   # only if sub-section C's trace confirms a live race and a concurrency test is added
```

## Risk / rollback

- Sub-section A: low risk, isolated to one method's error handling; rollback is a single-commit revert.
- Sub-section B: no code risk (decision-only in this task); the eventual backfill (separate task) carries real risk proportional to its chosen scope — a signature change across 30+ methods touches every high-fan-out caller in `internal/service` and must be reviewed as its own change.
- Sub-section C: risk depends on which direction the trace selects — a `WHERE`-clause refactor is low-risk and mirrors existing, audited-healthy siblings; a transaction wrap is also low-risk but adds a small amount of new transaction-lifecycle code that should be checked against the same `defer tx.Rollback()` + success-path `Commit()` pattern the audit verified as correct everywhere else in this package.

## Done means

- [ ] **A**: `ListPluginSettings` handles malformed `settings`/`schema` JSON consistently with `GetPluginSettings` (either fails closed per-row or explicitly documents a chosen skip-and-warn behavior); new test covers a malformed row.
- [ ] **B**: architect decision recorded (scope, prioritization, migration mechanics) — this sub-section is "done" when the decision is made and handed off as a new task, not when code is backfilled; no code change is required to close this sub-section.
- [ ] **C**: caller-concurrency trace completed and recorded in the Work Log; if a live race is confirmed, fix applied (preferring the `WHERE`-clause idiom) with a concurrency regression test; if no live race, doc-comment rationale added and finding closed as accepted-risk.
- [ ] Non-goal respected: no Store abstraction rewrite, no `GO-STORE-001`/`GO-STORE-002` work done incidentally.
- [ ] `go build`, `go vet`, `go test ./internal/store/...` pass.

## Work log

<!-- Worker fills in per sub-section: what was actually done, the sub-section C trace's conclusion and how it was reached, and the sub-section B decision once made (or a note that it's still pending architect input). -->

## Review notes

<!-- Reviewer fills in: pass/fail per sub-section, what was independently re-verified (e.g. re-ran the sub-section C trace's grep independently rather than trusting the worker's claim). -->
