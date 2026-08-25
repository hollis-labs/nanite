#!/usr/bin/env python3
"""Compare full-repo quality reports with the committed Wave 7 baseline."""

from __future__ import annotations

import argparse
from collections import Counter
import json
from pathlib import Path
import subprocess
import sys
from typing import Any


# The Stage 1 baseline is a *ceiling*: findings must not exceed it.  This is the
# matching *floor*, and it exists because a scan that covered a fraction of the
# repository is indistinguishable, at the comparator, from one that genuinely
# found little: well-formed JSON, a small Issues array, and -- when the package
# list shrank to a valid-but-partial set -- exit 0.  Without a floor the ratchet
# celebrates that as a multi-thousand-finding improvement.  (A list that shrank
# to *nothing* reachable is a different shape: v2.11.4 sets Report.Error and
# exits non-zero, which golangci_scan_error catches independently.)
#
# It is deliberately loose.  A floor only has to separate "the scan did not
# cover the repository" (0%, or a handful of surviving packages) from "the
# repository genuinely improved"; it cannot separate a small silent coverage
# loss from ordinary churn, and pretending otherwise would red-light the gate
# on honest remediation until someone weakened the check to shut it up.  The
# floor tightens for free every time the baseline is ratcheted downward: it was
# 1628 against the 3255 baseline this note was first written for, and is 1407
# against the 2813 baseline refreshed at 4f3d38c4 (CW-20260824-0025), where a
# real full-repo run measures 2813 -- exactly 1406 points of margin.
LINT_COVERAGE_FLOOR_PERCENT = 50

# gosec's own coverage floor, and a different thing from the one above: that
# one is keyed on the *lint* side's total finding count, this one is keyed on
# how much of the repository gosec says it parsed.  Findings are the wrong key
# here -- remediation moves them constantly, and Stats.found in particular
# equals len(Issues) and so tracks every fix -- while Stats.files and
# Stats.lines barely move.
#
# 75% is chosen against measured churn.  The largest legitimate peak-to-trough
# decline in this repository's non-test Go source over its whole history
# (first commit 2026-03-07 through eb182d71) is 7.53%: 651 -> 602 files and
# 151,555 -> 140,150 lines, both inside one week in August 2026 when several
# subsystems were cut.  Derive it again rather than trusting those figures --
# accumulate `git log --numstat -- '*.go'` excluding `_test.go` and take the
# largest drawdown.  A 25% margin therefore clears the worst removal wave this
# repository has ever had by more than three times, while still failing a run
# that parsed a quarter of the tree less than the committed measurement.  An
# exact-equality floor would red-light on the first file deleted; a floor loose
# enough to survive anything protects nothing; and a floor that red-lights on
# honest work gets weakened by whoever is trying to land that work.  Like the
# lint floor, it tightens for free whenever the committed measurement is
# refreshed upward.
#
# What this floor uniquely catches is a package that vanished *silently*.
# gosec v2.28.0's Analyzer.load logs "Skipping: <path>. Path doesn't exist."
# and returns an empty package slice with a nil error, so an unresolvable path
# contributes no issues, no error entry and no Stats at all.  A package whose
# analysis *failed* is a different shape and is caught precisely by
# gosec_scan_errors below.
GOSEC_COVERAGE_FLOOR_PERCENT = 75

# What an agreeing pair of gosec runs establishes, printed on every agreement.
#
# It is printed because the parts of this tool compose into something stronger
# than any of them says.  Read top to bottom, an operator sees "two runs agree"
# and then, four lines later, compare_counts asking them to "confirm the
# reduction reproduces before lowering the committed baseline" -- and the
# confirmation appears to have already been granted.  It has not: those are two
# different questions.  Agreement answers "did two adjacent runs of one tree
# produce the same report?".  The advisory is asking whether the reduction was
# earned, which only the diff since the committed measurement can answer.
GOSEC_AGREEMENT_BOUNDARY = (
    "standalone gosec repeat-run agreement: this rules out gosec run-to-run "
    "nondeterminism for this report, and nothing else. It is NOT confirmation "
    "that a reduction below was earned -- two runs of one tree reproduce an "
    "unearned drop exactly as well as an earned one. Bank a reduction only "
    "when it maps to a real code change since the committed measurement."
)


def load_json(path: Path) -> dict[str, Any]:
    try:
        with path.open(encoding="utf-8") as handle:
            value = json.load(handle)
    except (OSError, json.JSONDecodeError) as error:
        raise SystemExit(f"cannot read JSON report {path}: {error}") from error
    if not isinstance(value, dict):
        raise SystemExit(f"expected a JSON object in {path}")
    return value


def integer_counts(value: Any, label: str) -> dict[str, int]:
    if not isinstance(value, dict):
        raise SystemExit(f"{label} must be a JSON object")
    counts: dict[str, int] = {}
    for name, count in value.items():
        if not isinstance(name, str) or not isinstance(count, int) or count < 0:
            raise SystemExit(f"{label} must map names to non-negative integers")
        counts[name] = count
    return counts


