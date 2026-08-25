# Agent verification discipline

**Status:** active · authored 2026-08-24, session `session-20260824-37765f5d`
**Companions:** `failure-modes.md` (why measurements mislead) · `tracking-integrity.md` (drift checks)

---

## What this is, and how to use it

This is the block of execution rules that every dispatched agent must follow. It is **operational** — for an agent doing work right now. Its companion `failure-modes.md` is **analytical** — for an author writing a task file, explaining *why* these rules exist. Read that one once; follow this one every time.

**Reference this file from dispatch prompts. Do not copy it inline.**

That instruction is the point of the document. This block has been propagating between sessions by copy-paste — an agent inherits it in a boot prompt, copies it into the prompts it writes for its own subagents, sometimes appending a hazard it discovered. That has worked, but it is fragile: improvements live only in whatever prompt happened to be written last, and die when that session ends. Two hazards added on 2026-08-24 (§3.4, §3.5) existed nowhere but in one session's subagent dispatches until this file was written.

Suggested dispatch wording:

> Follow `docs/engineering/agent-verification-discipline.md` in full. It is not optional and not a summary of something else.

If you discover a new hazard while executing, **add it to §3 of this file** rather than to your own prompt. That is how it survives.

---

## 1. Numbers

### 1.1 Derive at the moment of use

Never carry a number from a task file, from your dispatch prompt, or from earlier in your own session. Measure it again, now.

Every count error in the 2026-08-21→23 audit batch came from a *stored* number. Not one came from a fresh measurement.

### 1.2 Ship the command next to the number

A number with its command is re-runnable by the next reviewer. A number in prose is unfalsifiable.

This is the single highest-leverage rule here, and the cheapest. It is why a fabricated "371 methods" was caught (the real figure was 237) — an executing agent could re-run the command and see the regex ended at `(`. It is why a `0 issues` lint result was caught on 2026-08-24 as a malformed invocation rather than believed.

Generated prose will produce a plausible number without effort. The command is the part that cannot be plausibly fabricated.

```
misspell: 439 | baseline 429 -> delta: +10

$ golangci-lint run --config <cfg> --enable-only=misspell \
    --issues-exit-code=0 --output.json.path=mis.json $(cat dirs.txt)
```

### 1.3 A zero is a signal, not a result

**A count of zero from a command that should match something is the cheapest available evidence that your command is wrong.** Check the command before reporting the zero.

Real, 2026-08-24: `golangci-lint` passed import paths where directory paths were expected, emitted `typechecking error: … directory not found`, then `0 issues.` and exit 0. Reported as-is it would have become "misspell is clean." It was a scan of nothing.

The same rule applies to a zero you *want* — verify the mechanism still fires. `gosec G104` genuinely dropped 103→0, confirmed by checking the rule still reports on a purpose-built sample.

### 1.4 Iteration counts, not verdicts

"It passed" is not a result. "0 failures in 20,000 iterations, `go test -count=20000 …`" is. A single green run never clears an intermittent defect.

---

### 1.5 A count from memory reads exactly like a count that was measured

Three in one work thread: five per-directory figures summed mentally as `141` when the total was
`161`; a comment count given as "twice" when it was once; a task id written from expectation rather
than read out of the create result. All three reached prose, and **all three were caught by the
person being corrected rather than the person asserting** — one of them inside a message correcting
somebody else's recollected number.

The pattern is not carelessness. **A count feels like an observation when it is a recollection, and
nothing in the sentence marks the difference.** "There are five" and "I counted five" are written
identically and read identically; only one of them happened. Every hedge we write attaches to
uncertainty, and a remembered number does not feel uncertain.

**The check:** ask whether the figure you are about to write is a measurement or a memory. If it is
a memory, take the measurement — let the tool do the counting and the arithmetic, and paste the
result. One command against an unfalsifiable claim in a document that reads as authoritative.

## 2. Documents and code

### 2.1 Code wins

Task files, design docs, comments and trackers are best-effort snapshots. When one disagrees with current source, **the source is right and the document gets corrected** — not worked around.

### 2.2 Re-derive every cited line number before editing

Citations drift. Both task files in the 2026-08-24 batch had drifted line numbers despite carrying correct `verified-at` stamps — the stamps did their job by making the drift expected.

When you correct a citation, say so in your report. That is a finding, not housekeeping.

### 2.3 Stamp what you cite

Any number or code citation you write into a durable artifact carries either its command or `verified-at: <commit>`.

### 2.4 A line number describes your tree, not the commit

`grep -n` reports where something sits **in the working tree you ran it in**. If that tree has
uncommitted changes to the file, the number is correct for you and wrong for the next reader — and
nothing in the number says which.

**Any line number leaving your session** — a brief, a task file, a doc, a commit message — is derived
against a revision:

```
git show "${rev}:path/to/file" | /usr/bin/grep -n 'thing'      # braced — see 3.18
```

**Scope.** This applies whenever the tree you derived from has uncommitted changes to that file,
**whether they are yours or another session's**. It does **not** apply to your own scratch
verification against a clean tree; there `grep -n` is the honest form and `git show` is ceremony.
The precondition is mechanically checkable — `git status --short -- <file>` — which is why it is the
right one; *"am I alone in this tree"* often cannot be answered at all.

