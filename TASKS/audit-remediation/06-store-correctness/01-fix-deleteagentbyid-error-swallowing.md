# Fix `DeleteAgentByID` swallowing every `GetAgent` error, not just not-found

**Phase:** Wave 2 — Correctness, lifecycle, concurrency
**Status:** not-started
**Depends on:** none
**Touches:** `internal/store/agents.go` (`DeleteAgentByID`, `GetAgent`); test file `internal/store/agents_fu28_test.go`. No other packages need code changes — `internal/plugin/agent_profiles.go`'s `SweepPluginAgentProfiles` is the real production caller this fix protects, but it calls `DeleteAgentByID` through its existing signature and needs no change itself (see Scope below for why).

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 2 — correctness, lifecycle, concurrency · **Dispatch unit:** `W2b`
> - **Depends on:** `00/01`
> - **Blocks:** `06/02`, `11/13`
> - **Parallel-safe with:** `07/01`, `07/02`, `07/03`
> - **Gated on:** none
> - **requires_security_review:** false · **requires_regression_test:** true

> ## ⚠ EVERY CODE SNIPPET BELOW IS PRE-SWEEP AND WILL NOT COMPILE (2026-08-22)
>
> `06/03`'s context-propagation sweep landed in `fe16e138` **after** this file
> was written and rewrote the exact functions it targets. Current signatures:
>
> ```go
> func (s *Store) DeleteAgentByID(ctx context.Context, id string) error   // agents.go:1218
> func (s *Store) GetAgent(ctx context.Context, id string) (*AgentProfile, error)  // agents.go:438
> ```
>
> This file's snippets still show `DeleteAgentByID(id string)` and
> `s.GetAgent(id)`. **Copying them verbatim produces code that does not
> compile.** Thread `ctx` through — the corrected shape is:
>
> ```go
> func (s *Store) DeleteAgentByID(ctx context.Context, id string) error {
>     a, err := s.GetAgent(ctx, id)
>     if err != nil {
>         if errors.Is(err, sql.ErrNoRows) {
>             return nil          // genuine not-found — unchanged behaviour
>         }
>         return err              // real DB error — the actual fix
>     }
>     return s.DeleteAgent(ctx, a.Slug)
> }
> ```
>
> The **defect is unchanged** — the sweep was mechanical and did not touch the
> error-swallowing this task fixes. Only signatures moved. Line-number
> citations below have also shifted; re-locate before editing rather than
> trusting them.

## Context

`requires_architect_decision: false` — this is a clear, high-priority bug fix with no design ambiguity. It is the single sharpest correctness finding the audit produced against `internal/store` and is flagged **release-priority within Wave 2** by the remediation guide's own "Store correctness" section: *"Prioritize `GO-STORE-003`: distinguish true not-found from real DB errors in `DeleteAgentByID`. Then triage `GO-STORE-004/005/006`. Do not turn this into a repository-wide Store abstraction rewrite."* This task is that prioritized fix; task `02-triage-store-context-and-transaction-gaps.md` in this same folder covers the other three.

### Findings addressed
- `GO-STORE-003` — severity **high**, confidence **high**. `docs/audits/2026-08-21-go-quality/REPORT.md` §8.1; `docs/audits/2026-08-21-go-quality/findings.json` id `GO-STORE-003`.

### Root cause

