# Full `context.Context` propagation sweep across `internal/store`

**Phase:** Audit remediation — out-of-wave mechanical sweep
**Status:** validated — acceptance criteria verified independently; deep code review deferred (operator's call, 2026-08-22)
**Depends on:** none technically — but **must not run concurrently with any other task in this batch.** See "Isolation" below; this is the binding constraint on when it runs, not a preference.
**Blocks:** `06/01`, `06/02`, `11/13`, `13/01`, `13/02` (all touch `internal/store`), and in practice every task touching a caller package.
**Parallel-safe with:** **nothing.**
**Touches:** all 67 non-test files in `internal/store/`, their `_test.go` siblings, and call sites across the 32 packages that import `internal/store`.
**Gated on:** AD-14 — **decided 2026-08-22: full sweep**, executed as a standalone mechanical task outside the wave structure.
**requires_security_review:** false
**requires_regression_test:** false — this task must not change behaviour, so there is no new behaviour to test. Its safety net is the existing suite passing unchanged.

---

> ## READ THIS FIRST — you are running this task in isolation
>
> You have been handed this file without the rest of the project's context.
> That is intentional. **Everything you need is in this file.** Do not go
> exploring the wider `TASKS/` tree for direction, and do not act on anything
> you find there — the rest of that tree describes work that is deliberately
> frozen.
>
> **This is a mechanical refactor. It must not change behaviour.** Not one
> line. If you find a bug while sweeping, **do not fix it** — write it down in
> the Work log and move on. A behavioural change smuggled inside a
> 371-signature diff is effectively unreviewable, which is the entire reason
> this task is scoped as narrowly as it is.
>
> **Scope fence, absolute:** signatures, call sites, and the `database/sql`
> method variants that consume the context. Nothing else. No renames, no
> reordering, no error-wrapping improvements, no new interfaces, no
> gofmt-of-unrelated-files, no "while I'm here" cleanups.

---

> ## ✅ CLOSED (2026-08-22) — `06/04` landed with it in `fe16e138`
>
> The regression described below was fixed by
> `04-cancellation-safety-for-terminal-writes.md` and both landed together.
> Final verified state: store SQL oracles 0/0/0, 371/371 methods with `ctx`,
> `go test ./...` 0 FAIL / 94 ok, `go vet` exactly the 4 pre-existing
> `container.go` findings, 246 `context.TODO()` calls with 246 markers.
> The historical record of the regression follows.
>
> ## (historical) NOT CLOSED — fix task `06/04` was outstanding
>
> The mechanical sweep verified clean and was independently re-measured:
> 265 → 0 non-context calls, 141 → 406 context calls (exact conservation),
> 237 → 0 methods without `ctx`, `go vet` unchanged, 245 markers, zero
> `context.Background()` in non-test files.
>
> **But this task's own "Done means" is not met on two counts:** `go test ./...`
> does not pass (`internal/service`, deterministic), and the failure is a
> behavioural change, which this task forbade. `UpsertWorkflowRunStep` now
> inherits the workflow's own deadline, so a timed-out workflow can no longer
> persist the record of its timeout.
>
> That is not a defect in the sweep — the sweep exposed a latent hazard that
> `Exec`-without-context was masking. Resolving it is
> `04-cancellation-safety-for-terminal-writes.md`. Do not mark this task
> complete or land the sweep until that closes.
>
> One correction carried forward: this file's completeness greps use
> `-h -o` before `grep -v _test`, which strips filenames first and therefore
> never excluded test files. `06/04` uses the corrected `--exclude` form.

## Context

### Finding addressed

**`GO-STORE-005`** (medium, from a Go quality/architecture audit of this
repository): *"Inconsistent `context.Context` propagation across the package:
only 22/63 files reference `context.Context` at all; `sessions.go` and
`agents.go` (the two hottest tables) have no context-taking methods at all,
while `teams.go`'s methods all take ctx."*

Independently re-verified against current `HEAD` on 2026-08-22: `grep -c 'ctx
context.Context'` returns **0** for both `sessions.go` and `agents.go`, and
**6** for `teams.go`. The finding is accurate and open.

### Why it matters

Without a context, a store call cannot be cancelled or time-bounded. A request
that the caller has already abandoned still runs its query to completion, and a
slow or lock-contended SQLite operation has no deadline. The inconsistency is
the sharper problem: a caller cannot tell by looking whether a given store
method respects cancellation, because 26% of them do and 74% don't.

### The measured baseline (this is your completeness oracle)

Taken 2026-08-22 against `internal/store/*.go`, excluding `_test.go`:

| Measure | Count |
|---|---:|
| Non-test files in `internal/store/` | 67 |
| Total exported `*Store` methods | 371 |
| Exported `*Store` methods **with** `ctx` | 134 |
| Exported `*Store` methods **without** `ctx` | **237** |
| Packages importing `internal/store` (fan-in) | 32 |

The original task draft reported 371 methods without `ctx` and a target of
505 methods with `ctx`. That count came from a grep whose match ended at the
opening `(`, so it counted all exported methods, including the 134 that
already took a context. Direct source-backed evidence against the clean task
baseline (`fc4ad513`) is: 371 total, 134 with `ctx`, and 237 without `ctx`.

`database/sql` call-site variants inside `internal/store/` (non-test):

| Context-aware (target) | Count | | Non-context (to convert) | Count |
|---|---:|---|---|---:|
| `QueryContext` | 50 | | `Query` | **70** |
| `ExecContext` | 86 | | `Exec` | **187** |
| `QueryRowContext` | 52 | | `QueryRow` | **111** |
| **total** | 188 | | **total** | **368** |

**The sweep is complete when `Query(`, `Exec(`, and `QueryRow(` all return zero
matches in non-test `internal/store/` files, the without-`ctx` method count
reaches zero, and all 371 exported methods take `ctx`.** Those conditions are
the acceptance test. Re-run the commands in "Verification" to confirm — do
not eyeball it.

### The pattern to copy

`internal/store/teams.go` already does exactly what every other file should.
Copy it rather than inventing a style:

```go
func (s *Store) GetTeam(ctx context.Context, id string) (*Team, error) {
	var t Team
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+teamColumns+` FROM teams WHERE id = ?`, id,
	)
	...
}
```

## What to do

### 1. Signatures

For every exported method on `*Store` that does not already take a context, add
`ctx context.Context` as the **first** parameter. Not second, not last, not
behind an options struct — first, always, matching `teams.go` and standard Go
convention.

Unexported helpers that perform database work should take `ctx` too, threaded
from their caller. Unexported helpers that do no database work and call nothing
that does should be left alone.

### 2. Call sites inside `internal/store`

Convert the 368 non-context `database/sql` calls to their context variants,
passing the `ctx` you just threaded in:

| Replace | With |
|---|---|
| `s.DB.Query(` | `s.DB.QueryContext(ctx, ` |
| `s.DB.Exec(` | `s.DB.ExecContext(ctx, ` |
| `s.DB.QueryRow(` | `s.DB.QueryRowContext(ctx, ` |

The same applies to calls on `*sql.Tx` and `*sql.Stmt` handles
(`tx.Exec` → `tx.ExecContext`, etc.). **Do not** change transaction
*structure* — if a method opens a transaction today, it opens one after;
`s.DB.Begin()` becomes `s.DB.BeginTx(ctx, nil)` and nothing else about the
transaction changes.

### 3. Callers across the other 32 packages

Every caller must now pass a context. The rule, in priority order:

1. **If the caller already has a real `ctx` in scope, pass it.** This is the
   common case and the whole point of the task — an HTTP handler has
   `r.Context()`, a method taking `ctx context.Context` has one already.
2. **If the caller has no context available, pass `context.TODO()`** — not
   `context.Background()` — and leave a marker comment on the same line:

   ```go
   rows, err := st.ListSessions(context.TODO(), limit) // TODO(ctx-sweep): no ctx available at this call site
   ```

   `context.TODO()` is the correct signal for "a context belongs here and
   nobody has plumbed one yet," and the marker makes every such site greppable
   afterward. **Do not** refactor the caller to acquire a real context — that
   is a behavioural change and it is out of scope. Just mark it.
3. **In `_test.go` files, use `context.Background()`.** Tests genuinely have no
   ambient context and `TODO` markers there would be noise.

Report the final `TODO(ctx-sweep)` count in your Work log. It is a useful
number: it measures exactly how much of the codebase has no context plumbing at
all, which is follow-up work nobody has scoped yet.

### 4. What NOT to do

- **No behavioural changes.** No timeouts added, no cancellation semantics
  introduced beyond what passing the context inherently provides, no retry
  logic, no error-handling improvements.
- **No new interfaces.** A separate decision (AD-14's other half) explicitly
  declines to introduce narrow consumer-defined interfaces for
  `internal/store`. Do not add any.
- **No renames, no signature changes beyond adding `ctx`**, no parameter
  reordering, no changing return types.
- **No fixing bugs you find.** Write them in the Work log. Someone else owns
  them.
- **No touching files outside `internal/store/` and the call sites that must
  change to compile.** In particular do not run a repo-wide formatter; a
  separate task owns the formatting backlog and a stray `gofmt` sweep here
  would collide with it badly.
- **No migrations.** This task needs no schema change. If you think it does,
  stop and report.

## Isolation — the binding constraint

This sweep touches all 67 store files and call sites in 32 packages. That makes
it **structurally incompatible with concurrent work**, in the same way a
repo-wide format sweep is:

- It **must run alone**, on a branch cut from a known-clean `main`, with no
  other task in flight.
- It **must land in one merge**, not incrementally over days, because a
  half-swept package does not compile.
- The operator sequences this. Do not start it because the file exists; start
  it when the operator tells you to.

If `main` moves under you mid-sweep, rebase and re-run the completeness
commands rather than assuming your earlier counts still hold.

## Verification

Run all of these. Every one must pass.

```bash
# 1. Completeness — all three must print 0
grep -rhoE '\.Query\(' internal/store/*.go | grep -v _test | wc -l
grep -rhoE '\.Exec\(' internal/store/*.go | grep -v _test | wc -l
grep -rhoE '\.QueryRow\(' internal/store/*.go | grep -v _test | wc -l

# 2. Every exported *Store method takes ctx — first must print 0
grep -rhE '^func \(s \*Store\) [A-Z][A-Za-z0-9]*\(' internal/store/*.go \
  | grep -vE '^func \(s \*Store\) [A-Z][A-Za-z0-9]*\(ctx context\.Context' \
  | wc -l   # 237 before; 0 after
grep -rhoE '^func \(s \*Store\) [A-Z][A-Za-z0-9]*\(ctx context\.Context' \
  internal/store/*.go | wc -l   # 134 before; 371 after

# 3. Builds and passes, unchanged
go build ./...
go vet ./...          # see note below
go test ./...
go test -race ./internal/store/... ./internal/service/... ./internal/api/...

# 4. Formatting clean on what you touched (NOT repo-wide)
gofmt -l $(git diff --name-only main | grep '\.go$')
```

**Note on `go vet`:** it is **not** clean on this repo today, and that is
expected. It reports exactly 4 findings, all in
`internal/service/container.go` (`stopReaper`/`stopRuntimeReaper` not used on
all paths). That is a separate, already-catalogued defect owned by another
task. **Your requirement is that `go vet` reports those 4 and nothing else** —
if a fifth appears, you introduced it.

**On the test suite:** existing tests must pass *unmodified except for adding
`context.Background()` at store call sites*. If a test needs a real logic
change to pass, you have changed behaviour — stop, revert that piece, and
report it in the Work log.

## Done means

- All three completeness greps return 0; the exported-method count moves
  237 → 0 without-`ctx` and 134 → 371 with-`ctx`.
- `go build ./...`, `go test ./...`, and the `-race` run above all pass.
- `go vet ./...` reports exactly the 4 pre-existing `container.go` findings and
  no others.
- Zero behavioural changes. The diff is signatures, call sites, `database/sql`
  variant swaps, and `context.TODO()`/`context.Background()` insertions —
  nothing else.
- The Work log records: the final `TODO(ctx-sweep)` marker count, any bugs
  noticed but deliberately not fixed, and any place where the mechanical rule
  didn't cleanly apply and you had to make a judgement call.

## Work log

- Baseline: clean `main` at `fc4ad513`, synchronized with `origin/main`.
- Corrected the exported-method oracle before completing the sweep. Evidence
  from `git grep` against `HEAD`: 371 total exported `*Store` methods, 134
  with `ctx`, and 237 without. The original 371-without/505-target figures
  double-counted the 134 context-taking methods because the original grep
  matched only through the opening `(`.
- Final completeness: non-context `Query` = 0, `Exec` = 0, `QueryRow` = 0;
  exported methods without `ctx` = 0; exported methods with `ctx` = 371.
- Final `TODO(ctx-sweep)` marker count after follow-up `06/04`: **246**. All are in non-test Go
  files, all use `context.TODO()`, and no newly added production call site
  uses `context.Background()`.
- Follow-up `06/04` found one additional `context.TODO()` in
  `chatServiceImpl.recordUtilityMetrics` whose selector and call had been
  split across lines by the sweep's AST rewrite, with an unrelated function
  comment displaced inside the expression. Restoring the expression and its
  required marker corrected the reported count from 245 to 246.
- Mechanical exceptions handled: `ListSessions` is variadic, including two
  zero-option test calls, so caller detection checked the first argument's
  type instead of relying only on arity. Existing store-backed consumer
  interfaces and their test doubles received matching first-position context
  parameters. A name/shape collision with the unrelated
  `coordination.CoordStore.Close()` and MCP transport `Close()` interfaces was
  detected during the first build and excluded; those interfaces and calls
  remain unchanged.
- Bugs noticed but deliberately not fixed: none.
- Verification: `go build ./...` passed; `go test ./...` passed;
  `go test -race ./internal/store/... ./internal/service/... ./internal/api/...`
  passed; `gofmt -l` on all touched Go files produced no output; and
  `git diff --check` passed.
- `go vet ./...` reported exactly the four pre-existing findings and nothing
  else: `internal/service/container.go:1213` and `:1233` report
  `stopReaper`/`stopRuntimeReaper` not used on all paths, with the paired
  reachable return at `:1293`.

## Review notes