**The case people don't anticipate is their own.** A session wrote `:285` into a task file and its
own edits in the same turn moved the passage to `:294`. The citation was stale before it finished
being written. That passage has now been at `:224`, `:285` and `:294` across three commits, all by
one session. *"Another session moved it"* is intuitive; *"I moved it myself"* is not, and this entry
exists for the second.

**Where a number is unavoidable, cite by name instead** — a `grep -n -F '<distinctive phrase>'`
form rather than a line. It survives every edit above the passage, it re-derives itself when run,
and when the passage is genuinely gone it reports **absence** rather than silently pointing at
whatever now occupies that line. A wrong line number and a correct one are indistinguishable on the
page; a grep that returns nothing is not.

---

### 2.5 Existence is not identity — a reference that resolves feels checked

**A wrong reference that does not exist fails loudly. A wrong reference that exists fails silently.**

Two sessions cited task `CW-20260825-0056` for a finding that lived on `CW-20260825-0087`. Nothing
could catch it: `0056` is a real, in-review task in another project, created within minutes of the
id being invented. A reader following it lands on a coherent task and gets no signal at all.

This is §3.16's symlink problem in the reference layer — `[ -e ]` on the id passes. Resolution is
not correctness.

**The check:** fetch the reference and read its title. Not *does this id resolve* but *is this the
thing I mean?* An identifier is a stored value and takes the same discipline as a number (§1.1): one
written from expectation rather than read from a result is an unverified claim that reads exactly
like a verified one. Where a document carries several, a cross-reference block listing each id with
what it is, stamped verified-by-fetch, costs one lookup each and makes the unverified ones visible.

**Scope:** identifiers a reader will follow — task ids, tickets, shas, cited paths. Not an argument
for re-fetching everything on every read; verify one at the moment you first write it down.

## 3. Environment hazards

Universal first, then repo-specific. **Append here when you find a new one.**

### 3.1 `grep -h` / `-o` voids a downstream path filter

`-h` strips filenames *before* your pipe, so a later `grep -v _test` filters nothing and the count silently includes test files. Use `--include`/`--exclude` instead.

### 3.2 Recursive commands rooted at `.` descend into nested worktrees

Agent worktrees under `.claude/worktrees/` are full repo copies. A `gofmt -l .` or `grep -r .` walks all of them. Root at explicit paths.

### 3.3 A regex ending at `(` counts every match, not your subset

Anchor the whole pattern you mean, or you will count supersets and report them as subsets.

### 3.4 `cp` is aliased to `cp -i` — it silently refuses

*Found 2026-08-24.* `cp -f src dst` prints `overwrite dst? (y/n [n]) not overwritten` and **exits without copying**. A restore that silently failed produced one wrong verification result this session before being caught.

Use `command cp -f`, and **always verify a restore** with `shasum` on both sides or `git diff --stat` against the expected diffstat. Never assume a restore took.

### 3.5 Know which package count you mean (Nanite-specific)

*Found 2026-08-24.* `go list ./...` returns **110** packages; CI's git-tracked filter yields **109**. The extra is `ui/node_modules/flatted/golang/pkg/flatted`, a stray Go package vendored inside npm. Both numbers are correct answers to different questions. State which you mean.

### 3.6 `timeout` is not a macOS builtin

Use `gtimeout` (coreutils) or rely on the tool's own timeout flag.

### 3.7 Go's default test timeout reports as a failure (Nanite-specific)

Go's default is **10 minutes per test binary**. Exceeding it prints `FAIL … 600.7s`, which reads exactly like a test failure. Pass an explicit `-timeout` on any high-count run. This misread has already cost time once.

### 3.8 `mkdir` is aliased to `mkdir -pv` — it writes to stdout

*Found 2026-08-25.* `mkdir -p some/dir` prints `some/dir`. In an interactive transcript that line appears immediately above whatever you ran next, and reads as that command's output — it cost one wrong reading of a `gofmt` result before being caught. Inside `$(...)` it silently contaminates the captured value.

Check with `type mkdir` before trusting interleaved output; use `command mkdir -p` in anything whose stdout you read. **Check the shell you are actually in rather than assuming §3.4's list is complete or current** — in the same shell where this was found, `type cp` reported `/bin/cp` with no alias.

### 3.9 `gofmt -l` prints nothing when it cannot run at all

`gofmt -l` and `goimports -l` write their file list to stdout and their errors to stderr, and a binary that is missing (127), a file they cannot parse (2), or a path they cannot read all produce **empty stdout**. Empty stdout is what "everything is formatted" also looks like. Any check built on `gofmt -l` must inspect the exit status; discarding stderr with `2>/dev/null` and ignoring the status makes a tool failure indistinguishable from a clean tree. This shipped twice in this repo — in `scripts/check.sh`'s format stage and in `lefthook.yml`'s `go-format` hook.

### 3.10 `ls-remote refs/tags/<t>` returns the tag object, not the commit

*Found 2026-08-25.* For an **annotated** tag, `git ls-remote origin refs/tags/v0.3.0` returns the
tag object's SHA, which never equals the commit. Comparing it against a pinned commit reports a
spurious mismatch. Peel it: `refs/tags/v0.3.0^{}`.

`git rev-list -n1 <tag>` and `git describe` peel on their own, so a check built on those is already
correct — the hazard is specific to `ls-remote` and to `cat-file`-style lookups. All four
`hollis-labs/libs` siblings use annotated tags, so this bites every pin-versus-tag comparison in
this repo.