def verify_platform(baseline_document: dict[str, Any], runner: str) -> None:
    platform = baseline_document.get("platform")
    if not isinstance(platform, dict):
        raise SystemExit("baseline has no platform object")

    expected_goos = platform.get("goos")
    expected_goarch = platform.get("goarch")
    expected_runner = platform.get("github_runner")
    if not all(
        isinstance(value, str) and value
        for value in (expected_goos, expected_goarch, expected_runner)
    ):
        raise SystemExit(
            "baseline platform.goos, platform.goarch, and "
            "platform.github_runner must be non-empty strings"
        )

    try:
        completed = subprocess.run(
            ["go", "env", "GOOS", "GOARCH"],
            check=True,
            capture_output=True,
            text=True,
        )
    except (OSError, subprocess.CalledProcessError) as error:
        raise SystemExit(f"cannot determine platform with go env: {error}") from error

    actual = completed.stdout.splitlines()
    if len(actual) != 2 or not all(actual):
        raise SystemExit(f"unexpected go env GOOS/GOARCH output: {completed.stdout!r}")
    actual_goos, actual_goarch = actual

    mismatches: list[str] = []
    if runner != expected_runner:
        mismatches.append(
            f"intended runner {runner!r} does not match baseline {expected_runner!r}"
        )
    if (actual_goos, actual_goarch) != (expected_goos, expected_goarch):
        mismatches.append(
            "go env GOOS/GOARCH "
            f"{actual_goos}/{actual_goarch} does not match baseline "
            f"{expected_goos}/{expected_goarch}"
        )
    if mismatches:
        raise SystemExit("platform mismatch: " + "; ".join(mismatches))

    print(
        "platform: runner="
        f"{runner} goos={actual_goos} goarch={actual_goarch} matches baseline"
    )


def load_verified_baseline(baseline_path: Path, runner: str) -> dict[str, Any]:
    baseline_document = load_json(baseline_path)
    verify_platform(baseline_document, runner)
    return baseline_document


def compare_counts(label: str, baseline: dict[str, int], actual: Counter[str]) -> int:
    unexpected = sorted(set(actual) - set(baseline))
    increases = [
        (name, baseline[name], actual.get(name, 0))
        for name in sorted(baseline)
        if actual.get(name, 0) > baseline[name]
    ]

    print(f"{label}: baseline={sum(baseline.values())} current={sum(actual.values())}")
    for name in sorted(baseline):
        current = actual.get(name, 0)
        marker = " INCREASE" if current > baseline[name] else ""
        print(f"  {name}: {current} (baseline {baseline[name]}){marker}")

    if unexpected:
        print(f"{label}: findings from unbaselined names: {', '.join(unexpected)}", file=sys.stderr)
    for name, expected, current in increases:
        print(
            f"{label}: {name} increased from {expected} to {current}",
            file=sys.stderr,
        )
    if unexpected or increases:
        return 1

    reductions = [
        name for name in sorted(baseline) if actual.get(name, 0) < baseline[name]
    ]
    if reductions:
        # A reduction is the improvement direction, and for most linters it is
        # exactly what remediation looks like: fix the misspellings, the count
        # falls, the baseline follows.  The one step *known* to also produce a
        # decrease nobody earned is standalone gosec, which has emitted a run
        # dropping a subset of its findings with identical Stats.files and
        # Stats.lines and a clean exit.  (Not the whole Stats block: it also
        # carries `found`, which equals len(Issues) and therefore moved with
        # the drop.  That is exactly why 04b step 3 keys its floor on
        # files/lines and not on counts.)  See
        # `docs/engineering/runbooks/full-repo-quality-gate.md`, "What this
        # gate guarantees", for the size of that drop and how often it was
        # seen -- deliberately not restated here.
        #
        # A repeat run is what tells the two cases apart, so the advisory asks
        # for one instead of singling out a linter: it costs a real improvement
        # one extra run, and it stops a transient one from being written into
        # the baseline permanently.
        #
        # The gosec caller now repeats automatically -- see
        # verify_repeat_run_agreement below, TASKS/gate-integrity/04 step 2
        # (04b).  That answers a NARROWER question than this advisory asks, and
        # the two must not be read as the same thing: two adjacent runs of one
        # tree rule out run-to-run nondeterminism, and an unearned drop
        # reproduces just as well as an earned one.  GOSEC_AGREEMENT_BOUNDARY
        # and gosec_command's docstring state that boundary.  The lint caller
        # has no automated repeat at all, which is why this text asks for one.
        print(
            f"{label}: reductions detected for {', '.join(reductions)}; re-run "
            "the same tree and confirm the reduction reproduces before lowering "
            "the committed baseline. A reduction that reproduces is a real "
            "improvement -- bank it; one that does not reproduce is a "
            "dropped-findings run, and baking it into the baseline deletes real "
            "findings."
        )
    print(f"{label}: ratchet passed")
    return 0


