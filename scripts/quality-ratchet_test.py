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

    def run_lint(
        self,
        issues: list[dict[str, str]],
        *,
        stage_2_active: bool = True,
        errcheck_baseline: int = 0,
        include_stage_2: bool = True,
        stage_2_value: object | None = None,
        stage_2_linters: object | None = None,
    ) -> subprocess.CompletedProcess[str]:
        report = self.write_json("lint.json", {"Issues": issues})
        linters_report = self.write_json(
            "linters.json",
            {
                "Enabled": [
                    {"name": "errcheck"},
                    {"name": "errorlint"},
                    {"name": "nilerr"},
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
        result = self.run_lint([])
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("errcheck=0, errorlint=0, nilerr=0", result.stdout)
        self.assertIn("zero-tolerance passed", result.stdout)

    def test_lint_stage_2_rejects_any_correctness_finding(self) -> None:
        result = self.run_lint([{"FromLinter": "errcheck"}])
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


if __name__ == "__main__":
    unittest.main()
