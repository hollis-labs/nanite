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


if __name__ == "__main__":
    unittest.main()