def golangci_scan_error(report: dict[str, Any]) -> str:
    """Return golangci-lint's self-reported scan error, or "" if it ran clean.

    golangci-lint sets Report.Error when it could not analyze something it was
    asked to analyze -- a mistyped path, an import path where a directory was
    expected, a package that would not load.  It still writes a well-formed
    report carrying every *other* package's findings, and simply omits that
    target's.  Measured against the pinned v2.11.4 on 2026-08-24, from the repo
    root, both with --issues-exit-code=0:

        ./internal/brand ./internal/task
            exit 0   Report={"Linters": [...]}, no Error key   Issues: 3
        ./internal/brand ./internal/task ./internal/does-not-exist
            exit 7   Report.Error="typechecking error: ... directory not found"
                     Issues: 3   <-- the two real packages still reported
        <module path>/internal/brand <module path>/internal/task
            exit 7   Report.Error set                          Issues: 0

    Note the exit code: v2.11.4 does NOT return 0 for these, so the workflow's
    own `shell: bash` (which GitHub runs as `bash -e`) already aborts the step.
    This check is not that backstop.  It defends the *comparator*, which is
    handed a report path and cannot see how the report was produced -- a stale
    file, a hand-assembled one, or a caller that dropped the exit code, such as
    the local reproduction procedure in
    docs/engineering/runbooks/full-repo-quality-gate.md, which does not check
    it.  A report carrying a scan error proves nothing about the counts inside
    it, whatever the producer's exit code was.
    """
    section = report.get("Report")
    if not isinstance(section, dict):
        return ""
    error = section.get("Error")
    if error is None:
        return ""
    if isinstance(error, str):
        return error.strip()
    return json.dumps(error)


def verify_lint_coverage(
    baseline: dict[str, int], actual: Counter[str], report_path: Path
) -> None:
    """Fail when the report holds too few findings to be a real full-repo scan."""
    baseline_total = sum(baseline.values())
    actual_total = sum(actual.values())
    if baseline_total <= 0:
        # An all-zero Stage 1 baseline asserts there is nothing to find, so
        # there is no floor to enforce.  This is not a way to neuter the gate:
        # with an all-zero baseline compare_counts flags *every* finding as an
        # increase, so the only report that passes is an empty one -- which is
        # the correct verdict for "we expect nothing and found nothing".
        return

    floor = (baseline_total * LINT_COVERAGE_FLOOR_PERCENT + 99) // 100
    print(
        f"audit-config linters coverage floor: current={actual_total} "
        f"floor={floor} (baseline {baseline_total}, "
        f"{LINT_COVERAGE_FLOOR_PERCENT}%)"
    )
    if actual_total >= floor:
        return
    raise SystemExit(
        f"golangci-lint report {report_path} holds {actual_total} finding(s) "
        f"against a Stage 1 baseline of {baseline_total}, under the "
        f"{LINT_COVERAGE_FLOOR_PERCENT}% coverage floor of {floor}. A drop this "
        "large means the scan did not cover the repository, not that the "
        "repository improved: check the discovered package list and the "
        "golangci-lint invocation. Do not lower the baseline to clear this."
    )


def lint_command(
    baseline_path: Path,
    report_path: Path,
    linters_report_path: Path,
    runner: str,
) -> int:
    baseline_document = load_verified_baseline(baseline_path, runner)
    stage_1 = baseline_document.get("stage_1")
    stage_2 = baseline_document.get("stage_2")
    if not isinstance(stage_1, dict) or stage_1.get("active") is not True:
        raise SystemExit("baseline must keep Stage 1 active")
    if not isinstance(stage_2, dict) or stage_2.get("active") is not True:
        raise SystemExit("Stage 2 must be active after 14/02")
    if stage_2.get("linters") != ["errcheck", "errorlint", "nilerr"]:
        raise SystemExit("Stage 2 must name exactly errcheck, errorlint, and nilerr")

    baseline = integer_counts(stage_1.get("counts"), "stage_1.counts")
    stage_2_linters = stage_2["linters"]
    nonzero_stage_2_baselines = {
        name: baseline.get(name)
        for name in stage_2_linters
        if baseline.get(name) != 0
    }
    if nonzero_stage_2_baselines:
        raise SystemExit(
            "Stage 2 linters must have zero Stage 1 baselines: "
            f"{nonzero_stage_2_baselines}"
        )
    linters_report = load_json(linters_report_path)
    enabled_entries = linters_report.get("Enabled")
    if not isinstance(enabled_entries, list):
        raise SystemExit(
            f"golangci-lint linters report {linters_report_path} has no Enabled array"
        )
    enabled: set[str] = set()
    for entry in enabled_entries:
        if not isinstance(entry, dict) or not isinstance(entry.get("name"), str):
            raise SystemExit(
                f"golangci-lint linters report {linters_report_path} has a malformed entry"
            )
        enabled.add(entry["name"])
    if enabled != set(baseline):
        missing = sorted(enabled - set(baseline))
        stale = sorted(set(baseline) - enabled)
        raise SystemExit(
            "Stage 1 baseline does not match the audit config's enabled linters: "
            f"missing={missing}, stale={stale}"
        )

    report = load_json(report_path)
    scan_error = golangci_scan_error(report)
    if scan_error:
        raise SystemExit(
            f"golangci-lint report {report_path} carries a scan error, so the run "
            "did not cover every requested package and its counts prove nothing: "
            + scan_error
        )
    issues = report.get("Issues")
    if not isinstance(issues, list):
        raise SystemExit(f"golangci-lint report {report_path} has no Issues array")

    actual: Counter[str] = Counter()
    for issue in issues:
        if not isinstance(issue, dict) or not isinstance(issue.get("FromLinter"), str):
            raise SystemExit(f"golangci-lint report {report_path} has a malformed issue")
        actual[issue["FromLinter"]] += 1
    verify_lint_coverage(baseline, actual, report_path)
    stage_1_failed = compare_counts("audit-config linters", baseline, actual)
    stage_2_findings = {
        name: actual.get(name, 0)
        for name in stage_2_linters
        if actual.get(name, 0) != 0
    }
    print(
        "correctness Stage 2: "
        + ", ".join(f"{name}={actual.get(name, 0)}" for name in stage_2_linters)
    )
    if stage_2_findings:
        print(
            f"correctness Stage 2 requires zero findings: {stage_2_findings}",
            file=sys.stderr,
        )
        return 1
    print("correctness Stage 2: zero-tolerance passed")
    return stage_1_failed


