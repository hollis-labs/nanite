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
   per-linter Stage 1 ratchet;
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

Stage 2 is deliberately inactive. It is future zero-tolerance for exactly
`errcheck`, `errorlint`, and `nilerr`, and may be activated only after
`TASKS/audit-remediation/14-followups/02-error-handling-backlog-paydown.md`
reduces all three backlogs to zero. Until that trigger, those three linters are
ordinary Stage 1 participants at 281, 48, and 24 findings respectively; do not
approximate Stage 2 with smaller nonzero thresholds.

## Standalone gosec known noise

Bare `gosec` does not understand golangci-lint's `//nolint:gosec` syntax. It
therefore reports G404 for `pickRecoveryGiphyQuery` even though the source has
the accepted suppression at
`internal/service/recovery_envelope_sink.go:223`. The function selects cosmetic
Giphy text and makes no security decision. The comparison runner recognizes
only that path/rule/symbol combination as GO-SVCCORE-005 known noise, and the
comparison fails unless exactly one such match exists. Zero matches means the
exception is stale; two matches means it has broadened or duplicated. Every
nonmatching issue remains actionable and participates in the per-rule baseline.
The command must keep `-exclude-dir=.claude` while leaving the tracked package
set in scope.

To refresh a reduced baseline, generate both JSON reports with the same pinned
versions and on `darwin/arm64`, inspect the removed findings, lower only the
corresponding counts, and rerun both comparison modes. Never raise a baseline
to make a new regression pass.
