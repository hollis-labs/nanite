# Wave 7 summary — for the operator

Wave 7 is complete: **2 reviewed, 0 in progress, 0 blocked**. It includes only
`12/01` and `12/03`; `12/02` remains the already-reviewed Wave 1 task and was
not reopened or counted.

## Full-repo quality gate

`12/01` added a GitHub Actions gate that runs nightly at **07:17 UTC** and by
manual dispatch. It uses the supported `macos-15` runner, validates runner
identity and actual `darwin`/`arm64` `GOOS`/`GOARCH`, and reconstructs
`apps/nanite` with four pinned public local-replace libraries:
`go-modelsdev`, `go-envelopes`, `go-harness-filters`, and
`go-runtime-events`.

Stage 1 is active across exactly **21 audit-config linters** at the clean,
Git-tracked-package total of **3,615**. Direct fail-fast `go list` discovery
selects **109 packages** before tracked-file filtering. Standalone `gosec`
ratchets **317 total findings**: exactly one accepted GO-SVCCORE-005 match and
**316 actionable** findings.

Stage 2 remains inactive and is not implemented as a tighter regression
threshold. The authoritative trigger is recorded in
`.github/quality/full-repo-baseline.json` at `stage_2.active`,
`stage_2.linters`, and `stage_2.activation_condition`; the workflow's final
comment and the runbook mirror it. Task `14/02` owns activating zero tolerance
for exactly `errcheck`, `errorlint`, and `nilerr`, only after all reach zero.
Their current counts are **281 / 48 / 24 = 353**.

The clean remote proof initially exposed that Nanite depended on unpublished
`go-envelopes` state. The operator authorized the v0.2.0 release to establish
a reproducible public CI dependency state. The workflow now pins release
commit `7642d69f64499ea180c0c596a48516e00cd28d46`.

## Goroutine-scan coverage

`12/03` added exactly `internal/sandbox`, `internal/permission`,
`internal/secrets`, `internal/pathsafe`, and `internal/fsutil` to the existing
nonfatal `lint-goroutines` target. The scan now reports the unchanged known
watcher at `internal/sandbox/proxy.go:342` (audit-time line 414). No Go source
changed.

## Review and completed verification

`12/03` passed fresh independent review on its first pass with no findings.
`12/01` went through multiple review/fix cycles: the first review found three
gate defects, the first fix cycle also identified a contaminated local
baseline, re-review found fail-open package discovery and stale current-count
text, and clean remote validation exposed the unpublished dependency state.
Separate fixes and the operator-authorized public release resolved each issue.
Final independent review was **PASS, no findings** for both tasks.

The aggregate race suite was actually executed twice during closeout:

- Orchestrator-owned local `go test -race ./... -count=1`: exit 0;
  `internal/store` **262.184s**.
- Released-pin clean Actions-shaped tracked-package race: exit 0;
  `internal/store` **235.123s**.

This summary did not rerun those suites. Future dependency validation should
run JSON validation, `actionlint`, `python3 scripts/quality-ratchet_test.py`,
the comparator's `platform --runner macos-15` check, the runbook's exact
tracked-package lint/gosec reproduction, `make lint-goroutines`, and ordinary
build/vet/test. Rerun `go test -race ./... -count=1` only when a future change
warrants its cost. Expected outcomes remain: valid JSON/workflow; four
comparator tests; platform `macos-15` / `darwin` / `arm64`; 109 packages;
lint 3,615/3,615; gosec 316/316 actionable plus exactly one accepted match;
the watcher at current line 342; and exit 0 for build, vet, tests, and any
warranted race run. The exact tracked-package procedure is in
`docs/engineering/runbooks/full-repo-quality-gate.md` and should not be
recreated from memory.

## Durable records and operator attention

The authoritative Wave 7 records are the entries in
[`TASKS/ESCALATIONS.md`](../ESCALATIONS.md) titled:

- “Wave 7 preflight found stale quality-ratchet and Wave 6 record facts”
- “Wave 7 worker tracker patch changed unrelated finding statuses”
- “Wave 7 `12/01` fresh review found four gate defects and a contaminated baseline”
- “`go-envelopes` v0.2.0 release checks exposed masked optional-tool failures”

The external `go-envelopes` follow-up is not a Wave 7 blocker. Its Makefile
masks installed `staticcheck` and `govulncheck` failures; direct checks found
U1000 on `manifestRel` and GO-2026-6218 against the Go 1.26.1 floor. Nanite's
clean gate passes on Go 1.26.7. The follow-up is already recorded in
`ESCALATIONS.md` and Vanta.

The working tree remains uncommitted. The user-owned untracked kickoff at
`docs/engineering/orchestrator-kickoffs/audit-remediation-w7.md` is not a
deliverable and remains untouched.

## Final status

| Task | Status |
|---|---|
| `12/01` | reviewed |
| `12/03` | reviewed |

| Status | Count |
|---|---:|
| Reviewed | 2 |
| In progress | 0 |
| Blocked | 0 |