def relative_report_path(raw_path: Any) -> str:
    if not isinstance(raw_path, str):
        return ""
    normalized = raw_path.replace("\\", "/")
    marker = "/internal/"
    if marker in normalized:
        return "internal/" + normalized.rsplit(marker, 1)[1]
    return normalized.removeprefix("./")


def is_known_gosec_noise(issue: dict[str, Any], policy: dict[str, Any]) -> bool:
    return (
        relative_report_path(issue.get("file")) == policy.get("file")
        and issue.get("rule_id") == policy.get("rule_id")
        and isinstance(issue.get("code"), str)
        and isinstance(policy.get("symbol"), str)
        and policy["symbol"] in issue["code"]
    )


def known_noise_label(policy: dict[str, Any]) -> str:
    finding_id = policy.get("finding_id")
    if isinstance(finding_id, str) and finding_id:
        return finding_id
    return f"{policy['file']}:{policy['symbol']}"


def load_known_noise(gosec_baseline: dict[str, Any]) -> list[dict[str, Any]]:
    """Validate and return the enumerated known-noise entries.

    Bare gosec does not understand golangci-lint's //nolint:gosec syntax, so
    every annotated-and-justified suppression in the tree reappears here as a
    finding.  The baseline enumerates them one entry per site rather than
    absorbing them into actionable_rule_counts: a count of 4 would be satisfied
    by any four sites, so deleting a justified one and adding an unjustified one
    would net to zero and pass.  Each entry must match exactly once -- zero
    means the exception went stale, two or more means it broadened -- and the
    corresponding actionable count then sits at 0, so a genuinely new site is an
    increase against zero.

    `reason` is required, not decorative.  It is the only thing standing between
    "this suppression was argued for" and "someone silenced a finding".
    """
    policies = gosec_baseline.get("known_noise")
    if not isinstance(policies, list) or not policies:
        raise SystemExit(
            "baseline standalone_gosec.known_noise must be a non-empty list of "
            "entries; the single-object form predates CW-20260824-0025 and is "
            "no longer accepted"
        )
    seen: set[tuple[str, str, str]] = set()
    for policy in policies:
        if not isinstance(policy, dict):
            raise SystemExit(
                "standalone_gosec.known_noise entries must be JSON objects"
            )
        for field in ("file", "rule_id", "symbol", "reason"):
            value = policy.get(field)
            if not isinstance(value, str) or not value:
                raise SystemExit(
                    "standalone_gosec.known_noise entry needs a non-empty "
                    f"string {field!r}: {policy}"
                )
        line = policy.get("annotation_line")
        if not isinstance(line, int) or isinstance(line, bool) or line <= 0:
            raise SystemExit(
                "standalone_gosec.known_noise entry needs a positive integer "
                f"'annotation_line': {policy}"
            )
        finding_id = policy.get("finding_id")
        if finding_id is not None and (
            not isinstance(finding_id, str) or not finding_id
        ):
            raise SystemExit(
                "standalone_gosec.known_noise 'finding_id' must be a non-empty "
                f"string when present: {policy}"
            )
        key = (policy["file"], policy["rule_id"], policy["symbol"])
        if key in seen:
            raise SystemExit(
                "standalone_gosec.known_noise repeats the path/rule/symbol "
                f"triple {key}; a duplicate entry absorbs a second real finding"
            )
        seen.add(key)
    return policies


