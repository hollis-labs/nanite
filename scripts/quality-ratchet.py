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
    if not isinstance(stage_2, dict) or stage_2.get("active") is not False:
        raise SystemExit(
            "Stage 2 must remain inactive until 14/02 reduces errcheck, "
            "errorlint, and nilerr to zero"
        )
    if stage_2.get("linters") != ["errcheck", "errorlint", "nilerr"]:
        raise SystemExit("Stage 2 must name exactly errcheck, errorlint, and nilerr")

    baseline = integer_counts(stage_1.get("counts"), "stage_1.counts")
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
    issues = report.get("Issues")
    if not isinstance(issues, list):
        raise SystemExit(f"golangci-lint report {report_path} has no Issues array")

    actual: Counter[str] = Counter()
    for issue in issues:
        if not isinstance(issue, dict) or not isinstance(issue.get("FromLinter"), str):
            raise SystemExit(f"golangci-lint report {report_path} has a malformed issue")
        actual[issue["FromLinter"]] += 1
    return compare_counts("audit-config linters", baseline, actual)


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


def gosec_command(baseline_path: Path, report_path: Path, runner: str) -> int:
    baseline_document = load_verified_baseline(baseline_path, runner)
    gosec_baseline = baseline_document.get("standalone_gosec")
    if not isinstance(gosec_baseline, dict):
        raise SystemExit("baseline has no standalone_gosec object")
    baseline = integer_counts(
        gosec_baseline.get("actionable_rule_counts"),
        "standalone_gosec.actionable_rule_counts",
    )
    policy = gosec_baseline.get("known_noise")
    if not isinstance(policy, dict):
        raise SystemExit("baseline has no standalone_gosec.known_noise policy")

    report = load_json(report_path)
    issues = report.get("Issues")
    if issues is None:
        issues = []
    if not isinstance(issues, list):
        raise SystemExit(f"gosec report {report_path} has no Issues array")

    actual: Counter[str] = Counter()
    ignored = 0
    for issue in issues:
        if not isinstance(issue, dict) or not isinstance(issue.get("rule_id"), str):
            raise SystemExit(f"gosec report {report_path} has a malformed issue")
        if is_known_gosec_noise(issue, policy):
            ignored += 1
            continue
        actual[issue["rule_id"]] += 1

    print(
        "standalone gosec: found "
        f"{ignored} accepted-policy {policy.get('rule_id')} finding(s) at "
        f"{policy.get('file')} (annotation line {policy.get('annotation_line')})"
    )
    cardinality_failed = ignored != 1
    if cardinality_failed:
        print(
            "standalone gosec: expected exactly 1 GO-SVCCORE-005 "
            f"path/rule/symbol match, found {ignored}",
            file=sys.stderr,
        )
    comparison_failed = compare_counts(
        "standalone gosec actionable rules", baseline, actual
    )
    return 1 if cardinality_failed or comparison_failed else 0


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
    return result


def main() -> int:
    arguments = parser().parse_args()
    if arguments.command == "platform":
        return platform_command(arguments.baseline, arguments.runner)
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
