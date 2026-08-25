# Prevention and rules table — audit remediation

The remediation guide's §7 output-format **D**. Its framing question, asked of
every meaningful finding:

> What regression test, automated check, architectural rule, or engineering
> standard would have prevented this?

This file is the batch's answer, aggregated by **defect class** rather than by
finding — because the guide's point (§3) is that *"recurring patterns should
normally produce a prevention mechanism,"* and one-off regression tests, while
required, do not stop the next instance of the same class.

`12/02` (add engineering standards docs) and `12/01` (full-repo scheduled lint
gate) are the two tasks that actually implement most of the right-hand column.
This file is their specification; they should not have to re-derive it.

---

## The headline finding of this analysis

**The lint rule that would have caught the batch's most severe finding already
exists in this repo — and was silenced in the package where the bug lived.**

`.golangci.yml:88-103` enables `forbidigo` with exactly the right rule:

```yaml
- pattern: '^filepath\.Join$'
  msg: "use internal/pathsafe.ResolveUnder to prevent path traversal (Phase 1 Wave 1)"
```

But `.golangci.yml:180-184` scopes it away from where it mattered:

```yaml
- text: 'ResolveUnder'
  linters: [forbidigo]
  path-except: '(internal/sandbox/|internal/mcp/|internal/service/install/)'
```

`path-except` silences the rule everywhere **outside** those three paths.
`GO-PLUGIN-002` (critical, unconfined path-traversal write) is
`internal/api/catalog.go:301` — a bare `filepath.Join(cs.pluginsDir,
entry.Name)` in `internal/api/`, which is not in that list. The rule was
correct, present, and configured not to look there.

The comment above the exclusion explains the intent honestly — it was a
deliberate "Phase 1 Wave 1 adoption scope (2026-04-12)" decision to surface a
bounded worklist without churning unrelated packages. That was a reasonable
call at the time. What is missing is the **ratchet**: nothing ever widened the
scope, and nothing flagged that `internal/api/` — which handles untrusted HTTP
input and writes to the plugins directory — was outside it.

This single observation is the strongest argument in the batch for `12/01`,
and it generalizes: **an adoption-scoped lint rule with no widening mechanism
is a rule that expires silently.** Every prevention below that takes the form
of a path-scoped rule inherits this hazard and needs an explicit widening
trigger, not just an initial scope.

---

## Defect classes and their preventions

### 1. Silent security degradation

> A required security boundary must not silently degrade while reporting
> success. *(guide §4 Wave 7 standard)*

| | |
|---|---|
| **Findings** | `GO-SEC4-001` (critical — Linux sandbox falls back to unisolated execution and reports success), `GO-SEC4-002` (high), `GO-SEC4-006`, `GO-PLUGIN-001` (critical — signature verification skipped with **no log line at all** when no public key is configured) |
| **Why it recurred** | Both instances share a shape: the degraded path is the `else` of a condition that is *false by default* out of the box. `GO-PLUGIN-001`'s `if sourcePublicKey != "" && entry.Signature != ""` never fires for the default seeded catalog source, and neither does its `else if` warning branch. |
| **Prevention** | (a) A test per security boundary asserting the **reported status** matches the **actual** enforcement — not that the happy path works. (b) A structural rule: a security check whose condition can be false at defaults must have a mandatory, unconditional `else` that either fails or logs at warn+. (c) Codify as a named standard. |
| **Enforcement point** | Regression tests in `02/01`, `01/01`. Standard text in `12/02`. Precedent to copy: `internal/plugin/install/verify_prod_enforcement_test.go` + `verify_dev_bypass_test.go` — the audit confirmed these are genuinely fail-closed and correctly build-tag gated. That pair is the model. |

### 2. Trust-boundary paths

> Values influenced by external callers, agents, plugins, catalogs, or
> persisted untrusted state must not become filesystem paths without canonical
> validation/confinement. *(guide §4 Wave 7 standard)*

| | |
|---|---|
| **Findings** | `GO-PLUGIN-002` (critical), `GO-AGENT-001`/`GO-AGENT-002` (high — slug→path traversal), `GO-API-001`, `GO-API-003`, `GO-MCPTOOL-008` (symlink TOCTOU), `GO-SEC-003` (68 production G304 sites at the audited commit) |
| **Why it recurred** | Two confinement mechanisms coexist with no rule about which applies where — `pathsafe.ResolveUnder` (3 of 4 API handlers) and `validatePluginID`-style allowlist (`install.DirStaging.Commit`). Plus the `forbidigo` scoping gap above. |
| **Prevention** | (a) **Widen the `forbidigo` `ResolveUnder` `path-except` to include `internal/api/`** — the narrowest, highest-value config change in this entire batch. (b) Pick one canonical mechanism per boundary and say so (AD-04). (c) Traversal regression test per entry point, following `TestHandleInstall_PathTraversal` (`internal/api/plugins_install_test.go:201`). |
| **Enforcement point** | `.golangci.yml` `forbidigo` exclusion rules — widened by `08/09`/`12/01`. Tests in `01/01`, `03/01`, `08/09`. Standard text in `12/02`. |