def gosec_scan_errors(report: dict[str, Any], report_path: Path) -> list[str]:
    """Return gosec's own per-file analysis errors, or [] if it ran clean.

    This is the direct analogue of golangci_scan_error, and unlike that one it
    is not a backstop -- it is the only place the signal survives.  gosec
    v2.28.0 records an entry under "Golang errors" (the key really does carry a
    space) for every package it could not load and every file it could not
    parse: Analyzer.Process calls AppendError on any worker result carrying an
    error and merges ParseErrors output, then still writes a well-formed report
    holding every *other* package's findings.  cmd/gosec's computeExitCode
    would exit non-zero for `len(errors) > 0` -- except that the gate passes
    `-no-fail`, which suppresses exactly that, so the workflow's `bash -e`
    cannot see it.  Without this check a run that failed to analyze part of the
    repository reaches the comparator as a clean reduction.

    The key is required to be present, not merely truthy.  gosec always emits
    it: ReportInfo.Errors carries no `omitempty` and NewAnalyzer initializes the
    map, so an absent key means this is not a v2.28.0 gosec report and the rest
    of the checks are reading a shape they were not calibrated on.  A JSON
    `null` is accepted, because a nil Go map and an empty one mean the same
    thing.
    """
    if "Golang errors" not in report:
        raise SystemExit(
            f"gosec report {report_path} has no 'Golang errors' key. gosec "
            "v2.28.0 always emits one, so its absence means this report was "
            "not produced by the pinned gosec and its counts cannot be "
            "compared against a baseline calibrated on that tool."
        )
    errors = report["Golang errors"]
    if errors is None:
        return []
    if not isinstance(errors, dict):
        raise SystemExit(
            f"gosec report {report_path} has a malformed 'Golang errors' "
            f"value: {json.dumps(errors)[:200]}"
        )

    described: list[str] = []
    for name in sorted(errors, key=str):
        entries = errors[name]
        if not isinstance(entries, list):
            described.append(f"{name}: {json.dumps(entries)}")
            continue
        for entry in entries:
            if isinstance(entry, dict):
                described.append(
                    f"{name}:{entry.get('line')}:{entry.get('column')}: "
                    f"{entry.get('error')}"
                )
            else:
                described.append(f"{name}: {json.dumps(entry)}")
    return described


def verify_gosec_coverage(
    gosec_baseline: dict[str, Any], report: dict[str, Any], report_path: Path
) -> None:
    """Fail when gosec parsed materially less of the repository than committed.

    Keyed on Stats.files and Stats.lines against a committed measurement, for
    the reasons in GOSEC_COVERAGE_FLOOR_PERCENT's note.  A missing or
    non-numeric Stats block is a hard failure and never a default of zero: a
    report that carries no evidence of what it covered is unusable, not a pass
    with unknown coverage.
    """
    committed = gosec_baseline.get("coverage")
    if not isinstance(committed, dict):
        raise SystemExit(
            "baseline standalone_gosec.coverage must be an object carrying the "
            "committed files/lines measurement this floor is derived from"
        )
    source = committed.get("source")
    if not isinstance(source, str) or not source:
        raise SystemExit(
            "baseline standalone_gosec.coverage needs a non-empty string "
            "'source' naming the run its files/lines came from; an unstamped "
            "number cannot be re-derived by whoever next has to move it"
        )

    stats = report.get("Stats")
    if not isinstance(stats, dict):
        raise SystemExit(
            f"gosec report {report_path} has no Stats object, so it carries no "
            "evidence of how much of the repository it parsed. That is an "
            "unusable report, not a pass with unknown coverage."
        )

    for key in ("files", "lines"):
        expected = committed.get(key)
        if not isinstance(expected, int) or isinstance(expected, bool) or expected <= 0:
            raise SystemExit(
                f"baseline standalone_gosec.coverage.{key} must be a positive "
                f"integer: {expected!r}"
            )
        measured = stats.get(key)
        if not isinstance(measured, int) or isinstance(measured, bool) or measured < 0:
            raise SystemExit(
                f"gosec report {report_path} has a missing or non-numeric "
                f"Stats.{key}: {measured!r}. Reading that as zero would let a "
                "report with no coverage evidence at all clear the floor."
            )
        floor = (expected * GOSEC_COVERAGE_FLOOR_PERCENT + 99) // 100
        print(
            f"standalone gosec coverage floor: {key}={measured} floor={floor} "
            f"(baseline {expected}, {GOSEC_COVERAGE_FLOOR_PERCENT}%)"
        )
        if measured < floor:
            raise SystemExit(
                f"gosec report {report_path} parsed {measured} {key} against a "
                f"committed measurement of {expected}, under the "
                f"{GOSEC_COVERAGE_FLOOR_PERCENT}% coverage floor of {floor}. A "
                "drop this large means gosec did not parse the repository, not "
                "that the repository shrank: check the discovered package list "
                f"({source} is what the committed measurement came from). Do "
                "not lower the committed measurement to clear this."
            )


