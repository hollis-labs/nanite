# Nanite testing workflow — tiers, and how to run tests *hard* in the right direction

**Authored** 2026-08-24 · session `session-20260824-37765f5d` · measured at `bdf3d4d9`
**Status** active. Figures are stamped at that commit — re-derive before citing one.
**Calibrated for:** pre-release, no consumers, machinery mid-rollout. Tighten at go-live (§6).

---

## Why this exists

The full race suite is now slow enough that running it on every change is not sensible, and — more importantly — **running it is not always the strongest check available.** Three defects fixed on 2026-08-24 each required a *different* amplification strategy, and the intuitive one ("add `-race`, raise `-count`") would have missed two of them.

This document says which mode to use when.

---

## 1. The two axes

Testing effort has two independent dials. Conflating them is the mistake.

| Axis | Dial | Finds |
|---|---|---|
| **Scope** | which packages | breakage your change caused elsewhere |
| **Amplification** | `-race`, `-count`, load | defects that only appear on some interleavings |

Scope is about *coverage*. Amplification is about *probability*. A change to a pure function needs scope and no amplification. A change to a shutdown path needs amplification and possibly narrow scope.

---

## 2. Amplification: `-race` and `-count` do opposite things

**This is the part that is not intuitive.**

### `-race` — for memory-model violations

Detects unsynchronized concurrent access to the same memory. When it fires, it usually fires **fast**.

- Evidence: `TestAgentEventBridgeFanoutRacesRouterUnbind` race-detected on the **first round** under `-race`, and passed **20 consecutive runs without it**.
- If you suspect a data race, `-race -count=1` is often enough. Raising the count adds little.

### High `-count` **without** `-race` — for timing and ordering bugs

`-race` instruments every memory access, which slows execution and **inflates the timing gaps** that ordering bugs depend on. It can hide them.

- Evidence: the durable-agent event-ordering bug. Inter-event gap p50 was **106 µs** without `-race` and **2,187 µs** with it. Failure rates:

| run | `-race` | count | failures |
|---|---|---:|---:|
| real test | yes | 1,000 | 0 |
| real test | yes | 20,000 | 1 |
| real test | **no** | 20,000 | **18** |

  `-race -count=100` had an expected failure count of **0.005** with the bug fully present. That was the task's prescribed acceptance gate. It proves nothing.

### Scheduler saturation — for "wide window" races

Some races need contention, not repetition. `GOMAXPROCS(1)` plus spinning goroutines reproduced a `TestShutdown` failure mode that `-race -count=50` never hit.

### The rule

> **Suspected data race → `-race`, low count.
> Suspected timing/ordering flake → high count, *drop* `-race`, consider saturation.
> Either way, the real fix is to make the test deterministic so neither is needed.**

A deterministic reproduction beats every amplification strategy. Both fixes landed 2026-08-24 replaced probabilistic waits with real barriers; a colleague built a 100%-reproducing test for the ordering bug using explicit timestamps. Prefer that.

---

## 3. The tiers

### Tier 0 — inner loop · seconds

```bash
go test ./internal/<pkg>/
```

No `-race`. The package you are editing. Run constantly. This is not a gate, it is feedback.

### Tier 1 — feature done, before commit · ~1–2 min

```bash
go test -race -count=1 ./internal/<pkg>/ ./internal/<dependents>/
```

Changed packages **plus their dependents**. The dependents part is the whole point — it catches "my change broke someone downstream," which is most of what full-suite runs actually find.

Find dependents:
```bash
go list -f '{{.ImportPath}} {{join .Deps " "}}' ./... \
  | grep 'nanite/internal/<pkg>' | cut -d' ' -f1
```

**This is the default for feature work.** Most packages run in 1–2 s.

### Tier 2 — touched concurrency, lifecycle, or shutdown · minutes

```bash
go test -race -count=20 ./internal/<pkg>/
```

Trigger: your change involves goroutines, channels, `context` cancellation, mutexes, `sync/atomic`, shutdown/cleanup ordering, or anything with a `time.Sleep` in its test.

If it involves *ordering* rather than *concurrent access*, also run the Tier 4 shape below.

### Tier 3 — full suite · ~9 min wall

```bash
go test -race -count=1 $(go list -f '{{.Dir}}' ./... | grep -v node_modules)
```

109 packages. Run when: you changed something cross-cutting, you are about to hand off, or you are investigating a failure of unknown origin. This is what the nightly gate runs.

**Cost is concentrated** — three packages dominate, the rest are ~1–2 s each:

