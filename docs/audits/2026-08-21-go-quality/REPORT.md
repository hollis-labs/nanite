# Nanite Go Code Quality & Architecture Audit — 2026-08-21

Status: **COMPLETE — mechanical/triage phase and all 13 package-cluster reviews landed.** Every `internal/*` package plus `cmd/nanite` has been reviewed against the guide's checklist. ~150 findings total across §3 (10 confirmed in the initial triage pass) and §8 (13 cluster reviews, condensed for length — see each cluster's own subsection for full evidence).
Commit audited: `8feeee5c` (main, worktree `go-quality-audit`)
Guide: `~/dev/chrispian/inbox/nanite-go-code-quality-audit-guide.md`

This is a read-only discovery audit. No production code has been modified.
Raw tool output backing every finding below lives in `raw/`.

---

## 1. Executive summary

- Build, `go vet` (module compiles cleanly modulo one real finding below), full
  test suite, `go mod verify`/`tidy -diff` all clean/passing.
- One concrete, verified concurrency/lifecycle defect found in
  `internal/service/container.go` (GO-LIFE-001).
- `govulncheck` reports 14 reachable vulnerabilities; 13 are Go stdlib fixes
  already released in patch versions ahead of the pinned `go 1.26.2`
  toolchain (mechanical bump, not a code defect); 1 is a real reachable
  third-party module vulnerability (GO-SEC-001).
- Two gravitational packages confirmed by import fan-in exactly matching the
  guide's predicted pattern: `internal/store` (fan-in 30) and `internal/service`
  (fan-out 64, i.e. the composition root) — see GO-DEP-001/002.
