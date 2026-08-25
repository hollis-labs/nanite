# Gate Integrity — Wave A handoff to the next Orchestrator

You have no memory of Wave A. Everything you need is here or in the repo.
**Written at `d35085ae`** (`git rev-parse --short HEAD`). Every number below
ships the command that produced it and was derived at that commit by the
doc-writer, not copied from a task file. Re-derive before acting; they drift.

Wave A ran `08` → `01` → `02` → `03` → `04a`, serially, against
`docs/engineering/orchestrator-kickoffs/gate-integrity-wave-a.md`. All five are
`reviewed` in `TASKS/INDEX.md`. 33 commits:
`git log --oneline 77137106..HEAD | wc -l` → `33`.

---

## 1. The end state, with the command for each assertion

Run these before you trust anything downstream of Wave A.

| Assertion | Command | Expect |
|---|---|---|
| `go.mod` has no `replace` of any kind | `grep -c '=>' go.mod` | `0` |
| …not even a non-`libs` one | `grep -c '^replace' go.mod` | `0` |
| The workflow checks out only nanite | `grep -c 'actions/checkout' .github/workflows/full-repo-quality.yml` | `1` |
| The workflow never mentions the sibling tree | `grep -c 'libs/' .github/workflows/full-repo-quality.yml` | `0` |
| Workflow step count (YAML `- name:`) | `grep -c '^      - name:' .github/workflows/full-repo-quality.yml` | `13` |
| Commit time is formatting only | `grep -c 'go-vet:\|go-lint:' lefthook.yml` | `0` |
| …and the live command set agrees | `lefthook dump` → `pre-commit.commands` | `frontend-lint` (`skip: true`), `go-format`, `migration-purity` |
| `pre-push` is branch-scoped and nothing else | `lefthook dump` → `pre-push.commands.go-test` | exactly `run: go test ./...` + `only: - ref: main`. **No `glob`, no `skip_empty`.** |

Sibling versions now resolved from the proxy (`grep -n 'hollis-labs/go-envelopes\|hollis-labs/go-modelsdev\|hollis-labs/go-harness-filters\|hollis-labs/go-runtime-events' go.mod`):

```
go.mod:20   go-envelopes        v0.3.0
go.mod:21   go-modelsdev        v0.2.0
go.mod:39   go-harness-filters  v0.1.1 // indirect
go.mod:102  go-runtime-events   v0.1.1
```

All four are on the proxy — asked over HTTP, **not** via `go list -m -versions`
(hazard §3.11):

```
for m in go-envelopes go-modelsdev go-harness-filters go-runtime-events; do
  printf '%-20s ' "$m"
  curl -sS "https://proxy.golang.org/github.com/hollis-labs/$m/@v/list" | tr '\n' ' '; echo
done
#  go-envelopes        v0.3.0 v0.1.0 v0.1.1 v0.2.0
#  go-modelsdev        v0.1.0 v0.2.0
#  go-harness-filters  v0.1.0 v0.1.1
#  go-runtime-events   v0.1.0 v0.1.1
```

**Independently re-verified by the doc-writer at `d35085ae`, not taken from a
Work log:** cloned this repo into the session scratchpad at a path whose
`../../libs` resolves to a directory confirmed absent (hazard §3.12 — check the
*resolved* path, `ls -la "$(cd <clone>/../.. && pwd)/libs"` → `No such file or
directory`; `/private/tmp/libs` and `/tmp/libs` are also absent now). There:

```
grep -c '=>' go.mod                 -> 0
go build ./cmd/nanite/              -> exit 0
go list -m -f '{{.Path}}@{{.Version}} {{.Replace}}' <the four>
    go-envelopes@v0.3.0 <nil> · go-modelsdev@v0.2.0 <nil>
    go-harness-filters@v0.1.1 <nil> · go-runtime-events@v0.1.1 <nil>
```

The clone was deleted afterwards; the main tree is clean
(`git status --porcelain` → empty, one worktree).