def gosec_agreement_fingerprint(report: dict[str, Any]) -> dict[str, Any]:
    """Reduce a gosec report to the content two runs of one tree must share.

    Order-insensitive by construction, and that is not caution -- gosec's issue
    order is genuinely not guaranteed.  cmd/gosec sorts with slices.SortFunc,
    an *unstable* sort, over (severity, details, file, line), which is not a
    total order: two findings differing only in column may be emitted either
    way round.  A byte-level diff of two honest reports would therefore invent
    disagreements, and a check that red-lights on honest input gets switched
    off.  Comparing the *multiset* of fully serialized issues instead catches a
    dropped finding, a swapped finding and any changed field, while staying
    silent on ordering.
    """
    issues = report.get("Issues")
    if isinstance(issues, list):
        serialized: Any = sorted(json.dumps(issue, sort_keys=True) for issue in issues)
    else:
        serialized = json.dumps(issues, sort_keys=True)
    return {
        "GosecVersion": report.get("GosecVersion"),
        "Stats": report.get("Stats"),
        # Known flattening, left as-is deliberately: .get() returns None for an
        # absent key and for a JSON null alike, which gosec_scan_errors is
        # careful to keep apart. Two reports differing only in that pair would
        # agree here, and only the first is passed to gosec_scan_errors. Not
        # reachable through gosec-repeat-run.sh, which runs one binary twice
        # over one tree; recorded so it is not rediscovered as a surprise.
        "Golang errors": report.get("Golang errors"),
        "Issues": serialized,
    }


def verify_distinct_reports(report_path: Path, repeat_report_path: Path) -> None:
    """Refuse to compare a report against itself.

    Two paths naming one file always agree, so the agreement check would report
    success without having checked anything -- the exact vacuous-pass shape this
    gate keeps finding in itself.
    """
    try:
        same = report_path.samefile(repeat_report_path)
    except OSError:
        # One of them does not exist or cannot be read. load_json reports that
        # with its own reason; do not pre-empt it with a worse message.
        return
    if same:
        raise SystemExit(
            f"--report and --repeat-report both name {report_path}. A report "
            "compared against itself always agrees and checks nothing: point "
            "them at two independent gosec runs over the same tree."
        )


def verify_repeat_run_agreement(
    report_path: Path,
    repeat_report_path: Path,
    report: dict[str, Any],
    repeat_report: dict[str, Any],
) -> None:
    """Fail unless two runs of the same tree produced the same gosec report.

    gosec has emitted a run over an unchanged tree that dropped a strict subset
    of its findings while exiting 0 with well-formed JSON and unchanged
    Stats.files/Stats.lines -- indistinguishable, at the comparator, from a real
    improvement, and the comparator's own advisory then asks for a repeat run
    before the baseline moves.  That repeat existed only as a human remembering
    to do it.  This is that repeat, as a mechanism.

    Everything after this point in gosec_command reads only the first report.
    That is sound precisely because agreement covers Stats, "Golang errors" and
    the full multiset of Issues -- every field the checks below touch.
    """
    first = gosec_agreement_fingerprint(report)
    second = gosec_agreement_fingerprint(repeat_report)

    differences: list[str] = []
    for key in ("GosecVersion", "Stats", "Golang errors"):
        if first[key] != second[key]:
            differences.append(
                f"{key}: {json.dumps(first[key], sort_keys=True)} != "
                f"{json.dumps(second[key], sort_keys=True)}"
            )

    left = first["Issues"]
    right = second["Issues"]
    if left != right:
        if isinstance(left, list) and isinstance(right, list):
            only_first = Counter(left) - Counter(right)
            only_second = Counter(right) - Counter(left)
            differences.append(
                f"Issues: {sum(only_first.values())} finding(s) only in "
                f"{report_path}, {sum(only_second.values())} only in "
                f"{repeat_report_path}"
            )
            for sample in sorted(only_first.elements())[:3]:
                differences.append(f"  only in {report_path}: {sample}")
            for sample in sorted(only_second.elements())[:3]:
                differences.append(f"  only in {repeat_report_path}: {sample}")
        else:
            differences.append(
                f"Issues: {json.dumps(left)[:200]} != {json.dumps(right)[:200]}"
            )

    if not differences:
        if isinstance(left, list):
            print(
                "standalone gosec repeat-run agreement: two runs of this tree "
                f"agree on {len(left)} finding(s), Stats and Golang errors"
            )
        else:
            # Two reports can agree and still be unusable. Say so rather than
            # printing a finding count for something that is not a list; the
            # Issues check below is what rejects it.
            print(
                "standalone gosec repeat-run agreement: two runs of this tree "
                "agree, and neither carries a usable Issues array"
            )
        # Unconditional, and deliberately so. The claim this bounds is made
        # every time an agreement is printed, so the bound has to be too.
        print(GOSEC_AGREEMENT_BOUNDARY)
        return

    raise SystemExit(
        "standalone gosec repeat-run disagreement: two runs over the same tree "
        "did not produce the same report, so neither one's numbers mean "
        "anything against the baseline. Do not re-run until one passes -- a "
        "disagreement is the defect, not the weather. Both reports are kept as "
        f"artifacts: {report_path} and {repeat_report_path}.\n"
        + "\n".join(differences)
    )