### 3.11 `GOPRIVATE` silently defeats a `GOPROXY=` prefix

*Found 2026-08-25.* `go env GOPRIVATE` is `github.com/hollis-labs/*`, and `GOPRIVATE` **defaults
`GONOPROXY` to the same value**. `GONOPROXY` beats a `GOPROXY=` prefix, so

```
GOPROXY=https://proxy.golang.org go list -m -versions github.com/hollis-labs/<m>
```

never contacts the proxy — it answers from `git ls-remote` on your own origin. Proof: point it at a
host that cannot resolve and it still succeeds.

```
GOPROXY=https://invalid.example.invalid go list -m -versions github.com/hollis-labs/go-envelopes
#   -> v0.1.0 v0.1.1 v0.2.0 v0.3.0        (the proxy was never consulted)
```

It returns the same answer whether or not the proxy has the version, so it is a §4.2 vacuous check
wherever "is this published?" is the actual question. proxy.golang.org populates **lazily on first
request**, so a tag pushed minutes ago exists in git and 404s on the proxy — the window where the
distinction matters is exactly the window you are usually checking in.

Ask the proxy directly instead:

```
curl -sS https://proxy.golang.org/github.com/hollis-labs/<m>/@v/list
```

Or clear the overrides — **with `=none`, not with an empty value.** An empty assignment does not
clear these; Go falls back to the `GOENV` file and the value survives:

```
GOPRIVATE=  go env GOPRIVATE GONOSUMDB     # -> github.com/hollis-labs/*   (still set!)
GOPRIVATE=none go env GOPRIVATE            # -> none
```

So the working form is `GOPRIVATE=none GONOPROXY=none GONOSUMDB=none GOPROXY=https://proxy.golang.org …`.
Note CI sets none of these, so CI resolves through the proxy regardless of what your shell does.

`curl -sS` without `-f` exits 0 on a 404, so **check for the version string, not for command
success.** An unreachable host is loud (`curl: (6) Could not resolve host`, exit 6); a missing
module is a 404 with a `not found:` body and exit 0.

**`GOPRIVATE` also defaults `GONOSUMDB`** — `go env GONOPROXY GONOSUMDB` returns the same value for
both. So hashes for these modules are computed from whatever your local fetch returned and are never
verified against sum.golang.org, and the module cache is populated by direct VCS fetch rather than from the
proxy. **A local `go.sum`, and a local `GOMODCACHE`, are not evidence about published bytes** —
comparing a sibling checkout against the cache compares git to git. This has already produced one
near-tautological "byte-identity" proof in this repo.

**Do not try to tell where a cached module came from by reading its `.info`.** `Origin.VCS` is
`"git"` either way — the proxy serves that field itself, byte-identical to what a direct fetch
writes. Verified 2026-08-25:
`curl -sS https://proxy.golang.org/github.com/hollis-labs/go-runtime-events/@v/v0.1.1.info` returns
`{"Origin":{"VCS":"git",…}}`.

To get real evidence about published bytes, fetch them: download `@v/<version>.zip` from the proxy
and diff it against `git archive <tag>`, or re-resolve into a **fresh, empty** `GOMODCACHE` with the
overrides cleared as above so the checksum database actually applies.

### 3.12 Verify a "path does not exist" proof **at the resolved path**

A decoupling proof usually works by building somewhere a relative dependency path cannot resolve —
for Go, `replace … => ../../libs/<module>` resolved against the main module's directory. The proof is
only as good as that path really being absent, and directory depth does not establish that.
Symlinks, `$TMPDIR` indirection, and macOS's `/tmp` -> `/private/tmp` mapping all break the
inference.

Enumerate and check the path itself:

```
resolved=$(cd <clone> && cd ../.. && pwd)/libs
ls -la "$resolved"     # must be: No such file or directory
```

**Never write "no `libs/` anywhere above it."** Say which path you checked and what it returned. A
proof that passes by silently using the tree it claims independence from is worse than no proof —
it reports success in exactly the check meant to rule that out.

*Concrete instance, 2026-08-25:* `/private/tmp/libs` was a symlink to
`/Users/chrispian/dev/hollis-labs/libs`, so a clone at `/private/tmp/<one-dir>/<module>` resolved
`../../libs` to the real sibling checkouts. Removed, and no other `/tmp`-family symlink into
`~/dev/hollis-labs/` remains (`find /private/tmp /tmp "$TMPDIR" -maxdepth 1 -type l`). If one
reappears, that is a signal, not a coincidence — see Torque `CW-20260825-0019`.

### 3.13 The matcher is not the one you assumed

Three ways this bites, and the remedy differs for each. The common shape is that you get a different matcher — or a different corpus — than the one you wrote the pattern for, and the mismatch reports as **no match**, which reads as a clean negative.

**(a) A different binary.** The shell you verify in is not the shell your code runs in.

`grep` at an agent prompt is a **shell function** from Claude Code's shell snapshot, resolving to
ugrep. Shell functions do not cross into child processes, so anything with a shebang gets
`/usr/bin/grep` (BSD grep). The identical line returns different answers on either side of that
boundary:

```
type grep                       # grep is a shell function from ~/.claude/shell-snapshots/…
grep --version | head -1        # ugrep 7.8.4
/usr/bin/grep --version | head -1   # grep (BSD grep, GNU compatible) 2.6.0-FreeBSD

printf 'costs $5 today\n' | grep -c 'costs $5'              # 0   ugrep
printf 'costs $5 today\n' | /usr/bin/grep -c 'costs $5'     # 1   BSD grep, POSIX BRE
```

Under POSIX BRE a `$` is an anchor only at the end of the pattern and literal elsewhere; ugrep
treats it as a metacharacter. **Any literal `$` mid-pattern is affected** — `$(`, `$5`, `${`.

This corrupts the cheapest signal we have. §1.3 says a zero is a signal that the command is wrong;
here it can instead mean the *matcher* differs from the one the pattern was written for, and it
fails silently in the direction that reads as "clean."

Use `-F` for literal patterns, `-P` where a real regex is wanted, or `/usr/bin/grep` explicitly.
**Prove a claim about a script by running the script, not by grepping it from a different matcher.**

The general rule outlives the instance: an interactive profile's functions and aliases are invisible
to every child process, so any verification typed at a prompt may not describe what a shebanged
script does.

**(b) A different dialect, same binary.** BSD `grep` is **BRE**: a bare `|` is a literal pipe, not
alternation. `grep -c 'cannot catch|does not catch' <file>` searches for that whole string and
returns `0`, which reads as "absent."

```
printf 'cannot catch\n' | /usr/bin/grep -c  'cannot catch|does not catch'   # 0   BRE: | is literal
printf 'cannot catch\n' | /usr/bin/grep -cE 'cannot catch|does not catch'   # 1
```

Escaping it as `\|` mostly works and therefore hides the case where it does not. **BRE alternation
is fine for plain literals and breaks when a branch is anchored** — `^` and `$` anchor only at the
very start and end of a BRE, not at the edges of an alternation branch, so one branch silently
stops matching:

```
printf 'not a back-fill\nnot a backfill\n' | /usr/bin/grep -c 'back-fill\|backfill'   # 2  fine
printf 'alpha\nbeta\n\ngamma:\n' | /usr/bin/grep -c  '^$\|:$'                        # 1  WRONG
printf 'alpha\nbeta\n\ngamma:\n' | /usr/bin/grep -cE '^$|:$'                          # 2
```

Each anchored branch matches correctly on its own (`^$` → 1, `:$` → 1); only the alternation loses
one. Worth its own mention because **the failure direction is *more* results, not none** — under a
`-v` filter the lost branch under-excludes, and every other trap in this section fails toward zero,
where a zero is at least suspicious. An inflated count reads as success.

**(c) A corpus with line breaks your phrase doesn't have.** A `grep -F` for a phrase that wraps
across two source lines returns `0` while the phrase is plainly present.

**Never grep a multi-word literal in prose.** Pick the longest single word in the phrase that is
distinctive on its own and match that; add a second single-word check if one word is too common. In
hard-wrapped prose the wrap point is a property of the file's width and the paragraph's history, not
of your phrase — you cannot predict it by looking at the string you typed.

**An edit changes the wrap points of every line after it**, so a post-edit grep of a multi-word
phrase is unreliable *by construction*, not by bad luck — and it fires at the moment you are
confirming a fix, when you are most confident and least suspicious. A one-bullet fix to a task file
rewrapped a sentence the fix was required to *retain*:

```
grep -cF 'one past the highest' <file>            # 1     looks like a pass
grep -nF 'one past the highest' <file>            # 553:  ...but that is the work log
sed -n '1,125p' <file> | grep -cF '...'           # 0     the body: false negative
sed -n '1,125p' <file> | tr '\n' ' ' | grep -cF … # 1     intact, merely rewrapped
```

One rewrap gives a **false negative** when you scope to the region you changed, and a **false
positive** when you don't, because an unrelated copy 428 lines away answers a question you did not
ask. Neither is distinguishable from a correct answer and they point in opposite directions: the
careful reader and the careless one are both wrong.

**Same class — the artifact you are reading is not the text you think you are reading.** A
`git diff -U0` hunk omits unchanged lines, so reading one as contiguous prose can make an intact
sentence look broken; a passage appeared to read "`Up` aborts with / fails to start" because the
clause between them was an unchanged line the zero-context diff never printed. Check the file before
calling a defect. In both cases the corruption is invisible because what comes back is well-formed.

**The reflex, not the awareness.** `-E` by default for anything with alternation, `-F` for anything
literal, `/usr/bin/grep` when you want POSIX BRE specifically, **single words rather than phrases
whenever the corpus is prose** — and a positive control on every `→ 0`. Naming a danger without giving a reflex produces an entry you read *after* the mistake: this
one was in the file, and cost two more wrong answers in the two days after it was written.

### 3.14 A scratch clone of the real repo has the real repo as `origin`

`git clone <path-to-real-repo> <scratch>` sets `origin` to that path, so **every push test in the
scratch clone targets the real repository.** Depth of nesting and a `/private/tmp` location do not
change this.

```
git init --bare "$SCRATCH/origin.git"          # do this
git clone "$SCRATCH/origin.git" "$SCRATCH/w"
case "$(git -C "$SCRATCH/w" remote get-url origin)" in *"/apps/nanite") exit 1;; esac
```

