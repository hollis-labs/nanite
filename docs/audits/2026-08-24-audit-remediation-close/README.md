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

Derived at `bdf3d4d9` against `.github/quality/full-repo-baseline.json`:

| Tool | Baseline | At close | Δ |
|---|---:|---:|---:|
| gosec `G104` (unchecked errors) | 103 | **0** | −103 |
| gosec `G301` | 60 | 56 | −4 |
| gosec `G304` | 64 | 61 | −3 |
| gosec `G703` | 22 | 21 | −1 |
| gosec `G704` | 3 | 2 | −1 |
| gosec `G122` | 4 | 3 | −1 |
| `forbidigo` | 236 | 227 | −9 |
| `gocognit` | 257 | 255 | −2 |
| golangci `gosec` | 635 | 630 | −5 |
| `errcheck` / `errorlint` / `nilerr` | 0 | 0 | held at zero (Stage 2) |

Two regressions were open at close and are **not** reflected in the committed
baseline:

| Tool | Baseline | At close | Δ |
|---|---:|---:|---:|
| `misspell` | 429 | 439 | **+10** |
| gosec `G404` | 3 | 4 | **+1** |

Both are documented in `CW-20260824-0025`. The `misspell` delta is a
locale-configuration question, not a defect — see that task.

Commands that produced these (re-derive rather than trusting the table):

```bash
golangci-lint run --config docs/audits/2026-08-21-go-quality/audit-golangci.yml \
  --output.json.path=/tmp/lint.json ./internal/... ./cmd/... ./pkg/...
gosec -fmt=json -quiet -out=/tmp/gosec.json ./internal/... ./cmd/... ./pkg/...
```

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