`DeleteAgentByID` was written on the assumption that `GetAgent` returning an error always means "no such row" — a legitimate case where a no-op delete is the documented, correct behavior (see the method's own doc comment, `internal/store/agents.go:1213-1216` — **Wave 0 revalidation (2026-08-22) note:** shifted +2 lines from the audit-era `1211-1214` by an unrelated doc-comment edit landed above it in the file by `e1ba2ac6`; the function itself is byte-identical to the audited commit). But `GetAgent` (`internal/store/agents.go:437-444`) wraps *every* `Scan` error identically with `fmt.Errorf("get agent %s: %w", id, err)`, whether the underlying cause is `sql.ErrNoRows` or a genuine driver/I-O failure (closed connection, disk error, canceled context, etc.). `DeleteAgentByID` never branches on *which* error it got — it treats the mere presence of an error as proof the row doesn't exist. This is a single missing `errors.Is(err, sql.ErrNoRows)` check, not a design flaw in the no-op contract itself.

### Current behavior

`internal/store/agents.go:1213-1223` (was `1211-1221` at the audited commit; see Wave 0 revalidation note above):

```go
// DeleteAgentByID removes an agent profile by ID. Mirrors DeleteAgent
// (slug-based) but for direct-ID callers. Returns nil if the row
// didn't exist. Tombstones for durable=1 rows are deferred future work;
// today a DELETE wipes the row regardless of durable. FU-28.
func (s *Store) DeleteAgentByID(id string) error {
    a, err := s.GetAgent(id)
    if err != nil {
        return nil
    }
    return s.DeleteAgent(a.Slug)
}
```

`GetAgent`, `internal/store/agents.go:437-444`:

```go
func (s *Store) GetAgent(id string) (*AgentProfile, error) {
    var a AgentProfile
    row := s.DB.QueryRow(`SELECT `+agentColumns+` FROM agent_profiles WHERE id = ?`, id)
    if err := scanAgent(row, &a); err != nil {
        return nil, fmt.Errorf("get agent %s: %w", id, err)
    }
    return &a, nil
}
```

Because `GetAgent` wraps with `%w`, `sql.ErrNoRows` (and any other underlying error) survives through the wrap and is reachable via `errors.Is` — the wrapping itself is not the bug; the missing branch in `DeleteAgentByID` is.

Confirmed independently by golangci's `nilerr` linter (audit raw log `raw/golangci-baseline.log:6421`).

**Real production caller:** `internal/plugin/agent_profiles.go:265-296`, `(*Host).SweepPluginAgentProfiles` — the plugin-unload cascade-delete cleanup path. Its own doc comment (`agent_profiles.go:265-267`) states it "reuses `store.DeleteAgentByID`'s existing full-cascade cleanup ... rather than re-deriving that sweep here." The call site:

```go
for _, a := range agents {
    if err := st.DeleteAgentByID(a.ID); err != nil {
        h.logger.Warn("plugin agent_profiles sweep: delete plugin-owned agent failed",
            "plugin", pluginID, "agent", a.Slug, "error", err)
        continue
    }
    removed++
}
```

Today, if the `GetAgent` lookup inside `DeleteAgentByID` fails for a transient reason (a busy/locked DB, a canceled context, a driver-level I/O error — anything other than "row genuinely absent"), `DeleteAgentByID` returns `nil`. The `Warn` log above never fires, `removed` is never incremented for that agent, and the sweep silently believes the agent profile (and its cascade of `session_agents`, `agent_tools`, `agent_skills`, `agent_projects`, reflexes, durable instances) was cleaned up when it was not. There is no log line anywhere in this path for the transient-failure case — it is indistinguishable from "this agent was already gone."

### Desired invariant

`DeleteAgentByID` must return `nil` if and only if the target row genuinely does not exist (i.e. the underlying `GetAgent` failure is `sql.ErrNoRows`, possibly wrapped). Any other error from `GetAgent` — or from the subsequent `DeleteAgent` call — must propagate to the caller as a non-nil error.

## What to do

1. In `internal/store/agents.go`, change `DeleteAgentByID` to distinguish the two cases:

   ```go
   func (s *Store) DeleteAgentByID(id string) error {
       a, err := s.GetAgent(id)
       if err != nil {
           if errors.Is(err, sql.ErrNoRows) {
               return nil
           }
           return fmt.Errorf("delete agent by id %s: %w", id, err)
       }
       return s.DeleteAgent(a.Slug)
   }
   ```

   Verify `errors` and `database/sql` are already imported in `agents.go` before adding the import (grep the existing import block first — `agents.go` already calls `s.DB.QueryRow` and similar, so `database/sql` may already be imported under a different alias or not directly needed if `sql.ErrNoRows` is referenced via an existing import; check current source, don't assume).

2. Do **not** change `GetAgent`'s own error-wrapping behavior (`fmt.Errorf("get agent %s: %w", id, err)`) — it already preserves `sql.ErrNoRows` through the `%w` wrap, which is exactly what `errors.Is` needs downstream. Confirm this by reading the current `GetAgent` body before editing, in case it has changed since the audit — the audit's own pointer (`agents.go:437-444`) may have drifted if other work landed on this file first.

3. Update the doc comment on `DeleteAgentByID` (`agents.go:1213-1216`) to state the corrected contract explicitly: no-op only on genuine not-found; any other lookup or delete error propagates. Keep the existing FU-28 tombstone note — it's unrelated to this fix and still accurate.

4. Do not touch `SweepPluginAgentProfiles` (`internal/plugin/agent_profiles.go`) — its existing `if err != nil { h.logger.Warn(...); continue }` handling around the `DeleteAgentByID` call already does the right thing once `DeleteAgentByID` starts returning real errors. No caller-side change is needed; this is purely a fix at the source of the bad signal, per the guide's principle of enumerating every caller of a corrected primitive to confirm none of them need extra handling — `SweepPluginAgentProfiles` is the only production caller (`internal/plugin/agent_profiles.go:286`) and it already handles a non-nil return correctly.

## Tests required

- A new test in `internal/store/agents_fu28_test.go` (alongside the existing `TestDeleteAgentByID`, `agents_fu28_test.go:169-183`) that forces the underlying query to fail for a **non-not-found** reason and asserts `DeleteAgentByID` returns a non-nil error. The existing `TestDeleteAgentByID` only covers the true not-found case (`s.DeleteAgentByID("does-not-exist")` returning nil, `agents_fu28_test.go:180-182`) and must be left passing unchanged.
- To simulate a transient/non-not-found failure without a new mock layer, follow the pattern already used elsewhere in this package's tests (e.g. `internal/store/compaction_events_test.go` closes `s.DB` before exercising a method to force a real driver-level error): create a test store via `newTestStore(t)`, create a real agent with `makeTestAgent`, close `s.DB` directly (not via `t.Cleanup`, since `Store.Close()` will also run and should tolerate an already-closed DB — check `Store.Close()`'s current behavior before relying on this), then call `s.DeleteAgentByID(a.ID)` and assert the returned error is non-nil and does **not** satisfy `errors.Is(err, sql.ErrNoRows)`.
- Confirm the existing `TestDeleteAgentByID` (not-found case) still asserts a nil return after the fix — this is a regression guard on the no-op contract itself, not just the new failure-path test.

## Prevention

`nilerr` is already enabled in this repo's golangci config and already caught this exact bug (`raw/golangci-baseline.log:6421`) — no new lint rule is needed. The prevention mechanism here is the new regression test itself: it is the guide's own "regression reproducing original defect" requirement, and it is what makes a future refactor of `DeleteAgentByID` (or a copy-paste of its shape into a new by-ID delete method elsewhere in the package) visible if it reintroduces the same swallow.

## Verification

```bash
go build ./internal/store/... ./internal/plugin/...
go vet ./internal/store/... ./internal/plugin/...
go test ./internal/store/... -run TestDeleteAgentByID -v
golangci-lint run ./internal/store/... --enable-only nilerr
```

Observable behavior required for PASS: the new failure-path test fails on `git stash` of the fix (i.e. it genuinely reproduces `GO-STORE-003` against the unfixed code) and passes after the fix; the existing not-found test is unaffected; `nilerr` reports zero hits in `internal/store/agents.go`.

## Risk / rollback

Low risk, narrowly scoped to one method plus one doc comment. The only behavioral change visible to any caller is that `DeleteAgentByID` now returns a non-nil error in a case where it previously silently returned `nil` — this can only ever make a previously-silent failure visible (a strictly safer direction), never turn a previously-successful delete into a failure. `SweepPluginAgentProfiles` already logs and continues on a non-nil return, so the plugin-unload sweep's overall behavior for the transient-failure case improves (now logged) without any change to its control flow. Rollback is a single-commit revert; no data migration or schema involved.

## Done means

- [ ] `DeleteAgentByID` distinguishes `sql.ErrNoRows` (no-op, returns `nil`) from any other `GetAgent` error (propagates, non-nil).
- [ ] `DeleteAgentByID`'s doc comment reflects the corrected contract.
- [ ] New test added covering the non-not-found failure path; existing `TestDeleteAgentByID` still passes unchanged.
- [ ] `nilerr` reports zero hits against `internal/store/agents.go`.
- [ ] `go build`, `go vet`, and `go test ./internal/store/...` all pass.
- [ ] No change made to `internal/plugin/agent_profiles.go` (confirmed unnecessary per the caller-enumeration above; if review finds otherwise, escalate rather than silently expanding scope).

## Work log

<!-- Worker fills in: what was actually done, any deviation and why. -->

## Review notes

<!-- Reviewer fills in: pass/fail, what was independently re-verified. -->
