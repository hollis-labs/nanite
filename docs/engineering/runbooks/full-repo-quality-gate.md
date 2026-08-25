# Full-repo quality gate

The `Full-repo quality gate` GitHub Actions workflow runs nightly at 07:17 UTC
and on manual dispatch. A one-entry `macos-15` matrix is the source of truth
for both `runs-on` and the comparator's intended runner identity. The
comparator also reads `go env GOOS GOARCH` and fails before comparing findings
unless all three values match the committed `macos-15` / `darwin` / `arm64`
metadata. Workflow logs and the uploaded
`full-repo-quality-reports` artifact are visible on the repository's Actions
page for 14 days.

Nanite is checked out at `apps/nanite`. Its four public local-replace modules
are checked out at the exact pinned commits below, under `libs/`, preserving
the `../../libs/<module>` geometry in `go.mod` without a cross-repository
secret:

- `go-modelsdev` — `7d932798b85145ec93f923e392f5d41762894e8c`
- `go-envelopes` — see the workflow; **re-derive, this pin has moved**:
  `grep -A2 'repository: hollis-labs/go-envelopes' .github/workflows/full-repo-quality.yml`
- `go-harness-filters` — `57a6b0919c0c5f06db90b367184988a72c430d39`
- `go-runtime-events` — `8756744985a6602d6ab1fb0df78d5aabc3920b1b`

Release resolution: the clean-checkout review initially found that public
go-envelopes stopped at `4456292`, while Nanite's passing local sibling state
was unpushed `7978078`. The operator authorized a release; v0.2.0 now peels to
`7642d69f64499ea180c0c596a48516e00cd28d46` on public `origin/main`, and the
workflow pins that exact release commit. The clean Actions-shaped ordinary and
race suites were rerun against this published state before the pin was recorded
as resolved.

## What this gate guarantees — read before reporting a result

This gate was stood up during Nanite's **first** audit cycle. Its purpose is to
show **direction** — are we improving or regressing — against a baseline
refreshed on 2026-08-25. It is not a release gate, and Nanite is pre-release
with no consumers. Judge it against that goal, not against a mature CI system.

**Derive the shape yourself rather than trusting this paragraph's numbers:**

```
grep -c '^      - name:' .github/workflows/full-repo-quality.yml
```

Most steps set up the environment. Only some assert. As of 2026-08-25, of 17
steps, 8 assert and 7 of those 8 are sound.

### What it does guarantee

- **Regressions fail.** `compare_counts` in `scripts/quality-ratchet.py` exits
  non-zero on any per-rule increase and on findings from rule names absent from
  the baseline. There is no known way for a straightforward regression to pass
  silently.
- **Coverage is canaried.** `Assert the discovered package list matches the
  committed shape` pins the package count handed to every scanner. This is the
  most load-bearing assertion in the workflow: without it, lint, govulncheck,
  gosec, deadcode and the race suite can all scan less and still report success.
- **The lint ratchet cannot pass vacuously.** `Report.Error` is checked, so a
  `golangci-lint` run that failed to analyze what it was asked to analyze fails
  instead of reporting zero issues.

### What it does not guarantee — one item, one direction

**The gosec step can record a false improvement.** `gosec` has produced a
non-reproducible run that dropped 17 findings as a strict subset, with `files`
and `lines` identical, exit 0 and well-formed JSON — indistinguishable from a
real improvement. Observed once in 12 runs.

A reduction passes, and the comparator prints guidance to lower the committed
baseline. **Do not act on that message for a gosec reduction without confirming
it reproduces.** Lowering the baseline on a spurious decrease permanently
deletes real findings — the same error as raising a baseline to make a
regression pass, in the opposite direction.

Because comparison is per-`rule_id`, a same-run regression *in the same rule*
could in principle be masked by this. Narrow and unlikely, but not zero.

### How to report a gate result honestly

- A green run means: no regressions in any asserting step, at the package count
  the baseline was calibrated on.
- A green run does **not** by itself mean a gosec reduction shown in the log is
  real. Confirm before banking it.
