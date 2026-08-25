# Guard migration numbers against collision and back-fill — the one unrecoverable failure class

**Phase:** 1 — Safe parallel work
**Status:** implemented
**Depends on:** none
**Touches:** `lefthook.yml`, a new check script (suggest
`scripts/check-migration-number.sh` — match whatever convention the repo's
other hook-invoked scripts use), and `docs/engineering/tracking-integrity.md`
(check 9). Repo: nanite.

## Context

**This is the task that makes parallel batch work safe.** Everything else the
gate catches is directional and recoverable — a lint regression, a gosec
increase, a race flake, all get found on the next manual gate run and fixed in
a follow-up. A bad migration number is different in kind, and *which* kind
depends on the database it lands on: **a service that fails to start**,
discovered at boot, or a migration that **silently never runs** on a database
that already has that number in its ledger.

Verified at `77137106`:

```
grep -n 'goose.NewProvider' internal/store/store.go
#   153:  provider, err := goose.NewProvider(goose.DialectSQLite3, s.DB, migrationsDir, goose.WithVerbose(false))
```

No `WithAllowOutofOrder`, so `allowMissing` is false — and what that produces
is **two outcomes, not one**. `UpVersions` in goose's `internal/gooseutil`
package keys its applied set on the version integer alone — no filename, no
checksum — and both of its selection loops skip any version already in that
set. So for a file numbered at or below a database's highest applied version,
which of two things happens depends on whether **that** database has already
applied that number:

- **Not previously applied** — it is collected as missing and the run fails
  with goose's missing-migration error. A hard boot error. Reproduced at goose
  v3.27.3 with Nanite's exact options as
  `detected 1 missing (out-of-order) migration lower than database version (137): version 135`.
- **Already applied** — both loops skip it. It never runs, nothing is
  reported, and goose considers the database up to date. **Silent.**

There is no third case: a version equal to the highest applied version is by
construction already applied. **The silent branch is the dangerous one** —
schema divergence between databases of different vintages, with no startup
failure to announce it. The fourth Work log pass below, "The 'hard boot error,
not a back-fill' framing was half the story", has the full reading of
`UpVersions` this rests on.

Nothing guards this today. `migration-purity` is the only migration-aware hook
and it greps staged *file contents* for a `VALUES` clause — it never reads a
filename:

```
grep -n 'migration-purity' -A 30 lefthook.yml | grep -n 'VALUES\|filename\|basename'
```

And it **structurally cannot** be fixed in place: a staged-file hook sees only
the files in its own worktree, so two agents each creating `149_*.sql` in
separate worktrees both pass, and the collision only exists once both reach
`main`.

Two distinct failure shapes, and a correct check must cover both:

1. **Collision** — two worktrees both claim the next number. Duplicate prefix
   on `main`.
2. **Back-fill into a burned hole** — `135` is empty (`63d79028` shifted Loops'
   `135`–`143` to `138`–`146` to clear a collision with Skills) and *looks*
   available. It is not. The live database's ledger max is `137` with no `135`
   row. **"Next free" is one past the highest, never the lowest unused
   integer.** `[[nanite_migration_holes_permanently_burned]]`

The claiming rule is already published — `TASKS/INDEX.md`'s "Migration
numbering" section and `docs/engineering/tracking-integrity.md` check 9. **This
task turns that documented rule into a mechanism.** A rule with no mechanism
behind it decays, and this one decays into a boot failure on some databases
and a silent divergence on the others.

## What to do

1. Write a check that, for each **added** migration file in the commit,
   compares against `main`'s tree rather than the local worktree:
   ```
   git ls-tree --name-only main -- internal/store/migrations/ | sort -t_ -k1 -n | tail -1
   ```
   Assert both properties: the new number is **not already present** on `main`,
   and it is **strictly greater than `main`'s highest** — the second is what
   covers the burned-hole case, and a check that only tests uniqueness will
   happily accept `135`.
2. Decide and document which hook it belongs on. **`pre-push` is the better
   fit than `pre-commit`** — the check needs `main` to be current, a commit can
   legitimately be made offline, and pre-push is where `go test ./...` already
   lives. Record the reasoning either way; this is an adjustable call, not a
   decided one.
3. Handle the case where `main` is stale or absent (fresh clone, detached
   worktree, offline). **Fail loudly with an actionable message, do not skip
   silently.** A guard that quietly no-ops when it cannot reach `main` is worse
   than no guard, because it reads as protection.
4. Make the failure message tell the author what to do — print `main`'s current
   highest, the number they used, and the number they should use. This check
   fires on someone mid-commit who needs to rename a file, not on someone
   reading a spec.
5. Cross-reference `docs/engineering/tracking-integrity.md` check 9 so the
   documented rule and the mechanism name each other.

## Done means

- A migration numbered at or below `main`'s highest is **rejected**, proven by
  actually attempting it — create `135_probe.sql` in a scratch branch and show
  the hook rejecting it, with the output in the Work log. Do not accept this
  task on code inspection.
- A duplicate of an existing number on `main` is rejected, proven the same way.
- The next legitimate number is **accepted**, proven the same way. A check that
  rejects everything passes the two tests above and is useless.
- The unreachable-`main` path fails with an actionable message, proven by
  running it with a bogus ref.
- The reasoning for pre-commit vs pre-push is recorded in the Work log.
- `docs/engineering/tracking-integrity.md` check 9 names the script, and the
  script names check 9.
- **Note for the reviewer:** this hook only protects clones where
  `lefthook install` has run. That is a real residual gap, not something this
  task can close — see the batch README's scope fence. Do not let it block
  acceptance, and do not let the Work log claim more coverage than that.

## Work log

**Worker, 2026-08-25.** Started at `b8d3aa61` on `main` in the main tree.
`git status --short` at start showed only the Orchestrator's two status flips
(`TASKS/INDEX.md`, this file); no other work in flight.

### What landed

| File | Change |
|---|---|
| `scripts/check-migration-number.sh` | **new**, mode 755 — matches `scripts/check.sh`'s `100755` (`git ls-files -s scripts/check.sh`). *(A line count stood here and rotted: it read `313` and was `355` by the time pass two ended. Deriving it is `wc -l < scripts/check-migration-number.sh`; it is not restated, because a stored line count in a document arguing against stored numbers is the defect it argues against.)* |
| `lefthook.yml` | new `pre-push` command `migration-number`; header's push-time paragraph extended to describe both commands |
| `docs/engineering/tracking-integrity.md` | new subsection under check 9: "The mechanism: `scripts/check-migration-number.sh`" |

No Go files touched — `git status --short | grep -c '\.go$'` → `0` (positive
control: `printf ' M foo.go\n' | grep -c '\.go$'` → `1`). That is why
`check.sh`'s lint stage reports `examined nothing`; see the acceptance section.

### Step 1's command has a dead sort key — corrected, and the correction is load-bearing

The command this task file gives in step 1 does not order migration numbers.
Under `-t_`, field 1 is the whole path prefix `internal/store/migrations/148`,
which `-n` reads as `0` on every line, so the numeric key never discriminates
and `sort` falls through to a byte comparison:

```
$ printf 'internal/store/migrations/9_a.sql\ninternal/store/migrations/148_b.sql\n' | sort -t_ -k1 -n | tail -1
internal/store/migrations/9_a.sql        # wrong: 9 outranks 148
$ printf 'internal/store/migrations/9_a.sql\ninternal/store/migrations/148_b.sql\n' | sort -t_ -k1,1 -n | tail -1
internal/store/migrations/9_a.sql        # bounding the field does NOT fix it
$ sort --version | head -1
sort (GNU coreutils) 2.3-Apple (195)
```

