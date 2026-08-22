# Wave 1 summary — for the operator

Wave 1 of the audit-remediation batch ("release-blocking trust boundaries")
is complete. This wave closed the batch's most severe findings: **2 critical
+ 2 high** in plugin installation, **1 critical + 1 high** (+1 low) in the
Linux sandbox, **2 high** in agent-slug path handling, plus one low-severity
item and one documentation-only codification task.

---

## What shipped, and why it matters

### Plugin installation no longer has a weaker, bypassable install path

The GUI "Plugin Manager"'s catalog-install feature (`POST
/api/plugins/catalog/install`) previously ran on an old, weaker pipeline
that (a) skipped signature verification whenever the catalog source had no
public key configured — true for the default seeded source, out of the box
— and (b) wrote installed files to an unconfined path built from an
untrusted catalog entry's name, a real path-traversal write. A newer,
correct, fail-closed pipeline already existed and was used by the CLI
(`nanite plugin install`); it just wasn't reachable from the GUI/API. The
GUI/API path now converges onto that same pipeline (task `01/01`), and a
related setting that was documented but silently inert
(`allow_unsigned_plugins`, a developer-only bypass gated behind a build tag
that never ships to production) was made to actually work as documented
(task `01/02`). See `TASKS/audit-remediation/ARCHITECT-DECISIONS.md`'s
**AD-04** and **AD-25** for the full decision record.

### Linux sandbox no longer silently degrades to unisolated execution

Previously, if `bwrap` (bubblewrap) wasn't installed on a Linux host, agent
shell/code-execution/workflow-shell/API-shell calls silently ran with no
OS-level isolation at all while still reporting success — and the one
warning about it fired at most once per process lifetime. Separately, even
when `bwrap` was present, configuring a network allowlist made the sandbox
*weaker* than configuring nothing at all (an inversion). Both are fixed:
Linux now fails closed without `bwrap` (with an explicit,
every-time-logged, operator-set opt-in to degrade instead), and the network
allowlist is now genuinely namespace-enforced via new infrastructure (a
"netns bridge") rather than relying on `HTTP_PROXY` convention. Task
`02/01`. See **AD-01** and **AD-02**.

A companion, lower-stakes finding on macOS — the seatbelt sandbox
intentionally allows unrestricted file reads/process-inspection by design —
was confirmed as an accepted, intentional tradeoff that stays as-is; what
actually needed fixing was that two internal engineering docs
(`docs/hardening-phase-plan.md`, `docs/programmatic-tool-calling-safety.md`)
were describing protections the shipped code doesn't provide. Both
corrected. Task `02/02`. See **AD-03**.

### Agent-config writes can no longer escape the managed directory via a crafted slug

An agent's `slug` field (and, found during this task's own required
caller-enumeration, a durable agent's slug too) was never validated before
being turned into a filesystem path. A crafted slug on create or rename
could write or overwrite files outside the intended managed directory. Both
paths — the original agent-profile path this finding named, and a second,
parallel durable-agent-config path the audit's evidence didn't mention at
all — are now validated with a canonical slug allow-list plus path
confinement as defense in depth. Task `03/01`.

### The audit's own hard-won lessons are now written down as citable standards

Six engineering standards derived from real, repeating defect patterns this
audit found (Production Reachability, Security/Correctness Migration
Completeness, Semantic Duplication, Lifecycle Ownership, Trust-Boundary
Paths, Silent Security Degradation) are now in
`docs/engineering/standards/coding-standards.md`, pulled forward from Wave 7
so the rest of this batch can cite them instead of rediscovering the same
judgment calls. Task `12/02`.

---

## Escalations this wave raised, and how they resolved

All full detail lives in `TASKS/audit-remediation/ARCHITECT-DECISIONS.md`
and `TASKS/ESCALATIONS.md` — summarized here, not re-explained.

- **AD-01, AD-02 (Linux sandbox fail-open, network allowlist inversion)** —
  decided 2026-08-22. AD-01: fail closed with an explicit, always-logged
  opt-in. AD-02: fix the inversion for real (a namespace-crossing relay),
  not just document it — real new infrastructure, not a flag flip.
- **AD-03 (macOS seatbelt read boundary)** — decided 2026-08-22. Disclose,
  don't narrow: the code posture is an accepted tradeoff; the documentation
  claiming otherwise was the actual defect, and it's now fixed.
- **AD-04 (plugin-install convergence shape)** — decided 2026-08-22, six
  sub-questions resolved. Notably caught a real gap neither the audit nor
  the original task file saw: converging onto the CLI's installer as
  originally scoped would have silently dropped `.zip` catalog-entry
  support (the CLI installer only ever handled `.tar.gz`) — caught and
  fixed as part of the decision, not discovered as a regression later.
- **AD-25 (`allow_unsigned_plugins`: wire or retire)** — this is worth
  flagging on its own: it's a genuine **planning-pass gap**, not a
  pre-existing decision that was simply pending. The `01/02` task file
  itself said an architect decision was required, but no matching `AD-NN`
  entry existed anywhere in the decision log at all. Found and closed
  during Wave 1's own dispatch prep, via direct operator confirmation
  (wire it), before any worker started. Worth knowing this kind of gap is
  possible — it means "the decision log has an entry" isn't fully
  equivalent to "someone checked whether it should."
- **Wrapper-subdirectory extraction tolerance** — `01/01`'s convergence
  initially and unintentionally dropped a real, pre-existing behavior: the
  old handler tolerated an archive whose `plugin.yaml` sits inside a single
  wrapper subdirectory (the shape a plain GitHub "Download ZIP" produces).
  An independent re-review caught this same day; the operator decided to
  restore it, and it was fixed cleanly inside the new extraction code with
  no changes to the underlying install pipeline. Documented in `01/01`'s
  own Work Log.
- **Leftover downloaded archive file (new finding, not previously
  catalogued)** — the same independent re-review of `01/01`'s follow-up fix
  found, incidentally, that installed plugin directories permanently retain
  the downloaded archive file (`.zip`/`.tar.gz`) alongside the actual
  extracted plugin content — nothing in the pipeline ever cleans it up
  after a successful install. This affects both the GUI/API and CLI
  install paths (they share the same downloader) and predates this wave
  (it's latent in the CLI pipeline the GUI/API converged onto — never
  caught before because nobody had test coverage checking install-
  directory *contents*). It's correctness/hygiene, not a security issue —
  doesn't touch verification or path confinement. **Not fixed in this
  wave** — logged as a fast-follow candidate in `TASKS/ESCALATIONS.md`'s
  2026-08-22 entry, with two candidate fix shapes already sketched there.

---

## Still flagged or deferred — needs attention before/alongside Wave 2

- **Leftover-archive-file cleanup** (above) — small, well-scoped fast-follow
  for whoever next touches `internal/plugin/install/`.
- **`02/01`'s minor, non-blocking test-robustness observation** — one test
  (`TestNetnsBridge_HostArbitraryPortStillBlocked`) can pass "for the wrong
  reason" in an unprivileged/restricted CI environment, because a bwrap
  setup failure and a correctly-blocked connection both look the same to
  the assertion. Confirmed by the reviewer that the actual shipped
  `applyOSSandbox` code path is not affected (verified separately, directly)
  — this is a test-fixture precision gap only. Cheap follow-up whenever
  `internal/sandbox`'s tests are next touched.
- **`go.mod`/Dockerfile Go-version drift** — `go.mod` declares `go 1.26.2`;
  the repo's `Dockerfile` `go-build` stage is pinned to
  `golang:1.25-alpine` (the same image `02/01`'s worker used, deliberately,
  for real-Linux verification). Not something this wave was scoped to fix;
  worth a look before anyone next touches the Dockerfile or bumps the Go
  version.
- **`03/01`'s deliberately-unfixed same-slug collision gap** — the DB-only-
  materialize update branch still has no exists-check, so two agents can
  in principle collide on the same slug and one silently overwrites the
  other's managed file. This is a separate, non-traversal data-integrity
  issue from the one this task fixed; the task file's own instructions
  explicitly scoped it out (confirm sufficiency, don't add the check), and
  it's flagged rather than silently dropped.
- **A review-documentation gap, found and closed same day.** This
  doc-writer pass initially found that `TASKS/INDEX.md` marked all six
  Wave 1 tasks `reviewed`, but only `02/01`'s task file carried a
  substantive `## Review notes` write-up — `01/01`/`03/01`'s real review
  activity lived only in their Work Logs' "Follow-up" sections, and
  `01/02`/`02/02`/`12/02` had empty placeholder `## Review notes` sections.
  Every task did in fact receive a genuinely independent, fresh-context
  reviewer dispatch (this was a record-keeping gap, not a skipped-review
  one) — the Orchestrator backfilled all five task files' `## Review notes`
  with the actual verdicts and findings from each real review round
  (commit `b4e2bc12`), the same day this gap was found. All six Wave 1
  task files now carry a complete, independently-checkable review record.

