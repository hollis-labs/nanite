# Tool-baseline delta: `8feeee5c` (audit) vs `1d3bfd96` (frozen HEAD, 2026-08-22)

Produced by `TASKS/audit-remediation/00-revalidate-baseline/02-refresh-tool-baseline-at-frozen-head.md`
(the mechanical half of Wave 0). Measures only — no disposition is set here;
see `00/01` for that. Raw evidence backing the "at `<FROZEN>`" column lives in
this directory (`raw-1d3bfd96/`); raw evidence backing the "at `8feeee5c`"
column lives in the sibling `raw/` directory rescued in `e02f52c9`.

Same Go toolchain both times: `go1.26.2 darwin/arm64` (confirmed via
`govulncheck`'s banner in both `raw/govulncheck.log`, implicitly, and
`raw-1d3bfd96/govulncheck.log` explicitly) — the diffs below are genuine code
drift, not a toolchain-version artifact.

## Primary count-bearing measures

| Measure | At `8feeee5c` (audit) | At `1d3bfd96` (frozen HEAD) | Delta | Affects |
|---|---:|---:|---:|---|
| `gofmt -l` files (excluding `ui/`) | 122 | 130 | **+8** | `13/03` |
| gosec G304, production code only | 68 | 70 | **+2** | `08/03` |
| govulncheck reachable vulns | 14 (13 stdlib + 1 module, `GO-2026-4985`) | 14 (same 13 stdlib IDs + same 1 module vuln) | **0** — identical vulnerability-ID set | `08/08` |
| `deadcode` unreachable symbols, whole repo | 214 (`raw/deadcode.log`, `unreachable func:` lines) | 202 (`raw-1d3bfd96/deadcode.log`) | **-12** | `13/01` (repo-wide signal) |
| `deadcode` unreachable symbols, GO-STORE-008's specific 4 (`internal/store/skill_mode_filter.go`'s 3 + `skills_source.go`'s 1) | 4/4 confirmed unreachable | 4/4 still confirmed unreachable, same symbols/lines | **0** | `13/01` (the exact list it trusts) |
| `dupl` hits inside `internal/store` (audit-only `audit-golangci.yml` config) | 41 hits across ~20 files | 40 hits across 22 files | **-1 hit / +2 files** | `11/13` |

## Full `audit-golangci.yml` per-linter totals (apples-to-apples — same config both times)

The audit's own complexity-config run (`raw/golangci-audit-complexity.log`,
cited by `REPORT.md:429-437`) is the correct comparison baseline here, not
`raw/golangci-baseline.log`'s 2,338 (that one used the *project's* plain
`.golangci.yml`, without the complexity/dup linters this task is told to
re-run). Confirmed the 14 linters common to both configs report identical
counts in both of the audit-era files (e.g. `errcheck: 284` in both), so this
is a like-for-like comparison, not a methodology mismatch:

| Linter | At `8feeee5c` | At `1d3bfd96` | Delta |
|---|---:|---:|---:|
| cyclop | 250 | 275 | **+25** |
| dupl | 95 | 95 | 0 |
| errcheck | 284 | 294 | **+10** |
| errorlint | 47 | 49 | **+2** |
| exhaustive | 20 | 23 | **+3** |
| forbidigo | 222 | 222 | 0 |
| funlen | 69 | 70 | **+1** |
| gocognit | 227 | 243 | **+16** |
| gocyclo | 247 | 272 | **+25** |
| gosec | 590 | 625 | **+35** |
| govet | 530 | 626 | **+96** |
| ineffassign | 2 | 2 | 0 |
| maintidx | 25 | 30 | **+5** |
| misspell | 394 | 415 | **+21** |
| nestif | 64* | 63 | **-1** |
| nilerr | 22 | 22 | 0 |
| revive | 68 | 70 | **+2** |
| staticcheck | 78 | 79 | **+1** |
| unconvert | 3 | 3 | 0 |
| unparam | 35 | 35 | 0 |
| unused | 43 | 43 | 0 |
| **Total** | **3315** | **3556** | **+241** |

\* `REPORT.md:437` prose says "nestif 59", but the raw evidence file it cites
(`raw/golangci-audit-complexity.log`'s own summary tally) actually says
`nestif: 64`. Not something this task fixes (`REPORT.md` bodies are frozen
except the line-8 fallback case) — flagging it here as a transcription
mismatch between the report's prose and its own cited raw evidence, for
whoever revalidates that finding. Using the raw file's actual number (64) as
the "at `8feeee5c`" baseline above since it's the direct tool output, not the
prose summary.

Affects `12/01`'s lint-gate/ratchet baseline task.

## Read on the deltas

**Every measure that changed, changed upward** except `nestif` (-1, noise-level)
and `dupl`-in-`internal/store` (-1 hit, +2 files — a wash, not a real
reduction; see below). `govet`'s +96 is the largest single jump (530 -> 626,
+18%) — plausible given `govet: enable-all: true`'s `shadow` check was
already flagged in `REPORT.md:327` as extremely noisy (502 of the original
590 govet hits were shadow, "ALL 502... sampled 20... false positive" per the
funnel), and the batch's own README cites `internal/store/skills.go` (+275
lines) and two new migrations (`136`/`137`) as landing squarely in
audit-flagged territory. This is a real regression signal for `12/01`'s
ratchet task, not noise — every one of these totals moving up during a period
when the repo was still under active (pre-freeze) development is exactly the
"backlog grows silently because nothing gates it" problem `GO-HYG-001`
describes.

`dupl`-in-`internal/store` going from 41/~20 files to 40/22 files means the
duplication didn't shrink — it *spread*: 2 more files now participate in a
dupl cluster than at audit time, even though the raw hit count ticked down by
one. `11/13` should treat this as "the scope grew," not "the problem is
smaller."

`gofmt -l` going from 122 to 130 (+8) means 8 more files entered a
never-formatted state since the audit — consistent with 40 commits of
development against a repo where nothing gates `gofmt` repo-wide (only
`--new`-scoped pre-commit linting, per `GO-HYG-001`).

`gosec` G304 production count (68 -> 70, +2) is a small, plausible drift
matching 40 commits of change — not disproportionate the way `govet` is.

`govulncheck` and the `GO-STORE-008` 4-symbol dead-code list are both
completely unchanged (identical vulnerability-ID sets; identical 4 symbols at
identical lines) — useful negative-result confirmation that `08/08`'s and
part of `13/01`'s scopes are exactly as stale-but-still-accurate as the audit
left them.

## Non-goals honored

No disposition set anywhere in this file or elsewhere. No production code
touched. No new finding from this refreshed run was mechanically fixed — the
`internal/service/container.go` `stopReaper`/`stopRuntimeReaper` `go vet`
finding in `raw-1d3bfd96/vet.log` is the same `GO-LIFE-001` defect the audit
already found (same file, same two functions), not a new one, so it did not
need an `ESCALATIONS.md` entry.
