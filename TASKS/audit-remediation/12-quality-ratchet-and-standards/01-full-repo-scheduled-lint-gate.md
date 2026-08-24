# Wire the existing full-repo uncapped lint gate into an actual enforcement point

**Phase:** Wave 7 — Quality ratchet
**Status:** reviewed
**Depends on:** none technically, but see Context — this task is a natural *late*
item in any eventual sequencing (the guide's own Wave 7 framing: enforcement
comes "after meaningful backlog reduction," not before). A planner should not
schedule this early just because it's cheap to describe.
**Touches:** `lefthook.yml` (hooks config, unchanged by this task itself —
read, not edited, to establish the fast-gate baseline this task must not
regress), `Makefile` (`lint` target, already correct, not edited by this
task), and whatever new CI/scheduling mechanism gets chosen (`.github/workflows/`
or another system entirely — **undetermined, see `requires_architect_decision`
below**). This task's own output is new config/workflow file(s) plus a short
runbook note; it does not touch any Go source.

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 7 — quality ratchet · **Dispatch unit:** `W7`
> - **Depends on:** Wave 6 complete, and `00/02` for the frozen-HEAD baseline numbers
> - **Blocks:** none
> - **Parallel-safe with:** `12/03`
> - **Gated on:** AD-21 — which historical lint classes become blocking. Constraint from the guide: baseline history, reject regressions; do **not** require the backlog to reach zero first.
> - **requires_security_review:** false · **requires_regression_test:** false

> ## ✅ AD-21 DECIDED (2026-08-22) — ship in two stages
>
> **Stage 1, now:** baseline every linter in the audit config and fail the gate
> on any increase. Justified empirically — across 40 commits of ordinary
> development the counts moved gosec +35, cyclop +25, gocyclo +25, gocognit
> +16, errcheck +10, with nothing watching.
>
> **Stage 2, on a named trigger:** zero-tolerance on `errcheck`, `errorlint`,
> and `nilerr`. These three are singled out on evidence — **`nilerr` already
> caught `GO-STORE-003`** (high severity) and was ignored because the fast hook
> runs `--new` and cannot see pre-existing findings in untouched code.
>
> ### ✅ Stage 2 prerequisite satisfied by `14/02` (2026-08-24)
>
> Re-measured at implementation time: errcheck **281** + errorlint **48** +
> nilerr **24** = **353**. Task `14/02` reduced all three to zero and activated
> Stage 2 as its closing act.
>
> **Do not activate stage 2 until that backlog is zero.** A gate that fails
> every merge from day one gets disabled within a week, taking stage 1 with it.
> Record the trigger explicitly in whatever config you ship.
>
> **Do not** reinterpret "zero-tolerance" as "regression-gate these three as
> well" — that is a weaker decision than the one made, and it would silently
> discard the reason those three were separated out.

## Context

`requires_architect_decision: true`. Two independent open questions block
implementation, not just design polish:

1. **Does a full-repo scheduled/merge gate already exist somewhere this audit
   pass had no visibility into?** The audit found *no* `.github/workflows/`
   directory anywhere in this repo and *no* hook (pre-commit or pre-push) that
   invokes `make lint`. That is strong evidence of a real gap, but it is not
   proof — this project could have an external CI system (a Cerberus
   pipeline, a separate scheduler, something outside GitHub Actions entirely)
   that already runs `make lint` on a schedule the audit's static repo-scan
   couldn't see. The audit's own false-positive note is explicit: *"Entirely
   possible this is intentional — the project may already plan a separate CI
   system (outside GitHub Actions) that runs `make lint`; only the repo
   itself shows no evidence of it. Confirm before treating as a gap rather
   than a known, accepted tradeoff."* (`docs/audits/2026-08-21-go-quality/REPORT.md`,
   GO-HYG-001 section, "False-positive considerations.") **The first
   implementation step is not writing a workflow file — it is asking the
   architect/operator whether such a system exists**, and if so, wiring into
   it rather than building a parallel one.
