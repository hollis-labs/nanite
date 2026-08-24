#!/usr/bin/env python3
"""Regression tests for the full-repo quality comparator."""

from __future__ import annotations

import json
from pathlib import Path
import subprocess
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("quality-ratchet.py")
RUNNER = "test-macos-runner"

# Stage 1 needs a non-zero baseline for any of the ratchet's real behavior to
# be reachable. The three Stage 2 linters are pinned at zero by the comparator
# itself, so the fixture baseline also carries gosec -- a real audit-config
# linter that is not under Stage 2 zero-tolerance.
STAGE_1_GOSEC_BASELINE = 40

# quality-ratchet.py's LINT_COVERAGE_FLOOR_PERCENT is 50, so a baseline whose
# counts total STAGE_1_GOSEC_BASELINE floors at half of it. Asserted directly by
# test_lint_prints_the_expected_coverage_floor so that changing the percentage
# fails loudly here instead of quietly loosening every test below.
COVERAGE_FLOOR = STAGE_1_GOSEC_BASELINE // 2


class QualityRatchetTest(unittest.TestCase):
    def setUp(self) -> None:
        goos, goarch = subprocess.run(
            ["go", "env", "GOOS", "GOARCH"],
            check=True,
            capture_output=True,
            text=True,
        ).stdout.splitlines()
        self.goos = goos
        self.goarch = goarch
        self.temporary_directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary_directory.cleanup)
        self.directory = Path(self.temporary_directory.name)

    def write_json(self, name: str, value: object) -> Path:
        path = self.directory / name
        path.write_text(json.dumps(value), encoding="utf-8")
        return path

    def baseline(self, *, goarch: str | None = None) -> Path:
        return self.write_json(
            "baseline.json",
            {
                "platform": {
                    "goos": self.goos,
                    "goarch": goarch or self.goarch,
                    "github_runner": RUNNER,
                },
                "standalone_gosec": {
                    "actionable_rule_counts": {"G101": 1},
                    "known_noise": {
                        "file": "internal/service/recovery_envelope_sink.go",
                        "annotation_line": 223,
                        "rule_id": "G404",
                        "symbol": "pickRecoveryGiphyQuery",
                    },
                },
            },
        )

    def lint_baseline(
        self,
        *,
        stage_2_active: bool = True,
        errcheck_baseline: int = 0,
        gosec_baseline: int = STAGE_1_GOSEC_BASELINE,
        include_stage_2: bool = True,
        stage_2_value: object | None = None,
        stage_2_linters: object | None = None,
    ) -> Path:
        baseline: dict[str, object] = {
            "platform": {
                "goos": self.goos,
                "goarch": self.goarch,
                "github_runner": RUNNER,
            },
            "stage_1": {
                "active": True,
                "counts": {
                    "errcheck": errcheck_baseline,
                    "errorlint": 0,
                    "nilerr": 0,
                    "gosec": gosec_baseline,
                },
            },
        }
        if include_stage_2:
            if stage_2_value is None:
                baseline["stage_2"] = {
                    "active": stage_2_active,
                    "linters": stage_2_linters
                    if stage_2_linters is not None
                    else ["errcheck", "errorlint", "nilerr"],
                }
            else:
                baseline["stage_2"] = stage_2_value
        return self.write_json("lint-baseline.json", baseline)

    @staticmethod
    def covering_issues(count: int = STAGE_1_GOSEC_BASELINE) -> list[dict[str, str]]:
        """Stage 1 findings that clear the coverage floor without tripping Stage 2."""
        return [{"FromLinter": "gosec"} for _ in range(count)]

    def run_lint(
        self,
        issues: list[dict[str, str]],
        *,
        report_document: object | None = None,
        report_text: str | None = None,
        report_missing: bool = False,
        stage_2_active: bool = True,
        errcheck_baseline: int = 0,
        gosec_baseline: int = STAGE_1_GOSEC_BASELINE,
        include_stage_2: bool = True,
        stage_2_value: object | None = None,
        stage_2_linters: object | None = None,
    ) -> subprocess.CompletedProcess[str]:
        if report_missing:
            report = self.directory / "absent-lint.json"
        elif report_text is not None:
            report = self.directory / "lint.json"
            report.write_text(report_text, encoding="utf-8")
        elif report_document is not None:
            report = self.write_json("lint.json", report_document)
        else:
            report = self.write_json("lint.json", {"Issues": issues})
        linters_report = self.write_json(
            "linters.json",
            {
                "Enabled": [
                    {"name": "errcheck"},
                    {"name": "errorlint"},
                    {"name": "nilerr"},
                    {"name": "gosec"},
                ]
            },
        )
        return subprocess.run(
            [
                "python3",
                str(SCRIPT),
                "lint",
                "--baseline",
                str(
                    self.lint_baseline(
                        stage_2_active=stage_2_active,
                        errcheck_baseline=errcheck_baseline,
                        gosec_baseline=gosec_baseline,
                        include_stage_2=include_stage_2,
                        stage_2_value=stage_2_value,
                        stage_2_linters=stage_2_linters,
                    )
                ),
                "--report",
                str(report),
                "--linters-report",
                str(linters_report),
                "--runner",
                RUNNER,
            ],
            check=False,
            capture_output=True,
            text=True,
        )

    @staticmethod
    def accepted_issue() -> dict[str, str]:
        return {
            "file": "/actions/apps/nanite/internal/service/recovery_envelope_sink.go",
            "rule_id": "G404",
            "code": "func pickRecoveryGiphyQuery() string {",
        }

    @staticmethod
    def actionable_issue() -> dict[str, str]:
        return {
            "file": "internal/example/example.go",
            "rule_id": "G101",
            "code": 'const credential = "example"',
        }

    def run_gosec(self, accepted_count: int) -> subprocess.CompletedProcess[str]:
        report = self.write_json(
            "gosec.json",
            {
                "Issues": [self.actionable_issue()]
                + [self.accepted_issue() for _ in range(accepted_count)]
            },
        )
        return subprocess.run(
            [
                "python3",
                str(SCRIPT),
                "gosec",
                "--baseline",
                str(self.baseline()),
                "--report",
                str(report),
                "--runner",
                RUNNER,
            ],
            check=False,
            capture_output=True,
            text=True,
        )

    def test_gosec_rejects_zero_accepted_matches(self) -> None:
        result = self.run_gosec(0)
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("expected exactly 1", result.stderr)
        self.assertIn("G101: 1", result.stdout)

    def test_gosec_accepts_exactly_one_match(self) -> None:
        result = self.run_gosec(1)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("found 1 accepted-policy", result.stdout)
        self.assertIn("G101: 1", result.stdout)

    def test_gosec_rejects_two_accepted_matches(self) -> None:
        result = self.run_gosec(2)
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("expected exactly 1", result.stderr)
        self.assertIn("found 2", result.stderr)
        self.assertIn("G101: 1", result.stdout)

    def test_platform_rejects_baseline_goarch_drift(self) -> None:
        other_goarch = "amd64" if self.goarch != "amd64" else "arm64"
        result = subprocess.run(
            [
                "python3",
                str(SCRIPT),
                "platform",
                "--baseline",
                str(self.baseline(goarch=other_goarch)),
                "--runner",
                RUNNER,
            ],
            check=False,
            capture_output=True,
            text=True,
        )
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("go env GOOS/GOARCH", result.stderr)

    def test_lint_stage_2_accepts_zero_correctness_findings(self) -> None:
        # This test used to pass an empty report and assert exit 0, which
        # codified the vacuous pass as intended behavior (CW-20260824-0023).
        # Stage 2 clearing means "the scan ran and found no correctness
        # findings", so the report must contain real Stage 1 findings.
        result = self.run_lint(self.covering_issues())
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("errcheck=0, errorlint=0, nilerr=0", result.stdout)
        self.assertIn("zero-tolerance passed", result.stdout)

    def test_lint_stage_2_rejects_any_correctness_finding(self) -> None:
        # Padded past the coverage floor so the failure is attributable to
        # Stage 2 and not to the report looking like a scan of nothing.
        result = self.run_lint(self.covering_issues() + [{"FromLinter": "errcheck"}])
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("requires zero findings", result.stderr)
        self.assertIn("'errcheck': 1", result.stderr)

    def test_lint_stage_2_rejects_nonzero_committed_baseline(self) -> None:
        result = self.run_lint([], errcheck_baseline=1)
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("must have zero Stage 1 baselines", result.stderr)

    def test_lint_stage_2_cannot_be_disabled(self) -> None:
        result = self.run_lint([], stage_2_active=False)
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("must be active", result.stderr)

    def test_lint_stage_2_cannot_be_absent(self) -> None:
        result = self.run_lint([], include_stage_2=False)
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("must be active", result.stderr)

    def test_lint_stage_2_rejects_malformed_structure(self) -> None:
        result = self.run_lint([], stage_2_value=[])
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("must be active", result.stderr)

    def test_lint_stage_2_rejects_missing_active_flag(self) -> None:
        result = self.run_lint(
            [],
            stage_2_value={"linters": ["errcheck", "errorlint", "nilerr"]},
        )
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("must be active", result.stderr)

    def test_lint_stage_2_rejects_missing_linters(self) -> None:
        result = self.run_lint([], stage_2_value={"active": True})
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("must name exactly", result.stderr)

    def test_lint_stage_2_rejects_wrong_linters(self) -> None:
        result = self.run_lint(
            [],
            stage_2_linters=["errcheck", "nilerr"],
        )
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("must name exactly", result.stderr)

    # --- Stage 1 coverage floor (CW-20260824-0023) -----------------------
    #
    # golangci-lint reports a scan of nothing exactly like a scan that found
    # nothing: well-formed JSON, an empty Issues array, exit 0 under
    # --issues-exit-code=0. Without a floor the ratchet reads that as every
    # linter improving to zero and passes.

    def test_lint_prints_the_expected_coverage_floor(self) -> None:
        # Pins the floor arithmetic so that changing LINT_COVERAGE_FLOOR_PERCENT
        # fails here loudly rather than quietly weakening the tests below.
        result = self.run_lint(self.covering_issues())
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn(
            f"coverage floor: current={STAGE_1_GOSEC_BASELINE} "
            f"floor={COVERAGE_FLOOR} (baseline {STAGE_1_GOSEC_BASELINE}, 50%)",
            result.stdout,
        )

    def test_lint_rejects_an_empty_report(self) -> None:
        result = self.run_lint([])
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("holds 0 finding(s)", result.stderr)
        self.assertIn(f"coverage floor of {COVERAGE_FLOOR}", result.stderr)
        self.assertNotIn("ratchet passed", result.stdout)

    def test_lint_rejects_a_substantially_shrunken_report(self) -> None:
        result = self.run_lint(self.covering_issues(COVERAGE_FLOOR - 1))
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn(f"holds {COVERAGE_FLOOR - 1} finding(s)", result.stderr)
        self.assertNotIn("ratchet passed", result.stdout)

    def test_lint_accepts_a_report_exactly_at_the_coverage_floor(self) -> None:
        # Positive control: the floor is a boundary, not a blanket rejection.
        result = self.run_lint(self.covering_issues(COVERAGE_FLOOR))
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("ratchet passed", result.stdout)

    def test_lint_skips_the_coverage_floor_for_an_all_zero_baseline(self) -> None:
        # Documented carve-out: a baseline asserting nothing to find has no
        # floor to enforce. It is not an escape hatch -- compare_counts then
        # treats every finding as an increase, so only an empty report passes.
        result = self.run_lint([], gosec_baseline=0)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertNotIn("coverage floor", result.stdout)

    # --- golangci-lint's self-reported scan error ------------------------
    #
    # The dangerous shape: a complete-looking report with a plausible finding
    # count that is silently missing an unknown subset. Verified against
    # golangci-lint v2.11.4 -- one bogus directory alongside real ones still
    # emits the real packages' findings, still exits 0, and sets Report.Error.

    def test_lint_rejects_a_scan_error_despite_plausible_counts(self) -> None:
        result = self.run_lint(
            [],
            report_document={
                "Issues": self.covering_issues(),
                "Report": {
                    "Linters": [],
                    "Error": "typechecking error: stat /x/nope: directory not found",
                },
            },
        )
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("carries a scan error", result.stderr)
        self.assertIn("directory not found", result.stderr)
        self.assertNotIn("ratchet passed", result.stdout)

    def test_lint_accepts_a_report_section_without_an_error(self) -> None:
        # Positive control: the presence of a Report section is normal; only a
        # populated Error is disqualifying.
        result = self.run_lint(
            [],
            report_document={
                "Issues": self.covering_issues(),
                "Report": {"Linters": [{"Name": "gosec"}]},
            },
        )
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("ratchet passed", result.stdout)

    # --- unusable reports ------------------------------------------------

    def test_lint_rejects_a_missing_report(self) -> None:
        result = self.run_lint([], report_missing=True)
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("cannot read JSON report", result.stderr)

    def test_lint_rejects_a_malformed_json_report(self) -> None:
        result = self.run_lint([], report_text='{"Issues": [')
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("cannot read JSON report", result.stderr)

    def test_lint_rejects_a_report_that_is_not_an_object(self) -> None:
        result = self.run_lint([], report_text="[]")
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("expected a JSON object", result.stderr)

    # --- Stage 1 regression against a non-zero baseline ------------------
    #
    # Previously untested end to end: every fixture baseline was all zeros, so
    # the ratchet's actual behavior (a count rising above a real baseline)
    # never ran.

    def test_lint_rejects_a_stage_1_regression(self) -> None:
        result = self.run_lint(self.covering_issues(STAGE_1_GOSEC_BASELINE + 1))
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn(
            f"gosec increased from {STAGE_1_GOSEC_BASELINE} to "
            f"{STAGE_1_GOSEC_BASELINE + 1}",
            result.stderr,
        )
        self.assertIn("INCREASE", result.stdout)

    def test_lint_accepts_a_report_exactly_at_the_stage_1_baseline(self) -> None:
        result = self.run_lint(self.covering_issues(STAGE_1_GOSEC_BASELINE))
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn(
            f"gosec: {STAGE_1_GOSEC_BASELINE} (baseline {STAGE_1_GOSEC_BASELINE})",
            result.stdout,
        )
        self.assertNotIn("INCREASE", result.stdout)

    def test_lint_reports_a_reduction_above_the_coverage_floor(self) -> None:
        reduced = STAGE_1_GOSEC_BASELINE - 5
        self.assertGreater(reduced, COVERAGE_FLOOR)
        result = self.run_lint(self.covering_issues(reduced))
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("reductions detected for gosec", result.stdout)

    def test_lint_rejects_findings_from_an_unbaselined_linter(self) -> None:
        # This is what stands between a non-compiling repo and a green gate:
        # golangci-lint emits a `typecheck` issue, which is absent from the
        # baseline. Previously relied on but never asserted.
        result = self.run_lint(
            self.covering_issues() + [{"FromLinter": "typecheck"}]
        )
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("unbaselined names: typecheck", result.stderr)

    # --- discovered package list -----------------------------------------

    def run_packages(
        self, entries: list[str], *, expected: int = 3, missing: bool = False
    ) -> subprocess.CompletedProcess[str]:
        package_list = self.directory / "packages.txt"
        if missing:
            package_list = self.directory / "absent-packages.txt"
        else:
            package_list.write_text("\n".join(entries) + "\n", encoding="utf-8")
        return subprocess.run(
            [
                "python3",
                str(SCRIPT),
                "packages",
                "--package-list",
                str(package_list),
                "--expected",
                str(expected),
            ],
            check=False,
            capture_output=True,
            text=True,
        )

    def test_packages_accepts_the_expected_count(self) -> None:
        result = self.run_packages(["./cmd/nanite", "./internal/api", "./internal/store"])
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("3 matches the committed expectation", result.stdout)

    def test_packages_rejects_a_shrunken_package_set(self) -> None:
        result = self.run_packages(["./cmd/nanite", "./internal/api"])
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("found 2, expected 3", result.stderr)

    def test_packages_rejects_a_single_surviving_package(self) -> None:
        # The shape `test -s` waved through: one byte of output is enough.
        result = self.run_packages(["./cmd/nanite"])
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("found 1, expected 3", result.stderr)

    def test_packages_rejects_a_grown_package_set(self) -> None:
        result = self.run_packages(
            ["./cmd/nanite", "./internal/api", "./internal/store", "./internal/chat"]
        )
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("found 4, expected 3", result.stderr)

    def test_packages_rejects_blank_padding(self) -> None:
        result = self.run_packages(["./cmd/nanite", "", "./internal/api", ""])
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("blank line(s)", result.stderr)

    def test_packages_rejects_duplicates(self) -> None:
        result = self.run_packages(
            ["./cmd/nanite", "./internal/api", "./internal/api"]
        )
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("repeats", result.stderr)

    def test_packages_rejects_non_relative_entries(self) -> None:
        # golangci-lint silently skips an import path passed where a directory
        # was expected -- the exact malformed argument this gate must catch.
        result = self.run_packages(
            [
                "./cmd/nanite",
                "./internal/api",
                "github.com/hollis-labs/nanite/internal/store",
            ]
        )
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("./-relative directories", result.stderr)

    def test_packages_rejects_a_missing_list(self) -> None:
        result = self.run_packages([], missing=True)
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("cannot read package list", result.stderr)

    def test_packages_rejects_a_nonpositive_expectation(self) -> None:
        result = self.run_packages(["./cmd/nanite"], expected=0)
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("must be a positive package count", result.stderr)


if __name__ == "__main__":
    unittest.main()