def gosec_command(
    baseline_path: Path,
    report_path: Path,
    repeat_report_path: Path,
    runner: str,
) -> int:
    """Compare a gosec run against the baseline -- and bound what that proves.

    THE BOUNDARY. Three checks compose in this function, in this order:
    repeat-run agreement, the files/lines coverage floor, then compare_counts'
    per-rule comparison. Stating what the assembly CANNOT catch is not
    pessimism; the parts read as stronger together than any of them is, and a
    rule with no written boundary gets applied at every boundary.

    1. Agreement rules out gosec run-to-run NONDETERMINISM, and only that. Two
       runs of one tree, back to back in one invocation, reproduce an unearned
       drop exactly as well as an earned one. An agreement is never
       confirmation that a reduction was earned. GOSEC_AGREEMENT_BOUNDARY says
       so in the output, because that is where the misreading happens.

    2. The coverage floor DOES NOT CATCH a findings drop at constant coverage.
       It is keyed on Stats.files and Stats.lines, and the run this task was
       opened for had *identical* files and lines: the files were parsed, and
       the findings went missing downstream of parsing. The floor catches a
       scan that covered materially less of the repository. It is deaf to a
       constant-coverage drop by construction, and no threshold on it helps.

    3. compare_counts' "a reduction that reproduces is a real improvement" is a
       NECESSARY condition, not a sufficient one. That wording is 04a's,
       approved in Wave A by the director layer, and this note corrects it
       rather than the string -- which is shared with the lint caller and must
       not be made to single out a linter. Reproducibility is not earnedness.
       A reduction is banked when it maps to a real code change since the
       committed measurement: read the diff, not the repeat.

    4. gosec_scan_errors reads "Golang errors" and CANNOT SEE a whole class of
       silent scan failure. Verified in gosec v2.28.0: buildSSA has unnamed
       returns and a deferred recover() that only logs, so a panic yields
       (nil, nil); checkAnalyzers guards `err != nil || ssaResult == nil` and
       has no error return to propagate; it is called per package, so one
       package's SSA-rule findings vanish and the rest survive -- a strict
       subset. Metrics.Merge is additive, so merging the empty Metrics{} the
       failure path returns leaves every Stats field untouched, found included.
       ParseErrors returns early when len(pkg.Errors) == 0, so "Golang errors"
       stays empty. Net: a subset of findings missing, full files/lines, empty
       "Golang errors", exit 0 under -no-fail. That is this task's own phantom
       drop, and its ONLY trace is two strings on gosec's stderr
       ("Panic when running SSA analyzer", "Error building the SSA
       representation") -- gosec.logger writes to os.Stderr. Point 1 catches
       this whenever it is nondeterministic, which is the real defense. Nothing
       here catches it when it reproduces.

    What the checks below do close is narrower and real: a disagreement fails,
    a "Golang errors" entry fails, a missing Issues array fails, and a scan
    covering a quarter less of the tree fails. None of those is a substitute
    for point 3, and none of them reads stderr.
    """
    baseline_document = load_verified_baseline(baseline_path, runner)
    gosec_baseline = baseline_document.get("standalone_gosec")
    if not isinstance(gosec_baseline, dict):
        raise SystemExit("baseline has no standalone_gosec object")
    baseline = integer_counts(
        gosec_baseline.get("actionable_rule_counts"),
        "standalone_gosec.actionable_rule_counts",
    )
    policies = load_known_noise(gosec_baseline)

    # Agreement first: no gosec number reaches the baseline comparison until
    # two independent runs of the same tree have produced the same report.
    verify_distinct_reports(report_path, repeat_report_path)
    report = load_json(report_path)
    repeat_report = load_json(repeat_report_path)
    verify_repeat_run_agreement(
        report_path, repeat_report_path, report, repeat_report
    )

    scan_errors = gosec_scan_errors(report, report_path)
    if scan_errors:
        raise SystemExit(
            f"gosec report {report_path} carries {len(scan_errors)} Golang "
            "error(s), so at least one package was not analyzed and this "
            "report's counts prove nothing about the packages it does cover: "
            + "; ".join(scan_errors[:10])
        )

    # Keyed on Stats.files and Stats.lines, deliberately not on finding counts.
    verify_gosec_coverage(gosec_baseline, report, report_path)

    # An absent Issues key is not an empty result.  gosec v2.28.0 reaches
    # filterIssues unconditionally and that returns a non-nil slice, so a real
    # report with no findings carries `"Issues": []` -- which is accepted here.
    # A missing key means truncated, hand-assembled, or another tool's output,
    # and reading it as zero findings turns an unusable report into the largest
    # improvement the ratchet has ever seen.
    issues = report.get("Issues")
    if not isinstance(issues, list):
        raise SystemExit(f"gosec report {report_path} has no Issues array")

    actual: Counter[str] = Counter()
    match_counts = [0] * len(policies)
    overlapping: list[str] = []
    ignored = 0
    for issue in issues:
        if not isinstance(issue, dict) or not isinstance(issue.get("rule_id"), str):
            raise SystemExit(f"gosec report {report_path} has a malformed issue")
        matched = [
            index
            for index, policy in enumerate(policies)
            if is_known_gosec_noise(issue, policy)
        ]
        if len(matched) > 1:
            overlapping.append(
                f"{issue.get('file')}:{issue.get('line')} matches "
                + ", ".join(known_noise_label(policies[index]) for index in matched)
            )
        if matched:
            for index in matched:
                match_counts[index] += 1
            ignored += 1
            continue
        actual[issue["rule_id"]] += 1

    print(
        f"standalone gosec known noise: {ignored} finding(s) matched "
        f"{len(policies)} enumerated entr{'y' if len(policies) == 1 else 'ies'}"
    )
    cardinality_failed = False
    for policy, count in zip(policies, match_counts):
        print(
            f"  {known_noise_label(policy)}: {count} {policy['rule_id']} "
            f"match(es) at {policy['file']} "
            f"(annotation line {policy['annotation_line']})"
        )
        if count != 1:
            cardinality_failed = True
            print(
                "standalone gosec: expected exactly 1 path/rule/symbol match "
                f"for {known_noise_label(policy)}, found {count}",
                file=sys.stderr,
            )
    for description in overlapping:
        cardinality_failed = True
        print(
            f"standalone gosec: known-noise entries overlap -- {description}",
            file=sys.stderr,
        )
    # A reduction printed here is bounded by this function's docstring, point
    # 3: the advisory compare_counts prints asks for something the agreement
    # check above did not supply.
    comparison_failed = compare_counts(
        "standalone gosec actionable rules", baseline, actual
    )
    return 1 if cardinality_failed or comparison_failed else 0