*Concrete instance, 2026-08-25:* a `git push -f origin seed:main` from such a clone was refused only
because `main` happened to be the checked-out branch (`receive.denyCurrentBranch`). On any other
branch it would have rewritten local `main` on top of hours of uncommitted work. **It was saved by
the state of the tree, not by design.** The safe pattern was already in use by two other agents in
the same session.

### 3.15 `bash` drops NUL from a command substitution; `zsh` preserves it

```
bash -c 'v=$(printf "a\0b\0"); printf "%s" "$v"'   # ab   + "warning: ignored null byte in input"
zsh  -c 'v=$(printf "a\0b\0"); printf "%s" "$v"'   # a \0 b \0
```

The interactive shell here is zsh, so a check typed at a prompt reports the opposite of what a
bash-shebang script does. Consequence for `-z`-style NUL-delimited git output: it **cannot** be
captured with `$(...)` in bash. Write it to a file and read with `read -r -d ''`.

Same family as §3.13 — verify in the shell the code runs in.

### 3.16 Two scratch paths differing only in case are one file

This machine's filesystem is case-insensitive, and there is nowhere to relocate to: `/private/tmp`,
`$HOME` and the repo all report the same device and mount.

```
printf 'one\n' > "$D/zz"; printf 'two\n' > "$D/ZZ"; cat "$D/zz"   # two — one file
```

The single-letter `a`/`A`, `b`/`B` convention that shell one-liners invite is exactly what triggers
it. Writing the second **silently truncates** the first — no error, just an empty input where data
was. Use distinct multi-character names (`set_phrase`, `set_symbol`), never case as the only
distinguisher.

Git agrees: `git config --get core.ignorecase` → `true`, so a case-only rename is not a change git
will show you either. No tracked paths collide today
(`git ls-files | tr 'A-Z' 'a-z' | sort | uniq -d` → empty).

*Concrete instance, 2026-08-25:* an intermediate set written to `$D/a` and then `$D/A` destroyed its
own input mid-derivation, and the run reported "B \ A = all 12" — a sensible-shaped result that
would have sent someone re-auditing two greps as disjoint. **A silent truncation that yields a
plausible number is worse than one that yields an obvious error.**

### 3.17 Ask whether the result would be correct if your hypothesis held

Before reporting a surprising result, ask what it would mean if the thing you are testing were
working correctly. **If the result is what correct behavior looks like, it is evidence about your
instrument, not your subject.** Discard it and fix the harness.

This is the counterpart to §4's "prove the check can fail": that one asks whether a passing check
could ever fail, this one asks whether a failing check is measuring anything.

Four instances on 2026-08-25, all caught this way and none reported:

- A matrix returning `127` on all eight rows — uniform implausibility; a reset had deleted the
  script under test, and every row would have read as a fail-closed pass.
- A guard "wrongly" accepting `149_a\nb.sql` — `149` was a legitimate number, so accepting it was
  correct; the test, not the guard, was wrong.
- A file reported as exclusively in set A *and* exclusively in set B — impossible under any
  hypothesis.
- A `git push -f` "succeeding harmlessly" — it had been refused, so the follow-on measurement
  proved nothing.

Two more found the same way by other agents in the same session: a `127` from a `git clean` that
removed a modified script, and a "regression" that was a correct duplicate rejection against a lab
remote that had already advanced.

The habit generalizes past this repo, and it is cheap: one question, asked before the result leaves
your hands.

### 3.18 `$rev:path` in zsh silently applies a history modifier — it corrupts `git show`

*Found 2026-08-25, measured at `3954f9be` with `R=3954f9be`.* **Whether this bites depends on the
path**, so testing the idiom once and concluding it is safe is exactly the wrong move. In zsh, a `:`
that immediately follows a *bare* parameter expansion and is followed by a valid **modifier letter**
is parsed as a history modifier and rewrites the value. Any other letter passes through literally:

```
printf '%q' "$R:scripts/check.sh"   # -> 3954f9bek.sh                            :s substitute
printf '%q' "$R:lefthook.yml"       # -> 3954f9beefthook.yml                     :l lowercase
printf '%q' "$R:cmd/x"              # -> 3954f9bemd/x                            :c
printf '%q' "$R:ui/src"             # -> 3954F9BEi/src                           :u UPCASED the sha
printf '%q' "$R:examples/x"         # -> xamples/x                               :e ate the sha
printf '%q' "$R:api/x"              # -> /Users/…/apps/nanite/3954f9bepi/x       :a prepended $PWD
printf '%q' "$R:scripts/x"          # -> zsh: bad substitution

printf '%q' "$R:go.mod"             # -> 3954f9be:go.mod           intact  (control)
printf '%q' "$R:docs/x"             # -> 3954f9be:docs/x           intact  (control)
printf '%q' "$R:internal/store"     # -> 3954f9be:internal/store   intact  (control)
printf '%q' "$R:TASKS/x"            # -> 3954f9be:TASKS/x          intact  (control)
```

In this repo `scripts/`, `cmd/`, `config/`, `ui/`, `examples/`, `api/` and `lefthook.yml` break,
while `go.mod`, `docs/`, `internal/`, `pkg/`, `plugins/`, `TASKS/` and `.github/` are fine. **Someone
checks `git show "$R:go.mod"`, sees it work, and generalizes — that is §4 again.** It is also
data-dependent past the first letter: after `:s` the *next* character becomes the delimiter, so
`scripts/check.sh` corrupts while `scripts/x` raises `bad substitution` instead.