---

## 2. What Wave A did **not** deliver — read this before scoping Wave C

**The repo is self-contained for Go. It is not for the frontend.** Three call
sites still hardcode `../../libs/go-envelopes`. Re-derive them yourself:

```
grep -rn "\.\./\.\./libs\|'libs'" --include='*.mjs' --include='Makefile' scripts/ Makefile
#  scripts/generate-plugin-imports.mjs:23   resolve(ROOT,'..','..','libs','go-envelopes','manifest','envelopes.yaml')
#  scripts/generate-envelope-types.mjs:36   resolve(ROOT,'..','..','libs','go-envelopes','manifest','schemas')
#  Makefile:24,27,31                        --manifest-dir ../../libs/go-envelopes/manifest/schemas
```

Two of them run automatically:

```
grep -n 'prebuild\|predev' ui/package.json
#  17: "prebuild": "node ../scripts/generate-plugin-imports.mjs && node ../scripts/generate-envelope-types.mjs"
#  18: "predev":   (identical)
grep -n '^install:' Makefile        #  20: install: generate-envelopes build-ui
```

So `npm run build`, `npm run dev` and `make install` still require the sibling
tree. Measured in the decoupled clone above, at `d35085ae`, exit status read
directly (not through a pipe — see §5):

```
node scripts/generate-plugin-imports.mjs --check   -> exit 1
    ERROR: Core envelope manifest not found: <clone>/../../libs/go-envelopes/manifest/envelopes.yaml
node scripts/generate-envelope-types.mjs           -> exit 1
make generate-envelopes                            -> exit 2  (make: *** Error 1)
```

**This is the single most important thing for Wave C.** The batch README says
Wave C's container shape must not be scoped until `01`-`03` has landed, because
scoping it earlier would bake in the workaround. `01`-`03` has landed *for Go
only* — a container built on today's tree can `go build` from source plus proxy
access, but cannot build the UI or run `make install` without the sibling repos
mounted. Decide the manifest-resolution mechanism before, or as part of,
scoping the image. `generate-envelope-types.mjs` already accepts
`--manifest-dir`; `generate-plugin-imports.mjs` has no equivalent flag. The
most plausible fix, recorded in `01`'s Work log and not implemented, is to read
the manifest directory from
`go list -m -f '{{.Dir}}' github.com/hollis-labs/go-envelopes`. That is a design
decision, not a mechanical edit.

This gap is stated in `TASKS/INDEX.md`'s keystone paragraph and in `01`'s Work
log under "Deliberately not done." It has no task file. It affects no Go build,
no `go test`, and no quality-gate step: the gate's 13 steps
(`grep -nE '^      - name:' .github/workflows/full-repo-quality.yml`) contain no
frontend step at all — the only `npm`/`node` occurrences in that file are two
comment lines about the package count, at `:68` and `:70`.

---

## 3. Wave B's gate: `07`, and why it is now more urgent

`TASKS/gate-integrity/07-migration-number-collision-guard.md` is `not-started`
and is the whole of Wave B. It guards the one **unrecoverable** failure class in
this repo.

Goose selects migrations by version number alone. In `UpVersions`
(`internal/gooseutil/resolve.go`, goose v3.27.3) the applied set is a map keyed
on the version integer — no filename, no checksum — and both selection loops
skip any version already in it. So for a file numbered N at or below a
database's highest applied version, **which of two things happens depends on
whether that database has already applied N**:

- **N not previously applied** → collected as missing; since
  `internal/store/store.go` builds its provider without `WithAllowOutofOrder`,
  the run fails with a missing-migration error. Hard boot error.
- **N already applied** → both loops skip it. The file never runs, nothing is
  reported, goose considers the database up to date. **Silent.**

There is no third case: a version equal to the highest applied version is by
construction already applied. The silent branch is the dangerous one — schema
divergence between databases of different vintages, with no startup failure to
announce it.