2. **If it needs to be built from scratch, where should it live?** GitHub
   Actions is the obvious default given `.github/workflows/` is the
   conventional location, but the guide only specifies "a nightly/scheduled
   job or a merge-to-main gate," not a specific mechanism, and this project's
   own deploy model (Cerberus-managed `launchd` services, per this repo's
   `CLAUDE.md`) suggests GitHub Actions may not be this project's only or
   even primary automation surface. That choice belongs to whoever owns CI
   infrastructure for this repo, not to this task file.

### Findings addressed

- `GO-HYG-001` — severity **informational**, confidence **high**.
  `docs/audits/2026-08-21-go-quality/REPORT.md` §3 (full writeup: "GO-HYG-001
  Full-repo uncapped lint has no enforcement point"); `findings.json` id
  `GO-HYG-001`.
- `GO-SVCCORE-005` — severity **informational**, confidence **high**.
  `docs/audits/2026-08-21-go-quality/REPORT.md` §8.3; `findings.json` id
  `GO-SVCCORE-005`. Related tooling-mechanics note folded into this same task
  because it directly affects how the gate this task builds should interpret
  `gosec` output — see "The GO-SVCCORE-005 caveat" below.

### Root cause

Not a code defect — a process gap. The project already did the hard part
correctly: `.golangci.yml` deliberately sets `max-issues-per-linter: 0` /
`max-same-issues: 0` (the file's own comment says this was "a prior audit's
fix"), and the `Makefile`'s `lint` target chains `go vet` + uncapped
`golangci-lint` + `staticcheck` + `errcheck` + `govulncheck` correctly
(`Makefile:54-63`). But nothing ever *calls* that target outside a developer
manually typing `make lint`. The two git hooks that do exist are both
deliberately narrow:

- `lefthook.yml:27-31` (`pre-commit` → `go-lint`): `golangci-lint run --new
  --timeout 30s` — changed lines only, by design (comment: "Only lints
  new/modified code so existing warnings don't block commits").
- `lefthook.yml:65-69` (`pre-push` → `go-test`): `go test ./...` — no lint at
  all.

Before this task was implemented, there was no `.github/workflows/` directory
anywhere in the repo (the audit's repo-wide check, confirmed independently
during the task-writing pass). This task adds that directory and the first
workflow after the operator selected GitHub Actions.

The clean, tracked-package implementation-time audit-config run surfaces
**3,615 issues**: cyclop 290, dupl 97, errcheck 281, errorlint 48, exhaustive
28, forbidigo 236, funlen 69, gocognit 257, gocyclo 287, gosec 637, govet 619,
ineffassign 2, maintidx 31, misspell 429, nestif 62, nilerr 24, revive 70, staticcheck 75,
unconvert 3, unparam 26, and unused 44. Whole-repo debt accumulates silently:
a file can carry an arbitrary number of pre-existing issues forever as long as
nobody touches those exact lines, because `--new`-only linting only ever looks
at diffs.

### The GO-SVCCORE-005 caveat (tool-syntax gap, fold into this gate's design)

Separately, the audit found a specific false-positive trap that will bite
whoever wires up the `gosec` leg of this gate: `internal/service/recovery_envelope_sink.go:223`
carries

```go
//nolint:gosec // G404: cosmetic Giphy-query selection, non-security.
func pickRecoveryGiphyQuery() string {
	return recoveryGiphyQueries[rand.Intn(len(recoveryGiphyQueries))]
}
```

`//nolint:gosec` is `golangci-lint`'s suppression syntax and works correctly
when `gosec` runs *as a linter inside `golangci-lint`* (as `make lint` already
does, `Makefile:60`). But the remediation guide's own §10/§25 verification
commands also recommend running the **bare `gosec` binary directly**
(`gosec ./...`, not through `golangci-lint`). A bare `gosec` run does not
understand `//nolint:gosec` syntax at all — it only recognizes its own
`#nosec` comment convention. So a raw `gosec ./...` invocation will produce
one avoidable false "HIGH" (G404) at this exact line, even though the
suppression is already correctly in place for the `golangci-lint`-mediated
path. This is not a code defect — the audit's recommendation is explicit:
*"Note in the audit runbook that `//nolint:gosec` won't suppress bare `gosec`
runs; consider adding a matching `#nosec` comment for consistency."*
(`findings.json`, `GO-SVCCORE-005.recommendation`)

This matters directly to this task because the full-repo gate this task
builds will very likely run `gosec` in one of two ways — through
`golangci-lint`'s `gosec` linter (already covered, no false positive) or as a
standalone `gosec ./...` step per the guide's Wave 7 "known-noise policy" for
`gosec` (a separate, additional signal source golangci's gosec linter
doesn't necessarily replicate 1:1). **Whichever gate design is chosen, its
`gosec`-specific known-noise policy must document this one line as an
expected, pre-triaged non-issue** — otherwise every future scheduled run
re-surfaces a "new HIGH finding" that isn't new and isn't real, which
directly undermines the ratchet's "reject regressions/new actionable
findings" premise (a gate that cries wolf gets ignored).

### Desired invariant

Quoting the guide directly (§4, Wave 7, "Full-repo scheduled/merge gate"):

> Do not require historical low-value debt to hit zero before introducing a
> ratchet. Baseline and reject regressions/new actionable findings.

Concretely: the 3,615-issue current audit-config backlog does **not** need to reach zero
before this gate exists. The gate's job is to (a) capture the current state
as a recorded baseline, and (b) fail when a *new* run's actionable finding
count exceeds that baseline (or, more precisely, when a genuinely new
actionable finding appears — not a re-count of the same historical debt) —
not to block on the existing 3,615. The fast developer gate
(`lefthook.yml`'s `pre-commit`/`pre-push`) stays exactly as it is; nothing in
this task adds to it.

## Scope

Per the guide's explicit Wave 7 split, this task covers only the "full-repo
scheduled/merge gate" side. The fast developer gate is explicitly **out of
scope** — see Non-goals.

The guide names six checks for the full-repo gate (§4, Wave 7):

- uncapped lint (`make lint`'s `golangci-lint run --max-issues-per-linter=0
  --max-same-issues=0`, already correctly configured — `Makefile:58-63`)
- `govulncheck` (already included as the last step of `make lint`,
  `Makefile:63`, and separately callable via `make vuln`, `Makefile:66-67`)
- `gosec` (with the GO-SVCCORE-005 known-noise caveat above baked into its
  policy — note `make lint`'s uncapped `golangci-lint` run already includes
  the `gosec` linter per the audit's own lint-triage table, 590 gosec issues
  in the current baseline; a standalone `gosec ./...` run per the guide's
  §10/§25 verification commands is a distinct, additional invocation this
  gate should also consider, with the caveat applied)
- module verification (`go mod verify`, `go mod tidy -diff` — both named in
  the guide's §10 post-remediation verification list, neither currently
  wired into any hook or workflow in this repo)
- dead-code report (`deadcode -test ./...`, per the guide's §10 list — not
  currently run anywhere in this repo either, per this task-writing pass's
  own check for a `deadcode` reference in `Makefile`/`lefthook.yml`, which
  found none)
- race suite where runtime permits (`go test -race ./...` — note `make test`,
  `Makefile:51-52`, already runs this locally; the question for this gate is
  whether a scheduled/CI run should also run it, and whether the "where
  runtime permits" qualifier implies a timeout or resource concern specific
  to this project's CI environment that needs to be determined once that
  environment itself is known)

## All production callers

Not applicable in the security/correctness-fix sense — this is a tooling/
process task, not a shared-primitive migration. There is no "caller" to
enumerate; the closest analog is "every place `make lint` or its constituent
tools are currently invoked," which is exactly the two narrow hook commands
identified above (`lefthook.yml:27-31`, `lefthook.yml:65-69`) plus manual
developer use of `make lint` itself. Both are unaffected by this task adding
a separate, additional gate.

## Proposed direction

1. **Resolve the open question first.** Before writing any workflow/config
   file, confirm with the architect/operator whether a CI mechanism already
   exists for this repo (Cerberus-based, external scheduler, or otherwise)
   that could run `make lint` on a schedule, versus needing this built from
   scratch. Do not assume GitHub Actions is the answer merely because
   `.github/workflows/` is the conventional location — this repo's own
   `CLAUDE.md` describes a Cerberus/`launchd`-centric deploy model, which may
   mean the natural home for a scheduled job is a Cerberus-managed mechanism
   instead. This determination is itself the architect decision this task is
   flagged for; do not proceed past it silently.
2. Once the mechanism is chosen, wire `make lint` (or an equivalent scoped
   command sequence covering the six checks above) into it as either:
   - a nightly/periodic scheduled job, or
   - a merge-to-main gate (blocking merge on `main`, not on every push to a
     feature branch),
   
   per the guide's own framing — either satisfies the "Tier B" placement the
   guide references; the choice between them is a second, smaller architect
   call (frequency/blocking-ness) that can be made at the same time as the
   mechanism choice.
3. Record a baseline. The existing `raw/golangci-baseline.log` /
   `raw/golangci-baseline.json` from this audit (referenced in
   `docs/audits/2026-08-21-go-quality/`) is a starting point for what
   "current state" means, but the gate's actual baseline artifact should be
   generated fresh at wiring time (current HEAD may have already drifted from
   the audited commit `8feeee5c` — see this batch's own top-level `README.md`
   "Audited commit vs. current state" note) rather than reusing the audit's
   snapshot directly.
4. Bake the GO-SVCCORE-005 caveat into the `gosec` leg's known-noise policy
   explicitly — document `internal/service/recovery_envelope_sink.go:223` as
   a pre-triaged non-issue in whatever runbook/config comment accompanies
   this gate, so a bare `gosec ./...` run's one false HIGH doesn't get
   treated as a real new finding on the first scheduled run.
5. Add a short runbook note (README section of this gate's config, or a
   `docs/` note if this project keeps CI runbooks separately — check current
   convention before choosing a location) describing: what the gate checks,
   how often it runs, where its output is visible, and the GO-SVCCORE-005
   nolint-syntax gap as a known, accepted noise source for the `gosec` leg
   specifically.

## Non-goals

- **Do not touch the fast developer gate.** `lefthook.yml`'s `pre-commit`
  (`go-format`, `go-lint --new`, `go-vet`, `migration-purity`,
  `frontend-lint`) and `pre-push` (`go-test`) stay exactly as configured.
  The file's own header states the target explicitly: *"Target: all checks
  complete in <15 seconds on staged files only."* (`lefthook.yml:3`) Nothing
  in this task adds a new command to that file or changes an existing one's
  behavior.
- **Do not drive the 3,615-issue backlog to zero.** That is explicitly not
  this task's job — see Desired invariant above. A separate mechanical-
  cleanup effort (this batch's `13-mechanical-cleanup/` folder, and likely
  further work beyond it) reduces the backlog over time; this task only adds
  the gate that prevents it from growing.
- **Do not invent a CI mechanism speculatively.** If the architect
  determination in step 1 above surfaces ambiguity that can't be resolved
  within this task's own scope, stop and escalate rather than guessing at a
  GitHub Actions workflow that may duplicate or conflict with an existing
  system.
- **Do not change `.golangci.yml` or the `Makefile`'s `lint`/`vuln` targets.**
  Both are already correct per the audit's own finding; this task only adds
  an enforcement point that calls them, not a change to what they check.

## Dependencies

- None within this batch. This task can be picked up independently of every
  other folder's work, though per the Context section above it is naturally
  a *late* item in any real sequencing (enforcement follows backlog
  reduction, not the reverse — see this folder's `README.md`).

## Tests required

- Not applicable in the "regression reproducing original defect" sense —
  there is no code defect to reproduce. Instead: once the gate is wired,
  verify it actually fires by intentionally introducing one throwaway
  actionable lint issue on a branch and confirming the gate catches it (then
  revert the throwaway change). This is the functional-correctness check
  for a gate rather than a unit test.
- Confirm the gate's baseline-comparison logic (however it's implemented)
  does *not* fail on the pre-existing 3,615-issue backlog on its first run —
  this is the direct verification of the "baseline and reject regressions"
  invariant.

## Prevention

This task *is* the prevention mechanism for GO-HYG-001 — there is no further
meta-prevention needed beyond "the gate exists and someone gets paged/blocked
when it fails." For GO-SVCCORE-005, the prevention is the runbook note
itself: documenting the tool-syntax gap once, in the place someone
configuring or debugging this gate will actually look, rather than
rediscovering it as a confusing false alarm on a future scheduled run.

## Verification

```bash
# Confirm the six checks all still run cleanly as a manual baseline,
# independent of wherever the gate ends up living:
go build ./...
go vet ./...
golangci-lint run --max-issues-per-linter=0 --max-same-issues=0
gosec -no-fail -exclude-dir=.claude -fmt=json -out=/tmp/gosec.json ./...
                          # the comparison policy accepts exactly the G404 at
                          # internal/service/recovery_envelope_sink.go:223;
                          # all other findings remain in the actionable ratchet
govulncheck ./...
go mod verify
go mod tidy -diff
deadcode -test ./...
go test -race ./...       # where runtime permits, per scope note above
```

Observable behavior required for PASS: the chosen scheduled/merge mechanism
actually invokes this sequence (or `make lint` plus the additions above) on
its configured cadence/trigger, its output is visible somewhere a human
checks, and a deliberately-introduced new actionable finding causes it to
fail while the pre-existing baseline does not.

## Risk / rollback

Low risk to production code (no source changes at all — this is pure
tooling/config). The main risk is process, not code: choosing the wrong
mechanism (duplicating an existing but undiscovered CI system) or setting the
gate to block merges before the team is ready for that friction. Both risks
are mitigated by resolving the architect decision (step 1) before building
anything. Rollback is simply removing or disabling whatever workflow/config
file this task adds — no code-level revert needed.

## Done means

- [x] Architect/operator has confirmed whether an existing CI mechanism
      already runs (or could run) `make lint` on a schedule, and if not, has
      chosen where the new gate should live.
- [x] A full-repo scheduled or merge-to-main gate exists that runs all six
      checks named in the guide's Wave 7 (uncapped lint, `govulncheck`,
      `gosec`, module verification, dead-code report, race suite where
      runtime permits).
- [x] The gate's `gosec`-specific known-noise policy documents
      `internal/service/recovery_envelope_sink.go:223` as a pre-triaged,
      accepted non-issue (GO-SVCCORE-005).
- [x] The gate does not fail against the current 3,615-issue baseline on its
      first run; it does fail when a genuinely new actionable finding is
      introduced.
- [x] `lefthook.yml`'s `pre-commit`/`pre-push` are unchanged — still under
      the <15-second target stated in the file's own header.
- [x] A short runbook note describing the gate's scope, cadence, and the
      GO-SVCCORE-005 caveat exists somewhere discoverable (exact location
      per this project's own doc conventions, determined at implementation
      time).

## Work log

- 2026-08-23: The operator selected GitHub Actions. Added a nightly 07:17 UTC
  plus manual-dispatch workflow, committed baseline, deterministic comparison
  runner, and runbook. Stage 1 covers all 21 audit-config linters. Stage 2
  remains deliberately inactive until `14/02` reduces errcheck 281 + errorlint
  48 + nilerr 24 (353) to zero.
- Added a committed JSON baseline and deterministic comparison runner. Stage 1
  is active across all 21 linters in the audit config and now passes at the
  clean, tracked-package count of 3,615 findings.
- 2026-08-24 (`14/02`): activated Stage 2 after the correctness-three backlog
  reached zero. The comparator now independently rejects any `errcheck`,
  `errorlint`, or `nilerr` finding and refuses a nonzero committed baseline for
  those linters.
- Fresh-review fix, 2026-08-23: rebuilt the Actions workspace as
  `apps/nanite` plus four public `libs/<module>` checkouts, pinned to the
  supplied remotely available commits. `go list ./...`, build, and vet all
  load successfully from that isolated geometry. The one-entry `macos-15`
  matrix feeds both `runs-on` and the comparator, which verifies the intended
  runner plus actual `go env GOOS/GOARCH` against baseline metadata before
  either comparison.
- The local 3,623 versus clean-checkout 3,615 discrepancy was one ignored npm
  dependency, `ui/node_modules/flatted/golang/pkg/flatted/flatted.go`; it
  contributed exactly eight complexity findings. The workflow now discovers
  buildable packages and keeps only directories containing Git-tracked Go
  files, producing 3,615 identically in fresh-cache local and isolated runs.
- Standalone gosec v2.28.0 over the explicit tracked-package set reports 317
  findings. The comparator requires exactly one GO-SVCCORE-005
  path/rule/symbol match and ratchets all 316 nonmatches by rule. End-to-end
  regression tests prove zero matches exit 1, one exits 0, and two exit 1;
  they also prove altered baseline GOARCH exits 1.
- Functional regression proof: temporarily added
  `internal/brand/quality_ratchet_regression.go` with one misspelling and one
  unused declaration. The comparator exited 1 after `misspell` increased
  429→430 and `unused` increased 44→45. The temporary file was then deleted;
  it is absent from the final tree. The current clean comparison is
  3,615/3,615 with exit 0.
- Final local verification exits: `go build ./cmd/nanite/` 0; `go vet ./...`
  0; `go test ./... -count=1` 0; tracked-package lint ratchet 0; tracked-package
  gosec ratchet 0; `go mod verify` and `go mod tidy -diff` 0; comparator tests
  0; actionlint 0; YAML and JSON parse checks 0. The earlier aggregate race
  suite completed with exit 0 against the developer sibling checkouts; the
  orchestrator-owned run recorded `internal/store` at 262.184s.
- Fresh-review blocker and resolution: the first clean proof found that public
  go-envelopes stopped at `4456292`, while Nanite's passing local state was
  unpushed `7978078`; the remote-pin ordinary suite exposed table-card and
  retired-envelope compatibility failures. The operator authorized publishing
  go-envelopes v0.2.0. Its annotated tag peels to release commit
  `7642d69f64499ea180c0c596a48516e00cd28d46` on public `origin/main`; the
  workflow now pins that exact commit, and the full clean ordinary/race proof
  passes. No review or approval is claimed.
- Released-pin clean verification exits: `go list ./...` 0 with 109 packages;
  `go build ./cmd/nanite/` 0; `go vet ./...` 0; `go test ./... -count=1` 0;
  tracked-package lint 0 at 3,615/3,615; tracked-package gosec 0 at 316/316
  actionable findings plus exactly one accepted match; `govulncheck` 0 with no
  vulnerabilities; `go mod verify` and `go mod tidy -diff` 0; deadcode 0 with
  a 64-line report; and tracked-package `go test -race -count=1` 0, with
  `internal/store` completing in 235.123s.
- Second re-review fix, 2026-08-24: package discovery now writes `go list`
  output with a direct fail-fast command before filtering begins, instead of
  consuming it through process substitution. A focused shell proof used a
  producer that wrote one partial row and returned 23; the wrapper returned 23
  and the filtering marker remained absent. Clean discovery still finds 109
  tracked packages, and the lint ratchet remains 3,615/3,615. Corrected the two
  current-summary `14/02` backlog references from 365 to 353 while preserving
  historical frozen-HEAD measurements. No review or approval is claimed.
- Orchestrator validation independently reproduced the partial-producer exit
  23 with no filtering output, confirmed real discovery still selects 109
  packages, reran all four comparator tests, the positive platform check,
  actionlint, JSON validation, whitespace/scope guards, and verified the two
  current summary rows now say 353. All passed; no application Go source,
  `lefthook.yml`, or `.golangci.yml` changed.

## Review notes

**2026-08-24 — final fresh-review verdict: PASS, no findings.** The initial
review found the missing sibling checkouts, unbounded gosec exception, and
unenforced/deprecated runner; re-review then found the package-discovery
fail-open and two stale current-count summaries. Separate worker fixes
resolved each finding. The reviewer independently verified all four public
dependency pins and the v0.2.0 tag peel, clean Actions-shaped package loading,
build/vet/ordinary tests, the complete 109-package scope, 3,615 findings
across exactly 21 audit-config linters, 317 standalone gosec findings (one
accepted and 316 actionable), gosec cardinality and platform regressions, all
six workflow families, actionlint/YAML/JSON/module/deadcode/vulnerability
checks, the recorded released-pin race exit 0, tracker and protected-file
scope, and the durable escalation entries. Final narrow review reproduced that
partial package output followed by exit 23 cannot reach filtering and
confirmed both current summary tables say 353. No findings remain.
