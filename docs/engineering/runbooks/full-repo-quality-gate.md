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
- `go-envelopes` — `7642d69f64499ea180c0c596a48516e00cd28d46`
- `go-harness-filters` — `57a6b0919c0c5f06db90b367184988a72c430d39`
- `go-runtime-events` — `8756744985a6602d6ab1fb0df78d5aabc3920b1b`

Release resolution: the clean-checkout review initially found that public
go-envelopes stopped at `4456292`, while Nanite's passing local sibling state
was unpushed `7978078`. The operator authorized a release; v0.2.0 now peels to
`7642d69f64499ea180c0c596a48516e00cd28d46` on public `origin/main`, and the
workflow pins that exact release commit. The clean Actions-shaped ordinary and
race suites were rerun against this published state before the pin was recorded
as resolved.

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

## Blocker: the gate cannot resolve a private module dependency

*Observed 2026-08-24 at `4f3d38c4`, the first two times the workflow ever ran:*
[32788460848](https://github.com/hollis-labs/nanite/actions/runs/32788460848)
and [32788631043](https://github.com/hollis-labs/nanite/actions/runs/32788631043).
Both failed identically at **Discover tracked Go packages**, 42s and 50s in:

```
internal/memory/service.go:22:2: github.com/hollis-labs/tesseract@v0.7.1-0.20260518032333-bbce958849ac:
  invalid version: git ls-remote -q --end-of-options https://github.com/hollis-labs/tesseract ...
  fatal: could not read Username for 'https://github.com': terminal prompts disabled
```

`github.com/hollis-labs/tesseract` is a **private** repository (`gh api
repos/hollis-labs/tesseract --jq .visibility`), and it is the only private
external module in `go.mod` — the other twenty `hollis-labs` requires are all
public. It is required by version, not by a local `replace`, so the four
`libs/` checkouts above do not cover it, and a pseudo-version cannot come from
the public proxy. `go list ./...` therefore fails before the package filter runs
and every later step is skipped.

Nothing downstream of that step has ever executed in CI. Every claim about this
gate's behavior — in this runbook and elsewhere — still rests on local
reproduction only.

Resolving it needs a credential decision (a repository secret with read access
to `hollis-labs/tesseract`, plus `GOPRIVATE`, or vendoring, or making the module
public). That is an operator call, deliberately not made here.

The failure mode is at least the correct one: the step is fail-closed by design,
so a partial `go list` never reaches the filtering loop and never produces a
short package list that would have scanned less while reporting success.