Its two structural claims still hold at `d35085ae` — re-derived:

```
grep -n 'goose.NewProvider' internal/store/store.go
#  153:  provider, err := goose.NewProvider(goose.DialectSQLite3, s.DB, migrationsDir, goose.WithVerbose(false))
grep -c 'WithAllowOutofOrder' internal/store/store.go        -> 0
ls internal/store/migrations/*.sql | wc -l                   -> 147
ls internal/store/migrations/*.sql | sed 's#.*/##' | sort -n | tail -1
#  -> 148_us_english_canceled_status.sql
```

And nothing guards it: `migration-purity` greps staged file *contents* for a
`VALUES` clause and never reads a filename —

```
awk '/^    migration-purity:/,/^    frontend-lint:/' lefthook.yml |
  grep -cE 'basename|filename|[0-9]{3}_'      -> 0
```

**Wave A made `07` more urgent, not less.** The argument for deferring it was
that a collision is near-impossible while working serially. Worktrees outside
`~/dev/hollis-labs/apps/` now build (§1), which is exactly the condition that
makes parallel authoring practical — and the moment two worktrees each author a
migration, the guard is load-bearing. Do not switch on parallel worktree work
before `07` lands.

Note `07`'s own scope fence: like every hook, its guard only protects clones
where `lefthook install` has run. The fresh-clone gap is explicitly out of
scope for this batch.

---

## 4. Deviations from the plan, and why

Two matter. Both are recorded in the task files; neither was an escalation this
session could cite, because `TASKS/ESCALATIONS.md` was declared out of scope by
the operator for the wave's closing pass.

