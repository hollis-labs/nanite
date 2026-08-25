#!/usr/bin/env bash
#
# scripts/gosec-repeat-run.sh — run gosec twice over the same tree and hand
# both reports to the comparator, which refuses to compare either one against
# the baseline unless the two agree.
#
# WHY TWICE. gosec has produced a run over an unchanged tree that dropped a
# strict subset of its findings while exiting 0, writing well-formed JSON, and
# reporting unchanged Stats.files/Stats.lines. At the comparator that is
# indistinguishable from a real improvement, and the comparator then advises
# confirming the reduction reproduces before the baseline moves. That
# confirmation existed only as a human remembering to do it, and a discipline
# with no mechanism behind it decays. This is the mechanism.
# See docs/engineering/runbooks/full-repo-quality-gate.md and
# TASKS/gate-integrity/04-gosec-determinism-and-coverage-floor.md.
#
# The second run is cheap relative to what it protects: the single gosec step
# took 13s in run 32878651576, in a job whose race suite alone took 7m54s.
# Re-derive from `gh run view <id>` rather than trusting those figures.
#
# WHY -no-fail STAYS. The gate's baseline tolerates findings, so gosec must not
# exit non-zero merely for finding them. The cost is that cmd/gosec's
# computeExitCode also stops reporting `len(errors) > 0` — a package it could
# not analyze — through the exit status. quality-ratchet.py recovers exactly
# that signal by reading the report's "Golang errors" object, so do not treat
# a 0 from gosec here as evidence the scan was complete.
#
# WHY STDERR IS CAPTURED AND ASSERTED ON. There is a second, narrower silent
# failure that writes to no other channel. In gosec v2.28.0, buildSSA
# (analyzer.go:697) has unnamed returns and a deferred recover() that only
# logs, so a panic yields (nil, nil); checkAnalyzers (:572) guards
# `err != nil || ssaResult == nil`, logs, and returns nil, &Metrics{} — it has
# no error return and cannot propagate. It is called per package, so one
# package's SSA-rule findings vanish and every other package's survive.
# Metrics.Merge is additive, so merging that empty Metrics{} leaves Stats
# untouched; ParseErrors returns early on a package with no errors, so
# "Golang errors" stays empty; and -no-fail keeps the exit status at 0. The
# report is therefore shaped exactly like a complete one, and the only trace is
# one of two strings written by gosec.logger — log.New(os.Stderr, "[gosec]",
# ...) at :235.
#
# SCOPE, and it is narrower than it reads. The above is true of SSA
# *construction* failure specifically. It is NOT true of silent partial
# analysis in general: analyzers/slice_bounds.go:142 declares named returns
# and its deferred recover() sets BOTH to nil — `err = nil // Return nil
# error to allow other analyzers to continue` — so analyzer.go:645 sees no
# error, :650 sees a nil result, and NOTHING is written to any channel.
# G602's findings for that package vanish with no trace at all, and no
# stderr assertion can ever cover it. Only run-to-run disagreement sees
# that one. Do not read this assertion as covering every way gosec can
# quietly return less than it found. Each run's stderr is captured to its own file so the assertion
# below has something to read and the workflow can upload it.
#
# A HIT IS A HARD FAILURE, AND NOT BECAUSE A PANIC IS SERIOUS. A recovered
# panic in one package may well have cost zero findings; nobody can tell, and
# that nobody can tell is the defect. What the hit establishes is that the
# report is NOT COMPARABLE: gosec analyzed less than it was asked to, by an
# unknown amount, and produced a report the comparator reads as complete.
# compare_counts then reads per-rule counts missing an unknown subset, and the
# coverage floor is blind because files/lines are intact. Comparing an
# incomplete report to a baseline is the mechanism that produces a phantom
# drop. This is the same assertion as the non-empty-report check below, one
# layer deeper: -no-fail makes the exit status uninformative about failure, and
# well-formed JSON is equally uninformative about completeness.
#
# Both reports and both stderr captures are written to --output-dir under fixed
# names so the workflow's upload step can retain them whether the run passed or
# failed. A disagreement is only diagnosable with both halves of it, and an SSA
# failure is only diagnosable from the stream it was written to.

