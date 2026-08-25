# Audit remediation — closing snapshot, 2026-08-24

Frozen copy of `TASKS/audit-remediation/findings.json` as it stood when the
batch closed and the AD-24 development freeze was lifted.

Archived at repo commit `bdf3d4d9`. `findings.json` here is byte-identical to
the live file at that moment (`shasum 67e690fe…`); it is a **snapshot, not a
second source of truth**. The live catalogue continues at
`TASKS/audit-remediation/findings.json`, and follow-up work is tracked in
Torque under project `PRJ-20260417-0002`, tag `audit-remediation-followup`.

Not to be confused with `docs/audits/2026-08-21-go-quality/findings.json` —
that is the **frozen original** the batch was measured against and is never
modified. This file is the *end* state; that one is the *start* state.

## Final state

Derived from this file at archive time:

```
total findings          113
task_status             reviewed 109 · validated 4
disposition             remediate 85 · retire-feature 9 · accepted-risk 8
                        defer 5 · false-positive 5 · needs-more-evidence 1
severity                low 45 · medium 32 · informational 24 · high 9 · critical 3
architect decisions     37 of 37 decided (ARCHITECT-DECISIONS.md)
```

### The four `validated` findings are not an oversight

`GO-SVCCORE-008`, `GO-AGENT-004`, `GO-CHAT-007` (task `13/03`) and
`GO-STORE-005` (task `06/03`) are implemented and acceptance-verified but
never received an independent fresh-reviewer pass — both at operator
direction, and both task files say so explicitly.

So the batch's headline "113/113" is precisely **109 reviewed + 4 validated**.
Tracked as `CW-20260824-0008`. Anyone citing this batch's completion should
cite that breakdown rather than the round number.

## What this batch measurably moved

> **Corrected 2026-08-24, after the pre-unfreeze batch re-measured everything
> over the gate's own package list.** The first version of this table was
> derived over `./internal/... ./cmd/... ./pkg/...` — **108** packages — while
> the gate uses the git-tracked set, **109**. It also reported a `G404`
> regression that does not exist. Both errors and their corrections are kept
> visible below rather than quietly overwritten; the wrong numbers were
> published and someone may have carried them.

Measured over the gate's 109-package list (see the command block below):

| Tool | Baseline | At close | Δ | first published |
|---|---:|---:|---:|---|
| gosec `G104` (unchecked errors) | 103 | **0** | −103 | −103 ✅ |
| gosec `G301` | 60 | 59 | −1 | ~~−4~~ |
| gosec `G304` | 64 | 62 | −2 | ~~−3~~ |
| gosec `G703` | 22 | 21 | −1 | −1 ✅ |
| gosec `G704` | 3 | 3 | **0** | ~~−1~~ — the 108-vs-109 artifact |
| gosec `G122` | 4 | 4 | **0** | ~~−1~~ |
| `forbidigo` | 236 | 227 | −9 | −9 ✅ |
| `gocognit` | 257 | 255 | −2 | −2 ✅ |
| golangci `gosec` | 635 | 631 | −4 | ~~−5~~ |
| `errcheck` / `errorlint` / `nilerr` | 0 | 0 | held at zero (Stage 2) | ✅ |

**Audit-attributable headroom: 122**, not the ≈129 first published — 107 gosec
plus 15 golangci. The single missing package is `scripts/verify-catalog`, which
holds exactly one `G704`; that one finding accounts for the `G704` line.

One regression was open at close and is **not** reflected in the committed
baseline as of this snapshot:

| Tool | Baseline | At close | Δ |
|---|---:|---:|---:|
| `misspell` | 429 | 439 | **+10** |

Resolved afterward by `CW-20260824-0027`, which migrated the repo to US
spellings and took `misspell` to **2** — both survivors deliberate
`legacyStatus = "cancelled"` constants in the regression tests that prove the
old value is gone.

**`gosec G404` was reported here as a `3 → 4` regression. It was not one.**
That compared a **raw** gosec count against a **post-filter** baseline: the
comparator absorbs one site via `known_noise` and saw 3 vs 3, passing at HEAD
before anything changed. The real defect was adjacent and worse — `known_noise`
held a single object, so its one exemption could be *swapped* to a different
site without tripping anything. It is now a list of four with `G404` baselined
at 0. See `CW-20260824-0025`.

### Commands

Derive over the **gate's** package list, not the `./internal/...` shorthand —
that shorthand is what produced two of the wrong numbers above:

```bash
# The gate's own discovery: go list, filtered to git-tracked packages.
# .github/workflows/full-repo-quality.yml, "Discover tracked Go packages".
go list -f '{{.ImportPath}}{{"\t"}}{{.Dir}}' ./... > /tmp/go-list.txt
: > /tmp/pkgs.txt
while IFS=$'\t' read -r _ dir; do
  rel=${dir#"$PWD"/}
  git ls-files --error-unmatch -- "$rel/*.go" >/dev/null 2>&1 && echo "./$rel" >> /tmp/pkgs.txt
done < /tmp/go-list.txt
wc -l < /tmp/pkgs.txt        # expect 109

golangci-lint run --config docs/audits/2026-08-21-go-quality/audit-golangci.yml \
  --output.json.path=/tmp/lint.json $(cat /tmp/pkgs.txt)
gosec -fmt=json -quiet -out=/tmp/gosec.json $(cat /tmp/pkgs.txt)
```

Note `go list ./...` alone returns **110** in a checkout that has run
`npm install` — the extra is `ui/node_modules/flatted/golang/pkg/flatted`, a
stray Go package vendored inside npm, which the git-tracked filter removes.

**A single gosec run is not a measurement.** The same command over the same
tree produced 193 findings once and 210 three times, with identical
`files`/`lines` stats and exit 0 — a strict subset, indistinguishable from a
good run, mechanism still unidentified. The pre-unfreeze baseline refresh
therefore required **8 byte-identical gosec runs** and 4 golangci runs before
any number was written. Do the same before trusting a delta.

## Reading this alongside the process docs

The batch's durable output is arguably not the findings but the two
engineering docs it produced, both of which draw every example from this
work:

- `docs/engineering/failure-modes.md` — how measurements and documents
  mislead. Every count error in the batch came from a number that had been
  *stored* rather than derived.
- `docs/engineering/tracking-integrity.md` — the eight drift checks, plus
  check 3b, a blind spot found the hard way.

## Provenance

10 waves, 2026-08-21 → 2026-08-24. Full narrative in
`TASKS/audit-remediation/README.md`; the durable cross-batch log is
`TASKS/ESCALATIONS.md`; the follow-up register with its Torque mapping is
`TASKS/audit-remediation/14-followups/README.md`.
