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
# The test stage is **Tier 1** — "feature done, before commit" — budgeted at
# ~1-2 min.
#
# Tier 1's canonical form is `go test -race -count=1` over the changed
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
# measured from the merge base with `origin/main`.
#
# **Read that base carefully: it is the last *pushed* commit, not the point your
# branch started at.** The two coincide only while `main` is fully pushed. When
# local `main` is ahead of `origin/main`, the base sits further back and this
# stage lints a **superset** of your branch's own diff — it over-reports, never
# under-reports, which is the safe direction for a stage like this, but it is
# not what "merge base" on its own suggests. Findings from commits you did not
# write are expected in that state, not a bug.
#
# What the base is deliberately *not* is local `main`: on `main` that equals
# HEAD, so every commit you just made would be silently out of scope. Committed
# and uncommitted work are both in scope here.
#
# Override the diff base with CHECK_LINT_BASE=<rev> if you want a narrower one.

set -uo pipefail

# Resolve this script's own path BEFORE the cd below. `$0` is relative to the
# caller's directory, so `cd internal && ../scripts/check.sh --help` would
# re-resolve it against the repo root and read nothing at all.
self=$0
case "$self" in
  /*)  ;;
  */*) self="$PWD/$self" ;;
  *)   self=$(command -v -- "$self" 2>/dev/null || printf '%s' "$self") ;;
esac

if [ "${1:-}" = "-h" ] || [ "${1:-}" = "--help" ]; then
  # Self-delimiting: print the leading comment block and stop where it actually
  # ends, rather than at a hardcoded line span. That span silently went stale
  # twice as the header grew, truncating the help text with no signal.
  help_text=$(awk 'NR == 1 { next } /^#/ { sub(/^# ?/, ""); print; next } { exit }' "$self")
  # `--help` used to exit 0 even when it printed nothing, so a broken read was
  # indistinguishable from a short header. Assert the read produced something.
  if [ -z "$help_text" ]; then
    echo "ERROR: --help read no header lines from '$self'." >&2
    echo "       The help text is broken, not empty." >&2
    exit 1
  fi
  printf '%s\n' "$help_text"
  exit 0
fi

cd "$(git rev-parse --show-toplevel)" || exit 1

failed=()
examined_nothing=()
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

# A stage that had nothing to look at is not the same result as a stage that
# looked and found nothing. Say which one happened; never print a bare OK for
# the former.
finish_examined_nothing() {
  # finish_examined_nothing <stage-name>
  local elapsed=$(( $(date +%s) - stage_start ))
  printf '    ---  (%ss)  examined nothing\n' "$elapsed"
  examined_nothing+=("$1")
}

# Tracked and untracked Go files, NUL-separated. Excludes ui/node_modules,
# which carries a stray vendored Go package that is not ours to format.
go_files() {
  # `git ls-files` reads the index, so it includes tracked paths intentionally
  # deleted in the worktree until their deletion is staged. Feeding those
  # nonexistent paths to gofmt/goimports makes a valid pre-commit deletion
  # impossible to check. Keep every tracked/untracked Go file that exists and
  # omit only deleted tracked entries; the NUL loop preserves unusual paths.
  {
    git ls-files -z '*.go'
    git ls-files -z --others --exclude-standard '*.go'
  } | while IFS= read -r -d '' file; do
    if [ -f "$file" ]; then
      printf '%s\0' "$file"
    fi
  done
}

# ── 1. format ─────────────────────────────────────────────────────────────
begin "format — gofmt + goimports"
# Two different inputs would let this stage report OK while checking nothing,
# and it has to defend against both.
#
# 1. An empty file list. Assert presence before properties — the count below.
# 2. A tool that never ran. `gofmt -l` and `goimports -l` print NOTHING on
#    stdout when they cannot do their job — binary not on PATH, a file they
#    cannot parse, a path they cannot read (a tracked file deleted from the
#    worktree but still in the index does this) — and an empty stdout is
#    indistinguishable from "everything is clean" unless the exit status is
#    inspected. So capture it, fold stderr in so the reason is printable, and
#    fail the stage loudly on a tool failure.
#
# The count printed below comes from `git ls-files`: it names paths git knows
# about, not files a tool managed to open. Those two diverge exactly when a
# tool fails, which is what makes the status checks below load-bearing rather
# than defensive decoration.
go_file_count=$(go_files | tr -cd '\0' | wc -c | tr -d ' ')
if [ "$go_file_count" -eq 0 ]; then
  echo "ERROR: found 0 Go files to check — the file list is broken, not clean."
  finish "format" 1