- Report the run URL and the step that failed. "The gate is red" without a step
  name is not a finding.
- **Do not describe this gate as untrustworthy.** It has one known soft spot,
  named above, in one direction. Anything broader is not supported by evidence.

### Known intermittents — report and move on, do not chase

- `internal/memory` failing with `SQLITE_BUSY` — Torque `CW-20260825-0001`.
  Root-caused in tesseract and fixed there; Nanite's pin has not picked it up.

## Checks

The workflow installs pinned tool versions and runs all six Wave 7 check
families:

1. `golangci-lint` with the uncapped audit config, followed by the committed
   per-linter Stage 1 ratchet and Stage 2 zero-tolerance check for `errcheck`,
   `errorlint`, and `nilerr`;
2. `govulncheck` over the discovered tracked package set;
3. standalone `gosec`, with `.claude` excluded so nested agent worktrees are
   not scanned and with the known-noise policy below;
4. `go mod verify` and `go mod tidy -diff`;
5. the non-blocking-output `deadcode -test` report over that set; and
6. `go test -race -count=1` over that set.

The baseline lives at `.github/quality/full-repo-baseline.json`. To reproduce
the active lint comparison locally:

```bash
set -euo pipefail
report=$(mktemp)
linters=$(mktemp)
go_list=$(mktemp)
trap 'rm -f "$report" "$linters" "$go_list"' EXIT
go list -f '{{.ImportPath}}{{"\t"}}{{.Dir}}' ./... > "$go_list"
packages=()
while IFS=$'\t' read -r _ directory; do
  relative=${directory#"$PWD"/}
  if git ls-files --error-unmatch -- "$relative/*.go" >/dev/null 2>&1; then
    packages+=("./$relative")
  fi
done < "$go_list"
golangci-lint linters \
  --config docs/audits/2026-08-21-go-quality/audit-golangci.yml \
  --json > "$linters"
golangci-lint run \
  --config docs/audits/2026-08-21-go-quality/audit-golangci.yml \
  --issues-exit-code=0 --max-issues-per-linter=0 --max-same-issues=0 \
  --output.text.path=/dev/null \
  --output.json.path="$report" "${packages[@]}"
python3 scripts/quality-ratchet.py lint \
  --report "$report" --linters-report "$linters" --runner macos-15
```

The package discovery deliberately keeps only buildable packages with tracked
Go files. A developer checkout may contain ignored Go sources inside
`ui/node_modules`; a raw local `./...` sees those files even though a clean
Actions checkout cannot. Filtering them keeps local and scheduled counts
identical while still discovering every buildable, tracked Go package.
`go list` must remain a direct command which finishes successfully before the
filtering loop starts. A process substitution would hide its exit status from
the loop and could turn partial producer output into a nonempty, passing package
file.

Focused fail-closed proof:

```bash
proof_dir=$(mktemp -d)
set +e
bash -euo pipefail -c '
  partial_go_list() { printf "partial\t/tmp/partial\n"; return 23; }
  partial_go_list > "$1"
  : > "$2"
' _ "$proof_dir/go-list.txt" "$proof_dir/filter-started"
status=$?
set -e
test "$status" -eq 23
test ! -e "$proof_dir/filter-started"
```

The final two assertions prove that partial output can exist while filtering
still never starts.

Stage 1 is active now: every linter enabled in the audit config has a committed
count, and any per-linter increase fails. Lower counts pass and should be
written back promptly so later increases cannot consume the improvement.

Stage 2 is active: `errcheck`, `errorlint`, and `nilerr` must each remain at
zero. Task `14/02` satisfied the activation trigger by reducing the measured
backlogs from 281, 48, and 24 findings respectively to zero. The comparator
also requires their Stage 1 baseline counts to remain zero, so raising a
baseline cannot silently weaken Stage 2.

## Standalone gosec known noise

