# 05 — Subagent execution ordering

Wave 2 (correctness/lifecycle/concurrency) per the remediation guide's §4
grouping: `internal/subagent/service.go` has two parallel-but-diverged entry
points into the same "run a subagent" lifecycle — `Spawn` (correctly gated by
the fan-out concurrency semaphore) and `Approve` (which bypasses it
entirely, GO-EXEC-001) — plus an ordering bug inside `Spawn`'s own gated
paths where a run is broadcast as "running" before it has a cancellation
hook registered, so an operator's `Cancel()` on a still-queued run doesn't
actually stop it (GO-EXEC-002). Both findings are concurrency/lifecycle
correctness bugs traced in full in
`docs/audits/2026-08-21-go-quality/REPORT.md` §8.10, not security or
data-integrity issues (`finalizeRun`'s reconciliation already prevents a
wrong terminal status from persisting) — the risk is wasted compute/tool
budget and a broken operator expectation, not corruption.

See `01-fix-approve-concurrency-cap-and-queued-cancel.md` for the full task
(findings GO-EXEC-001, GO-EXEC-002).
