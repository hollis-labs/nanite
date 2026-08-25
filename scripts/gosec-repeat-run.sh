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
# Both reports are written to --output-dir under fixed names so the workflow's
# upload step can retain them whether the run passed or failed. A disagreement
# is only diagnosable with both halves of it.

set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd "$script_dir/.." && pwd)

package_list=""
output_dir=""
runner=""
baseline="$repo_root/.github/quality/full-repo-baseline.json"

usage() {
  cat >&2 <<'USAGE'
usage: scripts/gosec-repeat-run.sh --package-list FILE --output-dir DIR \
                                   --runner NAME [--baseline FILE]

Runs gosec twice over the ./-relative package directories in FILE, writing
DIR/gosec.json and DIR/gosec-repeat.json, then runs the quality ratchet's
gosec comparison over both.
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

run_gosec() {
  local label="$1"
  local out="$2"
  rm -f "$out"
  echo "gosec-repeat-run: $label run over ${#packages[@]} package(s) -> $out"
  gosec -no-fail -exclude-dir=.claude -fmt=json -out="$out" "${packages[@]}"
  # gosec exits 0 under -no-fail even when it wrote nothing useful, so the
  # report's existence has to be asserted rather than inferred from status.
  if [ ! -s "$out" ]; then
    echo "gosec-repeat-run: $label run exited 0 but wrote no report to $out" >&2
    exit 1
  fi
}

run_gosec first "$output_dir/gosec.json"
run_gosec second "$output_dir/gosec-repeat.json"

python3 "$repo_root/scripts/quality-ratchet.py" gosec \
  --baseline "$baseline" \
  --report "$output_dir/gosec.json" \
  --repeat-report "$output_dir/gosec-repeat.json" \
  --runner "$runner"