## Real review rigor, for confidence in what shipped

Two of six tasks were not rubber-stamped — independent reviewers found and
required real fixes before acceptance:

- **`02/01`** — the fresh reviewer didn't just read the Work Log. It
  independently stood up a real Linux VM (colima+docker) and re-ran the
  bwrap-absent and bwrap-present scenarios itself, confirming the
  fail-closed path, the degraded-opt-in path, and the AD-02 netns-bridge
  end-to-end network path all genuinely work — not just that the code
  compiles.
- **`01/01`'s follow-up** — an independent re-review caught a real,
  silently-introduced regression (the wrapper-subdirectory extraction
  tolerance) that the original implementation's own Work Log had logged as
  a deliberate, considered trade-off rather than an oversight. The
  operator's call was to restore it rather than accept the trade-off, and
  it was fixed the same day. That same review pass is also what surfaced
  the leftover-archive-file finding above.
- **`03/01`** — a reviewer went further than reading the diff: it
  mutation-tested the worker's own regression test (temporarily reverting
  the fix the test was supposed to guard, confirming the test still passed
  — i.e., the test was a false guard) and required a second, better test
  before accepting the fix. That mutation-test methodology is documented
  directly in the task's Work Log.

---

## `TASKS/INDEX.md` state for Wave 1

All six tasks: **`reviewed`**.

| Task | Findings closed | Status |
|---|---|---|
| `01/01` | GO-PLUGIN-001 (critical), GO-PLUGIN-002 (critical), GO-PLUGIN-003 (high) | reviewed |
| `01/02` | GO-PLUGIN-008 (low) | reviewed |
| `02/01` | GO-SEC4-001 (critical), GO-SEC4-002 (high), GO-SEC4-006 | reviewed |
| `02/02` | GO-SEC4-005 (low) | reviewed |
| `03/01` | GO-AGENT-001 (high), GO-AGENT-002 (high) | reviewed |
| `12/02` | (guide-derived, not finding-derived) | reviewed |

Wave 1 was the entire content of the batch's release-blocking trust
boundaries — with Wave 1 closed, the next dispatchable work is Wave 2a
(`04-container-reaper-lifecycle/`, `05-subagent-execution-ordering/`), per
`TASKS/INDEX.md`'s own wave table. The repo-wide development freeze
(**AD-24**) remains in effect for every other batch; `TASKS/audit-
remediation/` remains the operator's sole authorized work until the
operator lifts it.