| package | `-race` time |
|---|---:|
| `internal/store` | 252 s |
| `internal/api` | 143 s |
| `internal/service` | 65 s |

If you need a fast approximation of the full suite, run everything *except* those three and then decide whether they are implicated.

### Tier 4 — flake investigation · deliberate, can be hours

Only when hunting a specific known flake.

```bash
# ordering / timing suspected — NOTE: no -race
go test -count=20000 -timeout=120m -run '^TestName$' ./internal/<pkg>/

# data race suspected
go test -race -count=50 -timeout=180m -run '^TestName$' ./internal/<pkg>/
```

Always pass an explicit `-timeout`. Go's default is **10 minutes per binary**, and exceeding it reports `FAIL` that looks like a test failure but is a timeout. That misread has already cost time once.

Record the iteration count in whatever you write up. "It passed" is not a result; "0 failures in 20,000" is.

---

## 4. Which tier does my task need?

| If your change… | Tier |
|---|---|
| touches one package, no concurrency | 1 |
| touches a shared type or interface | 1, widened to dependents |
| involves goroutines, channels, ctx, mutexes, atomics | 2 |
| involves shutdown, cancellation, or lifecycle ordering | 2 + a Tier 4 ordering run |
| is a **test-only** change fixing a flake | 4 — and prove the test **fails without your fix** |
| touches `internal/store` schema, migrations, or timestamps | 2 + Tier 3 (store is the slowest and most depended-on) |
| is a docs/comment change | 0 |
| is "I don't know what this touches" | 3 |

### Non-negotiable for any flake fix

**Prove the test fails without the fix.** Temporarily revert your change (or weaken the barrier), watch it fail, restore, verify the restore. Both fixes on 2026-08-24 did this and both claims held up under independent re-verification.

Guard the restore: `cp` is aliased to `cp -i` in this environment and will **silently refuse** to overwrite, printing "not overwritten". Use `command cp -f` and confirm with `shasum` or `git diff --stat`. A restore that silently failed already produced one wrong result this session.

---

## 5. Test quality — the layer that catches vacuous tests

A test can pass while proving nothing. Four instances surfaced on 2026-08-24 (see `failure-modes.md`, *vacuous verification*):

- a `for … range` over an empty slice, asserting nothing
- a security test that reports "still blocked" because nothing was ever reachable
- the lint ratchet returning success on an empty scan — with its **own test asserting that as intended**
- a task file prescribing an acceptance command that cannot detect its own bug

**Reviewer's rule:** for any assertion, ask *what input would make this pass while proving nothing?* An empty collection, a zero count, an unreachable target, a check that never ran. If that input is reachable, the test needs a **positive control** — prove the harness *can* observe the thing before asserting its absence.

**Writer's rules:**

- Assert **presence before properties**: `if len(x) != 1 { t.Fatalf(...) }` before iterating. An empty collection must fail, not pass silently.
- A test asserting something is *blocked/absent/rejected* needs a companion proving the mechanism can succeed in the same environment. Otherwise `t.Skip` with a reason — a skip is honest, a vacuous pass is not.
- No fixed sleeps as synchronization. A sleep bounds elapsed time, not progress. Use a barrier the code under test actually signals.

---

## 5a. Profiles not currently run — ranked by yield per effort

Each profile surfaces a **different defect class**. Running the same tests harder in the same way does not substitute for running them differently. All gaps below verified at `bdf3d4d9`.

### 1. `-shuffle=on` — test-order dependence · one flag, high value

**Not used anywhere** (`grep -rn shuffle .github/ lefthook.yml Makefile` → none).

Go randomizes test order and prints the seed. Catches tests that pass only because an earlier test left state behind — shared globals, package-level caches, seeded DBs, leftover temp dirs.

**Confirmed risk in this repo:** package-level mutable test state exists, e.g. `internal/worker/manager_test.go:71-74`:
```go
var (
	testManagers   []*Manager
	testManagersMu sync.Mutex
)
```

```bash
go test -shuffle=on ./internal/<pkg>/          # seed printed on failure
go test -shuffle=<seed> ./internal/<pkg>/      # reproduce
```

**Recommendation: add `-shuffle=on` to the nightly gate.** Costs nothing, and order-dependence is invisible until it isn't.

### 2. Native fuzzing — input-handling defects · highest bug yield here

**Zero fuzz targets exist** (`grep -rn 'func Fuzz' --include='*_test.go'` → none), despite the repo containing textbook fuzz targets, two of them security-critical:

| package | why it's a target |
|---|---|
| `internal/pathsafe` | path traversal — **security** |
| `internal/ssrf` | URL parsing / SSRF defense — **security** |
| `internal/truncate` | boundary handling, UTF-8 splitting |
| `internal/filter` | input filtering |
| `internal/llm/toolargs` | parses model-generated JSON — adversarial by nature |

Go has native fuzzing (1.18+), no dependency needed:
```go
func FuzzSafeJoin(f *testing.F) {
	f.Add("base", "../../etc/passwd")
	f.Fuzz(func(t *testing.T, base, rel string) {
		got, err := pathsafe.Join(base, rel)
		if err == nil && !strings.HasPrefix(got, base) {
			t.Fatalf("escaped base: %q", got)
		}
	})
}
```
```bash
go test -fuzz=FuzzSafeJoin -fuzztime=60s ./internal/pathsafe/
```

Corpus entries that find failures are written to `testdata/` and become permanent regression tests. **Start with `pathsafe` and `ssrf`** — a path-traversal or SSRF bypass is the highest-severity class this app could carry, and neither has fuzz coverage today.

### 3. `goleak` — goroutine leaks · currently 5 of 99 packages

```
packages with goleak: internal/lifecycle, internal/mcp, internal/plugin,
                      internal/selftools, internal/worker   (5)
total test packages:  99
```

This app spawns agent subprocesses, PTYs, MCP servers, heartbeats and retention goroutines — goroutine leaks are a *core* risk class for it, and 94 packages have no check. `goleak.VerifyTestMain(m)` in a package's `TestMain` is a handful of lines.

**Recommendation: extend to at least `internal/service`, `internal/background`, `internal/subagent`, `internal/scheduler`, `internal/messaging`, `internal/toolclient`** — the packages that own long-lived goroutines. Expect it to fail at first; that is the point.

### 4. Non-UTC timezone — clock and formatting assumptions · directly relevant

**CI pins no `TZ`** (`grep -n 'TZ\|LANG\|LC_ALL' full-repo-quality.yml` → none), so tests run in whatever the runner defaults to — effectively always UTC on GitHub runners. Any code that is correct only in UTC passes forever.

Given a timestamp-ordering bug was found today, this is worth one run:
```bash
TZ=Pacific/Kiritimati go test ./...   # UTC+14
TZ=America/St_Johns   go test ./...   # UTC-3:30, non-integer offset
```
Non-integer offsets catch a distinct set of assumptions from whole-hour ones.

### 5. Scheduler saturation — wide-window races · found a real bug today

`GOMAXPROCS=1` plus background load reproduced a `TestShutdown` failure mode that `-race -count=50` never hit. Worth a documented profile rather than an ad-hoc trick:
```bash
GOMAXPROCS=1 go test -count=25 -run '^TestName$' ./internal/<pkg>/
```

### Also worth considering, lower priority

- **`-p 1`** (serial package execution) — surfaces cross-package interference: shared temp dirs, fixed ports, SQLite files. Plausible here given a SQLite store and MCP servers.
- **Case-sensitive filesystem** — CI is macOS-only, which is case-insensitive by default. Any path-casing bug is structurally invisible. Folds into the Linux-matrix gap already recorded.
- **Coverage as a gap-finder** — `-covermode=atomic` does not find bugs, but it answers "what is never exercised," which is the other half of "are our tests good."
- **Build-tag combinations** — `internal/sandbox/os_other.go` compiles on *neither* darwin nor linux today. A compile-only pass over each tag combination would catch that class.

### Suggested rollout

| When | Add |
|---|---|
| now, with the ratchet arming | `-shuffle=on` on the nightly |
| next | `goleak` on the 6 long-lived-goroutine packages |
| next | fuzz targets for `pathsafe` + `ssrf`, 60s each in nightly |
| with the test audit | TZ variance run, saturation profile, mutation testing |

---

## 6. Tightening at go-live

Current settings are deliberately loose because the app is pre-release with no consumers. At go-live:

- Enable commit/push protection (already planned).
- Promote Tier 1 to a pre-push hook — but keep it **fast**, or it becomes something people `--no-verify` past, which is the same as not having it.
- Consider making the full gate run on push to `main`, not only nightly, once merge gating is available.
- Raise flake tolerance to zero: any test that needs `-count > 1` to be trustworthy should be made deterministic instead.

---

## 7. Open item — the test audit

An audit of the tests themselves is planned and has not run. §5 is the seed for its checklist: vacuous passes, missing positive controls, fixed-sleep synchronization, and assertions that cannot fail. Today's four instances were found incidentally while fixing unrelated defects, which suggests the systematic pass will find more.