It happens to return the right answer here only because every prefix is
uniformly 3 digits:
`ls internal/store/migrations/*.sql | sed 's#.*/##' | sed 's/_.*//' | awk '{print length($0)}' | sort | uniq -c`
→ `147 3`.

**I did not ship a sort at all.** The script extracts the integer
(`${base%%_*}`, then `$((10#$num))` so a zero-padded prefix is not read as
octal) and compares with `-gt`. Justification: it has no dependency on prefix
width, on `sort`'s implementation, or on locale; and the parse has to happen
anyway for the "already present" equality test, so a sort would be a *second*
representation of the same fact to keep in sync. The reasoning is recorded in a
comment on `migration_number()` so the next person does not re-introduce a sort.

**Proof against a synthetic 4-digit case, end-to-end rather than in isolation.**
On the scratch remote I landed a real `1000_four_digit.sql` (legitimate: 1000 >
the then-highest 151), then attempted `999`:

```
=== what the TASK FILE's sort form says the highest is ===
$ git ls-tree --name-only origin/main -- internal/store/migrations/ | sort -t_ -k1 -n | tail -1
internal/store/migrations/151_probe.sql          # WRONG — actual highest is 1000
=== what the shipped mechanism says ===
$ bash -x ./scripts/check-migration-number.sh 2>&1 | grep -E "^\+ remote_max=" | tail -1
+ remote_max=1000
=== pushing 999 ===
    origin/main's highest migration number: 1000
        used:   999  (at or below origin/main's highest (1000) — a hole, not a free slot)
        use:    1001
  RESULT: REJECTED
```

A sort-based implementation would have **accepted** 999 — a boot-breaking
back-fill. The basename form used in this doc's own "Deriving it" section
(`ls internal/store/migrations/ | sort -t_ -k1 -n | tail -1`) is *not* affected;
only the `ls-tree` path-based variant in step 1 is. I did not edit step 1 — the
task file is the Orchestrator's artifact — but it should not be copied forward.

### Ref mechanism: `git ls-remote` + a proven-fresh `origin/main` (option b)

Comparing against local `main`, as step 1 says literally, rejects every
legitimate migration in this repo. Work happens on `main` directly:

```
$ git rev-parse main HEAD | uniq -c
   2 b8d3aa61bbd614220cab17d24385e9f752e00303
```

so at pre-push time local `main` already contains the commit being pushed: the
new number *is* present and *is* the highest, so both assertions invert. The
comparison ref has to be the remote's state.

**The lefthook-stdin route (option a) is confirmed unavailable, not assumed
so.** A git pre-push hook receives `<local ref> <local sha> <remote ref>
<remote sha>` on stdin, but the installed hook is `call_lefthook run "pre-push"
"$@"` (`tail -2 .git/hooks/pre-push`) and nothing forwards the stream. I probed
it with a real push, against a `run:` command that reads whatever arrives:

```
┃  stdin-probe ❯
[stdin-probe] lines=0 content=[]
✔️ stdin-probe (2.01 seconds)
```

Zero lines, and the 2.01s is the `read -t 2` timeout expiring — so stdin was
neither fed nor closed. A comparison ref derived from it would silently resolve
to nothing and pass everything, which is exactly the failure class this task
exists to kill. **Shipped (b).**

The mechanism, in order, is: `git ls-remote origin refs/heads/main` for the
authoritative sha (no fetch, no stdin, nothing internal to lefthook) → compare
it to `git rev-parse refs/remotes/origin/main` → abort with `git fetch origin
main` if they differ → only then read the tree from `origin/main`, whose objects
we actually have. Exit status is inspected **before** output, because
`ls-remote` prints nothing both when the ref is absent and when it could not run
at all (hazard §3.9's shape; §3 currently has 12 entries,
`grep -cE '^### 3\.' docs/engineering/agent-verification-discipline.md` → 12).

### pre-push, not pre-commit

Recorded as step 2 asks. Pre-commit **structurally cannot** catch this: a
staged-file hook sees only the files in its own worktree, so two agents each
creating `149_*.sql` in separate worktrees both pass and the collision exists
only once both reach the remote. That is also why `migration-purity` could not
be extended — it greps staged *contents* for a `VALUES` clause and never reads a
filename, and a prefix check bolted onto it would still be comparing against the
staged set. Beyond that: this check needs the network, and `lefthook.yml`'s
stated design is that no pre-commit command may fail for a reason outside the
diff in front of you, because `--no-verify` is all-or-nothing — a
network-dependent check at commit time could take `go-format` down with it.
Push is the moment work leaves the machine and the moment the remote's state is
knowable. It is also where `go-test` already lives.

**No `glob`**, deliberately, per the same reasoning `go-test`'s comment block
spells out — and it bites harder here. `glob: "internal/store/migrations/*.sql"`
looks exactly right and is the worst option available: any push not itself
touching a migration would print `migration-number (skip) no matching push
files`, which reads as benign. The branch condition carries all the scoping;
the script derives its own file set.

### Deviations from the plan, and why

1. **`priority: 1` on the command.** I first placed `migration-number` above
   `go-test` in YAML and wrote a comment claiming it therefore ran first. That
   comment was **false** and a real push caught it — at lefthook 2.1.4 the
   commands run in *name* order, so `go-test` went first:
   `✔️ go-test (0.05 seconds)` then `✔️ migration-number (0.24 seconds)`. Adding
   `priority: 1` reverses it: `✔️ migration-number (0.17 seconds)` then
   `✔️ go-test`. Both directions observed on real pushes. `lefthook validate`
   accepts the key (`lefthook validate 2>&1 | grep -c 'migration-number'` → `0`;
   positive control `... | grep -c 'migration-purity'` → `1`). Note `lefthook
   dump` renders the commands in name order regardless and is **not** evidence
   of execution order; the comment says so.