set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd "$script_dir/.." && pwd)

package_list=""
output_dir=""
runner=""
baseline="$repo_root/.github/quality/full-repo-baseline.json"

# The two strings gosec writes when a package's SSA analysis fails, and the only
# trace that failure leaves anywhere. Verified in v2.28.0 at
# "$(go env GOMODCACHE)/github.com/securego/gosec/v2@v2.28.0/analyzer.go":
# analyzer.go:701 formats "Panic when running SSA analyzer on package: %s. ..."
# and analyzer.go:580 builds "Error building the SSA representation of the
# package " + pkg.Name + ": ". Both carry the package name; the substrings kept
# here are the parts that do not.
ssa_panic_marker='Panic when running SSA analyzer'
ssa_build_marker='Error building the SSA representation'

# Set by scan_stderr_for_ssa_failure, read by its callers. A global rather than
# a printed value on purpose: a function whose output is captured with $(...)
# runs in a subshell, where its `exit 1` on an unreadable capture would kill the
# subshell only and return an empty match set — a failed scan would read as a
# clean run, which is the exact confusion this file exists to remove.
ssa_failure_lines=""

usage() {
  cat >&2 <<'USAGE'
usage: scripts/gosec-repeat-run.sh --package-list FILE --output-dir DIR \
                                   --runner NAME [--baseline FILE]

Runs gosec twice over the ./-relative package directories in FILE, writing
DIR/gosec.json and DIR/gosec-repeat.json plus DIR/gosec-stderr.txt and
DIR/gosec-repeat-stderr.txt, then runs the quality ratchet's gosec comparison
over both reports.
USAGE
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --package-list) package_list=${2:-}; shift 2 ;;
    --output-dir)   output_dir=${2:-}; shift 2 ;;
    --runner)       runner=${2:-}; shift 2 ;;
    --baseline)     baseline=${2:-}; shift 2 ;;
    -h|--help)      usage; exit 0 ;;
    *)
      echo "gosec-repeat-run: unrecognized argument: $1" >&2
      usage
      exit 2
      ;;
  esac
done

for required in package_list output_dir runner; do
  if [ -z "${!required}" ]; then
    echo "gosec-repeat-run: --${required//_/-} is required" >&2
    usage
    exit 2
  fi
done

if [ ! -f "$package_list" ]; then
  echo "gosec-repeat-run: package list $package_list does not exist" >&2
  exit 1
fi

# Checked rather than created: everything below writes four files here, and a
# bare redirection into a missing directory fails with a message about a path
# nobody chose to name.
if [ ! -d "$output_dir" ]; then
  echo "gosec-repeat-run: output directory $output_dir does not exist" >&2
  exit 1
fi

packages=()
while IFS= read -r package; do
  [ -n "$package" ] || continue
  packages+=("$package")
done < "$package_list"

# The `packages` ratchet subcommand already validates this list's shape and
# count. This is the narrower assertion that the array reached gosec at all:
# with an empty array the invocation below would scan gosec's own default and
# still write a plausible report.
if [ "${#packages[@]}" -eq 0 ]; then
  echo "gosec-repeat-run: package list $package_list yielded no packages" >&2
  exit 1
fi

# grep -F for both needles: neither contains a metacharacter, and -F makes that
# true by construction rather than by inspection. A shebanged script gets the
# system grep rather than an interactive shell's grep function, so this is BSD
# grep on a macOS runner and GNU grep elsewhere; -F reads identically on both.
scan_stderr_for_ssa_failure() {
  local file="$1"
  local status=0
  ssa_failure_lines=""
  if [ ! -r "$file" ]; then
    echo "gosec-repeat-run: stderr capture $file is missing or unreadable, so this run cannot be asserted clean" >&2
    exit 1
  fi
  ssa_failure_lines=$(grep -F -e "$ssa_panic_marker" -e "$ssa_build_marker" -- "$file") || status=$?
  # 0 matched, 1 matched nothing, anything else means grep could not do the
  # search — which is a failed assertion, never a clean run.
  if [ "$status" -gt 1 ]; then
    echo "gosec-repeat-run: grep exited $status scanning $file; the SSA-failure assertion did not run" >&2
    exit 1
  fi
}