### 3. Migration incompleteness — the "sibling path" class

> When a security or correctness fix replaces a primitive or pipeline,
> enumerate and verify every production caller of the superseded
> implementation. *(guide §4 Wave 7 standard)*

| | |
|---|---|
| **Findings** | `GO-PLUGIN-003` (high — a fail-closed CLI install pipeline was built 2026-04-13 and the API handler was never migrated onto it), `GO-API-007` (Harness-v1 vs. native durable-agent handlers), `GO-SVCEXEC-004`, and most of `11/` |
| **Why it recurred** | The audit's most repeated structural observation: *"a newer/correct implementation beside an older stale implementation."* Nothing in the process required enumerating callers of the thing being replaced. |
| **Prevention** | (a) A mandatory **"All production callers"** section in any task that changes a shared primitive or security boundary — already the shape of every task file in this batch (guide §6); promote it from batch convention to project standard. (b) Where two implementations are deliberately kept, a **parity test** that fails on divergence (see AD-19). (c) A re-run of the caller-enumeration sweep at review time, not just at authoring time — `01/01`'s Prevention section already specifies this. |
| **Enforcement point** | `12/02` standard + the task-file template (`docs/engineering/templates/03-task-file-template.md`). Parity tests land per-task in Wave 6a. |

### 4. Production reachability — islands

> A feature is not done until its production entry point, wiring, invocation,
> and observable behavior are proven. *(guide §4 Wave 7 standard)*

| | |
|---|---|
| **Findings** | `GO-MEM-001`, `GO-MEM-002`, `GO-SVCEXEC-003`, `GO-MCPTOOL-001`, `GO-MCPTOOL-002`, `GO-MCPTOOL-003` — 6 fully-built, production-unreachable features |
| **Why it recurred** | Unit tests pass on all six. Nothing checked the wiring. The guide's do-not list names this exactly: *"mark a feature done based only on unit tests."* |
| **Prevention** | (a) The four-step reachability proof (`entry point → wiring → invocation → observable behavior`) as a definition-of-done item for any feature task. (b) `deadcode ./...` in the full-repo gate, with new unreachable exported symbols treated as a regression. (c) For anything deliberately deferred: a recorded **trigger and owner**, and docs that don't claim it's live. |
| **Enforcement point** | `12/01` (deadcode in the scheduled gate), `12/02` (the standard), and the wire/defer/retire decisions AD-06…AD-11. |

### 5. Lifecycle ownership

> Every goroutine/background worker/resource has an explicit owner and shutdown
> path; partial construction cleans up already-started resources.
> *(guide §4 Wave 7 standard)*