**A. The decoupling proofs used a scratch clone, not "move `libs/` aside."**
`03`'s *Done means* is explicit: `go build ./...` and `go test ./...` must pass
**with the entire `~/dev/hollis-labs/libs/` tree moved aside**. That was not
done. `03`'s Work log records the reason as an operator instruction: **a
parallel session was live inside those sibling repositories**, and moving the
tree would have pulled a directory out from under it. (`01`'s criterion already
offered the clone as an explicit alternative — *"or by building in a clone that
has no `libs/` sibling tree at all"* — so only `03` genuinely deviated.)

`03`'s substitute was a fresh `git clone` into the session scratchpad at a path
where the relative replace path cannot resolve, with a **fresh empty
`GOMODCACHE`** and the `GOPRIVATE`/`GONOPROXY`/`GONOSUMDB` overrides cleared
using `=none`, so the checksum database genuinely applied. Its worker ran a
negative control first — appending the two `replace` lines back onto the clone's
`go.mod` and observing `replacement directory ../../libs/... does not exist` —
so the proof's mechanism was shown able to fail. **This substitute is stronger
than the prescribed method for what it proves**: it demonstrates a clone
resolving everything from the proxy, where renaming two directories in the main
tree only shows the build survives their absence.

The doc-writer re-ran a **lighter** version at `d35085ae` (§1) — same
resolved-path check and same clone geometry, but using the shared module cache
rather than a fresh empty one, so it re-establishes "no sibling path is needed"
and does **not** re-establish "everything came from the proxy." If you write a
future decoupling criterion, prefer the clone form and require the resolved-path
check from hazard §3.12.

**B. `08` was closed, then reopened, to remove a `pre-push` `glob`.** `08`
originally kept `glob: "*.go"` on `pre-push`'s `go-test` — the task file
explicitly instructed keeping it. A fresh review of `03` found it was a **silent
fail-open**: `*.go` matches neither `go.mod` nor `go.sum` nor any embedded
input, so a migration-only or `go.mod`-only push to `main` ran no tests and
printed `go-test (skip) no matching push files`, which reads as a benign skip.
Derived at `d35085ae`:

```
grep -rn '^//go:embed' --include='*.go' . | grep -v node_modules | wc -l   -> 22
ls internal/store/migrations/*.sql | wc -l                                 -> 147
```

Operator decision, 2026-08-25: drop the `glob` entirely and let
`only: - ref: main` carry the whole scoping. The rejected alternative —
enumerating `go.mod`/`go.sum` into the glob — was rejected because it covers
only the case someone noticed and goes stale against the next `go:embed`
addition. Accepted cost: a docs-only push to `main` also runs the suite. The
proof was run in a `--no-hardlinks` bare clone used as a real `origin`, one
push set at a time, with the shipped `lefthook.yml` bytes verified by
`git hash-object` rather than paraphrased.

**Two smaller deviations, both recorded and both fine:**

- `08` deviated from its own step 2, which prescribed a bare `golangci-lint run`
  in the landing script. Whole-repo lint exits 1 on a large pre-existing body of
  findings, so a stage built on it could never pass. `scripts/check.sh`'s lint
  stage runs `golangci-lint run --new-from-rev "$(git merge-base HEAD origin/main)"`,
  overridable with `CHECK_LINT_BASE`, and reports a third outcome —
  `examined nothing` — that is neither OK nor FAIL, so a zero-changed-files run
  cannot read as "examined and clean."
- `04a` deviated from step 6's "point at the wrapper." The wrapper is `04b` and
  does not exist, so the printed advisory states the *requirement* (re-run, confirm
  the reduction reproduces, then move the baseline) and names no tool; only the
  code comment names `04` step 2 as the planned automation.

---

## 5. Hazards added to `docs/engineering/agent-verification-discipline.md` §3

Five landed this wave. Derive the set with:

```
git show 77137106:docs/engineering/agent-verification-discipline.md | grep -cE '^### 3\.'   -> 7
grep -cE '^### 3\.' docs/engineering/agent-verification-discipline.md                       -> 12
```

These are the most reusable thing Wave A produced. Several were found by workers
correcting durable docs written *earlier in the same session* — including two
corrections to §3.11 itself.

| § | Hazard | Landed in |
|---|---|---|
| **3.8** | `mkdir` is aliased to `mkdir -pv` here and writes the directory name to stdout — inside `$(...)` it contaminates the captured value, and in a transcript it reads as the *next* command's output. Cost one wrong reading of a `gofmt` result. Also records that §3.4's `cp` alias did **not** reproduce in the same shell (`type cp` → `/bin/cp`): check the shell you are in rather than assuming §3.4's list is current. | `1e9215ca` |
| **3.9** | `gofmt -l` / `goimports -l` print the file list to stdout and errors to stderr, so a missing binary (127), an unparseable file (2) or an unreadable path all produce **empty stdout** — identical to "everything is formatted." Any check on `gofmt -l` must inspect the exit status. This shipped twice in this repo, in `scripts/check.sh`'s format stage and in `lefthook.yml`'s `go-format` hook; both are now fixed. | `1e9215ca` |
| **3.10** | `git ls-remote origin refs/tags/<t>` returns the **tag object**, not the commit, for an annotated tag — comparing it to a pinned commit reports a spurious mismatch. Peel with `^{}`. All four `hollis-labs/libs` siblings use annotated tags. `git rev-list -n1` and `git describe` peel on their own, so the hazard is specific to `ls-remote`. | `7f868db9` |
| **3.11** | `GOPRIVATE=github.com/hollis-labs/*` defaults `GONOPROXY` to the same value, and `GONOPROXY` beats a `GOPROXY=` prefix — so `go list -m -versions` answers from `git ls-remote` and **never contacts the proxy**. It returns the same "yes" whether or not the version is published, which is a vacuous check wherever "is this published?" is the question. Ask the proxy with `curl`, and check for the version *string*, not exit status (`curl -sS` without `-f` exits 0 on a 404). | `82a1de6c`, extended `325a6c21`, corrected `e820e5c7` |
| **3.12** | Verify a "path does not exist" proof **at the resolved path**. Directory depth does not establish absence: symlinks, `$TMPDIR` indirection and macOS's `/tmp` → `/private/tmp` all break the inference. Never write "no `libs/` anywhere above it" — say which path you checked and what it returned. | `c169830f`, generalized `60cfb1be` |

**§3.11's two corrections are worth reading in full before any module work.**
`03`'s worker found (a) `GOPRIVATE=` with an *empty* value does **not** clear it
— Go falls back to the `GOENV` file and the value survives; only `=none` works,
and the section's own original escape hatch was therefore inert, leaving
`GONOSUMDB` set and the checksum DB bypassed; and (b) `Origin.VCS=git` in a
cached `.info` does **not** prove a direct VCS fetch — proxy.golang.org serves
that same field byte-identically. The working form is
`GOPRIVATE=none GONOPROXY=none GONOSUMDB=none GOPROXY=https://proxy.golang.org`.
A consequence stated there: **a local `go.sum` and a local `GOMODCACHE` are not
evidence about published bytes.** That invalidated `01`'s original byte-identity
proof, which compared git against git; `02`'s reviewer closed it properly by
diffing the proxy's `.zip` against `git archive <tag>`.

One more environment note, recorded in `03`'s Work log rather than in §3: in
this `zsh`, `$?` after a pipeline reports the **last** command's status. `cmd |
head; echo $?` reports `head`'s zero. Use `$pipestatus` (lowercase, in zsh) or
redirect to a file. Also: **do not run `go mod download all` before
`go mod tidy -diff` in a verification clone** — it writes `go.sum` and produces
a large phantom diff.

---

## 6. Discovered mid-wave, not in the original plan

- **The gate's step count moved and the runbook's derivation command moved with
  it.** `01` and `03` deleted four checkout steps; the workflow's YAML `- name:`
  count went 17 → 13 (`grep -c '^      - name:' .github/workflows/full-repo-quality.yml`).
  None of the four asserted anything, so the runbook's **8 assert / 7 of those 8
  sound** split is unchanged. The runbook now also names *which* of the two
  colliding "step count" figures it means, because `gh run view` reports a
  different one. Use the runbook's "What this gate guarantees" section verbatim
  when you report a gate result.
- **The workflow still checks Nanite out at `apps/nanite`** with a matching
  `defaults.run.working-directory`. That path existed only to preserve the
  `../../libs` geometry and no longer needs to. Flattening it touches
  `go-version-file`, `cache-dependency-path` and the artifact paths. Parked by
  `03`, no task file. Relevant to Wave C.
- **A coverage floor already exists on the lint side** (`verify_lint_coverage`,
  total findings against 50% of the Stage 1 baseline). What `04b` step 3 asks for
  — a floor on gosec's `Stats.files`/`Stats.lines` — genuinely does not exist.
  Different sides, different keys. `04`'s reviewer wrote this into step 3 so its
  implementer does not mistake one for the other.
- **`Stats.found` moves with a finding drop.** `04`'s reviewer disproved the
  "identical `Stats`" phrasing: `Stats` is `{files, lines, nosec, found}` and
  `found == len(Issues)`. Only `files` and `lines` were identical in the suspect
  run. This is why `04b`'s floor is keyed on those two and deliberately not on
  counts.
- **`lefthook validate` exits 1, and always has.** Three `skip_empty` keys remain
  on the pre-commit commands; the key is not in lefthook 2.1.4's schema. `08`
  reduced the carriers from six to three across two passes. Removing the last
  three is a real cleanup with a small behavioral risk and is parked, not done.
  Verify with `lefthook validate; echo $?` → `1`, three complaints.
- **`internal/memory` / `SQLITE_BUSY` (Torque `CW-20260825-0001`) did not fire**
  in either Wave A gate run. Still a known intermittent; report and move on.

---

## 7. Things to check before you trust the status column

Flagged for the operator in `WAVE-A-SUMMARY.md` as well. Do not silently work
around them.

1. **`TASKS/INDEX.md` says all five Wave A tasks are `reviewed`; every task
   file's own `**Status:**` field says `implemented`.** `sed -n '4p'` on each of
   `01`, `02`, `03`, `08` returns `**Status:** implemented`; `04`'s reads
   "`04a` implemented … `04b` … **not-started**". The convention elsewhere in
   this repo is to set the field to `reviewed`
   (`grep -h '^\*\*Status:\*\*' TASKS/audit-remediation/*/*.md | sort | uniq -c`
   → 66 `reviewed`). A reader of a task file alone cannot tell it was reviewed.
2. **`03`'s `## Review notes` section is empty** — the heading is the last line
   of the file (`tail -1 TASKS/gate-integrity/03-*.md` → `## Review notes`).
   `c169830f`'s commit message says "03 reviewed PASS" and `INDEX.md` records
   `reviewed`, but no verdict, findings or independent re-derivations were
   transcribed. `01`, `02`, `04a` and `08` all carry full transcriptions. `03`
   is the keystone change; its review evidence is the one you would most want.
3. **`08`'s fifth pass records a shared-checkout incident that has since been
   resolved, and the Work log now misdirects.** It says commit `517721fa`
   swept this pass's uncommitted work into a peer's commit, that it was "Not
   rewritten," and that "`517721fa` is the commit where the `pre-push` glob was
   removed, despite its subject line." At `d35085ae` that is false:
   `git merge-base --is-ancestor 517721fa HEAD` exits non-zero, and the change
   was split into `60cfb1be` (discipline doc only) and `aedbc296`
   (`lefthook.yml` + `CLAUDE.md` + `AGENTS.md` + `testing-workflow.md`) — which
   is the correct attribution. **`aedbc296` is where the glob was removed.**
4. **`01`'s Review notes mischaracterize two standards docs.** They call
   `docs/engineering/standards/patterns.md` and `standards/code-quality.md`
   "stubs whose only heading is *'Not yet documented'*." Both carry substantive
   bullet lists followed by a trailing `## Not yet documented` section
   (`wc -l` → 14 and 12), and `code-quality.md` contains the exact
   *"A silent fail-open is worse than a loud failure"* line that `08`'s fifth
   pass cited as authority. Neither changed this wave
   (`git diff --stat 77137106..HEAD -- docs/engineering/standards/` → empty), so
   this was wrong when written. `EXECUTION-PROCESS.md:62` does point reviewers
   there, and the checklist items it names are present. Treat "the standards docs
   are empty" as disproved.
5. **Nine commits are unpushed.** `git rev-list --left-right --count origin/main...HEAD`
   → `0 9`; `origin/main` is `83ac15eb`. The unpushed set includes **all of
   `04a`'s code** (`scripts/quality-ratchet.py`, `scripts/quality-ratchet_test.py`)
   and **`08`'s pre-push widening** (`lefthook.yml`). Neither Wave A gate run
   covers them. Note the first push of these will itself run the full suite,
   because the widened `pre-push` now fires on every push to `main`.

---

## 8. Verification you should insist on in Wave B

Carried forward because it worked. Workers reliably try to satisfy these by
inspection.

- **Prove behavior, not YAML.** `08`'s acceptance was met by committing a file
  with a deliberate `go vet` failure and showing the commit *succeeds*, and a
  misformatted file and showing it is *rejected*. Both, with real output.
- **Every zero needs a positive control.** `01` and `03` both ran each `grep -c
  … → 0` against the parent revision's blob and showed the same command
  returning non-zero there. A count of zero from a command that should match
  something is the cheapest signal the command is wrong.
- **A "path does not exist" claim needs the resolved path** — hazard §3.12,
  learned the expensive way this wave.
- **Ask the proxy over HTTP, never `go list -m -versions`** — hazard §3.11.
- **A decoupling proof needs its failure mode observed first.** `03` appended
  the replaces back onto the clone's `go.mod` and watched the build fail before
  claiming the clean build meant anything.