**Four outcomes, ordered by nastiness.** Most corruption is loud, and that is the good case:

1. **Intact** — `:` followed by a non-modifier letter. `go.mod`, `docs/`, `internal/`, `TASKS/`.
2. **Loud, exit 128** — the common corruption. `git show "$R:lefthook.yml"` and
   `git show "$R:scripts/check.sh"` both fail immediately; a mangled ref is not a ref.
3. **Path-shaped and plausible, still exit 128** — `:a` prepends `$PWD`, so `"$R:api/x"` becomes
   `/Users/…/apps/nanite/3954f9bepi/x` and git answers *"unknown revision or path not in the working
   tree"*. It reads like a missing file rather than a mangled command.
4. **Silent, exit 0** — the modifier consumed the path *entirely*, leaving a bare valid rev. `git
   show` then has a perfectly good commit to show, prints its message plus diff, and returns 0.

**Branch 4 is the one to fear, and it is the one that happened here:**

```
printf '%q' "$R:scripts/gosec-repeat-run.sh"                       # -> 3954f9be   (path gone)
git show "$R:scripts/gosec-repeat-run.sh"   | wc -l                # -> 301, exit 0
git show "${R}:scripts/gosec-repeat-run.sh" | wc -l                # -> 117   <- the actual file
git show "$R:scripts/gosec-repeat-run.sh"   | grep -cF 'gosec -no-fail'   # -> 0
git show "${R}:scripts/gosec-repeat-run.sh" | grep -cF 'gosec -no-fail'   # -> 1   (control)
```

A downstream `grep -n` returns **no match**, and a no-match reads as a clean negative result.

`:u` and `:e` reach branch 4 only under the same condition — the path fully consumed. Upcasing is not
itself a danger; it is what keeps the leftover *resolvable*, since git accepts an upper-case hex sha
(`git rev-parse --verify 26899CD0` -> `26899cd0b58f…`, `git show 26899CD0` exit 0). Leave any path
behind and it is loud again: `git show "26899CD0i/src"` exits 128.

**The lesson is not the mechanism, it is what the wrong answer looked like.** The conclusion drawn
from that `0` — that the wrapper passes no `-concurrency` — *is true*. It would have shipped as a
verified claim, obtained by a command that never read the file. §1.3's "a zero is a signal" was the
only thing that caught it.

**Two safe forms. Quoting is not one of them** — `"$R:…"` mangles identically, because the parse
happens inside the expansion. Terminate the parameter name, or keep the path out of the literal:

```
git show "${R}:scripts/check.sh" | wc -l   # -> 248   brace the rev          <- prefer this
P=scripts/check.sh; git show "$R:$P" | wc -l   # -> 248   path in a variable
git show 3954f9be:scripts/check.sh | wc -l     # -> 248   fully literal (control)
```

`:` followed by `$` is not a modifier letter, which is why the variable form is safe.

**That has a nasty consequence: the hazard hides from anyone who tests it in a loop.** The natural
way to check a list of paths is to iterate with the path in a variable — and that is one of the two
*safe* forms, so the check comes back clean and reads as disproof. Two separate reproductions
disagreed for exactly this reason during the investigation that found this, one having substituted
the path in before `eval` and the other having kept it a variable at expansion time. **The two
constructs look identical in the source.** Test it with a literal path, and compare `printf '%q'`
output rather than end results.

**This is a zsh language property, not an environment effect.** `zsh -f` loads no rc files and still
corrupts, so there is no alias, option or profile to configure away:

```
zsh -f -c 'R=3954f9be; printf "%s\n" "$R:scripts/check.sh"'   # -> 3954f9bek.sh   (zsh 5.9)
bash  -c 'R=3954f9be; printf "%s\n" "$R:scripts/check.sh"'    # -> 3954f9be:scripts/check.sh
```

`bash` has no such feature, so a bash spot-check reports it clean. **Brace the rev in every
`rev:path` you hand to git** — §2.3 asks you to pin citations to a commit, and `${var}` is what makes
that idiom usable with a variable at all.

`bad substitution` is a **parse** error, so it aborts the whole compound command at that point —
every later line in a multi-command block silently does not run, and the truncated transcript reads
as a complete one.

---

### 3.19 `readlink -f` is not an existence check on macOS

Given a **dangling** symlink, macOS `readlink -f` prints the path with the unresolvable trailing
component **silently dropped**, and exits 0.

```
d=$(mktemp -d); ln -s ./nowhere/target "$d/link"
readlink -f "$d/link"                                  # …/nowhere    <- note: no /target
[ -e "$d/link" ] && echo present || echo MISSING       # MISSING
```

Read alone that looks like a link that resolved. **The check:** `[ -e ]` (or `-d`/`-f`) on the link
path. Use `readlink -f` to *report* a target, `[ -e ]` to *assert* it.

**Scope:** about proving a symlink resolves. Says nothing about non-symlink paths, and nothing about
GNU `readlink`, which errors on a dangling argument rather than truncating.

**A related trap in the same family:** a bare path to a symlinked directory is silently empty to
shell tools — `grep -r`, `find`, `du`, and `tar` without `-h` all return nothing or one entry and
exit 0. A **trailing slash** decides whether the start point is followed at all; hop count only
decides whether following once is enough. Node's `fs.readdirSync` and Python's `os.walk` are
unaffected, so the tool you verify with may disagree with the consumer you care about.

