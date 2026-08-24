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
        print(
            f"{label}: reductions detected for {', '.join(reductions)}; "
            "lower the committed baseline to preserve them"
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


def gosec_command(baseline_path: Path, report_path: Path, runner: str) -> int:
    baseline_document = load_verified_baseline(baseline_path, runner)
    gosec_baseline = baseline_document.get("standalone_gosec")
    if not isinstance(gosec_baseline, dict):
        raise SystemExit("baseline has no standalone_gosec object")
    baseline = integer_counts(
        gosec_baseline.get("actionable_rule_counts"),
        "standalone_gosec.actionable_rule_counts",
    )
    policies = load_known_noise(gosec_baseline)

    report = load_json(report_path)
    issues = report.get("Issues")
    if issues is None:
        issues = []
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
    return gosec_command(arguments.baseline, arguments.report, arguments.runner)


if __name__ == "__main__":
    raise SystemExit(main())
