#!/usr/bin/env bash
# scripts/check.sh — the landing check.
#
# Run this when a feature lands: before you push a branch you care about, and
# before dispatching the full-repo quality gate. NOT on every commit — the
# pre-commit hooks are deliberately formatting-only so that a finding unrelated
# to the change in front of you never costs you every check at once via
# `--no-verify`.
#
# No arguments. Runs every stage, then names each stage that failed.
#
# ── Which tier is this? ───────────────────────────────────────────────────
# The test stage is **Tier 1** of `docs/engineering/testing-workflow.md` §3 —
# "feature done, before commit", budgeted there at ~1-2 min.
#
# That section's Tier 1 form is `go test -race -count=1` over the changed
# packages plus their dependents. This script runs the whole-repo, no-`-race`
# form (`go test ./...`) instead, because a landing script has to run with no
# arguments and cannot know which packages you changed. Wider in scope, no
# amplification, same wall-time budget. If your change touched goroutines,
# channels, `context` cancellation, mutexes, atomics, or shutdown ordering,
# this script is not enough — run Tier 2 (`-race -count=20` on the package you
# touched) yourself.
#
# **Tier 3** (full suite under `-race`, ~9 min) is what the nightly full-repo
# quality gate runs. **Tier 4** (`-timeout=180m` flake hunting) is deliberate
# and manual. Neither belongs in this script or on a git hook.
#
# ── Why the lint stage is scoped ──────────────────────────────────────────
# `golangci-lint run` over the whole repo exits non-zero on a large body of
# pre-existing findings, so a bare invocation here could never pass and would
# train you to ignore it. Whole-repo lint is the nightly gate's job, where it
# runs with `--issues-exit-code=0` against a committed baseline that ratchets
# (`scripts/quality-ratchet.py`). This stage uses the adoption-mode form that
# `.golangci.yml`'s own header prescribes: lint only what your work added,
# measured from the merge base with `main`.
#
# Override the diff base with CHECK_LINT_BASE=<rev>.

set -uo pipefail

cd "$(git rev-parse --show-toplevel)" || exit 1

if [ "${1:-}" = "-h" ] || [ "${1:-}" = "--help" ]; then
  sed -n '2,40p' "$0" | sed 's/^# \{0,1\}//'
  exit 0
fi

failed=()
stage_start=0

begin() {
  printf '\n==> %s\n' "$1"
  stage_start=$(date +%s)
}

finish() {
  # finish <stage-name> <exit-code>
  local elapsed=$(( $(date +%s) - stage_start ))
  if [ "$2" -eq 0 ]; then
    printf '    OK   (%ss)\n' "$elapsed"
  else
    printf '    FAIL (%ss)\n' "$elapsed"
    failed+=("$1")
  fi
}

# Tracked and untracked Go files, NUL-separated. Excludes ui/node_modules,
# which carries a stray vendored Go package that is not ours to format.
go_files() {
  git ls-files -z '*.go'
  git ls-files -z --others --exclude-standard '*.go'
}

# ── 1. format ─────────────────────────────────────────────────────────────
begin "format — gofmt + goimports"
# Positive control: an empty file list would make this stage pass while
# checking nothing. Assert presence before properties.
go_file_count=$(go_files | tr -cd '\0' | wc -c | tr -d ' ')
if [ "$go_file_count" -eq 0 ]; then
  echo "ERROR: found 0 Go files to check — the file list is broken, not clean."
  finish "format" 1
else
  echo "    $go_file_count Go files"
  unformatted=$(go_files | xargs -0 gofmt -l 2>/dev/null)
  if command -v goimports >/dev/null 2>&1; then
    # Newline separator matters: $(...) strips trailing newlines, so a bare
    # concatenation glues gofmt's last filename onto goimports' first.
    unformatted=$(printf '%s\n%s' "$unformatted" "$(go_files | xargs -0 goimports -l 2>/dev/null)")
  else
    echo "    note: goimports not on PATH — import grouping not checked"
  fi
  unformatted=$(printf '%s\n' "$unformatted" | sed '/^$/d' | sort -u)
  if [ -n "$unformatted" ]; then
    echo "Unformatted files:"
    printf '%s\n' "$unformatted"
    finish "format" 1
  else
    finish "format" 0
  fi
fi

# ── 2. vet ────────────────────────────────────────────────────────────────
begin "vet — go vet ./..."
go vet ./...
finish "vet" $?

# ── 3. lint ───────────────────────────────────────────────────────────────
lint_base="${CHECK_LINT_BASE:-}"
if [ -z "$lint_base" ]; then
  lint_base=$(git merge-base HEAD main 2>/dev/null) || lint_base=HEAD
fi
begin "lint — golangci-lint run --new-from-rev $(git rev-parse --short "$lint_base" 2>/dev/null || echo "$lint_base")"
echo "    (only findings on lines your work touched; whole-repo lint is the nightly gate)"
golangci-lint run --new-from-rev "$lint_base"
finish "lint" $?

# ── 4. test ───────────────────────────────────────────────────────────────
begin "test — go test ./...  (Tier 1, testing-workflow.md §3)"
go test ./...
finish "test" $?

# ── summary ───────────────────────────────────────────────────────────────
echo
if [ ${#failed[@]} -eq 0 ]; then
  echo "check.sh: all stages passed (format, vet, lint, test)"
  exit 0
fi
echo "check.sh: FAILED stages: ${failed[*]}"
exit 1
