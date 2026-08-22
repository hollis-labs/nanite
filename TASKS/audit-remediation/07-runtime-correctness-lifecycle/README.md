# 07 — Runtime correctness / lifecycle

This grouping covers five findings the remediation guide explicitly asks to be
reviewed together: `GO-RUNTIME-001`, `GO-RUNTIME-003`, `GO-RUNTIME-004`,
`GO-RUNTIME-005`, and `GO-RUNTIME-007`, all evidenced in
`docs/audits/2026-08-21-go-quality/REPORT.md` §8.13 (`internal/runtime`
(+agent), `internal/lifecycle`, `internal/scheduler`, `internal/background`,
`internal/server`, `internal/worktree`, `internal/workspace`, `cmd/nanite`).
They share a theme — lifecycle/error-handling correctness in the daemon's
boot sequence and long-running subsystems — rather than a single root cause;
each task file is independently scoped and independently fixable. (Two
related findings from the same cluster, `GO-RUNTIME-002` — auth/bind/TLS
posture — and `GO-RUNTIME-006` — comment-density hygiene — are **not** in
this folder: `GO-RUNTIME-002` belongs to `08-remaining-security-hardening/`
per the guide's Wave 3 grouping, and `GO-RUNTIME-006` is a documentation-only
observation, not a task-worthy defect on its own. `GO-RUNTIME-008`,
`agent.Boot`'s complexity, is tracked separately as a complexity-table entry,
not a remediation task.)

**Priority within this folder is explicit, not left to the reader to infer.**
The remediation guide's own text for this exact grouping states: *"Prioritize
observable behavioral defects such as wrong orphan branch cleanup and
unbounded retained job state over informational idempotency/comment
issues."* Concretely: **`01-fix-worktree-orphan-branch-cleanup.md`
(`GO-RUNTIME-003`) is the clearest, most severe, most observable behavioral
defect in this folder** — every `nanite serve` daemon boot silently fails to
delete the git branch it means to clean up, leaking a stray branch into the
host repo forever, and should be treated as this folder's first priority by
whoever sequences it into an actual batch. `02-fix-cmdserve-fatal-cleanup-bypass.md`
(`GO-RUNTIME-001`), `03-bound-background-job-registry-growth.md`
(`GO-RUNTIME-004`), `04-container-shutdown-idempotency-guard.md`
(`GO-RUNTIME-005`), and `05-fix-mcp-config-silent-decode-errors.md`
(`GO-RUNTIME-007`) are all lower-priority informational/robustness gaps by
comparison — bounded-blast-radius error-handling bypasses, unmeasured
unbounded memory growth, a currently-dormant idempotency gap, and a silent
config-decode failure, respectively — real, but none as sharply "this runs
on every boot and silently does the wrong thing" as task 01. This ordering
is this folder's own internal priority signal only; it is not a commitment
about where this folder sits relative to the other twelve `audit-remediation/`
folders — that cross-folder sequencing is explicit planner work, per the
parent `TASKS/audit-remediation/README.md`.
