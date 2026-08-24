# Wave 7 handoff — for the next orchestrator

Wave 7 is closed: **2 reviewed, 0 in progress, 0 blocked**. This wave counts
only `12/01` and `12/03`. Task `12/02` was reviewed in Wave 1 and was neither
reopened nor counted here.

The working tree remains uncommitted. The untracked Wave 7 kickoff at
`docs/engineering/orchestrator-kickoffs/audit-remediation-w7.md` is
user-owned context, not a Wave 7 deliverable, and remains untouched.

## What shipped

| Task | Actual outcome |
|---|---|
| `12/01` | Added the first GitHub Actions full-repo quality gate, its committed baseline, comparator and comparator tests, and a runbook. It runs nightly at **07:17 UTC** and by manual dispatch. |
| `12/03` | Added exactly `internal/sandbox`, `internal/permission`, `internal/secrets`, `internal/pathsafe`, and `internal/fsutil` to the existing nonfatal `lint-goroutines` scan. The unchanged watcher is now reported at `internal/sandbox/proxy.go:342` (audit-time line 414). No Go source changed. |

### Quality-gate contract

Stage 1 ratchets exactly the 21 linters enabled by the audit config. Its clean,
Git-tracked-package baseline is **3,615 findings**. Package discovery selects
**109 packages** and runs `go list` as a direct fail-fast producer before
filtering completed output; do not move discovery back into process
substitution. Standalone `gosec` reports **317 total findings**: exactly one
accepted GO-SVCCORE-005 path/rule/symbol match and **316 actionable** findings.

Stage 2 is **inactive**; it is not a tighter nonzero regression threshold. Its
authoritative trigger is in
`.github/quality/full-repo-baseline.json` under `stage_2.active`,
`stage_2.linters`, and `stage_2.activation_condition`. The workflow's final
comment and the runbook mirror that contract. Task `14/02` owns activation,
and may activate zero tolerance for exactly `errcheck`, `errorlint`, and
`nilerr` only after all three reach zero. Current counts are **281 / 48 / 24 =
353**; remeasure when `14/02` starts.

The workflow uses the supported `macos-15` runner with committed
`darwin`/`arm64` metadata and verifies intended runner identity plus actual
`GOOS` and `GOARCH`. It reconstructs the Actions workspace as `apps/nanite`
plus pinned public checkouts of `go-modelsdev`, `go-envelopes`,
`go-harness-filters`, and `go-runtime-events` under `libs/`. The operator
authorized the `go-envelopes` v0.2.0 release to resolve the clean CI dependency
state; the workflow pins release commit
`7642d69f64499ea180c0c596a48516e00cd28d46`.

## Review and verification record

`12/03` passed its first fresh review with no findings. The reviewer confirmed
the exact five-package expansion, preservation of the nonfatal scan, the
current line-342 watcher output, and the absence of Go-source changes.

`12/01` required real review/fix cycles. The first fresh review found missing
public sibling checkouts, an unbounded standalone-gosec exception, runner
enforcement/deprecation defects, and a baseline contaminated by ignored local
Go files. A subsequent review found package discovery could continue after a
failed partial `go list` and found two stale current-count summaries. Separate
fix passes resolved those defects; the clean remote proof then exposed the
unpublished `go-envelopes` dependency state, which the operator-authorized
v0.2.0 release resolved. Final independent review was **PASS, no findings**.

The aggregate race suite was executed, not merely added to the workflow:

- The orchestrator-owned local `go test -race ./... -count=1` exited 0;
  `internal/store` completed in **262.184s**.
- The released-pin, clean Actions-shaped tracked-package race run exited 0;
  `internal/store` completed in **235.123s**.

This handoff did not rerun either race suite; it records the completed Wave 7
verification.

## Dependency validation for the next orchestrator

Run the lightweight checks below before relying on the gate:

```bash
python3 -m json.tool .github/quality/full-repo-baseline.json >/dev/null
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.7 \
  .github/workflows/full-repo-quality.yml
python3 scripts/quality-ratchet_test.py
python3 scripts/quality-ratchet.py platform --runner macos-15
make lint-goroutines
go build ./cmd/nanite/
go vet ./...
go test ./... -count=1
```

Expected outcomes:

- JSON validation and pinned `actionlint` exit 0.
- The comparator suite reports four passing tests. On the Actions platform,
  the platform command exits 0 and reports `macos-15` / `darwin` / `arm64`.
- Reproduce tracked-package lint and standalone `gosec` using
  `docs/engineering/runbooks/full-repo-quality-gate.md`; do not duplicate or
  simplify its fragile package-discovery shell. Expect 109 packages, lint
  **3,615/3,615** across exactly 21 linters, and standalone `gosec`
  **316/316 actionable plus exactly one accepted match**, all with exit 0.
- `make lint-goroutines` exits 0 and prints the known watcher at current line
  342; the target remains informational and nonfatal.
- Build, vet, and ordinary tests exit 0.

Only when a future change warrants the cost, rerun:

```bash
go test -race ./... -count=1
```

The expected result is exit 0. For dependency-pin validation, use the
Actions-shaped tracked-package procedure rather than substituting a developer
sibling checkout.

## Durable records and external follow-up

Reference the authoritative entries in
[`TASKS/ESCALATIONS.md`](../ESCALATIONS.md) by these exact titles:

- “Wave 7 preflight found stale quality-ratchet and Wave 6 record facts”
- “Wave 7 worker tracker patch changed unrelated finding statuses”
- “Wave 7 `12/01` fresh review found four gate defects and a contaminated baseline”
- “`go-envelopes` v0.2.0 release checks exposed masked optional-tool failures”

The external `go-envelopes` maintenance follow-up remains outside Wave 7: its
Makefile masks installed `staticcheck`/`govulncheck` failures, direct checks
found U1000 on `manifestRel` and GO-2026-6218 against its Go 1.26.1 floor, and
Nanite's clean gate passes on Go 1.26.7. This is already recorded in
`ESCALATIONS.md` and Vanta and is not a Wave 7 blocker.

## Final state

| Status | Count |
|---|---:|
| Reviewed | 2 |
| In progress | 0 |
| Blocked | 0 |