- Full-repo `golangci-lint` (uncapped, the project's own committed config)
  surfaces 2,338 issues, but no enforced hook or CI ever runs it uncapped —
  pre-commit only lints changed lines (`--new`), pre-push only runs tests,
  and there is no `.github/workflows` at all (GO-HYG-001). Triaged the full
  2,338 rather than reporting the raw count: ~1,250 collapse into 9
  well-evidenced noise/mechanical clusters (a genuinely noisy govet `shadow`
  check, idiomatic errcheck Close/Rollback cleanup, gosec hits confined to
  test fixtures, the already-tracked forbidigo migration worklist, pure
  spelling fixes, auto-fixable staticcheck quickfixes), leaving ~1,088
  actionable, of which 394 are mechanical misspellings and ~694 need real
  judgment. Full funnel and cluster evidence in §3.1; one cluster (gosec
  G304 outside the tracked `pathsafe.ResolveUnder` adoption scope) was
  promoted to a Top-10 finding, GO-SEC-003.
- `go test -race ./...` did not produce a clean full-repo verdict: 5
  packages hit the default 10-minute per-package timeout under race
  instrumentation. Re-ran all 5 at `-timeout 25m`: 3
  (`internal/messaging`/`internal/selftools`/`internal/subagent`) now pass
  cleanly, confirming pure timeout artifacts. **`internal/api` and
  `internal/service` still don't complete at 25 minutes** — traced to a
  confirmed root cause, not a mystery: their test suites construct many
  `service.Container` instances and never call `Shutdown()` on any of them
  (verified by direct grep), leaking 2 reaper goroutines per test that
  accumulate across the run (288 counted in the 25-min dump, up from 56 at
  10 min) and, under `-race` overhead, prevent the suite from ever
  finishing. Confirmed **not** a production issue — `cmd/nanite/main.go`
  does call `Shutdown()` on the real exit path. See GO-TEST-001.
  Race-cleanliness for `internal/api`/`internal/service` remains unknown
  until that test-cleanup gap is fixed and the run repeated.
- Complexity signal (audit-only golangci config, guide §15 thresholds) found
  several strong-review-candidate outliers, sharpest being
  `internal/workflow/executor.go:(*Executor).Run` (cognitive complexity 87,
  threshold is 20) and `internal/subagent/service.go:(*Service).Spawn`
  (cognitive 62 / cyclomatic 47). Full list in §4. **§8's cluster reviews
  found two functions the mechanical pass missed entirely that are far
  sharper than either**: `(*chatServiceImpl).generateResponse`
  (`internal/service/chat_generate.go`, cognitive **458** — ~5x the
  above) and `agent.Boot` (`internal/runtime/agent/agent.go`, cyclomatic
  50/cognitive 59). Both judged essential-but-accidentally-shaped on
  manual read — see §8.4 and §8.13.

**Deep-dive phase (§8) findings — two promoted to critical:**

- **GO-PLUGIN-001/002 (CRITICAL)** — the GUI/API plugin-catalog-install path
  (the actual "Plugin Manager" UI) bypasses signature/checksum verification
  by default *and* has an unconfined path-traversal write, both because a
  genuinely solid, fail-closed pipeline was built later for the CLI install
  path and the older API handler was never migrated onto it. Full trace in
  §8.6.
- **GO-SEC4-001 (CRITICAL, still open)** — on Linux, missing `bwrap`
  silently degrades the sandbox to no OS-level isolation with no error —
  a re-confirmed, unfixed finding from an earlier dedicated sandbox audit.
  Full trace in §8.12.
- **GO-AGENT-001 (high)** — agent `slug` is never validated before being
  joined into a managed-config file path; a full path-traversal write chain
  traced from `POST /api/agents` to an unguarded `os.WriteFile`. §8.8.
- **GO-SVCEXEC-001/002 (high)** — `generateResponse` (cognitive 458, the
  highest-complexity function in the codebase by a wide margin, missed by
  the mechanical pass) and its owning type `chatServiceImpl` (52 fields /
  84 methods, a second god object alongside `Container`). §8.4.
- **GO-RUNTIME-002 (high, documented-intent caveat)** — auth is opt-in via
  env var with no default loopback bind, no TLS, and no startup warning —
  reported with the project's own documented single-operator-tool rationale
  stated alongside it, since an architect may reasonably judge the current
  tradeoff acceptable. §8.13.
- Full list of all ~150 findings, organized by cluster with complete
  evidence, is in §8. §9 synthesizes the cross-cluster patterns (god-object
  shape recurs in exactly 2-3 types; the mechanical triage table had real
  blind spots; a recurring "fully built but never wired" pattern across 4+
  features; a bimodal security posture where strong primitives coexist with
  2 unmigrated GUI/API callers; 3 naming collisions checked and found
  benign; 4 instances of "same lifecycle concept, one migrated, one not").

---

## 2. Top 10 architect-review candidates

Re-ranked after all 13 cluster reviews landed, using the guide's own severity
ranking (correctness/security first). Full evidence for every item is in §3
(items 6, 9-10) or §8 (all others, cited by cluster subsection).

1. **GO-PLUGIN-001/002 (CRITICAL)** — GUI/API plugin-catalog-install bypasses
   signature verification by default and has an unconfined path-traversal
   write; the CLI install path has a genuinely solid fix that was never
   back-ported to this handler. §8.6.
2. **GO-SEC4-001 (CRITICAL, re-confirmed still-open)** — Linux sandbox
   silently degrades to zero OS-level isolation when `bwrap` is missing,
   reports success either way. §8.12.
3. **GO-AGENT-001 (high)** — agent `slug` unvalidated before being joined
   into a managed-file path; full traced path-traversal write chain from
   `POST /api/agents` to `os.WriteFile`. §8.8.
4. **GO-SVCEXEC-001/002 (high)** — `generateResponse` (cognitive 458, the
   highest-complexity function in the codebase, missed by the mechanical
   pass) and its owning `chatServiceImpl` (52 fields/84 methods, the
   second confirmed god object). §8.4.
5. **GO-LIFE-001 / GO-TEST-001** — `internal/service/container.go`'s
   subagent/runtime reaper goroutines leak on 2 constructor error paths,
   and separately (confirmed distinct root cause) leak from every
   `internal/api` test that never calls `Shutdown()`, blocking a `-race`
   verdict for that package. §3.
6. **GO-DEP-001 / GO-SVCCORE** — `Container` (60 fields) confirmed
   wiring-only (2 methods) by direct read — the god-object signal correctly
   flags the package (31.6K LOC, fan-out 64) but mis-locates the mass;
   `chatServiceImpl` (item 4) is where it actually lives. §3, §8.3.
7. **GO-DEP-002** — `internal/store`: fan-in 30, the single most
   gravitational package; §8.1 traced the concrete consequence (76 direct
   `*store.Store` references in `internal/service` alone) and one real bug
   riding on it (`DeleteAgentByID` swallowing non-not-found errors,
   GO-STORE-003, high). §3, §8.1.
8. **GO-RUNTIME-002 (high, documented-intent caveat)** — auth opt-in via
   env var, all-interfaces bind by default, no TLS, no startup warning;
   reported alongside the project's own stated single-operator rationale.
   §8.13.
9. **GO-SEC-001/002/003** — reachable third-party CVE (OTel exporter,
   fixed upstream), Go toolchain behind on stdlib patches, and gosec G304
   hits outside the `pathsafe.ResolveUnder` tracked-adoption scope (traced
   to real sites in `internal/agent/managed_*` by §8.8). §3.
10. **GO-HYG-001** — uncapped `make lint` exists but nothing enforces a
    full-repo run; the triaged 2,338→1,088 funnel (§3.1) is the audit's
    methodology demonstration for turning lint volume into real signal.

**Second tier, high-value but not top-10** (full detail in §8): gosec G70x
taint-analysis cluster now triaged per-package rather than left pending —
zero hits in `internal/plugin`, confirmed CLI-trust false positives in
`cmd/nanite`, real findings folded into GO-SVCCORE-004 (SSRF) and
GO-MCPTOOL-008 (symlink TOCTOU) instead. `UnloadPlugin` (cyclomatic 63)
judged essential-with-one-real-TOCTOU-gap (GO-PLUGIN-004). `cmdServe`
(cyclomatic 45) judged essential, correctly sequenced (§8.13) — its
`slogx.Fatal` cleanup-bypass (GO-RUNTIME-001) and `worktree.CleanupOrphaned`'s
wrong-branch-name bug (GO-RUNTIME-003, orphaned git branches accumulate on
every daemon restart) are the real findings riding alongside it.

---

## 3. Confirmed findings

### GO-LIFE-001 Subagent/runtime reaper goroutines leak on container construction failure

- Severity: medium
- Confidence: high
- Category: concurrency | lifecycle
- Scope:
  - `internal/service/container.go`, `NewContainer` (constructor), lines
    ~1162–1400
- Evidence:
  - `go vet ./...` flags both cancel funcs directly:
    `internal/service/container.go:1162:2: the stopReaper function is not
    used on all paths (possible context leak)`;
    `internal/service/container.go:1182:2: the stopRuntimeReaper function is
    not used on all paths (possible context leak)`.
  - Manually traced: `reaperCtx, stopReaper := context.WithCancel(...)` at
    line 1162 immediately starts `subagentReaper.Start(reaperCtx)` (a
    background goroutine polling `subagent_runs`). Same pattern for
    `runtimeReaperCtx, stopRuntimeReaper` at line 1182, starting
    `runtimeReaper.Start(runtimeReaperCtx)` (polls `agent_runtime`).
  - Both cancel funcs are only captured into the `Container` struct (so
    `Shutdown`/`Close` can call them) at lines 1398/1400, near the end of
    the constructor.
  - Two confirmed early-return paths execute between reaper start and struct
    assembly without calling either cancel func:
    `container.go:1242` (`durable agent recipes` error) and
    `container.go:1246` (`sync managed durable agents` error) — both call
    `stopCatalog()` for an earlier resource but not `stopReaper()` /
    `stopRuntimeReaper()`.
- Why it matters:
  - On either error path, `NewContainer` returns `(nil, err)`. The caller
    never receives a `*Container`, so it has no reference to call
    `Shutdown`. The two reaper goroutines keep running and keep querying
    SQLite indefinitely — nothing in the process can stop them short of
    process exit. If container construction is retried in a loop (e.g. a
    supervisor restart-on-error), each failed attempt leaks one more pair of
    goroutines and DB polling loops.
- Recommendation (direction only):
  - Ensure every error return between reaper start and struct assembly also
    calls `stopReaper()`/`stopRuntimeReaper()` — e.g. via `defer` with a
    "committed" flag cleared only on the success path, matching the existing
    `stopCatalog()` cleanup pattern already present at both flagged sites.
- Suggested verification:
  - Unit/integration test that forces `NewContainer` to fail after line 1182
    (e.g. inject a failing `NewDurableAgentRecipeService`) and asserts no
    goroutine/reaper activity survives the failed call (goroutine-count
    diff, or a hook into the reaper's tick to detect post-return activity).
- False-positive considerations:
  - If `NewContainer` failures are effectively fatal (process always exits
    immediately after a construction error today), the leak is real but
    inconsequential in practice. Worth confirming actual caller behavior in
    `cmd/nanite/main.go` before prioritizing — the guide's severity ranking
    treats this as a real lifecycle defect either way, but the fix's urgency
    depends on whether failed construction is ever retried in-process.

### GO-SEC-001 Reachable third-party vulnerability: OTLP HTTP exporter memory exhaustion

- Severity: medium
- Confidence: high
- Category: security | dependency
- Scope:
  - `go.mod`: `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp
    v1.41.0`
  - `internal/otel/otel.go:64` (`otel.Init`)
- Evidence:
  - `govulncheck ./...` (raw: `raw/govulncheck.log`), vulnerability
    GO-2026-4985: "Oversized OTLP HTTP response bodies can cause memory
    exhaustion." Fixed in v1.43.0. Call trace: `otel.Init` →
    `otlptracehttp.New` / `otlptracehttp.client.UploadTraces`.
- Why it matters:
  - If OTel export is enabled and the configured collector endpoint returns
    (or is spoofed/MITM'd into returning) an oversized response, the
    exporter can be driven into unbounded memory growth.
- Recommendation:
  - Bump `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp`
    (and likely sibling `go.opentelemetry.io/otel/*` modules for version
    consistency) to >= v1.43.0. Standard dependency bump, not a design
    change.
- Suggested verification: `govulncheck ./...` clean re-run after bump; existing
  otel integration tests, if any, re-run.
- False-positive considerations:
  - Reachability requires OTel export actually being enabled at runtime and
    a malicious/misbehaving collector endpoint — worth confirming whether
    the collector endpoint is operator-configured (trusted) or ever
    user/tenant-influenced.

### GO-SEC-002 Go toolchain pinned behind released stdlib security patches

- Severity: low
- Confidence: high
- Category: security | dependency
- Scope:
  - `go.mod`: `go 1.26.2`
- Evidence:
  - `govulncheck ./...` reports 13 distinct stdlib vulnerabilities (GO-2026-
    6218, 6091, 6090, 6089, 5972, 5856, 5039, 5037, 5026, 4982, 4980, 4971,
    4918 — full detail in `raw/govulncheck.log`), each fixed somewhere in
    `go1.26.3`–`go1.26.6`, all reachable from application code (HTTP server/
    client, TLS, template rendering, plugin signature parsing, MCP proxying).
- Why it matters:
  - None of these are code defects; they're stdlib fixes not yet picked up
    because the toolchain/module `go` directive is pinned at `1.26.2`. Low
    severity individually, but several (crypto/tls handshake limits, HTTP/2
    SETTINGS frame infinite loop, html/template XSS escaper bypasses) are
    meaningfully security-relevant classes.
- Recommendation:
  - Bump the toolchain/`go.mod` `go` directive to `1.26.6` (or latest patch
    at remediation time) as a standard patch upgrade.
- Suggested verification: `govulncheck ./...` clean re-run; full test suite
  and `-race` suite re-run after the toolchain bump (routine, low-risk).
- False-positive considerations: none — this is a mechanical version-currency
  gap, not a design question.

### GO-HYG-001 Full-repo uncapped lint has no enforcement point

- Severity: informational
- Confidence: high
- Category: repository-hygiene | testing
- Scope:
  - `lefthook.yml` (pre-commit/pre-push hooks), `Makefile` (`lint` target),
    absence of `.github/workflows/`
- Evidence:
  - `lefthook.yml` pre-commit `go-lint` runs `golangci-lint run --new
    --timeout 30s` (changed lines only). Pre-push runs only `go test ./...`.
  - `Makefile`'s `lint` target (`go vet` + uncapped `golangci-lint` +
    `staticcheck` + `errcheck` + `govulncheck`) is real and already
    configured correctly (`.golangci.yml` deliberately sets
    `max-issues-per-linter: 0` / `max-same-issues: 0` — a prior audit's fix,
    per the file's own comment), but nothing invokes `make lint` on any git
    hook, and there is no CI workflow directory in the repo at all.
  - Running it directly today surfaces 2,338 issues under the project's own
    already-approved linter set (`raw/golangci-baseline.log`): errcheck 284,
    gosec 590, govet 530, misspell 394, forbidigo 222, revive 68,
    staticcheck 78, unparam 35, unused 43, errorlint 47, exhaustive 20,
    nilerr 22, ineffassign 2, unconvert 3.
- Why it matters:
  - Whole-repo debt accumulates silently. `--new`-only linting means a file
    can carry an arbitrary number of pre-existing issues forever as long as
    no one touches those exact lines. There is currently no scheduled or
    gated point where the 2,338-issue baseline gets re-measured or driven
    down.
- Recommendation:
  - Direction only, architect decision: wire `make lint` (or a scoped subset)
    into a nightly/scheduled job or a merge-to-main gate per the guide's
    Tier B suggestion — not into pre-commit/pre-push, which the project has
    deliberately kept fast (<15s target stated in `lefthook.yml`'s header).
- Suggested verification: none needed pre-remediation; this is a process gap,
  not a code defect.
- False-positive considerations:
  - Entirely possible this is intentional — the project may already plan a
    separate CI system (outside GitHub Actions) that runs `make lint`; only
    the repo itself shows no evidence of it. Confirm before treating as a
    gap rather than a known, accepted tradeoff.

### 3.1 Lint-finding triage funnel (methodology demonstration)

2,338 raw findings is not a usable number on its own — most audits stop at
reporting it, which tells an architect nothing about what to actually do.
Re-ran with structured JSON (`raw/golangci-baseline.json`) and manually
sampled each large cluster (`raw/lint-triage-output.txt`,
`raw/cluster-samples.txt`) to turn volume into cause:

```
2,338 raw findings
  - 222  forbidigo                → known, already-tracked Phase-1-Wave-1
                                     migration worklist (AtomicWriteFile /
                                     ResolveUnder adoption) — pre-existing,
                                     not new debt discovered by this audit.
  - 502  govet/shadow              → sampled 20 across 10+ files; ALL 502
                                     are `err` re-declared in a narrower
                                     scope (`if err := f(); err != nil`)
                                     inside a function that already has an
                                     outer `err`. This is the standard,
                                     idiomatic Go error-handling shape, and
                                     `shadow` is a well-known noisy
                                     experimental govet analyzer on exactly
                                     this pattern. Zero of the 20 sampled
                                     were a real shadowing bug.
  - 199  errcheck (Close/Rollback) → `rows.Close()` / `tx.Rollback()` /
                                     `db.Close()` in defer/cleanup position.
                                     Standard Go practice to leave unchecked
                                     (a failed Close after a fully-read
                                     result set, or a Rollback after a
                                     successful Commit returning
                                     sql.ErrTxDone, carry no real risk).
  - 327  gosec G301/G304/G306      → inside `_test.go` files (test-fixture
          (test-file only)           writes/permissions/opens) — never
                                     shipped, not production risk.
  ---------------------------------
  = 1,088 actionable candidates
```

Of the 1,088, one further split matters: **394 are `misspell`** — purely
cosmetic (British/US spelling in comments/strings, e.g. `cancelled` →
`canceled`), 100% mechanically batch-fixable, zero architectural weight.
That leaves **694** requiring real judgment, which cluster into these
underlying causes rather than 694 independent problems:

| # | Cluster | Count | Verdict |
|---|---|---:|---|
| 1 | gosec G304 (path traversal via variable) in **production** code, outside the `pathsafe.ResolveUnder` forbidigo-tracked path scope | 68 | **Promoted to Top 10 — see GO-SEC-003 below.** Sample (`cmd/nanite/mcp_cmd.go`, `internal/agent/managed_files.go`, `internal/agent/managed_section.go`) reads files by variable path with no visible sanitization in the surrounding code. |
| 2 | gosec G301 + G306 (file/dir permission mode) in production code | 77 | Single policy question for the architect: should config/log/export writes standardize on a more restrictive mode (0600/0700) instead of the 0644/0755 literals used throughout? Not individually distinct bugs — one naming/policy decision would resolve most of these at once. |
| 3 | gosec G70x taint-analysis family (G702 command-injection, G703 path-traversal, G704 SSRF, G706 log-injection) in production code | 49 | Gosec's newest, most speculative rule family — highest both false-positive risk *and* highest potential real-severity if any are real. Needs dedicated manual security triage, not batch judgment; not collapsed into a single verdict here. |
| 4 | errcheck, non-cleanup (i.e. not Close/Rollback) | 85 | Genuinely heterogeneous (internal/api 28, cmd/nanite 20, internal/server 9, internal/worktree 6, ...) — no shared root cause found; stays a real per-site worklist for the package-review phase. |
| 5 | nilerr ("error is not nil but returns nil") | 22 | The guide's own named anti-pattern (§8). Each is a distinct call site and a plausible real bug; not collapsible — worklist item. |
| 6 | exhaustive (missing switch case over enum) | 20 | Each is a distinct enum/switch pair; not collapsible — worklist item, concentrated in `internal/service` (10). |
| 7 | staticcheck `SA*` (real analysis findings: 1×SA4006 unused-write, 5×SA1019 deprecated-API, 2×SA1012 nil-context) | 8 | Individually reviewed already — see §7 (SA4006, SA1019 called out). |
| 8 | staticcheck `QF*`/`ST*` (auto-fixable style/quickfix suggestions) | ~70 | Mechanical, `golangci-lint run --fix`-able, zero architectural weight. |
| 9 | govet/unusedwrite | 24 | Sampled: 13/24 in `internal/a2a/a2a_test.go` are struct-literal field writes consumed by later whole-struct use — a known analyzer blind spot, not a bug. Remaining 11 elsewhere not yet sampled. |
| 10 | unparam, ineffassign, unconvert, revive (idiom/style) | ~113 | Not sampled individually — mechanical Go-idiom cleanup, expected low severity per guide's own ranking (idiom ranks below duplication/responsibility/maintainability). |

**Net result: of 2,338 raw findings, roughly 9 clusters explain ~1,250 of
them (shadow, errcheck-cleanup, gosec-test-noise, forbidigo, misspell,
staticcheck-quickfix, permission-policy, unusedwrite, idiom-style) — leaving
~225 genuinely worklist-shaped items (errcheck-other, nilerr, exhaustive,
gosec-taint-family, staticcheck-SA) for case-by-case remediation, of which
the G304 production cluster is the one promoted to the Top 10 as a real
architectural finding, not just a lint count.**

### GO-SEC-003 gosec G304 path-traversal surface extends beyond the tracked `pathsafe.ResolveUnder` adoption scope

- Severity: medium
- Confidence: medium
- Category: security | architecture
- Scope:
  - 68 production-code gosec G304 findings (`raw/golangci-baseline.json`,
    filter `FromLinter=gosec`, `G304`, non-`_test.go`)
  - Representative sites: `cmd/nanite/mcp_cmd.go:32`, `cmd/nanite/
    plugin_cmd.go:482,491`, `cmd/nanite/plugin_logs.go:22`, `cmd/nanite/
    serve_autostart.go:82,202`, `internal/agent/managed_files.go:151,180`,
    `internal/agent/managed_section.go:33,87`
  - Compare: `.golangci.yml`'s `forbidigo` rules only enforce
    `pathsafe.ResolveUnder` adoption inside `internal/sandbox/`,
    `internal/mcp/`, `internal/service/install/` — none of the sites above
    are in that path list.
- Evidence:
  - Sampled 10/68: all are `os.ReadFile(path)` / `os.Open(src)` /
    `os.OpenFile(dst, ...)` where the path argument is a local variable
    (`filePath`, `src`, `dst`, `path`, `logPath`, `lockPath`) with no visible
    sanitization at the call site.
- Why it matters:
  - The project already has infrastructure (`internal/pathsafe.
    ResolveUnder`) and an active adoption tracker (`forbidigo`) specifically
    for this class of risk, but the tracker's scope predates or doesn't
    cover these call sites. Whether each one is actually reachable by
    untrusted input (vs. operator-supplied CLI flags, which is a much lower
    risk trust boundary) hasn't been individually verified here.
- Recommendation:
  - Direction only: have the architect (or whoever owns the Phase-1-Wave-1
    migration) walk the 68-site list and classify each by trust boundary
    (CLI-flag-driven vs. externally-reachable) — likely most CLI-command
    sites are low-risk and most `internal/agent/managed_*` sites (which read
    files by agent-slug-derived paths reachable from GUI/API/MCP) deserve a
    closer look given they're on a path that already other code treats as
    needing `pathsafe` protection elsewhere.
- Suggested verification: for each site classified "reachable from untrusted
  input," trace the actual call chain back to its origin before deciding
  whether to add `pathsafe.ResolveUnder`.
- False-positive considerations:
  - gosec's G304 rule fires on any variable-driven file open, regardless of
    whether the variable is already constrained (e.g. by an earlier
    allow-list check, a fixed known directory, or operator-only CLI input).
    A meaningful fraction of the 68 are plausibly already safe by
    construction — this finding is "worth a scoped human pass," not "68
    confirmed vulnerabilities."

---

## 4. Largest / most complex functions and files (mechanical signal, guide §14 thresholds)

Full audit-only golangci-lint run (cyclop/gocyclo/gocognit/maintidx/nestif/
dupl/funlen, thresholds per guide §15) in `raw/golangci-audit-complexity.log`;
summary in `raw/complexity-summary.txt`. None of the below have been manually
judged yet (essential vs. accidental complexity) — this is a worklist for the
package-review phase, not a verdict.

Issue counts (production code, excluding `_test.go`): cyclop 250 total
(review threshold 15+), gocognit 227 total (threshold 20+), funlen 69,
maintidx 25, nestif 59, dupl 95.

Sharpest cognitive-complexity outliers (>2x the guide's 20-point inspect
threshold):

| Function | File | Cognitive | Cyclomatic | Length |
|---|---|---:|---:|---:|
| `(*Executor).Run` | internal/workflow/executor.go:58 | 87 | — | — |
| `(*Service).Spawn` | internal/subagent/service.go:597 | 62 | 47 | — |
| `UnloadPlugin` | internal/plugin/host.go:1240 | — | 63 | 268 |
| `cmdServe` | cmd/nanite/main.go:94 | — | 45 | 392 |
| `(*workflowStepExecutor).ExecuteLLMStep` | internal/service/workflow_step_executor.go:123 | 48 | — | — |
| `(*BuiltinWorkflowEngine).execute` | internal/service/workflow_engine.go:185 | 46 | — | — |
| `validateCrossRefs` | internal/plugin/install/validate.go:255 | — | 35 | 117 |
| `(*Store).ForkSession` | internal/store/sessions.go:685 | 35 | — | — |
| `(*artifactStasher).StashSlot` | internal/service/slot_stash.go:133 | 34 | 24 | — |
| `RankTools` | internal/toolclient/ranking.go:64 | 34 | — | — |
| `evalPredicateNode` | internal/agent/reflexes/evaluator.go:66 | — | 33 | — |
| `ValidateAgentConfig` | internal/agentvalidation/validation.go:37 | — | 31 | — |
| `(*TeamRoutingService).InstallTeamRunRouting` | internal/service/team_routing.go:477 | 32 | — | — |
| `BaseSeeds` | internal/agent/reflexes/seeds.go:85 | — | — | 367 |

Note: `BaseSeeds` at 367 lines is very likely static seed-data construction,
not control-flow complexity — flagged by `funlen` alone (no cyclop/gocognit
hit), which is exactly the guide §5 "may be a false positive" case to check
manually rather than assume is a problem.

---

## 5. Dependency hot spots (mechanical, from `go list -json` internal-package graph)

Full table: `raw/fanin-fanout.tsv`. Fan-in = number of internal packages that
import this package; fan-out = number of internal packages this package
imports.

| Package | Fan-in | Fan-out | Prod LOC | Files |
|---|---:|---:|---:|---:|
| internal/store | 30 | 5 | 14,802 | 63 |
| internal/plugin | 18 | 7 | 6,908 | 23 |
| internal/safego | 12 | 0 | 135 | 1 |
| internal/agent | 11 | 2 | 1,334 | 8 |
| internal/brand | 11 | 0 | 107 | 1 |
| internal/fsutil | 10 | 0 | 180 | 1 |
| internal/chat | 8 | 12 | 5,380 | 26 |
| internal/mcp | 7 | 8 | 5,771 | 14 |
| internal/service | 3 | **64** | 31,654 | 83 |
| internal/api | 2 | 30 | 16,351 | 69 |
| internal/selftools | 3 | 22 | 8,517 | 22 |
| cmd/nanite | 0 | 39 | — | — |

`internal/store` and `internal/plugin` are the confirmed high-fan-in
("everything depends on this") candidates the guide's §3.2 asks to check.
`internal/service` and `internal/api` are the confirmed high-fan-out
composition-root/transport candidates — expected for their role, but their
LOC (31.6K / 16.3K) means the "does it only wire, or also implement
behavior?" question from guide §4 needs a real answer, not an assumption.

---

## 6. Package LOC / file-count table (top-level files only; packages with
subdirectories need a rolled-up pass — not yet done)

Full table: `raw/package-loc-top.tsv`. Top 10 by production LOC:

| Package | Prod LOC | Test LOC | Prod files | Test files |
|---|---:|---:|---:|---:|
| internal/service | 31,654 | 33,748 | 83 | 126 |
| internal/api | 16,351 | 9,680 | 69 | 46 |
| internal/store | 14,802 | 11,844 | 63 | 70 |
| internal/selftools | 8,517 | 9,364 | 22 | 34 |
| internal/plugin | 6,908 | 4,536 | 23 | 22 |
| internal/mcp | 5,771 | 5,399 | 14 | 20 |
| internal/chat | 5,380 | 5,118 | 26 | 26 |
| internal/subagent | 3,269 | 4,941 | 6 | 16 |
| internal/toolclient | 2,523 | 2,171 | 10 | 10 |
| internal/permission | 2,189 | 2,329 | 9 | 8 |

Test/prod LOC ratio is roughly 1:1 or better almost everywhere sampled so
far — a healthy signal, not a finding.

---

## 7. Testing / build baseline

- `go build ./...` — clean.
- `go vet ./...` — 4 lines of output, all the GO-LIFE-001 finding (2 distinct
  issues, each reported twice).
- `go test ./...` — all packages pass, 0 failures (`raw/test.log`).
- `go test -race ./...` — 5 packages (`internal/api`, `internal/messaging`,
  `internal/selftools`, `internal/service`, `internal/subagent`) hit `panic:
  test timed out after 10m0s` (Go's default per-package timeout) under
  race-detector instrumentation. Re-ran those 5 individually with
  `-timeout 25m` to get a real verdict (`raw/test-race-extended-timeout.log`):
  - **`internal/messaging`, `internal/selftools`, `internal/subagent` — now
    PASS cleanly.** Confirms these 3 were pure timeout artifacts (large
    suite + `-race` overhead against the default 10-minute ceiling), not
    hangs. Zero `DATA RACE` reports. Race-clean.
  - **`internal/api` and `internal/service` still time out at 25 minutes.**
    This is no longer explainable as "just a big suite" — see GO-TEST-001.
    **Race-cleanliness for these 2 packages remains genuinely unconfirmed.**

### GO-TEST-001 `internal/api`/`internal/service` test suites leak Container reaper goroutines on every run, blocking a `-race` verdict

- Severity: medium
- Confidence: high
- Category: testing | concurrency
- Scope:
  - `internal/api`'s 6 test files that call `service.NewContainer(...)`
    directly (`artifacts_test.go`, `loom_curator_wake_test.go`,
    `providers_test.go` [4 call sites], `tools_call_test.go`,
    `recovery_test.go`, `api_test.go`)
  - `internal/service`'s own test suite (home package of `Container`)
- Evidence:
  - In the original 10-minute timeout dump, `internal/api`'s goroutine dump
    alone contained 56 goroutines traced to `internal/service.NewContainer`
    reaper-start calls (verified via line-range bucketing against the other
    4 packages' dumps — zero elsewhere).
  - Grepped all 6 `internal/api` test files for `Shutdown`/`.Close(`: every
    `t.Cleanup` found closes the underlying `store` (`s.Close()`), **none
    call `Shutdown()` on the `*Container` itself** — confirmed by direct
    inspection, not inference.
  - In the 25-minute re-run, the combined `internal/api` + `internal/service`
    timeout dump contains **288** such goroutines (up from 56), across 776
    total dumped goroutines — consistent with accumulation growing across
    the run, not a fixed one-time cost.
  - Confirmed this is **not** a production defect: `cmd/nanite/main.go:747`
    does call `container.Shutdown()` on the real shutdown path, wired
    through `daemonLifecycle.Shutdown` — the real server tears down
    correctly. This is specific to the test suites never calling the
    already-correct cleanup API.
- Why it matters:
  - Every `internal/api` test that goes through a `NewContainer`-based setup
    leaves 2 permanently-running background goroutines (subagent reaper +
    agent_runtime reaper, each on its own DB-polling ticker) alive for the
    rest of that test binary's process lifetime. Across dozens of tests in
    a 46-test-file package, these accumulate. Under `-race`'s per-goroutine
    instrumentation overhead, the accumulation is severe enough that the
    package cannot complete a race-detector run even at 2.5x the default
    timeout. Practical effect: nobody can currently get a real "is
    `internal/api` race-clean?" answer by running the standard tool the
    guide itself recommends (§9.2).
- Recommendation:
  - Direction only: add `t.Cleanup(func() { container.Shutdown() })`
    alongside the existing store cleanup in each of the 6 call sites (and
    check `internal/service`'s own test suite for the same gap in its home
    package). Straightforward, low-risk test-only change.
- Suggested verification: after adding cleanup, re-run
  `go test -race ./internal/api ./internal/service` and confirm both
  complete within the default 10-minute timeout with zero `DATA RACE`
  reports.
- False-positive considerations:
  - None — directly confirmed by grep (no cleanup call exists) and by
    goroutine-count growth between the two independent timeout dumps.
- `go test -coverprofile=... ./...` — 60.9% statement coverage overall
  (`raw/coverage-func.log`); per-package breakdown not yet triaged against
  "which authorization/failure/lifecycle paths are untested" per guide §12.3.
- `go mod verify` — all modules verified.
- `go mod tidy -diff` — clean, no stale requirements.
- `deadcode ./...` (214 lines) / `deadcode -test ./...` (65 lines) — raw
  output captured, not yet triaged into real dead-code candidates vs. build-
  tag/platform-specific false positives.
- `staticcheck ./...` — 57 findings, mostly `U1000` (unused, largely in test
  helper stubs) plus one real `SA1019` deprecated-API call
  (`internal/plugin/subprocess/plugin_test.go:279`,
  `plugin.PluginError` deprecated) and one `SA4006` unused-write
  (`internal/service/tool.go:194`).
- `errcheck ./...` — 729 lines raw (not yet cross-referenced against the
  golangci errcheck subset of 284; different default exclusions).
- `gosec ./...` — 329 issues (43 HIGH / 172 MEDIUM / 114 LOW), raw JSON in
  `raw/gosec.json`, not yet triaged for false positives.

---

## 8. Package-cluster review findings (deep-dive phase)

Full guide-depth review dispatched across 13 parallel clusters covering
every remaining `internal/*` package plus `cmd/nanite`. Each cluster review
was independently briefed with the guide, this report (to avoid re-deriving
already-confirmed findings), and the relevant mechanical pointers (complexity
outliers, gosec/lint clusters) for its scope. Findings below are appended
verbatim (lightly reformatted) as each cluster completes — this section is
built incrementally, not all clusters have landed yet.

Cluster status tracker:

| Cluster | Packages | Status |
|---|---|---|
| Store | internal/store (+migrations, +seedcatalog) | **done** — §8.1 |
| Service (core/wiring) | internal/service: container + wiring half | pending |
| Service (execution) | internal/service: chat/workflow/team/tool half | pending |
| API | internal/api | pending |
| Plugin | internal/plugin (+all subpackages) | pending |
| MCP/tool-client | internal/mcp, mcpconfig, mcpserver, toolclient, tool, selftools | pending |
| Agent | internal/agent (+reflexes,builtin,override), agentvalidation, agentworkflow | pending |
| Chat | internal/chat, messaging, envelope, elicitation, coordination | pending |
| Execution | internal/subagent, worker, workflow, workflowrunner, dispatch, dispatcher, executor | pending |
| Memory | internal/memory, context, contextbroker, grounding, learnings, recover, recovery, loopdetect, reminders | pending |
| Security primitives | internal/sandbox, permission, secrets, pathsafe, fsutil, safego | pending |
| Runtime | internal/runtime, lifecycle, scheduler, background, server, worktree, workspace, cmd/nanite | pending |
| Infra | internal/config, effort, eval, inspector, describer, classify, filter, builders, truncate, slogx, otel, version, brand, crossapp, a2a, providercatalog, skill, skillvendor, task, llm | **done** — §8.2 |

### 8.1 internal/store

Scope reviewed: `internal/store` (63 production files, 14,802 LOC; 70 test
files, 11,844 LOC) plus `internal/store/migrations` (134 embedded `.sql`
files, no Go logic) and `internal/store/seedcatalog` (1 file, 78 LOC).

**Package-cohesion verdict:** `internal/store` is the application's single
SQLite persistence layer — one `*Store{DB, dbPath}` handle (only 2 fields,
`store.go:22-25`) with 349 methods across 63 files, backing ~84 exported
row-shaped structs across ~25+ distinct domain areas (sessions, agents,
teams, plugins, schedules, workflows, a2a tasks, grounding, trust, roles,
skills, todos, plans, reminders, durable agents, reflexes, user settings,
etc). Structurally *not* a classic god object — the struct itself carries no
scattered cross-subsystem dependencies — but it is squarely the guide's
§3.2 "gravitational package": the dominant caller pattern (confirmed: 76
direct `*store.Store` field/parameter references in `internal/service`
alone) is "inject the whole concrete `*Store`," not a narrower per-domain
interface. Two healthy counter-examples exist (`grounding.ConsultationLogger`,
`dispatch.TrustResolver` — consumer-defined, narrow, satisfied structurally).
Migrations are cleanly separated (134 pure `.sql` files, zero Go logic).
One systemic responsibility-boundary concern: several `*Store` methods
(`CreateAgent`, `applyMultiAgentDefaults`, `UpdateUserSettings`) carry real
business rules (default-value policy, domain-specific validation) inside the
persistence layer rather than a layer above it — see GO-STORE-001.

**Findings:**

#### GO-STORE-001 Nearly all callers depend on the concrete `*store.Store` handle rather than narrower domain interfaces
- Severity: medium | Confidence: high | Category: package-cohesion, dependency, architecture
- Scope: `internal/store/store.go:22`; 76 direct `*store.Store` references in `internal/service/*.go` alone
- Evidence: contrast with the two healthy consumer-defined-interface examples (`internal/store/trust.go:22` implements `dispatch.TrustResolver`; `internal/store/grounding_log.go:18,61` implements `grounding.ConsultationLogger`) — both narrow and consumer-defined, proving the pattern is already known in this codebase, just not the default.
- Why it matters: any signature change to any of the 349 methods is a potential review/recompile trigger for all 30 fan-in packages, even when a caller only touches 1-2 domain areas.
- Recommendation: architect to identify 2-3 highest-fan-out consumer domains and evaluate a narrow consumer-defined interface (mirroring `trust.go`/`grounding_log.go`) only where real pain (test friction, unwanted recompilation) is identified — not repo-wide.
- False-positive considerations: a single shared persistence facade is a defensible, normal choice at this scale; this is "worth an architect look," not a confirmed problem.

#### GO-STORE-002 `*Store`'s 349-method surface spans ~25+ unrelated domain concepts
- Severity: informational | Confidence: high | Category: god-object
- Scope: `internal/store/*.go`, 349 methods / 84 exported struct types counted directly.
- Why it matters: method-set breadth is well above the guide's own §14 "20+ methods → inspect" heuristic; practical cost is discoverability and single-compile-unit blast radius, not correctness.
- Recommendation: do not split merely for size (guide §1.1/§1.4 forbid this); a natural side effect if GO-STORE-001 is pursued, otherwise just an observation.
- False-positive considerations: file-per-domain-concept within one package is the guide's own example of a healthy large package; each file is internally cohesive.

#### GO-STORE-003 `DeleteAgentByID` silently converts every `GetAgent` error (not just not-found) into a successful no-op
- Severity: **high** | Confidence: high | Category: error-handling, correctness
- Scope: `internal/store/agents.go:1211-1221`; real caller `internal/plugin/agent_profiles.go:265-289` (`(*Host).SweepPluginAgentProfiles`, plugin-unload cascade cleanup)
- Evidence:
  ```go
  func (s *Store) DeleteAgentByID(id string) error {
      a, err := s.GetAgent(id)
      if err != nil {
          return nil   // swallows ANY error, not just not-found
      }
      return s.DeleteAgent(a.Slug)
  }
  ```
  Confirmed by golangci's `nilerr` linter (`raw/golangci-baseline.log:6421`). `GetAgent` wraps *any* Scan error (row-not-found or a genuine driver/I-O error) identically — no `errors.Is(err, sql.ErrNoRows)` branch exists to tell them apart.
- Why it matters: on plugin unload, `SweepPluginAgentProfiles` calls this to cascade-delete an agent profile and its dependent rows (sessions, tools, skills, reflexes, durable instances). A transient DB failure during the lookup silently looks like "already gone, nothing to do" — the sweep believes cleanup succeeded and logs nothing, defeating the documented full-cascade-cleanup guarantee.
- Recommendation: distinguish `sql.ErrNoRows` (legitimate no-op) from any other error (should propagate).
- Suggested verification: a test that makes the underlying query fail for a non-not-found reason and asserts a non-nil return; existing `TestDeleteAgentByID` only covers the true not-found case.
- False-positive considerations: the "return nil if row doesn't exist" contract is intentional per the doc comment — the bug is failing to distinguish that case from other errors, not the no-op contract itself.

#### GO-STORE-004 `ListPluginSettings` has two unchecked `json.Unmarshal` calls; `GetPluginSettings` checks the identical unmarshal
- Severity: low | Confidence: high | Category: error-handling, duplication
- Scope: `internal/store/plugin_settings.go:134-135` (unchecked) vs. `:46-51` (checked, same operation)
- Why it matters: a malformed `settings`/`schema` JSON row fails closed via `GetPluginSettings` but silently zero-fills via `ListPluginSettings` — inconsistent behavior for the identical data.
- Recommendation: align `ListPluginSettings`'s error handling with `GetPluginSettings`'s.
- False-positive considerations: low real-world risk (both columns are always written via `json.Marshal` internally, so a malformed row requires external tampering) but the inconsistency is real.

#### GO-STORE-005 Inconsistent `context.Context` propagation across the package
- Severity: medium | Confidence: high | Category: go-idiom, concurrency
- Scope: package-wide; worst offenders `sessions.go` (1004 LOC, includes `ForkSession`) and `agents.go` (1298 LOC, includes `CreateAgent`) — neither has a single context-taking method.
- Evidence: only 22/63 production files reference `context.Context` at all; plain `s.DB.Query/Exec/QueryRow` outnumber context-aware equivalents ~2:1 (231 vs 116). Contrast: `teams.go`'s 6 CRUD methods all take `ctx` and use context-aware DB calls; `sessions.go`/`agents.go`'s ~30+ methods take no `ctx` at all.
- Why it matters: HTTP-request/agent-turn cancellation can never reach the DB layer for the two hottest tables' 30+ methods — a genuine inconsistency (some files migrated to the context convention, others didn't) rather than a uniformly-missing feature.
- Recommendation: architect decision on whether to backfill `ctx` through `sessions.go`/`agents.go`, prioritizing paths reachable from cancelable request contexts.
- False-positive considerations: may be an intentional, not-yet-reached migration backlog rather than an oversight — worth confirming with the store's owner before treating as urgent.

#### GO-STORE-006 `SyncDurableAgentInstanceConfig` reads-then-branches-then-writes without a transaction, unlike its siblings
- Severity: low | Confidence: medium | Category: concurrency, lifecycle
- Scope: `internal/store/durable_agents.go:301-367`
- Evidence: no `Begin()`/transaction wraps the read-decide-write sequence, unlike siblings `SetDurableAgentInstanceStatus`/`SetDurableAgentInstanceLaunchState`/`ArchiveDurableAgentInstance` (`:369-434`), which correctly push the "still exists / still active" check into a single atomic `UPDATE ... WHERE ... AND status != 'archived'` + `RowsAffected()` check.
- Why it matters: a classic check-then-act gap (guide §9.1) if concurrent same-slug calls are possible.
- False-positive considerations: caller concurrency was not traced within budget — confidence is medium specifically because whether this is a live race (vs. a single-threaded reconciliation loop) is unverified.

#### GO-STORE-007 Mechanical "query → scan-loop → append" duplication across ~20+ list methods
- Severity: low | Confidence: high | Category: duplication
- Scope: ~20 files, 41 `dupl` hits confirmed inside internal/store (`raw/golangci-audit-complexity.log`).
- Evidence: sampled 2 pairs, confirmed genuine structural duplication of "scan N rows into a typed slice" — the per-type `scanX` helpers are already consistently used and correct; only the outer loop shape repeats. No generic helper exists.
- Recommendation: optional — a generic `scanRows[T any](...)` helper could collapse this, but every site is already individually correct; this is a DRY question, not a defect.
- False-positive considerations: per guide §27, do not treat correct, readable, repeated boilerplate as "must fix" without identified pain — unlike GO-STORE-004, no bug was found riding on this duplication.

#### GO-STORE-008 Confirmed dead code: 4 exported functions, one with a factually stale doc comment
- Severity: low | Confidence: high | Category: dead-code, comments
- Scope: `internal/store/skill_mode_filter.go:34,59,84` (`ParseSkillModeIDs`, `MarshalSkillModeIDs`, `SkillMatchesMode`), `internal/store/skills_source.go:23` (`ClassifySkillSource`)
- Evidence: all 4 confirmed unreachable by `deadcode` (`raw/deadcode.log:152-155`) and by direct whole-module grep (zero real call sites). `skill_mode_filter.go:18-21`'s doc comment claims these are "still consumed by `internal/service/ingest.go`'s frontmatter → mode_ids resolution" — **this is false**; `ingest.go` does not call any of the three (only a test-file comment mentions them).
- Why it matters: exactly the guide's §27 "trust comments over executable behavior" trap — the comment's specific factual claim about a live caller is wrong, which is worse than no comment for a future reader deciding whether it's safe to remove.
- Recommendation: owner to confirm whether `skill_mode_filter.go`'s trio is genuinely still needed (re-verify the real intended consumer) and whether `ClassifySkillSource`'s described FE gating was ever implemented (no FE-side equivalent found either) — retire or wire up, not a blind delete.
- False-positive considerations: the package's own comment shows the team already knowingly kept `SkillMatchesMode` with no caller ("mirroring the tool_enrichments precedent"), so 1 of 4 is a known accepted state, not new information — the other 3 plus the stale citation are the new finding.

#### GO-STORE-009 Pervasive task-ID/date references embedded in permanent production comments
- Severity: informational | Confidence: high | Category: comments
- Scope: ~40 `CW-YYYYMMDD-NNNN` references across 25 files; ~23 files with `TASKS/*.md` path references.
- Recommendation: not a blanket removal — many are load-bearing rationale (e.g. `user_settings.go:262-264`'s default-value explanation); architect could scope a trim pass for pure task-ID citations without added rationale.
- False-positive considerations: several (e.g. `store.go:80-93`, `agent_runtime.go:227-244`) are exactly the invariant/safety documentation the guide says to *keep* — this is a "scoped trim," not "comments are bad."

**Reviewed and found healthy:**
- **Transaction safety**: all 9 `Begin()`/`BeginTx()` sites verified to have a matching `defer tx.Rollback()` + success-path `Commit()`; `ForkSession`/`CopyMessages` deliberately do read-side lookups before opening the write transaction (documented, not an oversight).
- **No unsynchronized shared state**: zero `sync.*`/mutex usage, zero background goroutines in the package — correctness delegated to the single-connection pool + SQLite WAL/busy-timeout.
- **gosec: clean** — all 11 hits are confirmed false positives on manual read (3× G202 "SQL concatenation" are fixed-fragment builders with every value still parameterized; 8× G104 are the already-triaged Close/cleanup pattern).
- **Shared helper adoption**: `nullIfEmpty` used consistently 84 times across 17 files — not bypassed anywhere.
- **Migrations/seedcatalog separation** clean; 23 dedicated migration tests exercise real embedded SQL, not mocks.
- **Test realism**: `newTestStore` builds a real `*Store` via the real constructor and real migrations; tests assert externally meaningful post-migration/post-seed state.
- **Consumer-defined interfaces** (`TrustResolver`, `ConsultationLogger`) are the guide's §6.1 recommended shape, done correctly.

**Not covered / out of budget:** did not read all 63 files line-by-line (prioritized flagged outliers + tool-cluster pointers); did not trace all callers of `SyncDurableAgentInstanceConfig`; did not do the deeper §12.3 "which failure paths are untested" pass beyond noting 55.4% average coverage; `AgentStateStore` interface (`agent_state_store.go`) noted as a currently-zero-consumer interface with a stated future purpose — not flagged as a defect per §6.1/§27 guardrails against auto-flagging one-implementation interfaces.

### 8.2 Infra/utility sweep (internal/config, effort, eval, inspector, describer, classify, filter, builders, truncate, slogx, otel, version, brand, crossapp, a2a, providercatalog, skill, skillvendor, task, llm)

**Verdict:** overwhelmingly healthy — every package answers "what single capability?" cleanly except `internal/config` (three unrelated concerns under one generic name). `internal/skill` vs `internal/skillvendor` and `internal/recover` vs `internal/recovery` (see §8.11) were both checked for accidental naming collision and found to be deliberate, sequenced, non-overlapping.

**GO-INFRA-001** (low, package-cohesion/naming) — `internal/config` bundles three distinct concerns: `Config` (user/project runtime, reads project-root `nanite.yaml`), `AppConfig` (checked-in tunables, reads `config/nanite.yaml` — a **different file with the same base name**), and `layout.go` (pure XDG path resolution, no YAML at all). Real confusion risk: an engineer editing "the nanite.yaml config" could edit the wrong one. Recommendation: rename one file/struct to disambiguate, or split the package along the `Config`/`AppConfig` boundary.

**GO-INFRA-002** (low, dead-code/duplication) — `inspector.trafficLight` (`internal/inspector/service.go:266-276`) is unreachable in production (only its own test calls it); `internal/service/inspector_producers.go:56-63`'s `trafficLightFor` is a byte-for-byte reimplementation, created because `trafficLight` is unexported so `internal/service` couldn't call it. Two copies of a 3-line business rule to keep in sync by hand. Recommendation: export `inspector.TrafficLight` and have `internal/service` call it.

**GO-INFRA-003** (low, error-handling) — `internal/task/snapshot.go:94-121`'s `scanTask` silently discards `json.Unmarshal`/`time.Parse` errors on `metadata`/`created_at`/`updated_at` during durable-recovery `Restore` — a malformed row silently produces an empty `Metadata`/zero-value timestamp with zero observability. Note: sibling `LocalBackend.List`/`Snapshot` already treat similar failures as skip-and-continue without logging, so this may be a deliberate "best-effort restore" convention rather than an oversight — confirm intent before prioritizing.

**GO-INFRA-004** (medium, duplication/error-handling) — OpenAI streaming (`internal/llm/openai/stream.go:96-123`) aborts the **entire turn** on the first malformed tool-call-argument JSON (`EventError` then `return`, dropping any other queued tool calls, usage, and the terminal `EventDone`). Anthropic's equivalent path (`internal/llm/anthropic/stream.go:232-253`) degrades gracefully instead (`{"_raw": raw}` fallback, continues processing). Real, confirmed control-flow divergence between the two providers for the same failure class; whether the downstream chat-loop consumer handles a stream that ends on `EventError` without `EventDone` cleanly was not traced (out of scope). A `stream.go:104-105` comment suggests the OpenAI early-return may be a deliberate "fail loud" choice, not an oversight — confirm intent before treating as a defect to fix.

**Task-specific checks, all confirmed:**
- `internal/brand` (fan-in 11, 107 LOC): confirmed healthy — every one of 22 call sites across 11 packages is a bare constant or one of 3 pure path helpers; no hidden logic. Textbook single-source-of-truth shape.
- `internal/llm/anthropic` vs `internal/llm/openai`: high-level shape is shared but concrete mechanics legitimately differ (named SSE events + rate-tracker/circuit-breaker vs. chunk-delta accumulation with **no** rate-tracker by design — documented, tracked as a deferred parity gap in `followups.nanite.cw_20260508_0012`). Not a collapse-worthy duplication; the one real gap found in the comparison is GO-INFRA-004.
- `internal/a2a/a2a_test.go`'s govet/unusedwrite flag: **confirmed false positive** — struct-literal fields written but only a subset asserted afterward, an analyzer blind spot on test fixtures, not a logic bug (though it does mean those tests don't verify full round-trip correctness — a minor testing nit).
- `internal/skill` vs `internal/skillvendor`: **not overlapping** — confirmed via `TASKS/skills/*.md` + git log that `skillvendor` is a brand-new package from a later phase of the same migration, deliberately built ahead of its not-yet-landed consumer, with the name `skillvendor` chosen specifically to avoid confusion with `internal/store`. Zero production callers today, by design.

Minor informational notes (not filed as findings): `internal/builders/skill_builder.go:52-66` hand-builds a JSON array via `fmt.Sprintf("%q", p)` instead of `json.Marshal` (low-severity idiom nit); `internal/eval/scorer.go:114-119` has a 4-line local `truncate` helper conceptually overlapping `internal/truncate.Output` — not worth abstracting.

Not checked: complexity/dupl tooling not independently re-run for this scope (relied on REPORT.md's existing table); chat-loop consumer of `EventError`-without-`EventDone` not traced; `internal/llm/anthropic`'s `params.go`/`cache_plan.go`/`client.go` and `internal/llm/openai`'s `params.go`/`embed.go` only skimmed for size, not read line-by-line.

### 8.3 internal/service — composition-root / wiring half (container.go, store.go, context.go, events*.go, envelope_*.go, dispatch_wiring.go, delegation.go, role_cascade.go, ingest.go, session.go, todo.go, skill.go, agent*.go, embedder_select.go, elicitation_emitter.go, hint_dispatch_adapter.go, known_tools_*.go, inspector_producers.go, recovery_*.go, cli_structured_input_fallback.go, handoff_glass4*.go, a2a_*.go, internal/service/install)

**The central question, answered with evidence:** `Container` the struct is genuinely wiring-only — **2 methods** (`RefreshUtilitySettings`, a 6-line setter, and `Shutdown`, pure orchestration) despite **60 fields** (55 dependency handles + 5 lifecycle fields), clearing the guide's god-object field threshold by 2x on structure alone but failing the *behavior* half of the test. `internal/service` **the package** is emphatically not wiring-only — ~30 files in this half implement substantial real domain logic (a full slot-assembly decision engine in `context.go`, a complete A2A task-routing state machine in `a2a_task_manager.go`, the managed-config atomic-write/optimistic-concurrency contract in `agent.go`/`role_cascade.go`, reconciliation algorithms in `ingest.go`/`known_tools_*.go`). **The 31.6K LOC / fan-out-64 signal correctly points at the package, but mis-locates the mass on `Container` when the actual complexity is already factored into well-named, single-responsibility files.** Only one production caller of `NewContainer` exists (`cmd/nanite/main.go:372`).

**Cross-cutting note for architect attention:** `chatServiceImpl` (defined in `chat.go`, the sibling reviewer's scope) has **84 methods** total, dwarfing `Container`'s 2 — it is very likely the real god-object candidate in this package, and at least 4 files in *this* scope (`delegation.go`, `inspector_producers.go`, `handoff_glass4_fallback.go`, `recovery_pack_glue.go`) add methods onto it. See §8.4's GO-SVCEXEC-002 for the full field/method inventory.

**GO-SVCCORE-001** (medium, concurrency) — `(*chatServiceImpl).DelegateAndAggregate` (`delegation.go:349-386`) spawns one `safego.Go` worker per sub-task; if a worker panics *before* writing to its result channel, the collector's unconditional blocking `for range { &lt;-ch }` (no `select`, no timeout, no `ctx.Done()`) hangs forever — unlike its sibling `DelegateTask`, which has a proper timeout/cancel `select`. Recommendation: give the collector the same safety valve, or have each closure's deferred recover write a synthetic error result.

**GO-SVCCORE-002** (low, concurrency/lifecycle) — Inconsistent goroutine-lifecycle tracking: ~18 `safego.Go` call sites in `events_composite.go`/`delegation.go`/`agent_deps.go` are bare fire-and-forget spawns with no owner `Container.Shutdown()` can drain — `safego.Go`'s own doc says callers needing cancellation should use `lifecycle.Manager.Go` instead. The codebase already knows this matters: one specific wake-reactor spawn was deliberately migrated to a tracked lifecycle manager after a PR review flagged exactly this risk (`container.go:1133-1143`'s comment), but the fix wasn't applied to these other sites.

**GO-SVCCORE-003** (medium, dead-code/comments) — `internal/service/install/adapters.go`'s `snapshotAdapterTargets` is dead (zero production callers, confirmed via `deadcode` + grep), but `adapter_cleanup.go:31-33`'s doc comment on `cleanupRemovedAdapters` (which performs a real destructive operation — stripping/deleting content from the project's `CLAUDE.md`/`AGENTS.md`) falsely claims "*Snapshot for rollback is handled by the existing snapshotAdapterTargets() pass that runs before cleanup*" — traced the only production call path (`freshScaffold`) and confirmed no such call exists, and no `Rollback` function exists anywhere in the repo to consume a snapshot even if one were taken. A textbook guide §27 "trust comments over executable behavior" trap. Blast radius is bounded (only strips Nanite's own previously-written managed markers, not arbitrary user content).

**GO-SVCCORE-004** (medium, security) — A2A push-notification webhook URL (`a2a_push_notifier.go:152-166`, sourced from caller-supplied `TaskSubmitRequest.PushNotificationConfig.URL` at task-submit time) has **no validation** — no scheme allowlist, no private-IP/localhost block — before `http.NewRequestWithContext` + `client.Do`. Gosec's G107 rule doesn't catch this because it targets `http.Get`/`http.Post` directly, not this idiom, so it wasn't previously surfaced. This is the classic webhook-callback SSRF pattern the guide names explicitly (§10). Confidence is medium specifically because the A2A endpoint's auth boundary (whether only trusted peer agents can submit tasks) was not independently verified — out of this reviewer's scope.

**GO-SVCCORE-005** (informational, repository-hygiene) — `//nolint:gosec` on `recovery_envelope_sink.go:223` correctly suppresses under `golangci-lint` but a bare `gosec` binary run (which the guide itself recommends at §10/§25) doesn't understand that syntax, producing one avoidable false "HIGH" (G404) in any raw `gosec ./...` run. Worth a runbook note, not a code fix.

**GO-SVCCORE-006** (medium, testing — corrects a plausible-but-wrong assumption in the base report) — `internal/service`'s own `-race` timeout is **not** the Container-reaper-leak mechanism GO-TEST-001 describes: confirmed via exhaustive grep that zero of the package's 126 test files ever call `NewContainer`. GO-TEST-001's recommended fix (add `t.Cleanup(func(){ container.Shutdown() })`) will do nothing for this package's timeout — the real cause needs separate investigation (candidates: sheer suite size × `-race` overhead across 776 test functions, and/or the untracked-goroutine accumulation from GO-SVCCORE-002).

**GO-SVCCORE-007** (informational, dead-code) — `WithAgentCycleKindForAPI`'s context value (`agent_cycles.go`) is written by 2 real `internal/api` call sites but has zero readers anywhere — the function's own doc comment concedes this is intentional forward-looking scaffolding. Not flagged as a defect; worth tracking so it isn't mistaken for wired behavior.

**GO-SVCCORE-008** (low, naming/idiom) — `internal/service/install/adapters.go:159,169`: a local `var any bool` shadows the Go builtin `any` type for the rest of that function's scope. Harmless today (revive already flags it); trivial rename recommended.

**GO-SVCCORE-009** (informational, comments) — several files (`ingest.go`, `known_tools_backfill.go`, `cli_structured_input_fallback.go`, `agent_deps.go`) carry long (15-50 line) task-ID/PR-review-history comments approaching implementation-diary territory, though each explains a real invariant rather than being pure filler — worth a future trim pass, not urgent.

**Reviewed and found healthy:** `Container`'s own shape (2 methods, no embedded domain logic beyond `Shutdown`'s orchestration); `store.go`'s 20-small-interface composition into one `Store` interface (textbook §6.1 segregation — only the composition root and `chatServiceImpl` take the full composite); `install/install.go`'s `copyTree` (the flagged cognitive-26 outlier — one coherent recursive algorithm, essential complexity) and its atomic staging/rename flow; `a2a_task_manager.go`'s "derive state from real execution, never cache independently" design with idempotent cancel semantics; `a2a_push_notifier.go`'s bounded retry/backoff (aside from GO-SVCCORE-004's URL gap); `events.go`'s narrow consumer-defined interfaces; error handling across `a2a_*.go`/`recovery_*.go` (consistent `%w` wrapping, nil-safety guards, no swallowed-to-nil errors beyond what's flagged above).

Not checked: full field/method inventory of `chatServiceImpl` (belongs jointly to both service reviewers — see §8.4); rest of `internal/service/install/` beyond `install.go`/`adapters.go`/`adapter_cleanup.go`; fuzz/coverage-gap analysis for this scope.

### 8.4 internal/service — chat/workflow/team/tool/subagent execution-engine half (chat*.go, workflow_*.go, team_*.go, tool*.go, slot_stash.go, stream.go, subagent_*.go, durable_*.go, managed_durable_configs.go, messaging_reactor.go, messaging_sink.go, reflex_schedule_hook.go)

**GO-SVCEXEC-001** (**high**, complexity) — `(*chatServiceImpl).generateResponse` (`chat_generate.go:122-2028`) is a **~1,900-line, cognitive-complexity-458, cyclomatic-225-228, maintainability-index-0** function — the single highest-complexity function found anywhere in this audit by a wide margin (~5x the next-sharpest outlier, `workflow/executor.go`'s `Run` at cognitive 87), and it was **missed entirely by the base report's mechanical triage table**. Coverage is 36.5% — the majority of its branches (provider-error, compaction-recovery, plugin-cancel paths) are exercised only incidentally. Structurally: session load → agent resolve → model/provider resolve → tool selection → slot assembly → one unbroken ~970-line tool-use loop with budget enforcement, plugin hooks, telemetry construction, provider streaming, error/compaction recovery, and tool dispatch all inlined, despite the file already having an established pattern of extracting helpers for other logic. **Verdict: largely accidental complexity** — real domain complexity exists, but the gap between this and the file's own `workflow_engine.go`-style DAG runner (comparable domain complexity, cleanly factored into named steps) is exactly the essential-vs-accidental line the guide asks reviewers to judge. Recommendation: architect review of whether the loop's phases can be extracted into named methods without changing the mutate-in-place state-machine shape; prioritize closing the coverage gap on error/recovery branches independent of any refactor.

**GO-SVCEXEC-002** (**high**, god-object) — `chatServiceImpl` (`chat.go:214-392`) has **52 fields** and **84 methods** — more than double the guide's own god-object thresholds on both axes. Distinct responsibility clusters visible in the field list: session/stream/context plumbing, plugin/command/process/task wiring, permission/path-grant state, embedding-warning dedup, tool-schema/cache/model-catalog state, 9 distinct `sync.Map`s for PTY/agent-runtime session lifecycle, route-dispatch wiring. Fails the guide's §4 "only wiring vs. embedded behavior" test decisively — `generateResponse` (GO-SVCEXEC-001) and dozens of other multi-hundred-line methods are direct behavior on this type. Recommendation: the PTY/agent-runtime session-lifecycle cluster (7 fields, already cohesive and clearly named) is the most promising extraction candidate — the codebase has direct precedent for exactly this kind of split (`StreamManager` was already pulled out of what the file's own comment describes as "Engine's 6 sync.Map fields").

**GO-SVCEXEC-003** (medium, dead-code/architecture) — Team semantic routing (`team_routing.go`'s entire production surface — `TeamRoutingService`, `SendToSlot`, `InstallTeamRunRouting`, etc.) has **zero production callers** — confirmed by `deadcode` and direct grep. The file's own header comment names the intended wiring point (a future HTTP launch handler); that handler has since shipped (`internal/api/team_runs.go`'s `handleLaunchTeam`) but calls only `LaunchTeamRun`, never `InstallTeamRunRouting`. `TASKS/teams/HANDOFF.md:30` **already names this exact risk** and asks a future reader to verify it — this audit did, and confirmed the gap is real. Transitively, `(*TeamRunLauncher).ResolveLazySlot` (a separately-flagged complexity outlier) is also unreachable in production, though `deadcode` doesn't catch it (its enclosing type *is* live, so the tool's heuristic doesn't drill into per-method reachability). A fully-implemented, extensively-tested feature (`@slot` addressing, priority-ranked routing rules) currently cannot fire in the running system.

**GO-SVCEXEC-004** (medium, duplication) — Two near-identical "resolve policy → busy-check → trigger" reactors have diverged on mechanism: `resolveMessageWakePolicy` (`messaging_reactor.go`, newer) is built on the shared `override.Resolve` cascade primitive; `resolveSubagentCompletionPolicy` (`subagent_reactor.go`, older) hand-rolls the identical three-tier walk independently, re-implementing the same validate-or-warn pattern the newer function delegates. Self-acknowledged as siblings in the package comment ("same shape... different default policy") but only one was migrated onto the shared mechanism — a future merge/precedence bug fix is likely to land in one and be missed in the other.

**GO-SVCEXEC-005** (medium, testing) — `buildRepairConfig` (tool_cache_wiring.go, resolves the C2 auto-repair pipeline's provider/model/timeout via a 4-level fallback chain) and `discoverManagedDurableAgentConfigs` (managed_durable_configs.go, boot-time config-directory loader) are both **0.0% test-covered**, standing out against a package where coverage is otherwise highly uneven-but-not-uniformly-thin (sibling functions in the same files sit at 77-95%). Both are pure/deterministic and cheap to cover.

**GO-SVCEXEC-006** (low, error-handling) — Several best-effort store writes are silently discarded with no log line; most are low-impact telemetry, but `durable_wake.go:232-233`'s write marking a one-shot schedule `expired` is the actual mechanism the file's own doc comment says prevents a documented double-fire race with the external scheduler's tick — silently swallowing its failure undermines the guarantee the comment claims.

**GO-SVCEXEC-007** (low, comments) — `chat_reflex_dispatch.go`'s 99-line package header and `team_routing.go`'s 43-line priority-clamp justification both duplicate content that (by their own admission) also lives in the referenced task file's Work Log — a consistent house style across this half of the package, not an isolated lapse.

**Complexity-outlier verdicts (essential vs. accidental) for the remaining pre-flagged items:** `ExecuteLLMStep` (essential — clean bounded loop, well-factored helpers), `BuiltinWorkflowEngine.execute` (essential — the guide's own "700-line state machine may be cohesive" example, applies directly), `SelectForAgent` (essential-borderline — real 9-phase pipeline with documented ordering constraints), `StashSlot` (essential — carefully-reasoned atomic cache-write protocol, one of the stronger examples of complexity earning its keep), `InstallTeamRunRouting`/`resolveAgentSlugForSlot` (essential, but currently unreachable per GO-SVCEXEC-003), `buildRepairConfig` (essential, untested per GO-SVCEXEC-005), `toInteger` (essential/mechanical — a coercion table, matches the guide's own "cyclomatic 20 may be clearer than fragmented helpers" example precisely), `LaunchTeamRun`/`ResolveLazySlot` (essential), `resolveMessageWakePolicy` (essential), `discoverManagedDurableAgentConfigs` (mild accidental — nesting-driven, low real complexity, untested per GO-SVCEXEC-005).

**God-object table:** `chatServiceImpl` 52 fields/84 methods (god object, GO-SVCEXEC-002); `toolServiceImpl` 5/13, `TeamRoutingService` 3/7, `TeamRunLauncher` 4/7, `BuiltinWorkflowEngine` 3/~10, `StreamManager` 9/— — all healthy, narrow, focused (StreamManager notably a good precedent for decomposing chatServiceImpl).

**Reviewed and found healthy:** context handling is consistently idiomatic package-wide (ctx-first everywhere, never stored in a struct — verified via grep across all files in scope); `stream.go`'s channel-close-driven goroutine lifecycle; `chat.go`'s generation launch/lifecycle bridging via `lifecycle.Manager.Go` + a bounded cancel-bridge goroutine (correct, and distinct from the container.go leak); `chat_tool_executor.go`'s bounded concurrent-tool-execution fan-out; `workflow_engine.go`'s lock-free concurrent-read reasoning during fan-out (explicitly and correctly justified in comments); `durable_agent_runtime_controller.go`'s consistent nil-safety and error handling; narrow consumer-defined interfaces (`WorkflowRunStore`, `WorkflowProviderResolver`, etc.); the reflex-dispatch vs. route-dispatch pre-loop split (investigated for possible duplication, found to be a deliberate, documented separation with a real import-cycle justification, not accidental overlap).

Not checked: `subagent_runner.go` (941 lines), `subagent_runner_boot.go`, `durable_agents.go`, `durable_agent_recipes.go` (1,401 lines), `chat_loop_state.go`, most of `chat_boot_drive.go`/`chat_tool_executor.go` beyond the concurrency sites already traced — sampled structure rather than full line-by-line reads; regression-test-quality verification (§12.2, does a named historical-bug test fail when reverted) not attempted for any specific bug in this scope; `internal/runtime/agent`'s `Manager.Shutdown` (which `observeSessionForRecovery` depends on to unblock its `Wait()`) not independently verified.

### 8.5 internal/api (16,351 LOC / 69 files, fan-out 30)

**Cohesion verdict:** genuinely thin transport layer for the large majority of ~280 routes/315 methods — decode → call domain service → encode, with error-classification as the main "logic." `RegisterRoutes` (339 lines) confirmed a pure mechanical route table, no branching — funlen false positive. Not uniform, though: real domain validation lives in the transport layer in three places (schedules field validation — already self-documented by the file's own comment as a known, deferred consolidation need across 3 independent producers; settings enum validation; memories filtering, which is also where a real bug lives — GO-API-005). Two separate handler-state types (`catalogState`, `pluginManagerState`) duplicate `API`'s response-boilerplate rather than sharing it, though both are correctly wired onto the same auth/CORS/body-limit middleware chain (verified, no auth bypass).

**GO-API-001** (low, security) — Autocomplete file listing (`autocomplete.go`) walks `project.RepoPath` with **no validation anywhere in the chain** (`handleCreateProject`/`handleUpdateProject` store it verbatim). Any authenticated caller can point a project at `/` or `$HOME` and enumerate filenames/sizes/mtimes (metadata only, no content) up to depth 8. Severity kept low because in the realistic single-operator local-deployment trust model, "authenticated caller" and "the person who set repo_path" and "someone who already has a shell on this machine" are the same person.

**GO-API-002** (low, security/testing) — `handlePlaceArtifact` accepts an unrestricted `StoragePath` at write time with zero sanitization, unlike its sibling `handleUploadArtifact` which correctly uses `pathsafe.ResolveUnder`. Not currently exploitable for content disclosure (the *download* path independently confines via `pathsafe.ResolveUnder`, confirmed via the existing regression test), but it's an inconsistency inviting future drift, and this specific write path has **zero test coverage** despite being the one artifact-write path that trusts a raw filesystem path. Doc comment frames it as an internal/trusted-caller surface, but nothing distinguishes that caller from any other authenticated one.

**GO-API-003** (low, security) — Catalog plugin install (`handleCatalogInstall`) fetches `entry.ArchiveURL` — sourced from an operator-configured catalog source's manifest, not the direct HTTP caller — via `http.DefaultClient` with **no explicit timeout** (bounded only by the inbound request's own context, if any) and no host/scheme restriction beyond http/https. Standard package-manager-registry SSRF shape; exploitation requires an already-added (or compromised/MITM'd) catalog source, a real but higher-bar precondition. Response size is correctly capped at 100 MiB.

**GO-API-004** (medium, package-cohesion/duplication) — Schedule and settings field validation live in the transport layer; the schedules case is self-documented in-repo as a known, real duplication across 3 independent producers (HTTP handler, reflex hook, self-tool handler) validating "the same conceptual fields... at different call sites... flagged here as a real follow-up candidate, not done in this task" — strong first-party corroboration this is tracked debt, not a new discovery.

**GO-API-005** (medium, correctness) — `handleListMemories` caps its underlying fetch to `limit+offset` **before** applying `status`/`q` filters, then reports `total: len(out)` (the post-filter page length, not a real match count). If matching records exist beyond the initial capped window, the endpoint silently under-returns below the requested `limit` even though more matches exist, and `total` is unconditionally misleading for any UI computing page counts from it. A straightforward code-order bug, not a hypothesis — confirmed by direct reading.

**GO-API-006** (low, duplication) — Response/decode boilerplate duplicated across 3 parallel handler-state types (`API`, `catalogState`, `pluginManagerState`); 2 of the 3 duplications are architecturally justified (different receiver types), but 3 `*API`-receiver call sites (`provider_manage.go`, `plugin_config.go`) bypass the existing shared `a.decode` helper for no apparent reason.

**GO-API-007** (medium, duplication) — Harness v1 durable-agent start/resume/wake handlers (`harness_v1.go`) are confirmed byte-for-byte duplicates (via `dupl`) of the native `/api/durable-agents/*` handlers — same decode target, same service call, same tail, hand-copied rather than delegating. Possibly a deliberate protocol-stability choice (insulating an external control-plane contract from internal refactors) but no comment states that rationale.

**GO-API-008** (low, duplication/security-consistency) — The plugin-UI static-file route (`plugins.go:107-120`) hand-rolls its own `Clean`+`HasPrefix` path-confinement check instead of using `pathsafe.ResolveUnder` — functionally sound against `..`/absolute-path traversal, but (unlike `pathsafe.ResolveUnder`) doesn't call `EvalSymlinks`, so a symlink inside an installed plugin's `ui/` dir pointing outside its root would not be caught. Requires an already-installed plugin (privileged action, and subject to signature verification elsewhere — see GO-PLUGIN-001/002 below for where that verification is actually broken).

**GO-API-009** (informational, duplication) — Confirmed 7 `dupl`-flagged pairs within `agent_capabilities.go`'s 4 parallel CRUD families (known-tools/known-skills/procedures/knowledge-seeds) — mechanical, low-risk, a judgment call on whether a generic helper is worth the abstraction cost.

**GO-API-010** (low, error-handling) — `bookmarks.go:90` uses `err != sql.ErrNoRows` instead of `errors.Is` — would silently misclassify a legitimate "not found" as an internal error if the underlying call is ever wrapped with `%w`.

**Security context reviewed and confirmed sound:** body-size limits (10 MiB default / 32 MiB for documented upload prefixes, enforced in `internal/server`, correctly matched to the two relevant routes); CORS allowlist; auth (uniform Basic Auth with two narrowly-scoped, correctly-implemented loopback-only exemptions — `tools/call` and `example/task-updates` — both read `net.ParseIP(host).IsLoopback()` against `r.RemoteAddr` before doing anything else); archive extraction (`extractTarGz`/`extractZip`) — zip-slip/tar-slip protected via `pathsafe.ResolveUnder` per entry, entry-count and per-file/total decompressed-size caps, genuinely solid. The blanket-auth-covers-everything design (no per-endpoint role tiers) is explicitly self-documented in-repo as an accepted local-single-operator tradeoff, not a silent gap.

**Complexity outliers — all manually judged:** `RegisterRoutes` (false positive, confirmed), `handleUpdateAgent`/`mergeBuilderProfileInput` (essential — ~25 independent optional-field checks each, no real branching depth), `handleCatalogInstall`/`handleInstallArchive` (essential — genuine multi-phase workflows with per-phase error/progress emission), `handleEnvelopeRespond` (essential — well-designed concurrency-safe claim/dispatch state machine, one of the better-designed handlers in the package), `handleListMemories`/`handlePatchSchedule`/`handleUpdateSettings` (mixed — length driven by real per-field domain rules, but the *location* of that logic is the actual finding, see GO-API-004/005).

**Testing quality:** `artifacts_test.go` sampled in depth — genuinely high-quality real-HTTP regression tests against a real store/filesystem, with two tests explicitly labeled as regressions for a named prior audit finding. The one gap: `handlePlaceArtifact` has zero equivalent tests (GO-API-002).

Not checked: ~40 of 46 test files beyond decode-helper-usage counting; `internal/server`'s own auth/CORS/body-limit implementation (read for context, not independently audited — out of assigned scope); 2 smaller `dupl` pairs (`plans.go`↔`todos.go`, `schedules.go`↔`teams.go`) not read in full; `cmd/nanite/main.go`'s route-registration-vs-listen ordering.

### 8.6 internal/plugin and all subpackages (catalog, install, subprocess, devmode, scaffold, allplugins, builtin/*)

**Cohesion verdict:** `internal/plugin` core is a coherent single-capability "plugin host" package — not a grab-bag. Subpackages are each cleanly single-purpose. **The real, headline problem is not file layout — it's that two independent implementations of "plugin catalog install" exist, and the one reachable from the GUI is the weaker one.**

**GO-PLUGIN-001 (CRITICAL, security)** — The GUI/API-driven catalog install path (`POST /api/plugins/catalog/install` — the actual "Plugin Manager" marketplace UI users click through) **bypasses signature/checksum verification by default**. `VerifyChecksum` returns nil if no checksum is specified ("user-uploaded trust model" per its own comment); `handleCatalogInstall`'s signature branch only runs `if sourcePublicKey != "" && entry.Signature != ""` — if the source has no configured public key (which is the **default state of the seeded "official" catalog source** — its `INSERT` statement never sets a `public_key`), neither branch runs at all: no check, no warning, install proceeds. This code path never references `devmode.HostDevSigningBypass` and is not gated by the `devmode` build tag — identical behavior in production and dev builds. Confirmed reachable: the frontend's `CatalogBrowser`/`PluginManager` components call this exact endpoint. Confirmed via git history that a genuinely solid, `devmode`-gated, fail-closed signature pipeline (Ed25519, `KeyRing`, expiry/revocation) was built later (2026-04-13) for the **CLI** install path (`nanite plugin install`) and the original API handler was simply never migrated onto it. Zero test coverage for this handler at all, unlike its sibling handlers in the same package which have explicit path-traversal regression tests.

**GO-PLUGIN-002 (CRITICAL, security)** — The same handler also has an **unconfined path-traversal write**: `target := filepath.Join(cs.pluginsDir, entry.Name)` (a plain `filepath.Join`, not `pathsafe.ResolveUnder`) where `entry.Name` comes straight from an untrusted, unvalidated `catalog.yaml`. The **sibling handlers in the same file** (`handleInstall`, `handleInstallLocal`) were already fixed for this *exact* bug class, with inline comments citing "audit finding: Critical — path traversal" — but `handleCatalogInstall` was never given the same fix. Combined with GO-PLUGIN-001 (no signature/checksum required by default), a malicious or compromised catalog entry can direct plugin files to be written to an attacker-chosen filesystem path with attacker-controlled content. The archive-extraction step itself is soundly defended (`pathsafe.ResolveUnder` per entry) — the vulnerable point is specifically the final `copyDir` from the safely-extracted temp dir to the unguarded final `target`.

**GO-PLUGIN-003** (high, duplication/architecture) — Direct architectural consequence of 001/002: two full parallel "fetch catalog → verify → download → extract → install" implementations exist side by side (`internal/plugin/catalog.go`+`signature.go`, weaker, feeding only the vulnerable API handler; `internal/plugin/catalog/`+`install/`, stronger, feeding only the CLI). Exactly the guide's named "GUI/API/CLI code implementing business rules independently" pattern, here with a real security-severity consequence, not just style drift. Recommendation: converge the API handler onto the stronger pipeline; the old one has no other callers today.

**GO-PLUGIN-004** (medium readability + low concurrency, complexity) — `UnloadPlugin` (cyclomatic 63, 346 lines): essential complexity dominates — a documented, carefully lock-disciplined 18-category teardown sweep, each category correctly reasoned about why it runs inside vs. outside the host mutex (several sub-registries take their own locks). ~10 of the 18 categories are still hand-inlined map-iterate-delete logic that could be extracted into small `sweepXByPlugin` helpers mirroring a pattern the file already uses for 4 other categories — would shrink the complexity score without changing behavior. Separately, a real narrow TOCTOU gap: the dependency check runs under an early lock/unlock, `p.Unload()` then runs lock-free by design (documented, to avoid deadlock on re-entrant plugin calls) — a concurrent `LoadPlugin(B)` where B depends on the plugin being unloaded can pass its own dependency check in that window, leaving B loaded with a now-missing dependency. Traced as a real, narrow race, not speculative; practical impact is an unenforced ordering invariant, not a crash.

**GO-PLUGIN-005** (low, complexity) — `validateCrossRefs` (cyclomatic 35, 126 lines): mechanically repetitive (9 near-identical per-manifest-section uniqueness checks) but not incorrect — a textbook "guide §5 may be clearer than fragmented helpers" case; a generic `checkUniqueKeys[T]` helper could collapse ~80 lines with no behavior change, not urgent.

**GO-PLUGIN-006** (informational, god-object) — `Host` has 38 fields / 123 methods across 9 files, above both guide thresholds, but is a single coherent domain ("everything a plugin can register") — roughly a quarter of the registration categories are already factored into dedicated sub-registry types with their own locks (`cardRulesRegistry`, `panelRegistry`, `FilterRegistry`, `MutablePluginMux`); the remaining ~12 stay raw maps directly on `Host`. This inconsistency is *why* `UnloadPlugin` ends up hand-inlined (GO-PLUGIN-004) — the established in-repo pattern is the natural extension path if the architect wants to reduce `Host`'s footprint.

**GO-PLUGIN-007** (low, duplication) — The 4 CLI-ecosystem adapter plugins (`adapter-claude/codex/gemini/opencode`) duplicate ~35-45 lines each of manifest-loading/registration/lifecycle boilerplate, byte-for-byte aside from names/strings (`adapter-nanite-native` genuinely diverges with real additional logic, correctly excluded from this finding). A shared `plugin.LoadEmbeddedManifest`/`plugin.BasePlugin` helper could remove most of it without touching per-adapter logic; some boilerplate is structurally unavoidable given the `init()`-based self-registration model.

**GO-PLUGIN-008** (low, dead-code/error-handling) — `user_settings.allow_unsigned_plugins` is stored and API-exposed but **never actually wired into the CLI signature verifier** — `SignatureVerifier.AllowUnsigned` is left at its zero-value `false` at the one production construction site, despite `verify.go`'s own doc comment describing the setting as the intended wiring. Opposite-direction of a security risk (stricter than documented, not weaker) — a functional gap in the documented dev workflow, and a stale comment.

**gosec G70x cluster (task item 4):** directly queried the raw JSON — **zero** G70x (command-injection/path-traversal-taint/SSRF/log-injection) hits anywhere in `internal/plugin`'s scope; the repo-wide 49-hit cluster is entirely in other packages (`cmd/nanite/plugin_cmd.go` etc., confirmed elsewhere by the runtime-cluster review as CLI-operator-trust false positives). `internal/plugin`'s own gosec footprint (96 hits, mostly G301/G304/G306 permission/path patterns) was sampled and every checked site traced to a pre-sanitized path (`resolvePluginAssetPath`'s absolute-path-rejection + `Clean`+`Rel` double-check) or a locally-trusted config read — confirmed false positives, not exhaustively re-verified for all ~22 G304 hits.

**Reviewed and found healthy:** CLI plugin-install signature verification is genuinely fail-closed and correctly build-tag-gated — traced `HostDevSigningBypass` end to end from the `!devmode`-tagged constant through `verify.go`'s constant-folding short-circuit to the `Makefile`'s `build:` target (no devmode tag, with an explicit "never add this tag" comment) to the actual deployed build strategy; both build-tag configurations have dedicated regression tests. `internal/plugin/install/extract.go`'s `TarGzExtractor` is a genuinely well-defended archive extractor (absolute/`..`/symlink rejection, size/entry-count/compression-ratio caps, `O_EXCL` writes, setuid-bit stripping). `internal/api/plugins.go`'s own `extractZip`/`extractTarGz` (feeding the *vulnerable* catalog-install handler) are independently sound at the extraction step — the GO-PLUGIN-002 bug is specifically in the post-extraction `copyDir`, not extraction itself. Subprocess lifecycle (`internal/plugin/subprocess/manager.go`) has bounded, race-considered shutdown with no unbounded hang path. `internal/plugin/catalog/trust.go`'s `KeyRing` is a clean, correct trust store.

Not checked: full trust review of all ~22 G304 hits individually; `internal/plugin/scaffold` and the 6 builtin widget/demo plugins beyond a structural skim; `internal/plugin/subprocess/transport.go`/`protocol.go`'s JSON-RPC layer beyond the lifecycle path; whether `internal/api/catalog.go` is reachable without auth in this instance's actual deployed configuration (out of scope, `internal/server`).

### 8.7 MCP / tool-client / self-tools domain (internal/mcp, mcpconfig, mcpserver, toolclient, tool, selftools)

**Cohesion verdict:** `internal/mcpconfig`/`internal/mcpserver` are clean, single-purpose, correct mirror-images of each other (client-consuming vs. server-hosting). `internal/mcp` is mostly cohesive but does double duty (generic MCP client library *plus* 3 of 4 first-party tool implementations, while the 4th — self-tools — deliberately lives in its own package; an inconsistency, not a defect). **`internal/tool` is unhealthy**: its package doc actively misdescribes the architecture — see GO-MCPTOOL-001. Explicitly checked for duplicate tool-routing (the dispatch's specific ask) and found **none** — exactly one live dispatch switch per logical server, all reached uniformly through `mcp.Manager.ExecuteTool`, with the CLI-subprocess self-tool proxy correctly forwarding rather than reimplementing.

**GO-MCPTOOL-001** (medium, dead-code/duplication/cohesion) — `internal/tool`'s canonical `Tool`-interface/builder architecture (`tool.go`, `builder.go`, `register.go`, `adapt.go`, `yaml_loader.go`) is **entirely dead** — every exported symbol flagged unreachable by `deadcode`, confirmed zero production callers beyond `ResultCache` (the package's only live export). A prior task's own comment already reached this conclusion (*"not wired into the live runtime"*) and left it in place. Yet `tool.go`'s package doc still actively claims this is "the primary way to construct tools in Go code" — false of the current runtime, where the real live mechanism is `mcp.Manager`'s registry + per-transport dispatch switch, an entirely different design. A future engineer could easily wire into the dead path or build a second, genuinely-competing mechanism.

**GO-MCPTOOL-002** (medium, dead-code/duplication/complexity) — Reasoning-augmented tool selection (`RankTools`/`SelectWithSignals`/`SelectToolsAugmented`) is dead in the production request path — its former consumer (a debug SQL row) was removed by an earlier task, per the code's own comment. The real production selection entry point doesn't call this path at all. **This explains 2 of the base report's pre-flagged, previously-unjudged complexity outliers** (`RankTools` cognitive 34, `SelectWithSignals` cognitive 25) — both are real, coherent algorithms that would have been judged "essential" without ever surfacing they're unreachable. Wiring cost is still paid on every boot regardless (skills-dir load, memory-recaller construction feed only this dead path).

**GO-MCPTOOL-003** (low, dead-code/duplication) — `tool_knowledge.go`'s entire curated-catalog intent-matching mechanism (405 lines) has zero callers — a **third** parallel "what tools match this intent" mechanism alongside the live `intent.go` keyword scorer and the dead `RankTools` (002).

**GO-MCPTOOL-004** (low, dead-code/comments) — `mcp/elicitation.go`'s package doc describes a client-side direction (`ClientElicitMiddleware`) that **does not exist anywhere in the codebase** — the type is purely aspirational documentation. The functions implementing that described direction (`parseElicitationCreate`, `routeClientElicitation`, etc.) are confirmed dead under both normal and `-test` reachability. Server-side elicitation (the other half) is confirmed live.

**GO-MCPTOOL-005** (informational, dead-code/performance) — Per-turn tool-name context is written every single tool dispatch (`WithTurnToolNames`, real production caller) but the reader (`TurnToolNamesFromContext`) has zero callers — small but needless per-call cost; the write side's own comment frames this as deliberate scaffolding for a future check.

**GO-MCPTOOL-006** (medium, god-object) — `SelfToolsTransport` is the **strongest god-object candidate in this cluster**: **31 fields**, **81 methods**, a single flat `CallTool` switch dispatching to ~62 tool names spanning ~15 largely-unrelated capability domains (todo/plan, messaging, subagent-dispatch, background jobs, workflow execution, panels, python execution, elicitation, learning capture, builder wizard, reactions), implemented as real 2,431-line business logic, not thin adapters. Fails the guide's "wire vs. implement" test the same way `chatServiceImpl` does (§8.4).

**GO-MCPTOOL-007** (informational, god-object) — `ToolClient` has 26 methods across 4 files backing 3 self-described responsibilities ("selection, permissions, and execution") — lower severity than 006 since methods are genuinely tool-selection-adjacent with no unrelated concerns mixed in; recorded for the architect's holistic pass, not flagged as confirmed drift.

**GO-MCPTOOL-008** (medium, security) — `callGrep`'s gosec G122 hit is **real, not a false positive**: `resolveAllowed` correctly validates the top-level `dir` argument (symlink-aware, delegates to `pathsafe.ResolveUnder`) once before the walk begins, but the `filepath.Walk` callback then calls `os.Open` on every discovered file with **no per-entry re-validation** and no symlink check — a symlink planted anywhere inside an already-granted directory would be dereferenced, leaking file *content* outside the grant boundary even though the grant boundary itself was validated correctly. `callGlob` has the same gap but lower severity (metadata only, no content read). Requires either write access to the granted directory or a pre-existing malicious symlink — a local-filesystem TOCTOU class, not remotely triggerable via MCP input.

**GO-MCPTOOL-009** (low, concurrency/lifecycle) — `Manager.RemoveServer` holds the registry-wide lock for the full duration of subprocess kill+reap (`Process.Kill()`+blocking `Wait()`, no timeout) — every other `Manager` operation for *unrelated* MCP servers queues behind the same lock while one server tears down. Reachable from plugin hot-unload. Narrow-window, not reproduced as an actual hang.

**GO-MCPTOOL-010** (informational, duplication) — Self-documented, deliberately independent second evaluation of the same `dispatch_to_agent` reflex rows (one in `internal/service/chat_reflex_dispatch.go`, upstream; one in `internal/selftools`, inside `task_execute` itself) — the code's own comment argues this is intentional (two genuinely different decision points), not accidental, though nothing tests that the two evaluators can never diverge.

**GO-MCPTOOL-011** (low, duplication/naming) — `DevServerName = "dev"` independently declared in both `internal/mcp` and `internal/toolclient`, despite the latter already importing the former and being able to reference it directly.

**GO-MCPTOOL-012** (low, duplication) — `Manager.ExecuteTool`/`ExecuteToolOnServer` textually duplicate the entire post-`CallTool` result-processing pipeline (assemble → validate size → span attributes); both call sites are genuinely necessary (different valid callers), only the shared tail is duplicated.

**GO-MCPTOOL-013** (low, error-handling) — `mcpconfig.Export` silently drops 2 `json.Unmarshal` errors on stored `Args`/`Env` — part of the base report's already-tracked errcheck cluster, cited here with a precise location.

**Duplication synthesis (5 independent tool-classification mechanisms found across the cluster):** of 5, only 2 are live (`stash.BuiltinCategorizer` for bucketing, `toolclient.SelectByIntent` for keyword-scoring selection) and they serve genuinely different purposes — no live conflict today. But 3 dead, textually-independent classification schemes sitting in the tree (register.go's category maps, `tool_knowledge.go`, `RankTools`) alongside the 2 live ones is real drift risk: `register.go`'s hardcoded tool lists are already stale (reference tools since removed elsewhere).

**Security review:** MCP trust-tier system (2 MiB/512 KiB/256 KiB/128 KiB ceilings by tier) confirmed real and correctly wired, not just documented — validated at both discovery and execution time, unknown/imported servers correctly default to the strictest tier (fail-closed). Response-size bounding is real at both transport levels with unit tests at the boundary. Subprocess env isolation is real (allowlist, not full host-env inheritance). GO-MCPTOOL-008 is the one confirmed gap.

**Reviewed and found healthy:** the full trust-tier pipeline; stdio/HTTP transport response-size handling including a previously-fixed goroutine leak (with regression tests); `internal/mcpconfig`/`internal/mcpserver`; single-path tool dispatch (the thing this cluster's dispatch most explicitly asked to hunt for); `internal/tool/stash`/`internal/tool/intent` (live, cohesive, correctly separated from the dead parent-package machinery); `callWebFetch`'s SSRF defense (essential complexity, well-designed); a prior task's collapse of three independent envelope-marker hand-scans onto one shared helper — direct evidence this exact anti-pattern has been caught and fixed here before.

Not checked: `internal/selftools/reactions` subpackage implementation depth; `self_tools_python.go`'s sandboxed-execution security boundary; `code_exec_tools.go`; no fuzz corpus for the JSON-RPC/`.mcp.json` decoding boundaries the guide's §13 flags as candidates.

### 8.8 internal/agent (+builtin, +override, +reflexes), internal/agentvalidation, internal/agentworkflow

**Cohesion verdict:** all four cleanly single-capability. `agentvalidation`'s own doc comment states its narrow scope exists specifically to avoid an import cycle between `store` and `toolclient` — a real, checkable architectural reason. `agentworkflow`'s `doc.go` explicitly disambiguates itself from the unrelated `internal/workflow` package up front. `internal/agent`'s fan-in of 11 traced to real importers: 5 of 11 are the CLI-adapter plugins implementing its shared interface (healthy interface fan-in), the rest genuine domain consumers — not a "helpers"-shaped gravitational package.

**GO-AGENT-001 (HIGH, security)** — Agent `slug` is **never validated for path-safety** before being joined into a managed-config file path, on **both** create and update. Full traced chain: `POST /api/agents` copies `req.Slug` verbatim with only an empty-check → `agentvalidation.ValidateAgentConfig` runs but **never references `Slug` anywhere in its body** → `AgentConfigService.Create` calls `agent.ManagedAgentPath(configRoot, slug)`, which does a plain `filepath.Join(configRoot, "agents", slug+".md")` with **no character allow-list, no traversal rejection** → `WriteManagedAgentProfile` → `atomicWriteFile` writes fully attacker-controlled YAML+markdown content to the resulting path. `configRoot` itself is fixed/operator-controlled — `slug` is the only attacker-controlled variable in the entire chain. **`handleUpdateAgent`'s variant is more severe**: its rename path has no "already exists" guard the way Create does, so a crafted slug can **overwrite** an existing arbitrary `*.md` file, not just create a new one. A slug-format allow-list regex (`^[a-z0-9]+(?:-[a-z0-9]+)*$`) already exists elsewhere in the codebase (`internal/builders/agent_builder.go`) gating a *different* endpoint (the agent-builder wizard) — it was simply never applied to the direct CRUD endpoints. The one mitigating factor: the final path component is always `.md`-suffixed, constraining (not eliminating) real-world impact to `*.md` files the process's OS user can write. The auth boundary on `POST/PUT /api/agents` was not independently verified (out of this cluster's scope) — reachable population unknown, but the code path itself performs zero slug sanitization once a request lands.

**GO-AGENT-002** (high, testing) — Directly tied to 001: **every function in `managed_files.go` and `source_class.go` is at 0.0% test coverage**, confirmed via the coverage report and confirmed by directory listing that no test file exists for either — including `Classification.IsWritablePath`, the codebase's own trust-boundary gate for "is this a writable managed path" (used defensively elsewhere, but never on the write path GO-AGENT-001 describes). A fix for 001 currently has no safety net.

**GO-AGENT-003** (low, dead-code) — 3 exported symbols (`EnsureManagedDirs`, `UserManagedAgentPath`, `ValidationResult.Error`) confirmed unreachable under both normal and `-test` reachability — genuinely dead, not just untested.

**GO-AGENT-004** (low, naming) — `permissions_test.go`'s only remaining tests (after a prior removal task) test an unrelated concern (`ParentDispatchAllowlist`), not permissions — stale filename, guide §18 case.

**GO-AGENT-005** (low, naming/correctness) — A devmode-only builtin agent still stamps the legacy `Source="builtin"` value that the sibling package's own doc says is retired (`"internal"` is the current convention) — traced the classification consequence: this specific profile falls through to `ManageClassManaged` (GUI/API-editable) instead of the hidden/read-only treatment every other embedded harness profile gets. Devmode-only blast radius.

**GO-SEC-003 per-site trace (assigned follow-up work):** the 4 read-side gosec G304 hits the base report flagged (`managed_files.go`, `managed_section.go`) traced to **low risk** — one is gated behind `Classification.IsWritablePath` before the call; the others' production callers (5 CLI-adapter plugins' project-sync flow) trace back to the install/adopt flow, not arbitrary input. One caveat found: `internal/selftools/self_tools_transport.go`'s `callInstallProject` takes `project_dir` directly from a self-tool call's arguments with no validation, meaning an **agent** (not just an operator) can point the managed-section writer at an arbitrary directory via fixed filenames — narrower than 001 (fixed filenames, no traversal *within* a slug) but real. The much more serious finding turned out to be on the *write* side of a related call graph — see GO-AGENT-001 above, which is the actual headline result of this trace.

**Complexity outliers — all manually judged, all essential/healthy** except where a Slug-validation gap was the real problem, not the complexity itself: `evalPredicateNode`/`evalNameWindow` (pure dispatch tables, healthy), `EvaluateState`/`Apply` (clean orchestration/state-machine, healthy), `Resolve` (genuine XACML-style combining-algorithm implementation, essential — optional readability extraction noted, not required), `EmitFirings` (essential), `ValidateAgentConfig` (essential as a validation checklist — the problem is what it's missing, not its shape), `BaseSeeds` (confirmed false positive — pure data literal, zero control flow).

**God-object check:** `agent.Definition` has ~29 fields but only 1 method — a legitimate config/DTO mirroring a real external YAML format, not a dependency magnet; correctly judged not a god object despite crossing the field-count heuristic. No other candidates found.

**Concurrency:** zero goroutines spawned anywhere in this cluster's 6 packages (confirmed via grep) — nothing to flag.

**Error handling:** consistently idiomatic throughout; one previously-fail-open path (`Resolve`'s combining-algorithm fallback) is already correctly `slog.Warn`-logged per a prior fix task — noted as healthy, not re-flagged.

**Reviewed and found healthy:** package cohesion across all six directories; `internal/agent`'s fan-in (traced, confirmed healthy); no god objects beyond the already-judged-fine `Definition`; all complexity outliers essential; zero concurrency risk; idiomatic error handling; no naming stutter; `reflexes` package doc's proactive resolution of its own historical naming collision with a retired predecessor package — a good example of the guide's §18 ask, already self-resolved.

Not checked: HTTP-layer auth/middleware boundary for `POST/PUT /api/agents` (GO-AGENT-001's severity partly depends on this — flagged as an open question, not guessed at); full per-test-body regression-quality read; `agentworkflow.LoadRegistryDir(dir)`'s own directory-traversal reachability (distinct question from the agent-slug finding, lower priority given budget).

### 8.9 internal/chat, internal/messaging, internal/envelope, internal/elicitation, internal/coordination

**Cohesion verdicts:** `messaging`/`envelope`/`elicitation`/`coordination` are all cleanly single-purpose (messaging is called out as one of the most consistently well-documented packages reviewed in the whole audit). `internal/chat` is the domain's legitimate "vocabulary + assembly" package (fan-in 8) but carries real breadth beyond that: an outbound HTTP telemetry client, OS-process lifecycle management, and a full task-decomposition/orchestration subsystem (`Decomposer`/`Orchestrator` — confirmed **actively wired**, not dead) all live here too. Confirmed **no import cycle** with the 4 subsystem packages it pulls in for response-handler wiring (`subagent`, `elicitation`, `mcp`, `plugin`) — this is a "wide waist" architecture question for the guide's §3.1, not a broken-layering defect.

**GO-CHAT-001** (low, duplication/architecture) — Three independent packages (`chat`, `envelope`, `plugin.Host`) each hold their **own separate copy** of the shared `*envelopes.Registry` pointer, requiring `main.go` to remember 3 manual setter calls with nothing enforcing they stay in sync. Low risk today (one composition-root site, no reload path yet) but shaped like a bug waiting for a future hot-reload/test-isolation feature.

**GO-CHAT-002** (medium, duplication) — `chat.replayContent` and `recovery/pack.MessagePlainText` implement the **identical** StructuredMessage-JSON-unwrap algorithm — one using the full shared type, the other a hand-rolled anonymous struct re-deriving the same two fields. Self-documented as intentional (avoiding a cross-package import) via a comment in `chat/structured.go` — but the reviewer's own dependency check found no actual cycle risk in that direction, so the avoidance may be unnecessary caution rather than a hard constraint. If the wire contract changes, only one of the two copies is guaranteed to update.

**GO-CHAT-003** (low, duplication/hygiene) — `internal/elicitation/service_test_helpers.go` has no `_test.go` suffix, so it **compiles into the production binary** — including `elicitWithDuration`, a near line-for-line reimplementation of `Elicit`'s body that could silently diverge from real `Elicit` behavior on any future change. Straightforward naming fix (`export_test.go` pattern), not a design problem.

**GO-CHAT-004** (low, duplication/error-handling) — `ElicitUserInput`/`routeClientElicitation` (in `internal/mcp`) are near-duplicate ~25-line bodies with **inconsistent `context.Canceled` handling** — one swallows it into a graceful cancel result, the other surfaces it as both a cancel payload *and* a non-nil error. Downstream consumer of the second path's error wasn't traced — unclear if the inconsistency is currently user-visible.

**GO-CHAT-005** (low, dead-code) — `coordination.PrefixLock`/`PrefixState`/`LockTTL` declared with zero consumers anywhere — looks like a planned-but-never-built "resource lock" feature layered on `CoordStore`, distinct from the real, consumed `PrefixHeartbeat`/`PrefixTask`/`PrefixWorker` constants.

**GO-CHAT-006** (low, comments/dead-code) — `hint_catalog.go`'s package doc claims the loader reads `config/think-hints/hints.yaml`; the actual `//go:embed` directive embeds a **different** file (`internal/chat/hints/hints.yaml`). The two are currently byte-identical, but no build step, generator, or code path anywhere copies one into the other — an operator editing the file the doc comment points them to would see zero runtime effect.

**GO-CHAT-007** (informational, hygiene) — 12 files in this cluster fail `gofmt -l` (part of the pre-existing repo-wide 122-file gap already covered by GO-HYG-001, not cluster-specific).

**GO-CHAT-008** (informational, architecture) — `internal/chat`'s wide fan-out (11 internal packages) mixes broadly-consumed wire-vocabulary types with subsystem-specific response-handler wiring in the same package — not a cycle, not urgent, but means a caller wanting only `chat.Envelope`/`ResponseV1` transitively depends on the entire subagent+elicitation+mcp+plugin graph anyway.

**GO-CHAT-009** (informational, concurrency) — `elicitation.Service.Elicit`'s three-way `select` has a theoretical response/timeout race inherent to any `select`-based timeout (Go picks pseudo-randomly among simultaneously-ready cases); negligible at production's 5-minute default timeout, proportionally larger (still very unlikely) at the sub-second timeouts the GO-CHAT-003 test helper uses.

**Explicitly checked and cleared (no finding):** response-handler routing duplication with `messaging`/`coordination` — checked, **not found** (genuinely different dispatch shapes, no overlap). `internal/envelope`'s registry pattern — not ceremony, a legitimate startup-configured shared-resource pattern genuinely swapped between production and test. God-object scan — no candidates (largest is `Envelope`, a 15-field pure wire-format DTO with no behavior, correctly judged a false positive for the responsibility concern the threshold exists to catch). Handoff state machine (`messaging.ApproveHandoff`) — reviewed and **healthy**: transactional, idempotent, with dedicated regression tests including one added specifically to catch a prior silent-failure class. Elicitation block-until-response — reviewed and **healthy**: always cleans up via defer, never leaks a goroutine, correctly handles the double-respond race.

**Reviewed and found healthy (bonus, found while reading):** `internal/messaging/subscribe.go`'s doc comment documents a *previously-fixed* race directly in the code — exactly the guide's "why an apparently simpler implementation is unsafe" invariant-comment ask, not bug-archaeology. `envelope_marker.go` — a verified, genuine DRY collapse of three previously-independent hand-scan implementations across `service`/`api`/`mcpserver` onto one shared helper (direct evidence a real instance of this audit's core concern has already been caught and fixed here). Interface audit — all consumer-defined, narrow, several with explicit rationale comments; none flagged as ceremony. Mechanical complexity signal for this cluster confirmed genuinely low (manually verified, not just trusting the absence of a mechanical-scan hit).

Not checked: `routeClientElicitation`'s downstream error-consumer path; `config/think-hints/hints.yaml` git-history intent; cluster-scoped `dupl` output filtering (relied on manual reading); `internal/plugin`'s own envelope-schema wiring beyond the one cited registry-triplication site.

### 8.10 internal/subagent, internal/worker, internal/workflow, internal/workflowrunner, internal/dispatch, internal/dispatcher, internal/executor/envelope_render

**Cohesion verdict:** every package in this cluster answers "what single capability" cleanly. Explicitly checked the guide's named "multiple ways to launch/stop/resume the same entity" risk across all 5 launch-shaped mechanisms found here and traced how they relate rather than assuming overlap: **no accidental duplication found** — `subagent.Spawn`, `worker.SpawnFull`, `dispatch.ExecuteTask`, and `workflowrunner.Launch` are genuinely different callers/shapes, and #1/#2 both correctly converge on the single low-level `dispatcher.Dispatcher.Run` door by an explicit, documented prior unification effort — this is the architecturally correct resolution of exactly the risk the guide asks reviewers to hunt for, already in place.

**`(*Executor).Run` — internal/workflow/executor.go, cognitive 87 (the base report's previously-unjudged sharpest outlier):** **Verdict: mostly essential, one clearly extractable accidental component.** The core algorithm (level-synchronized concurrent DAG execution with skip-propagation, gating, retry, cancellation) is one cohesive state machine — splitting it would only distribute the same complexity. But the ~70-line per-step goroutine closure is defined inline, nested 4 levels deep, which is exactly the shape that inflates cognitive-complexity scores disproportionately to real reading difficulty; extracting it (and the dependency-skip-check block) into named methods would materially cut the score without touching the shared state machine. **Concurrency verified race-free by manual trace** (the one shared-state read after `wg.Wait()` is correctly protected by WaitGroup happens-before semantics, confirmed by tracing defer-registration order) — `go test -race -count=5` also passed clean. One recommendation: the happens-before reasoning is correct but undocumented; a one-line comment would prevent a future refactor from accidentally introducing a real race.

**`(*Service).Spawn` — internal/subagent/service.go, cognitive 62/cyclomatic 47 (the base report's second-sharpest outlier):** **Verdict: essential domain logic wearing an accidental single-function shape.** Every phase (arg validation, role-gate, recursion-depth cap, timeout/retry resolution, trust resolution, approval-gate branching) is individually simple and already delimited by its own substantial rationale comment in the source, citing specific tickets — extraction into named helpers (`resolveTimeout`, `resolveRetryPolicy`, `resolveTrust`, `handleApprovalGate`) would be low-risk and behavior-preserving, since the phase boundaries already exist conceptually. **This full end-to-end read is what surfaced the two real lifecycle bugs below** — plausibly easier to catch with the phases already isolated and independently testable.

**GO-EXEC-001** (medium, concurrency) — `Approve()` **bypasses the fan-out concurrency semaphore entirely**: `Spawn`'s ungated paths correctly acquire a slot from the documented cap-of-3 semaphore before dispatching, but `Approve` launches its dispatch goroutine directly with no slot acquisition. Multiple approvals in quick succession (human or automated) can drive concurrently-executing subagent runners past the stated cap, defeating the resource-exhaustion protection it exists to provide. No test found covering this specific gap (existing fan-out tests only exercise the `Spawn` path).

**GO-EXEC-002** (medium, concurrency/lifecycle) — A run cancelled **while still queued** for a spawn slot is not actually stopped — only relabeled after it finishes running anyway. Traced in full: the run row is marked `running` and broadcast via `emitStatus` *before* the slot semaphore is even acquired; the cancellation hook (`cancelers[run.ID]`) is only registered *after* the slot is acquired. If an operator calls `Cancel(runID)` on a run the UI already shows as "running" but which is actually still queued, the DB flips to `cancelled` but the runner proceeds to fully execute once capacity frees up, silently wasting real compute/tool budget — the terminal status is eventually correctly reconciled by `finalizeRun`'s re-read logic (data integrity is not at risk), but the run itself was not interrupted. The existing test suite covers a *different* cancellation trigger (the caller's own context expiring while queued, correctly handled) but not an operator-issued `Cancel` on an already-broadcast-as-running-but-still-queued run.

**Lower-priority complexity outliers, all judged essential/healthy on manual read:** `(*Service).execute` (well-commented context discipline), `(*Manager).SpawnFull` (correctly race-guarded against concurrent Cancel/Shutdown, fixes a documented prior leak), `convertStep` (1:1 with a real type vocabulary), `(*ParallelStep).Execute` (proportionate to genuine concurrent-handler responsibilities).

**GO-EXEC-003** (informational, naming) — Three "workflow"-branded packages (`internal/workflow`, `internal/workflowrunner`, `internal/agentworkflow`) implement three different execution models. The maintainers were **already aware**: `agentworkflow/doc.go` explicitly documents why it wasn't named/nested as a `workflow` subpackage, specifically to avoid this exact collision. Still worth architect awareness as a real grep/mental-model cost even though the naming choice was deliberate, not an oversight.

**GO-EXEC-004** (low, naming) — `internal/dispatch` vs `internal/dispatcher` — similar names, genuinely different well-documented concepts; flagged per guide §18 for awareness only, no rename recommended without architect review.

**God-object check:** `subagent.Service` (14 fields/29 methods) — just under the field threshold, all serving one coherent domain via narrow single-method dependency interfaces; correctly judged **not** a god object. `dispatcher.Dispatcher` — explicitly, deliberately a single-field struct by design. `worker.Manager` — 6 fields, single domain. No god objects found in this cluster.

**Error handling:** consistently strong — fail-closed trust-boundary behavior throughout (untrusted refuses outright per an explicit "must NOT be bypassed" doc comment; a trust-resolver error defaults to the *safer*, approval-required tier, not a bypass; a parentage-lookup error for the recursion cap also fails closed). Sentinel errors used correctly with `errors.Is`/`errors.As` throughout — no string-comparison error handling found.

**Testing quality:** `internal/subagent`'s ~1.5x test-to-prod LOC ratio confirmed **genuine, not padding** — sampled all 122 test functions across 16 files; real behavioral/concurrency/lifecycle coverage (reaper idempotence, fan-out semaphore edge cases, retry taxonomy, trust boundaries) via real test doubles, not reimplemented logic. The two coverage gaps found map exactly to GO-EXEC-001/002.

**Dynamic verification:** `go test -race` run (count=3-5) on every in-scope package except `internal/subagent` itself — all clean. `internal/subagent`'s own `-race` run did not complete within this review's budget (consistent with the base report's finding that it needs an extended timeout); relying on the base report's already-confirmed clean extended-timeout pass plus this reviewer's own manual trace of the Spawn/execute/finalizeRun/Cancel shared-state paths (no data race identified by inspection — GO-EXEC-001/002 are ordering/invariant bugs, not races). Reaper's own Start/Stop confirmed correctly idempotent beyond what the base report already covers in its caller.

**Comments and naming:** unusually high quality across this whole cluster — consistently explain invariant/ordering rationale rather than pure diary content; several files proactively document design decisions and naming-collision avoidance (a genuine strength, credited explicitly).

**Reviewed and found healthy:** all 6 in-scope packages race-clean under repeated runs; `dispatch.ExecuteTask`'s clean guard-clause validation and sentinel errors; the multi-surface/single-runner convergence architecture; `workflowrunner.Launch`'s deliberate, well-justified independence from the agent-boot machinery; trust/fail-closed discipline throughout Spawn.

Not checked: independent confirmation of `internal/subagent`'s own `-race` cleanliness within this review's time budget (relied on the base report's result); `internal/agentworkflow`'s own internal quality (out of assigned scope); `internal/service/dispatch_wiring.go`/`subagent_runner.go`/`chat_route_dispatch.go` beyond wiring-confirmation reads; no live reproduction test attempted for GO-EXEC-001/002 (both based on close manual tracing cross-checked against existing test coverage, not a new failing test).

### 8.11 internal/memory, internal/context, internal/contextbroker, internal/grounding, internal/learnings, internal/recover, internal/recovery (+broker, +orphansweep, +pack), internal/loopdetect, internal/reminders

**Cohesion verdict — `memory` vs `context` vs `contextbroker`:** genuinely distinct, deliberately-bounded packages, not accidental overlap — `memory` is a thin adapter over the embedded Tesseract store; `context` is the pure wire-shape/budget/compaction substrate; `contextbroker` is multi-source retrieval + an assembly decider, with an actively-maintained one-way dependency boundary (explicitly documented: `contextbroker` deliberately does *not* import `internal/context` despite consuming its concepts, to keep the dependency direction clean). The name overlap is a real hazard for search/grep, but the responsibility split is sound. One real (informational) architecture note: `contextbroker` bundles two conceptually different jobs (external retrieval, in-process assembly decision) under one "broker" name — defensible today, worth a future split if the package grows.

**Cohesion verdict — `recover` vs `recovery`:** genuinely different domains (tool-call-argument repair vs. process/session crash recovery) — confirmed via grep that no file imports both, zero evidence of accidental confusion in practice. Still a real naming-collision risk for future contributors; worth a short glossary entry, not urgent.

**`internal/recovery/orphansweep.RuntimeReaper` — direct lifecycle review (independent of the already-covered container.go caller):** **this implementation is solid, better-defended than its caller** — `Start`/`Stop` correctly use `sync.Once`+`atomic.Bool`, the nil-deps early-return path still correctly signals done (so a later `Stop()` never deadlocks), and `Stop()` blocks until the goroutine actually exits. One real gap found: the sweep's own `ctx` parameter is explicitly discarded (`_ = ctx`), so an in-flight DB call cannot be cancelled mid-flight by the shutdown signal (low risk, local SQLite, bounded — GO-MEM-006 below). The periodic ticker-driven `loop` itself has 0.0% test coverage (only the pure decision core and the no-store early-return path are exercised).

**GO-MEM-001** (medium, dead-code/architecture) — `internal/grounding`'s entire pre-strategy memory-recall subsystem (recall, outcome-tracking, consultation-logging — ~530 LOC, extensively tested) is **fully built but never wired in production**: `GroundingRecaller`/`GroundingLogger` fields exist on the self-tools transport, but zero production code anywhere constructs and assigns a `*grounding.Recaller` to them (confirmed by `deadcode` + exhaustive grep). Also gated behind an env var defaulting off. Design intent ("prevent premature strategy dispatch without grounding in relevant memory") isn't happening today. Worth knowing before either wiring it up or scheduling removal — code quality is high, arguing for "not yet wired" over "abandoned."

**GO-MEM-002** (low dead / would-be-medium if revived, dead-code/correctness) — `contextbroker/gate_hadron_blueprints.go`'s entire `ContextGate`/`HadronBlueprintGate` (302 LOC + 337 test LOC) is confirmed **entirely dead** — never registered as a `ContextSource` in the composition root, unlike its 4 live siblings. Contains a **latent unbounded-relevance-score bug**: `calculateRelevance` additively sums up to 6 independent bonuses with no clamp, against a documented 0.0–1.0 contract that the broker's global cross-source sort relies on — a revived-but-unfixed version could silently out-rank every correctly-bounded item from live sources. Flagging the bug now so it's fixed before any future revival, not after.

**GO-MEM-003** (low, dead-code/hygiene) — `BudgetForIntent`/`IntentSourcePriority` unused (a second, dead budget-allocation strategy alongside the one actually used); `DefaultBudget()`'s `SourceWeights` includes a `"engine": 0.15` entry with **no backing `ContextSource` implementation anywhere in the repo** — inert, misleading configuration, not a functional bug.

**GO-MEM-004** (informational, duplication/idiom) — Each `contextbroker` source computes `ContextItem.Relevance` via an unrelated method (static lookup table, store-provided confidence + ad-hoc boost, hardcoded literals, or the now-dead unbounded heuristic from GO-MEM-002) yet all are merged and cross-compared on one global scale with no shared contract beyond "roughly 0-1." Worth documenting/enforcing a shared normalization step before any future source is added.

**GO-MEM-005** (informational, duplication) — Two independent "chars/4" token-estimation formulas (`internal/context`: floors, `internal/contextbroker`: always rounds up) measure the *same content* at two different stages of the same budget pipeline, diverging by a few tokens for any length not a multiple of 4 — small drift, but in exactly the accounting this architecture is built to keep tight. `contextbroker` avoiding an `internal/context` import (per the cohesion note above) means unifying this would need a shared lower-level package, not a direct import.

**GO-MEM-006** (low, lifecycle/performance) — `internal/loopdetect.Detector.windows` map has **no production eviction path** — `Reset(sessionID)` exists specifically for this (per its own doc comment) but has zero production callers anywhere; the single process-lifetime `Detector` is constructed with no options. For a long-running daemon, every distinct session that ever calls a tool adds a permanent entry. Small per-entry cost, unbounded growth over uptime.

**GO-MEM-007** (low, dead-code/comments) — `orphansweep.SweepOrphans` is dead in production (bypassed by `RuntimeReaper.SweepOnce` calling the inner function directly), but multiple *other* files' doc comments (`internal/runtime/agent/deps.go`, `internal/store/agent_runtime.go`) still describe it as the live entry point — stale cross-references, not a functional gap.

**GO-MEM-008** (informational, false-positive-leaning) — `recovery/broker`'s `With*` constructor options are `deadcode`-flagged, but the equivalent `Set*` post-construction setters (which *are* live) exist for a documented, legitimate reason: the concrete hooks close over state constructed after the broker itself, so late-binding is required for 2 of 3 hooks. Recorded specifically so a future reviewer doesn't re-flag this as real dead code — a good example of the guide's own "reported dead ≠ remove" guardrail.

**God-object/concurrency/error-handling:** no new god-object candidates (largest struct is `recovery/broker.Broker` at 7 fields, all justified). Concurrency reviewed as careful and consistent throughout — session-scoped cancel-token validation against replay, panic-recovering hook invocation, correct terminal-state cleanup on every path; no unguarded shared-map access, no lock-held-during-I/O, no bare `go` statements outside the already-reviewed reaper loop. Error handling generally strong; one `SA9003` lint hit confirmed a deliberate documentation-only guard clause, not a defect.

**Complexity:** confirmed the cluster's premise — nothing here crosses the guide's review thresholds. The ceiling (`stageDedupeToolResults`, cognitive 30) was read in full and is a clean, well-commented two-pass dedup algorithm — essential complexity, not accidental.

**Reviewed and found healthy:** `internal/context/compaction.go`'s escalating pipeline (well-ordered, includes a genuinely useful negative-savings guard with an inline post-mortem for a real production regression it fixes); `contextbroker/assembly.go`'s stash-before-pointer atomicity and its deliberately one-way dependency boundary; `internal/learnings` (a model example of building on `internal/memory` via a narrow interface rather than reimplementing); `internal/recover` (thorough layered error classification, correct `Unwrap()` chain); `recovery/broker` (session-scoped security, panic-recovering hooks, correct per-session state cleanup on every terminal path); `orphansweep.RuntimeReaper` (more defensive than its caller); `recovery/pack` (a well-justified, explicitly-intentional trivial-helper duplication to avoid an import cycle — exactly the kind of duplication the guide's guardrails say not to flag); `internal/reminders` (correct in-memory cleanup on fire, unlike loopdetect's unbounded map).

Not checked: independent `-race` re-run for this cluster (base report's whole-repo run didn't flag anything here, not independently re-confirmed); full line-by-line read of `internal/recover/repair.go` beyond its top ~80 lines, or `internal/recovery/broker`'s `classifier.go`/`remediator.go`/`deps.go`/`types.go`/`envelope.go`; whether the external MCP server literally named `"conduit"` (a second access path into similar content alongside the embedded memory store) is actually running in any real deployment — flagged as an open architect question, not asserted as active duplication.

### 8.12 Trust-boundary primitives: internal/sandbox, internal/permission, internal/secrets, internal/pathsafe, internal/fsutil, internal/safego

**Cohesion verdict:** all six packages are cleanly single-purpose primitives — exactly what boundary primitives should look like. No god objects (`Engine` 6 fields, `PathGrants` 4, `Proxy` 10 — all legitimately scoped). `internal/permission` bundles two related-but-separable concerns (rule-based tool authorization, session path-scope tracking) — not a smell today, worth awareness if the package keeps growing.

**Methodology note:** this review re-verified all 12 findings from the prior dedicated `docs/audits/2026-04-10-sandbox-hardening/` deep audit against **current** code rather than trusting that document — several have since been fixed (session-ID path traversal, seatbelt-profile injection, proxy SSRF bypass via DNS→RFC1918, a proxy goroutine-leak-on-Stop bug), two remain open and are re-confirmed below with fresh evidence.

**GO-SEC4-001 (CRITICAL, still open, re-confirmed)** — On Linux, if `bwrap` (bubblewrap) isn't installed, `applyOSSandbox` **silently falls back to no OS-level isolation and returns success** — `AgentExec` treats this identically to a real sandbox being applied; nothing distinguishes the two outcomes at any call site. Bubblewrap is not a default package on most distros. On any such host, every other protection this review verified as solid (path-scoping, seatbelt-equivalent confinement, network isolation) simply doesn't exist — the only remaining control is the bypassable substring denylist (GO-SEC4-006). Identical, unfixed, to the 2026-04-10 audit's Critical finding.

**GO-SEC4-002 (high, still open, re-confirmed)** — Linux network allowlist is **not enforced at the OS/namespace level** in proxy mode: when `NetworkAllow` is non-empty, the sandboxed process keeps the **host's network namespace** — confinement to the loopback proxy relies entirely on `HTTP_PROXY`/`HTTPS_PROXY` env-var convention, which any tool can ignore (raw sockets, non-HTTP protocols, explicit `--noproxy`). The rest of the 2026-04-10 audit's Linux-hardening gaps in this same finding **have all since been fixed** (namespace isolation, tmpfs `/tmp`, narrowed bind mounts) — only this specific network-namespace gap remains. macOS is unaffected (seatbelt's network-outbound deny is real, kernel-enforced).

**GO-SEC4-003** (medium, security/error-handling — needs architect confirmation of intent) — `permission.Engine`'s `ModeDefault` doesn't actually ask before non-destructive writes, **contradicting its own doc comment** ("prompt for destructive/write operations"). Traced the name-based heuristic that classifies tools: `dev_write`/`dev_edit` match neither the read-only nor destructive pattern lists, so both flags resolve `false`, and `ModeDefault`'s switch has no `{false,false}` branch — it falls through to `DecisionAllow`. The more-restrictive-sounding `ModeAcceptEdits` *does* have the missing branch (`!meta.IsReadOnly → Ask`) right next to it. This is the harness's primary chat-loop authorization gate (`ModeDefault` is the default engine construction). Untested: the exact `{false,false}` case is the one combination the existing test table doesn't cover. **Substantial mitigating consideration**: `PathGrants`' own documented design philosophy is "explicit-mention auto-grant, no nag-again" — it's plausible the intended architecture is "PathGrants governs whether a path is writable at all; once writable, don't prompt per-call," making this a stale-comment issue rather than a live regression. Reported as a fact (code contradicts its own doc, untested) with that ambiguity stated explicitly, not asserted as a confirmed bug.

**GO-SEC4-004** (medium, still open, re-confirmed) — Secret-key-name substring heuristic (`KEY`/`SECRET`/`TOKEN`/`PASSWORD`/`CREDENTIAL`/`AUTH`) still misses `DATABASE_URL`, `REDIS_URL`, `SLACK_WEBHOOK_URL`, `GH_PAT`, and other credential-bearing names that don't contain those substrings — unchanged from the 2026-04-10 finding. Highest-risk surface is `AgentExec` (agent/LLM-influenced code), not `UserExec` (user's own shell).

**GO-SEC4-005** (low, tracked, deliberate tradeoff, not new) — macOS seatbelt profile stays `(allow default)` for file reads/mach-IPC/process-inspection by design; the original finding frames this as an explicit, defensible beta-stage tradeoff contingent on user-facing disclosure (not independently re-verified here). Restated for visibility alongside GO-SEC4-002 (both platforms leave the *read* boundary unenforced by design), not re-litigated in full.

**GO-SEC4-006** (low, still open, re-confirmed) — Command denylist remains a bypassable literal-substring blocklist (whitespace variation, flag reordering, or wrapping in an interpreter all defeat it). Low severity in isolation since the real boundary is normally the OS sandbox — but on Linux without `bwrap` (GO-SEC4-001), this denylist becomes the *only* remaining control, which is exactly where its weakness starts to matter.

**GO-SEC4-007** (medium, duplication/security) — The SSRF-defense CIDR denylist (loopback/RFC1918/CGNAT/link-local-IMDS/IPv6 ULA) is **independently duplicated** between `internal/sandbox/proxy.go` and `internal/mcp/general_tools.go`, with the sandbox copy's own comment explicitly stating the intent is parity ("so both network egress paths enforce the same policy") — but nothing enforces that parity mechanically. If one list is updated (e.g., a new cloud-metadata range) and the other isn't, the two egress paths silently drift apart with no signal. A simple equality test between the two lists (not even a refactor) would catch drift today.

**GO-SEC4-008** (low, security) — Sandbox proxy's `http.Server` has no `ReadHeaderTimeout` (gosec G112, Slowloris). Real exposure is narrow (loopback-only bind, reachable only by the sandboxed subprocess itself) but it's a zero-risk one-line fix.

**GO-SEC4-009** (informational, repository-hygiene/concurrency) — `make lint-goroutines`'s scanned-package list omits every package in this cluster (`sandbox`, `permission`, `secrets`, `pathsafe`, `fsutil`). Manually ran the equivalent check and found exactly one bare `go func()` in scope (`sandbox/proxy.go`'s `runTunnel`), confirmed benign (tightly-scoped, self-terminating, already inside a lifecycle-tracked goroutine) — but the guardrail tooling itself doesn't cover this cluster, so a future genuinely-unowned goroutine here wouldn't be caught automatically.

**GO-SEC4-010** (informational, error-handling) — `secrets.Get` collapses "key not found" and "keychain access error" into the identical silent empty-string return, with no logging (unlike its sibling `Delete`, which does log on failure) — an availability/observability gap, not a leak.

**Reviewed and found healthy — this is the strongest security-posture section of the whole audit:** `pathsafe.ResolveUnder` (the primitive GO-SEC-003's 68 flagged repo-wide call sites are supposed to migrate to) holds up under adversarial testing — correct absolute-path handling, full symlink-chain resolution, containment via `filepath.Rel` not naive prefix matching, and a genuinely adversarial existing test suite (dotdot-mid-path, dotdot-that-lands-back-inside, symlink-escape, symlink-escape-subpath, null bytes). `fsutil.AtomicWriteFile` — genuine temp+fsync+chmod+rename+parent-fsync sequence, named-return defer cleanup on every error path, explicit short-write detection, 15 tests including concurrent-writers and rename-failure cases. `safego.Go`/`.Call` — real panic recovery, honestly-scoped doc comment (explicitly defers cancellation to `internal/lifecycle` rather than overclaiming), confirmed real adoption at 82 call sites across 25 files. `sandbox/proxy.go`'s SSRF protection — DNS-rebind-safe pinned-IP dialing (resolve once, reject if *any* returned IP is denied, dial the pinned literal — closing the TOCTOU gap), CONNECT restricted to a TLS-port allowlist, redirects never auto-followed; every specific bypass from the 2026-04-10 audit's SSRF finding is confirmed closed in current code, and the prior CONNECT-goroutine-leak-on-Stop bug is also confirmed fixed with the exact recommended pattern. `sandbox.go`'s session-ID validation plus `pathsafe.ResolveUnder` defense-in-depth, and `os_darwin.go`'s seatbelt-literal validator, which goes *further* than the original 2026-04-10 recommendation (rejects any structurally-meaningful byte outright rather than escape-and-continue). `sandbox/exec.go`'s subprocess launch uses argv directly, never a shell — G204 gosec hits are the expected shape for a code-exec sandbox, not real injection risk; `exec_unix.go`'s process-group kill correctly resolves the prior orphaned-grandchild-process finding. `permission/resolve.go` fails closed on workspace-escape and resolves symlinks in the working-dir prefix. `permission/path_grants.go` — deliberately narrow, goroutine-safe, cycle-guarded, depth-capped. Zero G118 (context.Background in goroutines) hits in this scope; the one G402 (`InsecureSkipVerify`) hit is confirmed test-only.

Not checked: whether `permissions.yaml`/`LoadRulesFromFile` is wired into any production boot path with a default rule set that would supersede the `nil`-rules default construction (confirmed zero production callers via grep, but didn't trace every possible late-binding path, e.g. a plugin-installed rule set); the actual `slog` handler configuration bearing on G706 log-injection risk assessment; user-facing documentation disclosure of the read-boundary gaps (GO-SEC4-002/005); did not run `go test -race -count=20` for this cluster independently (relying on the base report's whole-repo run, which didn't flag these packages).

### 8.13 internal/runtime (+agent), internal/lifecycle, internal/scheduler, internal/background, internal/server, internal/worktree, internal/workspace, cmd/nanite

**`cmdServe`/`startBackgroundWorkers` — essential vs. accidental complexity:** **verdict: essential**, correctly sequenced. Read `cmdServe`'s full 670-line body end to end — a single linear composition-root sequence where nearly every step genuinely depends on the prior one's output (several dependencies explicitly documented in-line, e.g. why scheduler wiring can't move inside `NewContainer`). Most of cyclomatic-45 is `if err != nil` idiom across ~30 fallible boot steps, not tangled branching — matches the guide's own explicit allowance for this shape. `startBackgroundWorkers` structurally cannot hit the "worker N fails, are 1..N-1 cleanly stopped" failure mode the guide's §9.3 asks about, because the underlying `lifecycle.Manager.Go` never returns an error and never blocks. Two remaining candidates for optional, non-urgent readability extraction (workflow-engine wiring, scheduler wiring) noted but not required.

**GO-RUNTIME-001** (low-medium, lifecycle/error-handling) — `cmdServe`'s `slogx.Fatal` exit paths (`os.Exit(1)`, doesn't run deferred functions) bypass **every** deferred cleanup registered earlier in the same function — log-handler close, OTel flush, SQLite store close, Badger coordination-store close — on 5 real startup-failure call sites. Bounded blast radius (process always terminates immediately after, functionally similar to `kill -9` at that instant — no accumulating leak), but silently defeats cleanup the code appears to promise via `defer`.

**GO-RUNTIME-002 (HIGH, with a substantial documented-intent caveat)** — Auth is entirely opt-in via env vars ("local dev mode" if unset — a no-op middleware), the listener binds **all interfaces** by default (no loopback-only option exists in config at all), there's no TLS anywhere, and **no startup-time log line** announces whether auth is enabled or disabled. With default configuration, `nanite serve` runs an unauthenticated HTTP API bound to every network interface with zero operator-visible warning. **Substantial mitigating context**: this is an explicit, in-repo-documented design decision ("single-user local app... Basic Auth is optional and coarse") — the gap is between that stated intent and the implementation providing no enforcement or even a default toward it (no loopback-only bind, no runtime signal when the "local-only, trust-based" assumption is silently unmet, e.g. the host later lands on a shared network or a firewall rule lapses). Reported per the guide's explicit ask to check "unsafe HTTP defaults"/"authorization fail-open," with the documented-intent caveat stated directly so the architect can weigh it as accepted-tradeoff-vs-real-gap.

**GO-RUNTIME-003** (medium, correctness/lifecycle/duplication) — `worktree.gitManager.CleanupOrphaned` computes the **wrong branch name** and therefore never deletes the actual orphaned git branch: `Create` uses the full session ID (`worker-<26charULID>`), but `CleanupOrphaned` independently re-derives the branch name using an **8-character-truncated** version — a `git branch -D` on a name that (for real ULID-length session IDs) essentially never matches what `Create` made, failing silently (best-effort, return value discarded). This runs on **every daemon boot** to sweep crash-orphaned worktrees, so every cleanup leaves a permanent stray branch in the host git repo, accumulating unboundedly across restarts/crashes. The existing regression test doesn't catch this because it only asserts directory removal, never that the branch itself is gone.

**GO-RUNTIME-004** (low-medium, lifecycle/performance) — `internal/background.Service`/`PTYBackend` job registries **grow unboundedly for the process's entire lifetime** — every job that starts (success, failure, or cancellation) stays in the map forever except one narrow immediate-failure path; `jobRecord` additionally retains up to 1 MiB of captured output per job, indefinitely. `PTYBackend.Status`'s own doc comment claims completed jobs are "reaped out of the map" — **false**, no reaping logic exists anywhere in the file. Real-world impact scales with `background_job` submission volume over uptime, which wasn't measured (out of scope).

**GO-RUNTIME-005** (informational, lifecycle) — `service.Container.Shutdown` has **no idempotency guard** at all (no `sync.Once`/closed-flag), unlike its sibling `lifecycle.Manager.Shutdown` which is correctly idempotent. Currently dormant (exactly one production call site, fires once by construction) — flagged because the guide explicitly asks "can Stop be called twice?" and the honest answer here is "unguarded, not defended against." A related, smaller observation: `agent.Session.Stop` also has no internal idempotency guard, but its one real caller gates access via an atomic `sync.Map.LoadAndDelete`, making the underlying gap practically unreachable today — judged healthy-with-a-caveat, not a separate finding.

**GO-RUNTIME-006** (informational, comments) — Same systemic ticket-archaeology comment density pattern noted elsewhere in this audit, particularly dense in this cluster (`cmdServe` is roughly half comment-lines to code-lines) — not a functional defect, a maintainability/hygiene observation for a future architect-level documentation pass, not mechanical stripping (some of this content is genuinely load-bearing invariant documentation, not diary).

**GO-RUNTIME-007** (low-medium, error-handling) — `loadPersistedMCPServers` silently discards 2 of 3 JSON-decode errors on persisted MCP server config (`Args`, `Env`) while correctly warn-logging the third (`EnvAllowlist`) right next to them in the same function — a malformed persisted row silently launches with empty args/env instead of the operator's intended configuration, with zero signal anywhere that the persisted config differs from what actually ran.

**Second-highest complexity function in the entire codebase, found during this cluster's review (not in the base report's table or this dispatch's pre-flagged list):** `agent.Boot` (`internal/runtime/agent/agent.go:323`) — cyclomatic **50**, cognitive **59**, maintainability index **10**, 234 lines. **Verdict: essential, same shape as `cmdServe`** — a single sequential resolve→materialize→compose→start-and-wait-for-ready chain with a correctly-applied `cleanup` closure at every one of ~12 early-return points past bootdir setup (verified each one), and a genuinely necessary 3-way `select` at the end. Flagging this explicitly for the coordinator: it should be added to the master complexity/Top-10 tracking alongside `cmdServe`/`UnloadPlugin`/`Executor.Run`/`Service.Spawn` — it was missed by the mechanical-scan-sorted table.

**Security triage of the `cmd/nanite` gosec cluster (assigned follow-up):** every traced hit across `plugin_cmd.go`, `mcp_cmd.go`, `plugin_logs.go`, `serve_autostart.go` originates in `os.Args` (CLI positional/flag input) or a deterministic OS-derived state path — never a network or lower-privilege caller. Confirmed low real risk across the board, consistent with (and now with concrete backing for) the base report's framing that CLI-flag-driven code is lower risk than server-facing code. **One correction to the base report's framing**: `cmd/nanite/plugin_dev_cmd.go` has **zero** gosec findings (the earlier sample cited it inaccurately), and **G115 (integer overflow) does not occur anywhere in `cmd/nanite`** — its 4 real hits are in `internal/coordination`, `internal/plugin/install`, and `internal/api`, none of which are this cluster's scope. Nothing here was promoted to Top-10 the way `internal/agent`'s GUI/API-reachable GO-SEC-003 sites were — the CLI trust boundary genuinely is lower-risk here.

**Server security (`internal/server`) — reviewed in full, mostly healthy:** explicit, documented HTTP timeouts (Slowloris-aware `ReadHeaderTimeout`); real `MaxBytesReader` body caps split by upload-path; a genuine CORS allowlist with correct wildcard/credentials handling (explicitly replacing a prior "reflect-any" policy a previous audit flagged Critical — good evidence a past finding was actually fixed, not just noted); panic-recovery middleware that caps stack traces and never leaks detail to the client; path-traversal-safe SPA serving via `embed.FS`. The one real gap is GO-RUNTIME-002 above. Also observed: the SIGINT/SIGTERM handler `os.Exit(0)`s immediately after `container.Shutdown()` without ever calling `httpSrv.Shutdown(ctx)` — in-flight HTTP requests are hard-terminated rather than gracefully drained (small, easy fix, noted for completeness against §9.3).

**Package cohesion:** all packages in this cluster are healthy, single-domain. `internal/workspace` is specifically called out as a good example to hold up elsewhere in the audit — genuinely security-conscious (symlink rejection at both leaf and parent-directory level, explicit "# Security" doc section). `cmd/nanite`'s `main.go` at 1,432 lines crosses the guide's own "1,200+ strong review candidate" file-size threshold — flagged as informational (it's one cohesive boot-sequence function plus directly-adjacent helpers, not scattered responsibility).

**Duplication pattern (not textual, worth naming):** the "single giant function linearly sequencing many fallible steps with per-step cleanup" shape recurs independently across `cmdServe`, `startBackgroundWorkers`, `agent.Boot`, `cmdPlugin`/`pluginNew`, `adminExportDecisionTables` — each judged essential-complexity in isolation, but naming the pattern here is useful architect context: this is the dominant complexity shape in this cluster, not five unrelated problems.

**Other complexity outliers, all judged essential/healthy:** `harnessClient.StreamEvents` (clean SSE-decode loop with correct cancellation and synthetic error-event pattern instead of silent truncation); `sandbox_content_claude.go`'s `BuildAgentContext` (confirmed false positive — 5 near-identical simple field-render blocks); `adminExportDecisionTables` (a well-designed one-shot migration tool with a genuinely good post-export count-verification safeguard); `cmdPlugin`/`pluginNew` (CLI dispatch tables, essential).

**Reviewed and found healthy:** `internal/lifecycle.Manager` — the reference implementation the rest of the codebase should be measured against (correct, idempotent, well-documented shutdown); `internal/workspace`; `internal/server`'s timeout/body-cap/CORS/panic-recovery/static-serving design; `internal/background`'s process-group-kill/SIGTERM-then-SIGKILL/zombie-reaping hygiene (well-tested — the only gap is the unbounded map, GO-RUNTIME-004); `internal/runtime/agent`'s `Layout` interface (a genuine substitution boundary, 3 real implementations); `agent.Boot`'s partial-failure cleanup discipline; `chatServiceImpl.CloseAgentSession`'s atomic double-Stop guard at the actual call site; `internal/scheduler`'s retry/backoff state machine; the `cmd/nanite` gosec cluster, confirmed (not assumed) to be CLI-trust-boundary false positives.

Not checked: external-module (`go-agent-wrapper`, `acp.Client`, `go-scheduler`) idempotency claims taken from in-repo doc comments, not independently verified against those modules' own source; `background_job` real-world submission volume/telemetry; full line-by-line read of all 45 files in `internal/runtime/agent` (prioritized lifecycle-relevant sections and the one flagged complexity function); `internal/scheduler/store_adapter.go`/`telemetry.go` beyond structural skim; independent `-race` re-run for this cluster (relying on the base report's whole-repo run, which didn't flag anything here).

---

## 9. Cluster-review synthesis — cross-cutting themes

With all 13 clusters landed, several patterns recur across independent reviewers who had no visibility into each other's work:

1. **God objects concentrate in exactly two types**, both already flagged individually: `internal/service.Container` (60 fields, but genuinely wiring-only — 2 methods) and `chatServiceImpl` (52 fields / **84 methods**, fails the wiring-only test decisively via `generateResponse` and dozens of other large methods). A third, `internal/selftools.SelfToolsTransport` (31 fields / 81 methods), is architecturally the same shape one layer over in the tool-dispatch domain. All three are composition points for ~15-25 unrelated capability domains implementing real behavior directly on one type, not just wiring dependencies — this is the single most consistent structural finding across the whole audit.
2. **The mechanical complexity triage table in §4 missed the two most complex functions in the codebase**: `generateResponse` (cognitive 458, ~5x the table's previously-listed "sharpest" outlier) and `agent.Boot` (cyclomatic 50/cognitive 59) — both surfaced only because cluster reviewers read the actual files rather than trusting the pre-flagged worklist. This is itself a finding about the audit methodology: mechanical file-path filtering during the initial pass had real gaps.
3. **A recurring "feature is fully built, tested, and documented, but never wired into production" pattern** appears independently in at least 4 places: `internal/grounding`'s memory-recall subsystem, `contextbroker`'s Hadron blueprint gate, `team_routing.go`'s semantic slot-routing (explicitly self-flagged as an open risk in the project's own `HANDOFF.md`), and `internal/tool`'s builder/YAML-loader architecture (whose package doc still describes it as canonical). None of these are hidden — most are `deadcode`-confirmed and several are already self-documented as gaps in-repo — but the volume of live-looking-but-inert code is worth an architect's single pass to either wire up or retire deliberately.
4. **The security posture is bimodal**: the purpose-built trust primitives (§8.12 — `pathsafe`, `fsutil`, `safego`, the sandbox proxy's SSRF defenses, MCP's trust-tier system) are consistently well-designed and, where a prior dedicated audit existed, mostly already remediated. But two GUI/API-reachable paths bypass those same primitives entirely — the plugin catalog-install handler (GO-PLUGIN-001/002, critical) and the agent-slug managed-file path (GO-AGENT-001, high) — both because a newer, correct pipeline was built for one caller (CLI) and never back-ported to an older sibling handler (GUI/API). This is a specific, actionable pattern: **when auditing future security fixes, check every caller of the vulnerable primitive, not just the one that prompted the fix.**
5. **Naming collisions were checked deliberately in 3 places and found genuinely benign in all 3** (`skill`/`skillvendor`, `recover`/`recovery`, `dispatch`/`dispatcher`) — each is a deliberate, sequenced, or well-documented split, not accidental duplication. Worth noting since this is exactly the kind of thing that's easy to flag reflexively and wrong to.
6. **Duplicated semantics cluster around "the same lifecycle concept implemented twice, once migrated onto a shared primitive and once not"**: `resolveMessageWakePolicy`/`resolveSubagentCompletionPolicy` (§8.4), the CIDR SSRF denylist in `sandbox`/`mcp` (§8.12), the CLI-vs-GUI plugin-install pipelines (§8.6), Harness-v1-vs-native durable-agent handlers (§8.5). In every case one side is newer/correct and one is older/stale — none are two-actively-diverging implementations, which suggests these arise from incremental migration, not parallel design.