## 4. Verifying that your verification verifies

The defect class named in `failure-modes.md` §6 — a check that reports success without having checked. Four instances surfaced on 2026-08-24.

### 4.1 Prove the check can fail

**For any fix with a regression test: revert the fix, watch the test fail, restore, verify the restore byte-for-byte.** Then report that you did it and what the failure looked like.

An assertion never observed failing is an assertion of unknown strength.

### 4.2 Ask what input would make this pass while proving nothing

An empty collection, a zero count, an unreachable target, a check that never ran. If that input is reachable, the check needs a **positive control** — prove the mechanism *can* observe the thing before asserting its absence.

### 4.3 Assert presence before properties

```go
if len(workers) != 1 { t.Fatalf("got %d workers, want 1", len(workers)) }
if workers[0].Status != want { … }
```

A bare `for … range` over an empty slice asserts nothing and passes. That is a real defect found in this repo, not a hypothetical.

---

### 4.4 When success and total failure print the same bytes

A tool whose clean output is empty or minimal often produces **the same output when it did not run at
all**, with the discriminator on a stream nobody reads — an exit status, or stderr. Three instances
in this repo:

- `gofmt -l` prints nothing whether the tree is clean, a file is unparseable, a path is missing, or
  the binary is absent. Only the exit status separates them: `0 / 2 / 2 / 127`.
- A broken `golangci-lint` run prints `0 issues.` on a cold cache — byte-identical to success — with
  the diagnosis only on stderr.
- `gosec` under `-no-fail` returns a **well-formed report missing a strict subset of findings**, with
  `files`/`lines` intact and `Golang errors` empty, when an SSA panic is recovered. Its only trace is
  two lines on stderr.

**The dangerous form: the coinciding condition is one you did not choose, cannot see, and meet
first — and the natural act of checking destroys it.** Warm the cache and the broken lint run becomes
obviously broken; cold is the *first* run, which is when someone debugging arrives. A hazard that
appears only on a first run and vanishes on every retry **gets filed as unreproducible**, because the
obvious way to check is to run it again. `gofmt` does not have this property; the lint case does, and
that is what makes it worse rather than merely equal.

**Two mitigations, and the second is the one to copy.**

*Reader-side* — say how to interpret the ambiguous output where the reader actually is. Necessary
when the tool is not yours to change.

*Tool-side* — **don't emit an ambiguous result at all.** `scripts/check.sh` makes *examined nothing* a
first-class third outcome (`grep -n 'examined nothing' scripts/check.sh`) rather than relying on
anyone to read `$?`, which is exactly what this entry says nobody does. That countermeasure was built
in this repo, for this class, and then the class was hit twice more without anyone recognising it —
which is the argument for a pattern rather than a third log entry.

**Scope.** This is about tools whose *clean* output is empty or minimal. Where success output is
substantive, an empty result is already visibly wrong and a third outcome is noise that trains people
to ignore the real ones.

**The diagnostic, which transfers past these tools:** when something is unreproducible, ask **what the
first run had that the retry didn't** — cold cache, empty state, unwarmed index, first connection.
*"Could not reproduce"* is evidence about the retry's conditions, not only about the original.

### 4.5 A control built from the assertion's own inputs proves nothing

The sharpest instance of 4.2 found in this repo, because the artifact at fault is **the check written
to prevent exactly this class**.

A stderr assertion greps for two fixed marker strings. Its positive control built the "should match"
fixture by **interpolating the same marker variables** the assertion greps with. So a *misspelled*
marker matched its own fixture, the control passed, and the assertion could never have matched the
real tool. Demonstrated with a mutant — `Panic while running SSA analyzer` for `when` — which exited
`0` against a stub that was emitting a genuine panic line.

**A positive control must be built from the artifact, not from the assertion's own inputs.** The
fixture is now a literal copy of what the tool emits. Note what that still does not cover: the tool
renaming its message upstream. Say so rather than implying the control is total.

### 4.6 A discriminator is only evidence about the failure it was designed for

Choosing a discriminator is choosing which failures stay invisible.

A task repaired symlinks that resolved nowhere, verified per-entry with `[ -e ]` — correct for that
failure, and it caught it. The repaired links then passed `[ -e ]` and `ls` while returning **empty**
to `find` and `grep -r` over the same path: a second, narrower instance of the very failure the task
existed to remove. The verification was not sloppy. It was aimed at the previous failure mode.

**The check:** when a fix passes, ask *which* failure your instrument rules out, and name one it does
not. Where several access patterns exist, **verify that they agree with each other** rather than that
any one returns a good number. Disagreement between two methods over the same path is itself the
finding.

**And the qualifier this rule needed, produced by its own author within the hour of writing it.** The
follow-up fix was verified by exactly this method and its commit message claimed "every access pattern
agrees." It did not — every method checked shared a trailing slash that was never a conscious choice.
**Agreement across methods you already thought of is agreement among your assumptions.** Ask what the
methods you ran have *in common* — a flag, a path form, a working directory — because that shared
property is the one thing the agreement cannot test.

**Scope:** about verifying a change you just made. Not an argument for running every check on
everything; an argument for knowing which check you ran and saying what it leaves uncovered.