# Positive control for the matcher, run on every invocation before gosec is.
# A grep that silently matches nothing is indistinguishable from a clean run —
# which is this task's own failure mode reappearing inside the assertion written
# to detect it.
#
# The two fixture lines are LITERAL copies of the shapes analyzer.go:701 and
# analyzer.go:580 produce, deliberately not built from the marker variables: a
# fixture interpolating the same variable the matcher greps for agrees with any
# typo in it, so a misspelled marker would match the fixture, pass the control,
# and then never match gosec. Written out separately, the markers are asserted
# against an independent statement of what gosec writes, and a typo fails here
# rather than in production. What this still cannot detect is gosec renaming the
# message upstream: re-derive both from the module cache when the pin moves.
verify_ssa_matcher() {
  local hit_fixture="$output_dir/gosec-matcher-control-hit.txt"
  local clean_fixture="$output_dir/gosec-matcher-control-clean.txt"
  local hit_count=0
  cat > "$hit_fixture" <<'FIXTURE'
[gosec]2026/01/01 00:00:00 Panic when running SSA analyzer on package: ssacontrol. Panic: control fixture
Stack trace:
[gosec]2026/01/01 00:00:00 Error building the SSA representation of the package ssacontrol: no ssa result
FIXTURE
  printf '[gosec]2026/01/01 00:00:00 Checking file: /matcher/control.go\n' > "$clean_fixture"

  scan_stderr_for_ssa_failure "$hit_fixture"
  hit_count=$(printf '%s\n' "$ssa_failure_lines" | grep -c . || true)
  if [ "$hit_count" -ne 2 ]; then
    echo "gosec-repeat-run: SSA-failure matcher self-check failed: expected both markers to match the fixture copies of gosec's own messages, got $hit_count of 2" >&2
    echo "gosec-repeat-run: the assertion on gosec's stderr therefore proves nothing, so this run is void" >&2
    exit 1
  fi

  scan_stderr_for_ssa_failure "$clean_fixture"
  if [ -n "$ssa_failure_lines" ]; then
    echo "gosec-repeat-run: SSA-failure matcher self-check failed: matched a fixture carrying neither marker" >&2
    exit 1
  fi

  rm -f "$hit_fixture" "$clean_fixture"
}

# Names the packages gosec failed on. Both markers carry one: "...on package:
# NAME. Panic: ..." and "...of the package NAME: ...". Degrades to empty rather
# than guessing if either format changes upstream — the matched lines are
# printed in full above this, so the name is never lost either way.
ssa_failed_packages() {
  printf '%s\n' "$ssa_failure_lines" \
    | sed -n -e 's/.*on package: \([^.]*\)\..*/\1/p' -e 's/.*of the package \([^:]*\):.*/\1/p' \
    | sort -u \
    | tr '\n' ' ' \
    | sed 's/[[:space:]]*$//'
}

assert_no_ssa_failure() {
  local label="$1"
  local captured_stderr="$2"
  local failed_packages=""
  scan_stderr_for_ssa_failure "$captured_stderr"
  if [ -z "$ssa_failure_lines" ]; then
    return 0
  fi
  failed_packages=$(ssa_failed_packages)
  echo "gosec-repeat-run: the $label run reported an SSA analysis failure on stderr ($captured_stderr):" >&2
  printf '%s\n' "$ssa_failure_lines" | sed 's/^/    /' >&2
  if [ -n "$failed_packages" ]; then
    echo "gosec-repeat-run: the $label run's failing package(s): $failed_packages" >&2
  fi
  return 1
}