Bare `gosec` does not understand golangci-lint's `//nolint:gosec` syntax, so
every annotated-and-justified suppression in the tree reappears as a finding.
`standalone_gosec.known_noise` is a **list**, one entry per suppressed site, and
`G404` sits at `0` in `actionable_rule_counts`. Measured at `4f3d38c4` with
`gosec` v2.28.0 over the 109 tracked packages, all four raw G404 findings are
enumerated:

| Source site | `//nolint` at | `symbol` | Why it is accepted |
|---|---|---|---|
| `internal/service/recovery_envelope_sink.go:225` | `:223` | `pickRecoveryGiphyQuery` | Cosmetic Giphy-query selection (`GO-SVCCORE-005`) |
| `internal/chat/errors.go:95` | `:94` | `errorGiphyQueries` | Cosmetic Giphy-query selection in the error envelope |
| `internal/mcp/web_fetch_resilience.go:108` | `:107` | `fetchUserAgents` | UA rotation — load distribution, not a security decision |
| `internal/mcp/web_fetch_resilience.go:234` | `:233` | `jitterFactor` | Retry-backoff jitter — unpredictability is not a security property |

Matching is on path/rule/symbol, never on line number, so ordinary edits above a
site do not red the gate. **Each entry must match exactly once.** Zero matches
means the exception went stale; two or more means it broadened or duplicated;
one finding matching two entries is an overlap and also fails. Every
nonmatching issue remains actionable and participates in the per-rule baseline.
Every entry requires a written `reason` — the comparator rejects the baseline
without one, which is what keeps "add a suppression" from being cheaper than
"argue for the suppression". The command must keep `-exclude-dir=.claude` while
leaving the tracked package set in scope.

**Why a list rather than `G404: 4`.** A bare count is satisfied by any four
sites. Deleting a justified one while adding an unjustified one nets to zero and
passes. Reproduced at `4f3d38c4` against a real report with `jitterFactor`
removed and an unannotated `rand.Intn` added: the pre-`CW-20260824-0025`
comparator and baseline printed `G404: 3 (baseline 3)` and `ratchet passed`,
exit 0. The enumerated form fails the same report twice over — a stale
`jitterFactor` entry and `G404 increased from 0 to 1`.

To add an entry, confirm the site really carries a `//nolint:gosec` with a
written justification in situ, pick a `symbol` that appears in the finding's
`code` snippet and cannot plausibly appear at an unrelated site, and drop the
matching `actionable_rule_counts` entry by one in the same commit.

To refresh a reduced baseline, generate both JSON reports with the same pinned
versions and on `darwin/arm64`, inspect the removed findings, lower only the
corresponding counts, and rerun both comparison modes. Never raise a baseline
to make a new regression pass.

**Run `gosec` at least three times and require identical finding sets before
writing any number into the baseline.** A degraded run has been observed on this
tree that returned a strict subset of the findings while being otherwise
indistinguishable from a good one: exit 0, empty `Golang errors`, well-formed
JSON, and identical `files`/`lines` stats. Averaging or taking the lowest would
bake a permanently-red baseline into the gate.

## Module resolution — every external dependency resolves without a credential

The gate carries **no repository secret, no `GOPRIVATE`, and no vendoring**.
Confirm before assuming otherwise:

```
grep -in 'secret\|GOPRIVATE\|token' .github/workflows/full-repo-quality.yml
```

That returns nothing. Every `hollis-labs` module in `go.mod`, including
`github.com/hollis-labs/tesseract`, resolves from the public proxy. Check any
one of them with:

```
gh api repos/hollis-labs/tesseract --jq .visibility        # public
```

**If `Discover tracked Go packages` ever fails with `could not read Username
for 'https://github.com'`, that is this property breaking** — a module in
`go.mod` has become unreachable without a credential. Identify which one from
the error, check its visibility, and treat it as a dependency decision
(publish, vendor, or add a secret plus `GOPRIVATE`) rather than a workflow bug.

The step is fail-closed by design: a partial `go list` never reaches the
filtering loop, so it cannot produce a short package list that scans less while
reporting success. A failure here stops the run rather than shrinking it.
