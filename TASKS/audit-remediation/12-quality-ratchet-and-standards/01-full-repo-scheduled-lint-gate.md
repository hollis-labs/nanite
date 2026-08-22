# Wire the existing full-repo uncapped lint gate into an actual enforcement point

**Phase:** Wave 7 — Quality ratchet
**Status:** not-started
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

And there is no `.github/workflows/` directory anywhere in the repo (audit's
own repo-wide check, confirmed independently during this task-writing pass:
`ls .github/workflows` against this worktree returns no such directory).

Running `make lint` directly today surfaces **2,338 issues** under the
project's own already-approved linter set (`raw/golangci-baseline.log`,
referenced in the audit's evidence): errcheck 284, gosec 590, govet 530,
misspell 394, forbidigo 222, revive 68, staticcheck 78, unparam 35, unused 43,
errorlint 47, exhaustive 20, nilerr 22, ineffassign 2, unconvert 3. Whole-repo
debt accumulates silently: a file can carry an arbitrary number of
pre-existing issues forever as long as nobody touches those exact lines,
because `--new`-only linting only ever looks at diffs.

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

Concretely: the 2,338-issue current backlog does **not** need to reach zero
before this gate exists. The gate's job is to (a) capture the current state
as a recorded baseline, and (b) fail when a *new* run's actionable finding
count exceeds that baseline (or, more precisely, when a genuinely new
actionable finding appears — not a re-count of the same historical debt) —
not to block on the existing 2,338. The fast developer gate
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
- **Do not drive the 2,338-issue backlog to zero.** That is explicitly not
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
  does *not* fail on the pre-existing 2,338-issue backlog on its first run —
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
gosec ./...              # expect exactly one known/accepted G404 at
                          # internal/service/recovery_envelope_sink.go:223
                          # per the GO-SVCCORE-005 caveat above
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

- [ ] Architect/operator has confirmed whether an existing CI mechanism
      already runs (or could run) `make lint` on a schedule, and if not, has
      chosen where the new gate should live.
- [ ] A full-repo scheduled or merge-to-main gate exists that runs all six
      checks named in the guide's Wave 7 (uncapped lint, `govulncheck`,
      `gosec`, module verification, dead-code report, race suite where
      runtime permits).
- [ ] The gate's `gosec`-specific known-noise policy documents
      `internal/service/recovery_envelope_sink.go:223` as a pre-triaged,
      accepted non-issue (GO-SVCCORE-005).
- [ ] The gate does not fail against the current ~2,338-issue baseline on its
      first run; it does fail when a genuinely new actionable finding is
      introduced.
- [ ] `lefthook.yml`'s `pre-commit`/`pre-push` are unchanged — still under
      the <15-second target stated in the file's own header.
- [ ] A short runbook note describing the gate's scope, cadence, and the
      GO-SVCCORE-005 caveat exists somewhere discoverable (exact location
      per this project's own doc conventions, determined at implementation
      time).

## Work log

<!-- Worker fills in: what was actually done, any deviation and why. -->

## Review notes

<!-- Reviewer fills in: pass/fail, what was independently re-verified. -->