run_gosec() {
  local label="$1"
  local out="$2"
  local captured_stderr="$3"
  local status=0
  rm -f "$out" "$captured_stderr"
  echo "gosec-repeat-run: $label run over ${#packages[@]} package(s) -> $out (stderr -> $captured_stderr)"
  # Operand order is load-bearing. `2>"$file"` sends stderr to the file and
  # leaves stdout alone, which is what is wanted. `2>&1 >"$file"` is the
  # opposite — stderr to the OLD stdout, stdout to the file — and would fill the
  # capture with the wrong stream, giving an assertion that can never fire and a
  # positive control that passes on the stream nobody is asserting about.
  gosec -no-fail -exclude-dir=.claude -fmt=json -out="$out" "${packages[@]}" \
    2>"$captured_stderr" || status=$?
  # Mirrored back to the job log so that capturing the stream takes nothing away
  # from someone reading the run; the file exists so the assertion below and the
  # artifact upload have something to read.
  cat "$captured_stderr" >&2
  if [ "$status" -ne 0 ]; then
    echo "gosec-repeat-run: $label run exited $status; its stderr is above and in $captured_stderr" >&2
    exit 1
  fi
  # gosec exits 0 under -no-fail even when it wrote nothing useful, so the
  # report's existence has to be asserted rather than inferred from status.
  if [ ! -s "$out" ]; then
    echo "gosec-repeat-run: $label run exited 0 but wrote no report to $out" >&2
    exit 1
  fi
}

first_stderr="$output_dir/gosec-stderr.txt"
second_stderr="$output_dir/gosec-repeat-stderr.txt"

verify_ssa_matcher

run_gosec first "$output_dir/gosec.json" "$first_stderr"
run_gosec second "$output_dir/gosec-repeat.json" "$second_stderr"

# Asserted per file, after both runs have happened. Failing straight after the
# first would throw away the distinction a reader needs: a hit on one run only
# is the NONDETERMINISTIC case, which the agreement check below also catches; a
# hit on both is reproducible and needs a fix in that package before any gosec
# number from this tree means anything.
ssa_hits=0
assert_no_ssa_failure first "$first_stderr" || ssa_hits=$((ssa_hits + 1))
assert_no_ssa_failure second "$second_stderr" || ssa_hits=$((ssa_hits + 1))
if [ "$ssa_hits" -ne 0 ]; then
  cat >&2 <<'VOID'
gosec-repeat-run: this run is VOID — no report from it may be compared to the
  baseline. The failure is not that gosec panicked: a recovered panic in one
  package may have cost zero findings, and nobody can tell. That nobody can tell
  is the defect. What the line(s) above establish is that gosec analyzed less
  than it was asked to, by an unknown amount, and wrote a report shaped exactly
  like a complete one — full Stats.files/Stats.lines, empty "Golang errors",
  exit 0 under -no-fail, only that package's SSA-rule findings missing. The
  comparator would then read per-rule counts missing an unknown subset, and the
  coverage floor is blind because coverage is intact. Comparing an incomplete
  report to a baseline is the mechanism that produces a phantom drop.
gosec-repeat-run: what to do — re-run the gate. Both stderr captures are
  uploaded with the reports, so the run that hit and the package it hit on stay
  recoverable afterwards. A hit on one run only is the nondeterministic case; a
  hit on both is reproducible, and if it recurs on the same package then that
  package's SSA analysis is failing and must be fixed (or excluded deliberately,
  with the committed baseline re-measured) before any gosec number from this
  tree is compared again. Do not lower the baseline off a run that hit this.
VOID
  exit 1
fi

python3 "$repo_root/scripts/quality-ratchet.py" gosec \
  --baseline "$baseline" \
  --report "$output_dir/gosec.json" \
  --repeat-report "$output_dir/gosec-repeat.json" \
  --runner "$runner"