| | |
|---|---|
| **Findings** | `GO-LIFE-001` (reaper goroutines leak on two `NewContainer` error paths), `GO-SVCCORE-001`, `GO-SVCCORE-002` (~18 untracked `safego.Go` sites), `GO-RUNTIME-004` (unbounded job registry), `GO-TEST-001` (API tests create Containers without shutdown) — **13 concurrency + 11 lifecycle** category hits total |
| **Why it recurred** | `go vet` flagged `GO-LIFE-001` directly (both cancel funcs "not used on all paths") and it shipped anyway — a signal existed and wasn't gating. Separately, `make lint-goroutines` is a grep sweep over a **hardcoded package list** (`Makefile:74-81`) and is prefixed with `-` so it never fails the build. That is `12/03`'s finding. |
| **Prevention** | (a) `go vet`'s coverage as a **blocking** gate — but not at commit time. `lefthook.yml` pre-commit is formatting only (`TASKS/gate-integrity/08`); no hook runs vet or lint. The nightly full-repo quality gate carries it instead, and carries strictly more of it: `docs/audits/2026-08-21-go-quality/audit-golangci.yml:82-85` enables `govet` with `enable-all: true` minus `fieldalignment`, which is 45 analyzers against `go vet`'s own 35 with none of the 35 missing (compare the `linters.settings.govet.disable` enum from `golangci-lint config verify` against `go tool vet help`'s registered list), and it includes `lostcancel` — the analyzer that emits the *"not used on all paths"* diagnostic this row cites. (b) Fix `lint-goroutines` to cover all packages and actually fail (`12/03`). (c) A constructor-cleanup pattern documented once, with `stopCatalog()`'s existing correct usage in the same function as the in-repo example. |
| **Enforcement point** | `Makefile` `lint-goroutines` (`12/03`); the nightly gate's ratcheted `govet` (`.github/workflows/full-repo-quality.yml:129-141`, feeding `scripts/quality-ratchet.py lint` — a ceiling ratchet, so one new `lostcancel` finding lifts `govet` above its committed baseline in `.github/quality/full-repo-baseline.json` and fails the run); `./scripts/check.sh`'s `vet` stage, run by hand when a feature lands; `12/02` standard. Line numbers and config verified at `6c139038`. |

### 6. Semantic duplication

> Duplicating syntax is a maintainability concern. Duplicating a semantic rule
> is a correctness concern. *(guide §4 Wave 7 standard)*

| | |
|---|---|
| **Findings** | 26 `duplication`-category findings — the single largest category in the audit. All 16 tasks in `11/`. |
| **Why it recurred** | No mechanism distinguishes the two kinds. `dupl` (enabled only in the audit-only config, `audit-golangci.yml`) reports textual duplication, which conflates harmless boilerplate with genuinely divergent copies of one rule. |
| **Prevention** | (a) The guide's five-way classification applied per instance (`textual-only boilerplate / same semantics-stable / same semantics-divergent / migration drift / intentionally independent`) — Wave 6a produces this table. (b) Parity tests for anything classified "intentionally independent" that shares a rule. (c) Explicitly **not** LOC reduction — the standard should say so, because the natural reading of a duplication finding is "delete some code." |
| **Enforcement point** | `12/02` standard; Wave 6a classification table; per-task parity tests. AD-19 sets the policy. |

### 7. Silent error swallowing

| | |
|---|---|
| **Findings** | `GO-STORE-003` (high — `DeleteAgentByID` cannot distinguish not-found from a real DB error), `GO-RUNTIME-007` (MCP config decode errors silently dropped), `GO-STORE-004`, plus **13** `error-handling`-category findings |
| **Why it recurred** | `GO-STORE-003` was caught by golangci's `nilerr` linter (`raw/golangci-baseline.log:6421` per `REPORT.md:693`) — the finding was **already in the lint output** and invisible because the fast gate runs `golangci-lint run --new` (changed code only) and the full run was never capped-off or reviewed. |
| **Prevention** | This is the clearest case for `12/01`: the uncapped full-repo run surfaces what `--new` structurally cannot — pre-existing findings in untouched code. Baseline the history, block regressions (AD-21). |
| **Enforcement point** | `12/01`. `errcheck`, `nilerr`, `errorlint` are already enabled in `.golangci.yml`; the gap is capping and cadence, not linter selection. |

### 8. Documentation that asserts things that are false

| | |
|---|---|
| **Findings** | `GO-STORE-008`, `GO-STORE-009`, `GO-CHAT-004`, `GO-MCPTOOL-004` (a package doc describing a `ClientElicitMiddleware` type that **does not exist**), plus **9** `comments`-category findings. `REPORT.md:731` records a doc comment claiming three symbols are "still consumed by `internal/service/ingest.go`" — *"this is false."* |
| **Why it recurred** | Nothing verifies a doc comment's factual claims, and a confidently wrong comment is worse than none: `13/01` had to independently disprove one before removing dead code. |
| **Prevention** | (a) Treat a doc comment naming a caller/consumer as a claim needing a citation, and prefer *not* naming callers over naming them and going stale. (b) When a symbol is deleted, grep for prose references, not just code references — cheap, and catches this class entirely. |
| **Enforcement point** | `12/02` standard; `13/02`'s cleanup pass is the one-time debt payment. No automated enforcement is proposed — this one is genuinely a review-discipline item, and pretending otherwise would be the kind of low-value ceremony the guide warns against. |

### 9. Backlog invisibility — the meta-class

| | |
|---|---|
| **Findings** | `GO-HYG-001` (informational — 122 files failing `gofmt -l`; errcheck 284 and other totals at `REPORT.md:290`) |
| **Why it matters** | Every class above was *findable* by tooling this repo already runs. What was missing is a place where the uncapped output is looked at. The fast gate is correctly optimized for `<15 seconds on staged files` (`lefthook.yml:3`) — that is the right design for a pre-commit hook and should not change. The gap is the absence of a slower companion. |
| **Prevention** | `12/01`, exactly as the guide's Wave 7 splits it: keep the fast developer gate fast; add a separate full-repo scheduled/merge gate for uncapped lint, `govulncheck`, `gosec` with a known-noise policy, module verification, dead-code report, and the race suite. Baseline history; reject regressions (AD-21). |
| **Enforcement point** | `12/01`. `00/02` supplies the frozen-HEAD numbers that become the baseline. |

---

## In-repo precedents worth copying rather than inventing

The batch should reuse these three established local patterns instead of
introducing new mechanisms — all confirmed present at planning time:

1. **Invariants doc + enforcing test with deliberate-violation coverage** —
   `internal/context/INVARIANTS.md` paired with
   `internal/service/slot_invariants_test.go`. Each invariant names its
   enforcing check function and has a test proving the check has teeth. Per
   `docs/engineering/architecture/26-architecture-enforcement-tests.md`, this
   is *"the established style for 'make an architectural rule enforceable'
   here — a Go test, not CI-only static lint."* The right home for the
   security-boundary invariants in classes 1, 2, and 5.

2. **Cheap grep-based hooks for rules linters can't express** —
   `lefthook.yml`'s `migration-purity` hook rejects `INSERT/UPDATE/DELETE` in
   migration files with a plain `grep -qiE`. Fast, obvious, no new tooling.
   The model for anything `forbidigo` can't match (it cannot match the `go`
   keyword, which is why `lint-goroutines` exists at all).

3. **Path-scoped `forbidigo` rules** — already wired, already proven. The
   mechanism is sound; the *scoping discipline* is what failed. Any new rule
   added by this batch must come with a widening trigger recorded next to it,
   or it repeats the headline finding above.