else
  echo "    $go_file_count Go files (tracked + untracked, per git ls-files)"
  format_tool_failed=""

  gofmt_out=$(go_files | xargs -0 gofmt -l 2>&1)
  gofmt_status=$?
  if [ "$gofmt_status" -ne 0 ]; then
    echo "ERROR: gofmt did not run cleanly (exit $gofmt_status) — this stage"
    echo "       cannot report OK for files it never examined:"
    printf '%s\n' "$gofmt_out" | sed 's/^/       /'
    format_tool_failed=1
    gofmt_out=""
  fi

  goimports_out=""
  if command -v goimports >/dev/null 2>&1; then
    goimports_out=$(go_files | xargs -0 goimports -l 2>&1)
    goimports_status=$?
    if [ "$goimports_status" -ne 0 ]; then
      echo "ERROR: goimports did not run cleanly (exit $goimports_status) —"
      echo "       this stage cannot report OK for files it never examined:"
      printf '%s\n' "$goimports_out" | sed 's/^/       /'
      format_tool_failed=1
      goimports_out=""
    fi
  else
    echo "    note: goimports not on PATH — import grouping not checked"
  fi

  if [ -n "$format_tool_failed" ]; then
    finish "format" 1
  else
    # Newline separator matters: $(...) strips trailing newlines, so a bare
    # concatenation glues gofmt's last filename onto goimports' first.
    unformatted=$(printf '%s\n%s' "$gofmt_out" "$goimports_out" | sed '/^$/d' | sort -u)
    if [ -n "$unformatted" ]; then
      echo "Unformatted files:"
      printf '%s\n' "$unformatted"
      finish "format" 1
    else
      finish "format" 0
    fi
  fi
fi

# ── 2. vet ────────────────────────────────────────────────────────────────
begin "vet — go vet ./..."
go vet ./...
finish "vet" $?

# ── 3. lint ───────────────────────────────────────────────────────────────
# The base is the merge base with **origin/main**, not with local `main`.
# On `main`, local `main` is HEAD, so `merge-base HEAD main` is HEAD and
# everything you have already committed is invisible to `--new-from-rev`.
# `origin/main` is the last pushed commit, which is the "run this when a feature
# lands, before you push" scope this script documents, and it covers committed
# and uncommitted work alike.
#
# On a feature branch this resolves to the last pushed ancestor, which equals
# the branch point only while `main` is fully pushed. Otherwise it sits further
# back and the stage sees a superset of the branch's own diff — over-reporting,
# which is the safe direction. See the header.
lint_base="${CHECK_LINT_BASE:-}"
if [ -z "$lint_base" ]; then
  lint_base=$(git merge-base HEAD origin/main 2>/dev/null) ||
    lint_base=$(git merge-base HEAD main 2>/dev/null) ||
    lint_base=HEAD
fi
lint_base_short=$(git rev-parse --short "$lint_base" 2>/dev/null || echo "$lint_base")

# Positive control. `--new-from-rev` reports "0 issues." both when your changes
# are clean and when it was handed no changes at all, and those are different
# results. Count the Go files in scope first and say which case this is.
changed_go=$(
  {
    git diff --name-only --diff-filter=d "$lint_base" -- '*.go' 2>/dev/null
    git ls-files --others --exclude-standard -- '*.go'
  } | sort -u | sed '/^$/d' | wc -l | tr -d ' '
)

begin "lint — golangci-lint run --new-from-rev $lint_base_short"
if [ "$changed_go" -eq 0 ]; then
  echo "    0 changed Go files vs $lint_base_short — nothing to lint"
  finish_examined_nothing "lint"
else
  echo "    $changed_go changed Go file(s) vs $lint_base_short; only findings on"
  echo "    lines they touched — whole-repo lint is the nightly gate's job"
  golangci-lint run --new-from-rev "$lint_base"
  finish "lint" $?
fi

# ── 4. test ───────────────────────────────────────────────────────────────
begin "test — go test ./...  (Tier 1, testing-workflow.md §3)"
go test ./...
finish "test" $?

# ── summary ───────────────────────────────────────────────────────────────
echo
if [ ${#failed[@]} -eq 0 ]; then
  if [ ${#examined_nothing[@]} -eq 0 ]; then
    echo "check.sh: all stages passed (format, vet, lint, test)"
  else
    echo "check.sh: no stage failed — but these examined nothing: ${examined_nothing[*]}"
  fi
  exit 0
fi
echo "check.sh: FAILED stages: ${failed[*]}"
exit 1