2. **An intra-push duplicate check, beyond the literal instruction.** Step 1
   asks only for comparison against `main`'s tree. But the Context's failure
   shape 1 ("two worktrees both claim the next number") survives that check
   whenever the branches are merged into local `main` and pushed *together* —
   which is precisely what `EXECUTION-PROCESS.md` prescribes ("merge their
   branches back sequentially"). Two files both claiming `150` are both absent
   from the remote and both above its highest, so both pass a remote-only check.
   Added; ~5 lines; proven below.
3. **An empty remote migration set is a hard error, not a pass.** Per §4.2: an
   empty collection makes every assertion vacuously true, and an empty result is
   also exactly what a wrong `MIG_DIR` or a wrong ref looks like. This repo
   demonstrably has migrations on `main`
   (`git ls-tree --name-only origin/main -- internal/store/migrations/ | wc -l`
   → `147`), so the check now carries its own positive control on every run.
4. **`--no-renames` on the diff.** Not cosmetic. With default rename detection,
   renumbering an existing migration — literally what `63d79028` did — is
   reported as `R` and dropped by `--diff-filter=A`, so a renumber into a hole
   would sail through. Control against that real commit:
   ```
   $ git diff --name-only --diff-filter=A 63d79028^ 63d79028 -- internal/store/migrations/ | wc -l
   0
   $ git diff --no-renames --name-only --diff-filter=A 63d79028^ 63d79028 -- internal/store/migrations/ | wc -l
   9
   ```
5. **Non-`.sql` files skip; a `.sql` file with an unparsable prefix is a hard
   error.** `store.go:18` is `//go:embed migrations/*.sql`, so nothing else in
   that directory is a migration. A stray `README.md` there should not brick the
   guard; a `no_number.sql` must not be waved through.
6. **Two bugs in my own first draft, found and fixed before acceptance.**
   (a) `next_free` mutated `claimed` inside a `$(...)` subshell, so every
   rejected file in a multi-file push would have been told to use the *same*
   number; it now sets a variable in the caller's shell. (b) The rename hint
   stripped at `"${n}_"`, which fails on a zero-padded prefix (`008_x.sql` → `n`
   is `8`, and `*/8_` does not match); it now strips at the basename's first
   underscore. (c) A parse-only failure still printed the per-file summary block
   with an empty file list, which read as a broken check; suppressed.

### Acceptance — every case run for real, both directions

Testing used a `--no-hardlinks` bare clone as `origin` plus a working clone
under the session scratchpad, with `lefthook install` run in it. **No `.sql`
file was created under the real repo's `internal/store/migrations/` at any
point** — verified after every step; final state
`ls internal/store/migrations/*.sql | wc -l` → `147`, and
`git status --short` never showed anything under that directory. A
`lefthook-local.yml` (scratch clone only, never committed) neutralised `go-test`
so the suite was not under test here; `lefthook.yml` itself was copied in
byte-identical each round (`shasum` on both sides, hazard §3.4).

Verdicts are read from whether the bare origin's `refs/heads/main` actually
moved, not from hook output alone. Final matrix, remote highest = 150:

| # | Case | Expected | Observed |
|---|---|---|---|
| 0 | implementation only, no new migration | accept | **ACCEPTED** |
| 1 | `135_probe.sql` — the burned hole | reject | **REJECTED** — `used: 135 (at or below origin/main's highest (150) — a hole, not a free slot)` / `use: 151` |
| 2 | `150_probe.sql` — duplicate of the highest | reject | **REJECTED** — `used: 150 (already on origin/main)` / `use: 151` |
| 3 | `151_probe.sql` — next legitimate | **accept** | **ACCEPTED**, origin advanced |

Cases 1 and 2 report *different* reasons, which is the evidence that the
"already present" and "at or below highest" branches are independently
reachable rather than one masking the other. Case 3 is the one that rules out a
reject-everything check.

Further cases, each a real push unless noted:

| Case | Observed |
|---|---|
| `150` claimed twice in one push (failure shape 1, post-merge) | **REJECTED**: `150_probe_worktree_b.sql` — `used: 150 (claimed twice inside this push)` / `use: 151`; the `_a` file was correctly kept |
| three back-fills at once (`100`,`101`,`102`) | **REJECTED**, suggestions `150`, `151`, `152` — **distinct**, which is the regression proof for the subshell bug in deviation 6(a) |
| no `origin` remote (`git remote rename origin upstream`, push by name to `upstream`) | **BLOCKED**, `git ls-remote origin refs/heads/main failed (exit 128): fatal: 'origin' does not appear to be a git repository`, fix `restore network access to origin, or check git remote -v` |
| origin URL → a path that does not exist (standalone) | **exit 1**, same loud shape. Path checked at its resolved form per §3.12: `ls -la /private/tmp/claude-501/…/mig-guard/does-not-exist.git` → `No such file or directory` |
| remote reachable, no `refs/heads/main` (bare repo on `trunk`) | **exit 1**, `origin has no refs/heads/main`, fix `push main to origin first…`. Distinct from the previous row *because* exit status is checked before output |
| **stale `origin/main`** | **BLOCKED** — see below |
| remote `main` with no migrations at all | **exit 1**, `origin/main lists no files under internal/store/migrations/ … an empty set makes every assertion below vacuously true` |
| `no_number_here.sql` | **BLOCKED**, `cannot parse the migration name … Migrations must be named NNN_name.sql`, no empty summary block |

**The stale-ref case is the one worth reading in full**, because it is the
fail-open the design exists to close. A second clone landed `150` on the bare
origin behind the first clone's back; the first clone did not fetch:

```
  stale origin/main highest : 149
  actual remote highest     : 150
  -> 150 looks free to a stale ref, and is not.

ERROR: migration-number guard could not establish a comparison ref.
  origin/main is stale.
      local  origin/main = 30c908f192e5e7ecfe6410d94ec754fe57a41b89
      remote refs/heads/main = 8dc71cc1c8537fb6fefc7aa9ea308289862c1c9e
  Fix:    git fetch origin main
error: failed to push some refs
```

and following the guard's own instruction produces the *correct* verdict rather
than another wall:

```
$ git fetch origin main
   30c908f1..8dc71cc1  main       -> origin/main
$ ./scripts/check-migration-number.sh
  origin/main's highest migration number: 150
  internal/store/migrations/150_probe_stale.sql
      used:   150  (already on origin/main)
      use:    151
```

That is the two-agents-both-claim-150 case caught end to end.

**Re-run against the final bytes.** The last edit was comment placement only
(`migration_number()`'s doc comment had been split from its function by the
`is_migration()` insertion), but the matrix was re-run rather than assumed —
against a remote whose highest was by then the 4-digit `1000`, so this pass
doubles as a second confirmation of the width-independence:

```
remote highest = 1000
  135  -> REJECTED
  1000 -> REJECTED
  1001 -> ACCEPTED
```

with the script byte-identical on both sides
(`shasum` → `f6222282507b3dfb4d719995d719522bb0691533`, two matching lines).

### Cross-reference (step 5)

Bidirectional, and each direction derived rather than asserted:

```
$ grep -c 'check-migration-number.sh' docs/engineering/tracking-integrity.md   -> 2
$ grep -c 'check 9'                   scripts/check-migration-number.sh        -> 4
$ grep -c 'check-migration-number.sh' lefthook.yml                             -> 2
$ grep -c 'check 9'                   lefthook.yml                             -> 1
$ grep -c 'check-migration-number.sh' docs/engineering/failure-modes.md        -> 0   (control: should not appear)
```

`GLOSSARY.md` has no entry colliding with `migration-number` or
`check-migration-number.sh` (`grep -nE 'check-|scripts/|migration-number|hook'
docs/engineering/GLOSSARY.md` returns only the `Skill`/`Landing check` entries;
control `grep -cE '^\*\*' docs/engineering/GLOSSARY.md` → `49`). The command
name deliberately echoes `migration-purity`'s style and the doc's existing
"**migration-number** row" vocabulary.

### Landing check

`./scripts/check.sh` — **no stage failed**: `format OK (3s)`, `vet OK (1s)`,
`lint --- (0s) examined nothing`, `test OK (4s)`. The lint stage examining
nothing is correct and expected, not a miss: it lints only what the branch added
since the merge base with `origin/main`, and this change adds zero Go lines (the
`grep -c '\.go$'` above). Baseline separately: `go build ./cmd/nanite/` OK,
`go vet ./...` OK.

Tier 1 is the right depth — this change is a shell script and lefthook config,
no Go, no goroutines or lifecycle ordering, **and no schema change**, so
`testing-workflow.md` §4's "touches migrations → Tier 2 + Tier 3" row does not
apply. `shellcheck` is not installed here (`command -v shellcheck` → empty), so
the script was checked with `bash -n` only; a reviewer with shellcheck should
run it.

### Scope limit — stated plainly

**This hook protects only clones where `lefthook install` has run.**
`find .git/hooks -type f ! -name '*.sample'` returns nothing until it has. That
gap is real, is out of scope here, and is not closed by anything in this task.
The guard also does not check task-file *claims* — check 9's unlanded-claim
bullets remain a manual, review-time concern; the script sees only files a push
actually adds. And it cannot see a migration added by a clone that pushes
without hooks.

### Noticed, deliberately not fixed

1. `docs/engineering/tracking-integrity.md`'s "Deriving it" example shows
   `147_remove_untouched_official_catalog_source.sql # → next free is 148`. The
   *command* is correct (it is the basename form), but its sample output is
   stale: it now returns `148_us_english_canceled_status.sql`
   (`ls internal/store/migrations/ | sort -t_ -k1 -n | tail -1`), so the
   worked example teaches a next-free of 148 when it is 149. Not my task; the
   script makes the number derivable rather than read.
2. `lefthook validate` exits non-zero on this repo, on the pre-existing
   `skip_empty` findings for `go-format`, `migration-purity` and
   `frontend-lint`, which `lefthook.yml`'s own header already documents. My
   command contributes none of them (count `0` above).
3. There are **147** files but the highest number is **148**
   (`git ls-tree --name-only origin/main -- internal/store/migrations/ | wc -l`
   → 147; highest → 148) — self-consistent with exactly one burned hole at 135,
   and worth stating because I initially misread the highest as 147 from the
   doc's stale example before deriving it.

**Status left untouched** at the Orchestrator's instruction; `## Review notes`
left empty for the Reviewer. Nothing was committed or pushed in this repo — the
four changed/added paths are staged nowhere and left in the working tree. All
push testing ran against a scratch bare origin under the session scratchpad and
never touched this repo's `origin`.

---

## Work log — second pass (2026-08-25)

Three rulings came back. All three implemented. First-pass record above is left
intact; this section appends to it. Started this pass at `b8d3aa61`,
`git status --short` showing the same five paths as the end of pass one.

### Item 1 — offline pushes that add no migration are no longer blocked

Implemented as a **reorder, no new logic**, exactly as directed. New order in
`scripts/check-migration-number.sh`:

1. `§1` (local, offline-capable) — `$TRACKING` must resolve; `git diff
   --no-renames --diff-filter=A $TRACKING HEAD -- $MIG_DIR/`; **empty set →
   `exit 0` without contacting the remote.**
2. `§2` — `git ls-remote` (the only place the network is required).
3. `§2b` — staleness compare against the `tracking_sha` captured in §1.
4. `§3` — `git ls-tree` → `remote_max` / `remote_nums`. Then assert as before.

I kept the `$TRACKING`-resolves check ahead of the diff rather than relying on
`diff_status` alone. Both fail closed, but a missing tracking ref then reports
`no local origin/main ref — nothing to compare against` / `git fetch origin
main` instead of a raw `git diff … failed (exit 128)`. `rev-parse --verify
--quiet` is a purely local lookup, so this costs nothing offline.

**The safety argument is written into the script**, per the ruling, not just
implemented: a local tracking ref only advances by fetching *from* the remote,
and migrations are append-only, so `files(local origin/main)` ⊆ `files(remote
main)`. The added set is `files(HEAD) \ files(TRACKING)`, so a smaller
`files(TRACKING)` can only make it **larger** — added-vs-local is a superset of
added-vs-remote. A stale ref can over-report, never under-report; over-reporting
just routes down the authoritative path, which fails closed on staleness.

**One thing I added to that argument, because the ruling's version is
unconditional and the property is not.** The superset inference rests on two
premises: `main` is not force-pushed backwards, and no migration is ever
*deleted* from it. Either would break it — if a migration present in both
`TRACKING` and `HEAD` were deleted on the remote, it would be absent from
`added` yet added relative to the remote, and the short-circuit would miss it.
The previous ordering did not have this gap, because it proved
`TRACKING == remote` *before* diffing, making the two sets identical by
construction. This is a real, if narrow, price of the short-circuit rather than
a free win. Both premises are already requirements of the claiming rule ("a
number is claimed by a file existing"), and both violations are catastrophes in
their own right outside what this guard can see — so I implemented the ruling as
written and recorded the precondition in the script comment rather than treating
it as grounds to deviate.

Both fail-closed boundaries verified as still closed rather than assumed — see
paths 11 and 11b below. `diff_status` does carry the missing-`$TRACKING` case for
free as predicted, but it is now reached only if the explicit `rev-parse` check
is removed; I verified the boundary through the behavior, not the code path.

### Item 2 — shellcheck

```
$ command -v shellcheck && shellcheck --version | grep '^version'
/opt/homebrew/bin/shellcheck
version: 0.11.0

$ shellcheck scripts/check-migration-number.sh ; echo "exit=$?"
exit=0
```

Clean, no output, **no `# shellcheck disable` directives anywhere in the file**.
Fixed by the consistency route rather than a suppression: `root=$(git rev-parse
--show-toplevel 2>&1)` now captures `root_status=$?` and tests
`[ "$root_status" -ne 0 ]`, matching `ls_status`, `lt_status` and `diff_status`.
That form is what silences SC2181 at the other three sites, which is why they
never flagged.

**shellcheck is a local developer tool here, not a CI dependency.** Nothing
invokes it: `grep -rn 'shellcheck' .github/ lefthook.yml scripts/check.sh`
returns no matches, and neither does `grep -rln 'shellcheck' scripts/`. A clone
without shellcheck installed is unaffected.

### Item 4 — the stale worked example

**Chosen: self-deriving, with no committed sample output.** Reasoning, since the
ruling asked for it: a stamp makes staleness *visible*, but it still stores the
number, and a reader in a hurry copies the number and skips the stamp. For this
particular value — the one whose off-by-one is a service that will not boot —
not storing it is strictly better than storing it legibly. This is
`agent-verification-discipline.md` §1.1 applied to a document.

What the example was actually teaching ("next free is one past the highest, not
the lowest unused") does not depend on the current value, so I kept a concrete
illustration using **synthetic numbers that cannot rot** — highest `042`, hole
at `037`, next free `043` — alongside the bare command. Concrete illustration
preserved; nothing to go stale. No number was bumped.

**This surfaced a contradiction I had to fix in the same edit.** The paragraph
immediately below ("Run the command against `main`, not against your worktree")
recommended the **path-based** sort form — the broken one from pass one's
finding:

```
$ printf 'internal/store/migrations/9_a.sql\ninternal/store/migrations/148_b.sql\n' | sort -t_ -k1 -n | tail -1
internal/store/migrations/9_a.sql          # wrong
$ printf '…same…' | sed 's#.*/##' | sort -t_ -k1 -n | tail -1
148_b.sql                                  # correct
```

Left alone, the doc would have warned against the trap in one paragraph and
prescribed it in the next. Corrected to
`git ls-tree --name-only main -- internal/store/migrations/ | sed 's#.*/##' |
sort -t_ -k1 -n | tail -1`, with the `sed` flagged as load-bearing. Both sort
forms remaining in the file are now correct — the `ls` form at `:67` operates on
basenames already, and the `ls-tree` form at `:105-106` strips first:

```
$ grep -n -B1 'sort -t_' docs/engineering/tracking-integrity.md
66-```
67:ls internal/store/migrations/ | sort -t_ -k1 -n | tail -1
105-git ls-tree --name-only main -- internal/store/migrations/ \
106:  | sed 's#.*/##' | sort -t_ -k1 -n | tail -1
```

(Verified: the `ls-tree` form returns `148_us_english_canceled_status.sql`, and
survives a synthetic 4-digit case — `1490_b.sql` beats `148_a.sql` and
`99_c.sql`.)

### Behavior table — every path, exit code read **without a pipe**

`cmd >/dev/null 2>&1; echo $?`. Scratch clone with `lefthook install` run,
`origin` a `--no-hardlinks` bare clone, remote highest **148** unless noted.

| # | Path | Exit | Note |
|---|---|---|---|
| 1 | no migration added, remote reachable | **0** | |
| 2 | no migration added, remote **unreachable** | **0** | **NEW — the fix** |
| 3 | migration added, remote **unreachable** | **1** | **NEW — the fix did not become a fail-open** |
| 4 | `135` — burned hole | **1** | |
| 5 | `148` — duplicate of highest | **1** | |
| 6 | `149` — next legitimate | **0** | rules out reject-everything |
| 7 | `150` claimed twice in one push | **1** | intra-push duplicate |
| 8 | unparsable `.sql` name | **1** | |
| 9 | remote has no `refs/heads/main`, migration added | **1** | `origin has no refs/heads/main.` |
| 10 | remote `main` has no migrations, migration added | **1** | `origin/main lists no files under …` |
| 11 | **`$TRACKING` absent**, migration added | **1** | `no local origin/main ref …` |
| 11b | **`$TRACKING` absent, NO migration added** | **1** | **boundary: missing ≠ empty; did not widen** |
| 12 | `origin/main` stale, migration added | **1** | `origin/main is stale.` |
| 13 | `origin/main` stale, no migration added | **0** | new consequence of the reorder, stated for the record |

Row 11b is the one that proves the short-circuit did not swallow the
fail-closed boundary, and row 3 is the one that proves the offline fix did not
swallow the guard. Row 13 is a genuine behavior change I am flagging rather than
burying: a stale ref no longer blocks a push that adds nothing. That is sound
under the superset argument (nothing added ⇒ nothing to collide) and it is the
direct consequence of the ruling.

Headline cases re-confirmed through **real pushes**, verdict read from whether
the bare origin's `refs/heads/main` actually moved (remote highest 149 by then):

```
  135   -> BLOCKED (git push exit=1)
  149   -> BLOCKED (git push exit=1)
  150   -> PUSHED  (git push exit=0)

  docs-only push with unreachable origin: exit=0   (0 = not blocked)
```

### Two of my own measurement errors this pass, caught and corrected

Recording these because §7 item 8 asks for them.

1. **A behavior table that was entirely `127`.** My first matrix run returned
   exit `127` for all eight paths. `127` is "command not found", and a uniform
   implausible result across every row is the §1.3 signal that the harness is
   wrong, not the subject. Cause: my `reset_clean` did `git reset --hard
   origin/main`, but I had never *pushed* the implementation commit in that
   clone — so every reset deleted `scripts/check-migration-number.sh`. Confirmed
   directly (`ls -l scripts/check-migration-number.sh` → `No such file or
   directory`), fixed by pushing the implementation first so it survives the
   reset, and re-run. Had I reported the first table it would have read as
   thirteen fail-closed passes.
2. **Three `grep -c` counts of `0` that should have been `1`.** While checking
   the reorder had not duplicated or dropped a block, patterns containing `$(`
   returned `0`. Not a missing block — `$(` in a basic regular expression does
   not match literally:
   ```
   $ printf 'a=$(foo)\n' | grep -c 'a=$(foo)'    -> 0
   $ printf 'a=$(foo)\n' | grep -cF 'a=$(foo)'   -> 1
   ```
   Re-derived with `grep -cF`: every key command appears exactly once
   (`tracking_sha=$(git rev-parse`, `remote_out=$(git ls-remote`,
   `remote_files=$(git ls-tree`, `added=$(git diff --no-renames`, `remote_max=0`
   → 1 each; `exit 0` → 2, the short-circuit and the all-clear). Control:
   `NONEXISTENT_SENTINEL` → 0.

### Acceptance

```
$ shellcheck scripts/check-migration-number.sh ; echo "exit=$?"
exit=0

$ ./scripts/check.sh >…/check.out 2>&1 ; echo "check.sh exit=$?"
check.sh exit=0
check.sh: no stage failed — but these examined nothing: lint
```

The lint stage examining nothing is correct, not a gap: it lints only what the
branch added since the merge base with `origin/main`, and this changeset has
zero Go files — `git status --porcelain | grep -c '\.go$'` → `0` across
`git status --porcelain | wc -l` → `5` changed paths.

No `.sql` was created under the real repo's `internal/store/migrations/` at any
point in this pass either; `ls internal/store/migrations/*.sql | wc -l` → `147`,
and `git status --short` in the real repo was checked after every verification
step and never showed anything under that directory. All probing ran in
`…/scratchpad/mig2/`. Nothing committed or pushed in this repo.

### Still true, and still the limit

The scope fence is unchanged and this pass does not widen it: **the hook
protects only clones where `lefthook install` has run**, it checks files a push
adds rather than task-file claims, and it cannot see a push made from a clone
without hooks. The offline reorder does not change any of that — it changes only
*when* the network is consulted.

Item 3 (lefthook name-ordering / `priority: 1`) needed nothing from me and I
changed nothing about it; it stands as recorded in pass one, labelled there with
the both-directions evidence I actually observed.

---

## Work log — third pass, reviewer findings (2026-08-25)

Reviewer returned PASS-with-findings: 3 of 36 attacks succeeded. All four
actionable items fixed. Prior passes left intact above.

### F1 (HIGH) — `core.quotePath` routed a real migration around the hard error

**Reproduced before fixing.** One variable, same commit:

```
$ git diff --no-renames --name-only --diff-filter=A origin/main HEAD -- internal/store/migrations/
"internal/store/migrations/135_caf\303\251.sql"

quotePath default (the shipping config) -> exit=0   135 back-fill ACCEPTED
quotePath=false (control)               -> exit=1   135 back-fill REJECTED
$ git config --get core.quotePath          -> ''
$ git config --global --get core.quotePath -> ''
```

Zero bytes on stdout, and a real `git push` **landed the 135 back-fill on
`main`** (origin's `refs/heads/main` moved). Remote side confirmed too: with a
quoted `900_café.sql` on `main`, `bash -x` showed `remote_max=148` against an
actual highest of `900` — the highest-number assertion disabled for every
subsequent push by every clone.

**(a) `-z` at both read sites**, chosen over `-c core.quotePath=false`: the
config flag fixes non-ASCII but leaves a filename containing a literal newline
breaking a line-based read. `-z` closes both, and I verified the newline case
rather than assuming it (path 16). This forced a structural change worth
flagging — **NUL cannot survive a shell variable**, so `-z` output goes to temp
files under an `mktemp -d` with a `trap … EXIT`, not to `$(...)`:

```
$ bash -c 'v=$(printf "a\0b\0"); printf "%s" "$v" | od -c | head -1'
0000000    a   b            <- both NULs silently stripped
```

(Worth noting for anyone re-deriving this: the *interactive* shell here is zsh,
which **preserves** NUL in variables, so the naive check suggests the opposite
of what the script's `#!/usr/bin/env bash` actually does.)

**(b) The silent-skip branch is deleted.** `is_migration()` is gone; every path
either parses as `NNN_name.sql` or is a hard error. Re-derived the evidence that
nothing legitimate is lost:

```
$ git ls-files internal/store/migrations/ | /usr/bin/grep -cv '\.sql$'     -> 0  (of 147)
$ git log --all --diff-filter=A --name-only --format='' \
    -- internal/store/migrations/ | /usr/bin/grep -v '\.sql$' | /usr/bin/grep -v '^$'  -> empty
$   positive control, .sql ever added                                       -> 183
```

The irony the reviewer named is in the comment: the unparsable-name hard error
exists so the guard never guesses, and quoting routed a real `.sql` file around
it into the skip.

**A further defect this exposed, mine, found by the newline test.** The
rejection report still accumulated `path<TAB>num<TAB>reason` lines and re-read
them with `IFS=$'\t' read`, which a newline in a path splits into two records —
printing a **phantom rejection with an empty number** next to the real one. The
verdict was correct; the message was not. Now three parallel arrays indexed by
position, so no delimiter can collide. After the fix the same input prints one
entry, `used: 135`, `use: 149`, with the path rendered faithfully across two
lines because it genuinely contains one.

### F2 (MEDIUM) — empty-set positive control, broken by the reorder

**Attribution, as instructed: this one came with the offline reorder, which was
a ruling handed down to me. It is not a defect I chose.** Recording it plainly
rather than as my own error, and equally not as anyone's blame.

`git diff` with a pathspec matching nothing exits 0 with empty output, so a
wrong `MIG_DIR` short-circuited at §1 before §3's empty-set abort could run.
Fixed by moving the tree read up into §1 — `git ls-tree "$TRACKING"` is purely
local, so the non-empty assertion runs ahead of the short-circuit **without
costing the offline property**. Both directions (path 17): typo'd `MIG_DIR` with
a 135 back-fill now `exit=1`; control with the correct `MIG_DIR`, same push,
`exit=1`. Before the fix the typo'd run was `exit=0`, zero bytes.

Both false assertions corrected in the same pass — the script header's "carries
its own positive control on every run" and the matching sentence in
`docs/engineering/tracking-integrity.md` — and both now state *why the position
is load-bearing*, so the next reorder does not silently undo it.

### F3 (MEDIUM) — documented, not coded

`git push origin feature:main` from a non-`main` branch: `only: - ref: main`
evaluates the current branch, both pre-push commands print `(skip) by
condition`, and the push lands unchecked. **No code change**, per the ruling.
Qualified in two places: the script header now carries the claim as "fails
closed, never silently — **with one stated exception**" and names it, and
`tracking-integrity.md`'s scope limit is now "**two gaps, both real**" and
describes it. An accepted gap nobody knows about is not an accepted gap.

### F4 (LOW) — the append-only premise was asserted as fact and is false

Re-derived, and the coordinator's framing is right about the mechanism but I
found the historical record is worse than "narrow":

```
$ git diff --no-renames --diff-filter=D --name-only 63d79028^ 63d79028 \
    -- internal/store/migrations/ | wc -l                              -> 9
$ git log --diff-filter=D --format='%h' -- internal/store/migrations/ | wc -l  -> 3
```

`63d79028` — the very commit this task's Context cites as the source of the
burned `135` hole — is one of them. (It shows **0** deletions *with* rename
detection and **9** without; either way the paths stopped existing. That is also
why the two commands return different commit sets.) And of the three
rename-detected deletion commits, **`efee58da` took the highest from 27 down to
1** — a real historical instance of the only case F4 says is dangerous, not a
hypothetical. Derived:

```
for c in aefda9b5 6e9b07c3 efee58da; do  max(c^) -> max(c)  done
   aefda9b5: 93 -> 94      6e9b07c3: 85 -> 85      efee58da: 27 -> 1
```

The comment now states the premise is **not** true, names `63d79028` and
`efee58da`, and explains why it stays LOW: deleting a number *below* the max
changes nothing because the max rule still rejects everything at or below it;
only deleting the current **highest** (or force-pushing `main` backwards)
lowers `remote_max` and re-opens numbers. Written down so nobody re-escalates it
and nobody mistakes the premise for a proof.

### F5 — no action taken, on the record.

### Refreshed behavior matrix — exit codes read **without a pipe**

`cmd >/dev/null 2>&1; echo $?`. Scratch lab with a **`git init --bare` origin**
(never the real repo — asserted in the harness: `[ "$(git remote get-url
origin)" = "$R" ] && ABORT`), `lefthook install` run, remote highest 148 unless
noted. Script byte-identical to the real repo's on every round (`shasum`, two
matching lines).

| # | Path | Exit | |
|---|---|---|---|
| 1 | nothing added, remote reachable | **0** | |
| 2 | nothing added, remote unreachable | **0** | offline |
| 3 | migration added, remote unreachable | **1** | offline fix not a fail-open |
| 4 | `135` burned hole | **1** | |
| 5 | `148` duplicate of highest | **1** | |
| 6 | `149` next legitimate | **0** | rules out reject-everything |
| 7 | `150` claimed twice in one push | **1** | |
| 8 | unparsable `.sql` name | **1** | |
| 9 | remote has no `refs/heads/main` | **1** | |
| 10 | remote `main` has no migrations | **1** | |
| 11 | `$TRACKING` absent, migration added | **1** | |
| 11b | `$TRACKING` absent, **nothing** added | **1** | boundary: missing ≠ empty |
| 12 | `origin/main` stale, migration added | **1** | |
| 13 | `origin/main` stale, nothing added | **0** | accepted consequence of the reorder |
| 14 | **non-ASCII name at `135`** | **1** | **F1 — was 0** |
| 15 | **non-ASCII name at a free number** | **0** | **F1 — fix is not reject-everything** |
| 16 | **literal newline in name at `135`** | **1** | **F1 — what `quotePath=false` would miss** |
| 17 | **wrong `MIG_DIR`, `135` in push** | **1** | **F2 — was 0, zero bytes** |
| 18 | **non-`.sql` path reaches the parser** | **1** | **F1b — hard error, no longer a skip** |

Real pushes, verdict read from whether the bare origin's `refs/heads/main`
actually moved:

```
  135 hole                          push exit=1  BLOCKED
  non-ASCII at a free number (151)  push exit=0  PUSHED, landed as 151_café.sql
  150 next legit                    push exit=0  PUSHED
  docs-only push, origin unreachable            exit=0  (not blocked)
```

### Two harness errors of my own this pass

1. **I `rm -rf`'d the scratch working clone while my shell's cwd was inside
   it.** Every command after that failed against a deleted directory, and the
   run still printed `BLOCKED` at the end — a false green produced entirely by
   broken plumbing. Caught because `cp`, `chmod`, `git add` and `shasum` had all
   errored above it and the exit code was `127`, not `1`. Rebuilt from a safe
   cwd and re-ran everything.
2. **A mislabelled real-push row.** I recorded "non-ASCII 149 (valid) →
   BLOCKED" and briefly read it as a regression. It was correct: a sibling clone
   had already landed `149`, so `149` was a duplicate. Re-ran at a genuinely free
   number (`151`) and it pushed. The guard was right; my label was wrong.

### Acceptance

```
$ shellcheck scripts/check-migration-number.sh ; echo "exit=$?"
exit=0                          # no output, and no `disable` directive in the file

$ ./scripts/check.sh >…/c3.out 2>&1 ; echo "check.sh exit=$?"
check.sh exit=0
check.sh: no stage failed — but these examined nothing: lint
```

`lint` examining nothing is correct: zero `.go` files in the changeset —
`git status --porcelain | /usr/bin/grep -c '\.go$'` → `0` of `5` paths.

Used `/usr/bin/grep` or `-F` for literal patterns throughout this pass, per the
ugrep-vs-BSD-grep hazard. No `.sql` was created under the real repo's
`internal/store/migrations/` at any point; `ls internal/store/migrations/*.sql |
wc -l` → `147`, and the real repo's `git status --short` was checked after every
verification step and never showed anything under that directory. Nothing
committed or pushed in this repo.

### What I could not prove

- **F3 is documented, not closed.** A push from a non-`main` branch to `main`
  still bypasses both pre-push commands. That is the ruling, and the docs now
  say so — but the gap is live.
- **The `lefthook install` gap is unchanged** and unclosable from here.
- **F4's premise remains false rather than fixed.** Deleting the highest
  migration, or force-pushing `main` backwards, still re-opens numbers. Both are
  outside what a pre-push hook can observe; the comment says so instead of the
  code defending it.
- `remote.origin.pushurl` divergence (F5) is untested and uncovered, by ruling.

---

## Work log — fourth pass, comment and documentation only (2026-08-25)

Reviewer returned PASS. This pass changed **no executable logic** — proof below.
Scope held to `scripts/check-migration-number.sh`, `docs/engineering/tracking-integrity.md`,
and this Work log.

### 1. F4's comment now carries all four elements

Re-derived rather than accepted:

```
$ git diff --no-renames --diff-filter=D --name-only 63d79028^ 63d79028 \
    -- internal/store/migrations/ | wc -l                                  -> 9
$ git ls-tree --name-only -z efee58da^ -- internal/store/migrations/ \
    | tr '\0' '\n' | sed 's#.*/##;s/_.*//' | sort -n | tail -1            -> 027
$ ...same against efee58da                                                 -> 001
$ git log --diff-filter=D --format='%h' -- internal/store/migrations/ | wc -l -> 3
```

**(a)** premise stated false; **(b)** both commits named with those commands;
**(c)** the guard **structurally cannot see it** — once the file is gone from
the remote its number is absent from `remote_nums` and no longer feeds
`remote_max`, so neither check has any signal; explicitly contrasted with the
burned hole, which the guard *does* catch precisely because the max still
remembers it; **(d)** the consequence, verified.

### 2. The "hard boot error, not a back-fill" framing was half the story

**I read `internal/gooseutil/resolve.go` myself rather than taking the finding
on trust.** goose v3.27.3 (`grep -F goose go.mod` → `v3.27.3`), at
`$(go env GOMODCACHE)/github.com/pressly/goose/v3@v3.27.3/internal/gooseutil/resolve.go`:

```
:30   dbAppliedVersions := make(map[int64]bool, len(dbVersions))   // version number only
:47   if dbAppliedVersions[v] { continue }     // missing loop
:73   if dbAppliedVersions[v] { continue }     // new loop
```

No filename, no checksum in the key. Confirmed by reading the whole
`UpVersions` body: for a number already in the ledger it is added to neither
`missing` nor `out`, so `newMissingError` is never reached and goose returns
`nil` — **the migration silently never runs.** For a number *not* in the ledger
and below its max, `missing=[N]` and `len(missing) > 0 && !allowMissing`
returns the boot error. Two outcomes, selected by the database's vintage.

One thing I checked that the finding did not mention: a number exactly equal to
`dbMaxVersion` but unapplied cannot arise — `dbMaxVersion` is drawn from
`dbVersions`, every element of which is in `dbAppliedVersions`. So there is no
third branch, and the two-outcome statement is complete rather than merely
correct as far as it goes.

Corrected in the script header, in the user-facing rejection message, and in
`tracking-integrity.md`'s "Why filling a hole is worse than a stale number".
`135` is now used as the canonical example of *both* outcomes at once — a
database that applied the old `135_goals.sql` before `63d79028` takes the silent
path; one created after takes the boot error.

**Scope fence honoured.** `/usr/bin/grep -c 'back-fill'` in the script was `6`;
the four that use "back-fill" as a *label for the act* were left alone, and only
the two that asserted the *mechanism* (header block, closing message) were
rewritten. Nothing outside the script and `tracking-integrity.md` was touched —
in particular **not** `TASKS/INDEX.md`, the batch READMEs, or either handoff.

**No claim about any particular deployment**, per the constraint: the wording is
source-level and holds for every database. I did **not** open
`~/.local/share/nanite/workspaces/default/main.db`. In the course of rewriting
that block I *removed* the doc's existing `sqlite3 ~/.local/.../main.db` command
and its per-deployment assertion (`/usr/bin/grep -c 'sqlite3'` → `0` now, `1` at
HEAD) — that sentence claimed "every already-migrated database is affected",
which the source shows is exactly the half that is false.

### 3. The stray-file blocking behavior — an accepted cost of a directed change

**Attributed as instructed, and the attribution is the point.** Deleting the
silent-skip branch was a ruling handed down to me, in the same way the offline
reorder was; the following is that ruling's downstream cost, not a consequence
of how I implemented it:

> **A single non-`.sql` file already on `main` hard-blocks every
> migration-adding push, for everyone, until it is removed.** A `README.md`
> added to `internal/store/migrations/` — plausibly to document the numbering
> rule itself — makes a legitimate `201_ok.sql` push exit 1, naming the
> offending path; a docs-only push still exits 0.

The ruling stands and would be made again: it is the correct fail-closed
direction, it is recoverable, the message names the exact path, and no
non-`.sql` file has ever existed in that directory across the entire history
(`git ls-files internal/store/migrations/ | /usr/bin/grep -cv '\.sql$'` → `0` of
147; the all-history form returns empty against a positive control of `183`
`.sql` additions), so the trigger requires something that has never been done.

Pre-empted in `tracking-integrity.md` with one sentence, placed *ahead* of the
"Two things that trip people up" list so a reader meets it before tripping it —
and placed ahead deliberately rather than inside, because inserting it into that
list would have made "Two things" name three.

### Verification — deliberately light, and aimed at proving it was a comment change

The baseline blob could not be recovered with `git cat-file`: the script is
untracked, so `ac262fd8…` is a hash `git hash-object` can compute but the object
store does not contain. My first attempt did exactly that and silently produced
an **empty** baseline, against which everything looked like an addition. The
`0` executable lines gave it away. The real pre-edit bytes were in the pass-3
scratch clone, validated first:

```
$ git hash-object …/mig3/work/scripts/check-migration-number.sh
ac262fd8525823fd63b9100fecbf289044d4a35e     # matches the stated baseline
$ wc -l < …                                   -> 461
```

| # | Check | Result |
|---|---|---|
| 1 | logic-line diff vs baseline (comments, blanks and `echo`/`printf` stripped) | **0 lines** — 152 vs 152, identical |
| 1 | non-comment diff | only `echo` string literals, i.e. the rejection message prose item 2 required |
| 1 | `tracking-integrity.md` | prose only; the 3 command-line changes in `git diff` are **pass-2** edits (the diff is cumulative against HEAD), plus this pass's removal of the `sqlite3` live-DB line |
| 2 | `shellcheck` | **exit 0**, `grep -c 'shellcheck disable'` → `0` |
| 3 | `/bin/bash -n` (3.2.57) | **exit 0** |
| 4 | `./scripts/check.sh` | **exit 0**; `examined nothing: lint` correct, `.go` files in changeset → `0` |
| 5 | `git status --short` | see note below |

**Positive control for check 1**, because "no differences" is exactly what a
broken comparison also prints: tampering `MIG_DIR="internal/store/migrations"`
→ `MIG_DIR="TAMPERED"` makes the same filter emit a 4-line diff. My first two
attempts at this control both reported nothing — the first because the `sed`
pattern is what I changed rather than what existed, the second because I piped
`diff` through `grep -E '^[<>]'` and **`diff`'s output here is ANSI-colorized**,
so the lines begin with an escape sequence and not with `<`. Same family as the
ugrep hazard already flagged. Fixed by reading the raw diff.

**Line count and hash after this pass: 516 lines,
`ca8c32646a72f09612c36d541b4c30cd0695a113`** (was 461 / `ac262fd8…`; the entire
delta is comment and message prose).

### One thing to flag: a sixth path in `git status`

`TASKS/gate-integrity/06-citation-and-config-drift-sweep.md` is now modified.
**It is not mine.** Its diff adds a section headed *"Added 2026-08-25 by the
Wave B orchestrator"* describing this very mechanism correction as item 9 of the
sweep. I verified it is not my edit before continuing and have left it entirely
alone, as the scope fence requires. Flagging only because a reviewer diffing the
tree will see six paths where my work accounts for five.

No `.sql` was created under the real repo's `internal/store/migrations/` in this
pass — it never needed a push test. `ls internal/store/migrations/*.sql | wc -l`
→ `147`. Nothing committed or pushed.

## Review notes

**Reviewed 2026-08-25 at `b8d3aa61` by a fresh reviewer dispatch** — no shared
context with the worker. **Verdict: PASS**, reached across two rounds: an initial
review of the pass-2 state returning PASS-with-findings (3 successful attacks out
of 36), and a re-verification of the fixes returning PASS with no regression.
Both rounds ran against a `git init --bare` origin, with exit codes read without
a pipe. Transcribed by the orchestrator; the reviewer wrote nothing to this file.

**Round 1 — PASS-with-findings.** All seven "Done means" bullets independently
reproduced. 33 attacks failed to break the guard, spanning filename parsing
(zero-padded, octal-looking, no-underscore, space, subdirectory, 19-digit),
history shape (two-commit push, merge commit, add-then-delete, renumber-down,
rename+add together), and environment (offline, absent `$TRACKING`, detached
HEAD, linked worktree). It cleared its own `.SQL`-uppercase suspicion by
compiling a real `//go:embed m/*.sql` probe and confirming `137_upper.SQL` is not
embedded. Three attacks succeeded:

- **F1 (HIGH) — `core.quotePath` silent fail-open.** Git's default C-escapes and
  double-quotes any path containing a non-ASCII byte in `--name-only` output, so
  the basename ended in `"` rather than `.sql`, the parser missed, and the file
  took the branch meant for a stray README: **exit 0, zero bytes of output, a
  `135` back-fill accepted.** Isolated to one variable — `core.quotePath=true`
  (the shipping default, unset at repo and global level) → `exit 0`;
  `=false` → `exit 1`. Confirmed to reach goose via a real `//go:embed` probe.
  The remote side was worse and persistent: one quoted filename already on `main`
  dropped out of `remote_max`, disabling the highest-number assertion for every
  subsequent push by every clone.
- **F2 (MEDIUM) — the empty-set positive control became unreachable.** `git diff`
  with a pathspec matching nothing exits 0 with empty output, so a wrong
  `MIG_DIR` short-circuited before the control could fire. **This regression came
  from the offline-short-circuit ruling handed down to the worker, not from the
  worker's implementation.**
- **F3 (MEDIUM, accuracy) — `git push origin feature:main`** from a non-`main`
  branch: `only: - ref: main` evaluates the current branch, so both pre-push
  commands print `(skip) by condition` and the push lands. The ref-gate ruling
  working as ruled; documented rather than changed.
- **F4 (LOW, accuracy)** and **F5 (LOW, informational, `pushurl`)** — see below.

**Round 2 — PASS, fixes verified, no regression.** F1 closed on both sides:
newline, tab, double-quote, backslash and non-ASCII each reject at `135` and
accept at `151`; with `200_café.sql` on the remote, `remote_max` reads `200`
rather than `148`, so the duplicate and a `199` back-fill both reject. Confirmed
end-to-end through lefthook on real pushes, with the verdict read from whether
the bare origin's `refs/heads/main` moved. F2 closed, and the control now
genuinely runs on every invocation — a wrong `MIG_DIR` fails loud on a docs-only
push, which is the case that proves it. Offline intact on all five paths.

It **confirmed the `-z` amendment rather than accepting it**: `ls-tree -z` output
is NUL-*terminated*, not merely separated, and `read -r -d ''` reads exactly two
records from `a\0b\0` on both `/bin/bash` 3.2.57 and homebrew bash 5.3.3.

The new surface took 15 fresh probes, all correct or fail-closed: `mktemp`
stubbed to exit 1 → `1` on both a migration push and a docs-only push (does not
take the short-circuit); tmpdir `chmod 555` → `1` with bash's own redirect error
quoted verbatim, which also proves the `2>&1 >"$FILE"` ordering works when the
redirect itself fails; trap coverage across six exit paths (`before=34 after=34`,
with a working positive control, and the EXIT trap firing on SIGINT); 10 parallel
invocations yielding one distinct checksum with no temp-dir leak; and bash 3.2.57
array behaviour under `set -u`.

30 re-run attacks showed zero regressions. Three deliberate changes, all
fail-closed, all consequences of deleting the silent-skip branch: `.SQL`/`.Sql`
variants, `.sql.bak`, and a `README.md` in the migrations directory now reject
where they previously passed.

**Findings left open by design, confirmed not re-raised.** F3 remains open and is
now documented in the script and in `tracking-integrity.md`. F4's premise is
corrected; the deletion case remains outside what a pre-push hook can observe.
F5 is on the record untested, by ruling.

**Nits judged and deliberately not raised:** the rename hint for a path
containing a literal newline spans two lines (the honest rendering of a two-line
filename); a 20-digit prefix wraps under 64-bit arithmetic and is caught by
goose's own int64 parse instead; and one `[ -n "$path" ] || continue` the
reviewer believes unreachable — stated as *belief from failing to break it*,
not as proof.

**One new observation, correctly not called a defect.** Deleting the skip branch
means a single non-`.sql` file already on `main` hard-blocks every
migration-adding push until it is removed. Reproduced with `NOTES.md`. This is
the accepted downstream cost of the skip-branch ruling, not a property of the
implementation. Fail-closed, recoverable, and the message names the offending
path; `tracking-integrity.md` now states that the directory holds only `.sql`
files so the rule is readable before it is tripped.

**The reviewer reported two harness errors of its own** — a `127` from a
`git clean -e` that removed its modified script, and a mislabelled accept whose
lab remote had already advanced to `149` — surfacing both rather than burying
them. Both were caught by noticing the result could not be correct under the
hypothesis being tested.

**Orchestrator re-derivation.** Every load-bearing claim above was independently
reproduced before acceptance, in scratch clones built by the orchestrator rather
than inherited from the worker or the reviewer: the F1 `core.quotePath` flip, the
F2 `MIG_DIR` short-circuit, the full behaviour matrix across offline, stale-ref,
absent-`$TRACKING` and burned-hole cases, and `efee58da` taking the highest
migration from `027` to `001`. Post-fix acceptance re-run at
`ca8c32646a72f09612c36d541b4c30cd0695a113`: `shellcheck` 0 with zero disables,
`/bin/bash -n` 0, `./scripts/check.sh` 0 (`examined nothing: lint` correct — zero
`.go` files in the changeset), and a six-case behavioural smoke matrix confirming
the final comment-only pass moved no logic.