### 4.7 Counts are not evidence of resolution

A directory of N dangling symlinks and a directory of N working ones are **byte-identical** under
`ls -1 <dir> | wc -l`. A portfolio survey printed `REAL DIR (20 entries)` for two repos and they were
read as the known-good reference to copy. Per-entry testing showed both resolved **zero**. Had the
count been trusted, the fix would have been to replicate a configuration that works nowhere.

This generalizes past symlinks: **any count over a set whose members can individually be broken is
equally consistent with all-fine and all-broken.** §3.17's question returns *yes* for every such
count, which makes counts a named blind spot in that rule rather than a separate concern — and it is
its own entry because someone applying §3.17 would not find it there.

**The check:** test members, not the container. A count answers "how many names are here", never "how
many of them work". **And when comparing two enumerations, compare sets, not counts** — equal totals
are not identity, since two different 92s compare equal:

```
comm -23 a b | wc -l    # 0
comm -13 a b | wc -l    # 0   <- both empty is the assertion; matching totals is not
```

Rule out the cheap agreements first: `grep -l ''` skips empty files, `ls` hides dotfiles where `find`
does not, and a recursive tool counts at every depth. Compare like for like.

### 4.8 An inherited frame is answered rigorously in the wrong space

§4.1–4.7 are instruments answering the wrong question. This is the **question itself** arriving
pre-shaped, where rigor makes the wrong answer more convincing.

A task reported that a reviewer tried to invoke the `code-review` **skill** and had none. Every
investigation searched the skills tree. The conclusion — *no skill named `code-review` exists* — was
derived carefully, stated with its command, verified by two sessions, and written into a commit
message. It was also beside the point: a **role** named exactly `code-review` existed the whole time,
one directory over, referenced by three agents in the project config. Nobody looked, because the word
"skill" arrived in the problem statement and was never treated as a claim.

**The check:** before searching, ask *who chose the noun in this question, and did they verify it?* A
term inherited from a task description, a bug report, or an upstream agent is a hypothesis about
where the problem lives, not a constraint on where to look. When a search over the named space comes
back clean, that is the moment to widen it, not the moment to conclude.

The tell is a confident negative — *"no X named N exists"* is only ever as wide as the space
searched, and the space came from someone else.

**Scope:** about inheriting the *category* of a thing. Not an argument for unbounded search, and it
does not apply where the category is genuinely fixed by the request.

## 5. Amplification has directions

Running a test *harder* is not one axis. Picking the wrong direction produces a false green.

| Suspected | Use | Because |
|---|---|---|
| data race | `-race`, low count | usually fires on the first round |
| timing / ordering | high `-count`, **drop `-race`** | `-race` inflates the gaps these bugs live in |
| wide-window race | `GOMAXPROCS=1` + load | needs contention, not repetition |

Measured 2026-08-24: a task prescribed `-race -count=100` as its acceptance gate for an ordering bug. `-race` made that flake **~18× rarer** (inter-event gap p50 106 µs → 2,187 µs); expected failures under the prescribed command were **0.005** with the bug fully present. It would have gone green and been called fixed.

**A deterministic reproduction beats every amplification strategy.** Prefer making the test not need luck.

Full guidance: `testing-workflow.md`.

---

## 6. Scope and escalation

- **Do the dispatched task. Nothing else.** If you find other work — and you will — **write it down and report it; do not do it.**
- **Escalate rather than deciding alone** when a fix requires changing production behavior rather than test/sync code, when it would touch a file outside your stated fence, or when a question your dispatch flagged has no clear answer in the code.
- **Do not expand scope to route around a blocker.** Stop and ask.

An honest negative result is a real deliverable. *"Not reproduced in N iterations; here is the hazard I found by reading, here is what I ruled out"* is worth more than a manufactured fix.

---

## 7. Reporting

State, every time:

1. Starting commit and tree state.
2. Every file changed and why.
3. Line numbers **you** derived, with the command.
4. Every command run and its result — commands beside numbers.
5. Iteration counts.
6. Evidence the regression test fails without the fix.
7. **Scope-parking list** — what you found and deliberately did not fix.
8. Corrections you made mid-flight to your own earlier numbers.

Item 8 matters more than it looks. Agents that report their own corrections are measurably more trustworthy than agents that present a clean narrative, because the clean narrative is the cheaper thing to generate.

---

## Provenance

These rules were not designed. They accumulated from real failures in the `TASKS/audit-remediation/` batch (Waves 0–4, 2026-08-21→23), where a verification pass ran at every handoff and **every pass produced a finding** — almost never a bad judgment call, almost always a wrong number or a stale citation.

They then propagated by copy-paste: into a handoff prompt written by one session, inherited by the next, copied into that session's own subagent dispatches, extended with newly found hazards, and passed on again. Nobody instructed this. It was observed on 2026-08-24 when an orchestrator was asked how it had arrived at "ship the command next to the number" and found the instruction verbatim at line 112 of its own boot prompt, authored by a session it never spoke to.

That propagation is a good sign and an unreliable mechanism. This file exists to make it durable.

**Portability:** §§1, 2, 4, 5, 6, 7 and hazards 3.1–3.3, 3.6 are project-agnostic and should be carried to other repos as-is. Hazards 3.4, 3.5 and 3.7 are environment- or repo-specific — keep the section, replace the contents.