def packages_command(package_list_path: Path, expected: int) -> int:
    """Assert the discovered package list is the shape the gate was calibrated on.

    Every scanning step in the workflow -- lint, govulncheck, gosec, deadcode
    and the race suite -- is handed this one file.  Only lint and gosec have a
    comparator behind them, so if the list silently shrinks the other three
    scan less and report success with nothing to catch them.  `test -s` only
    proved the file held one byte.
    """
    if expected <= 0:
        raise SystemExit("--expected must be a positive package count")
    try:
        text = package_list_path.read_text(encoding="utf-8")
    except OSError as error:
        raise SystemExit(
            f"cannot read package list {package_list_path}: {error}"
        ) from error

    entries = text.splitlines()
    blank = [number for number, entry in enumerate(entries, 1) if not entry.strip()]
    if blank:
        raise SystemExit(
            f"package list {package_list_path} has blank line(s) at {blank}; "
            "a padded list would satisfy a bare count check while scanning less"
        )
    malformed = sorted({entry for entry in entries if not entry.startswith("./")})
    if malformed:
        raise SystemExit(
            f"package list {package_list_path} must hold ./-relative directories: "
            f"{malformed}"
        )
    repeated = sorted(
        name for name, count in Counter(entries).items() if count > 1
    )
    if repeated:
        raise SystemExit(
            f"package list {package_list_path} repeats {repeated}; duplicates "
            "inflate the count while leaving real packages unscanned"
        )

    if len(entries) != expected:
        print(
            f"tracked Go packages: found {len(entries)}, expected {expected}. "
            "Re-derive with the 'Discover tracked Go packages' step and update "
            "--expected in .github/workflows/full-repo-quality.yml only after "
            "confirming this is a real package addition or removal.",
            file=sys.stderr,
        )
        return 1
    print(f"tracked Go packages: {len(entries)} matches the committed expectation")
    return 0


def platform_command(baseline_path: Path, runner: str) -> int:
    load_verified_baseline(baseline_path, runner)
    return 0


def parser() -> argparse.ArgumentParser:
    result = argparse.ArgumentParser(description=__doc__)
    subparsers = result.add_subparsers(dest="command", required=True)
    for command in ("platform", "lint", "gosec"):
        subparser = subparsers.add_parser(command)
        subparser.add_argument(
            "--baseline",
            type=Path,
            default=Path(".github/quality/full-repo-baseline.json"),
        )
        subparser.add_argument("--runner", required=True)
        if command != "platform":
            subparser.add_argument("--report", type=Path, required=True)
        if command == "lint":
            subparser.add_argument("--linters-report", type=Path, required=True)
        if command == "gosec":
            # Required, not optional.  An agreement check nobody has to pass
            # decays back into the human-remembered protocol it replaces.
            subparser.add_argument("--repeat-report", type=Path, required=True)
    packages = subparsers.add_parser("packages")
    packages.add_argument("--package-list", type=Path, required=True)
    packages.add_argument("--expected", type=int, required=True)
    return result


def main() -> int:
    arguments = parser().parse_args()
    if arguments.command == "platform":
        return platform_command(arguments.baseline, arguments.runner)
    if arguments.command == "packages":
        return packages_command(arguments.package_list, arguments.expected)
    if arguments.command == "lint":
        return lint_command(
            arguments.baseline,
            arguments.report,
            arguments.linters_report,
            arguments.runner,
        )
    return gosec_command(
        arguments.baseline,
        arguments.report,
        arguments.repeat_report,
        arguments.runner,
    )


if __name__ == "__main__":
    raise SystemExit(main())
