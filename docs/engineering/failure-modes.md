# Failure modes: how measurements and documents mislead

**Required reading before writing a task file, kickoff prompt, handoff, or
review.**

Every example here is real, from the `TASKS/audit-remediation/` batch
(Waves 0–4, 2026-08-21 → 2026-08-23). Nothing is hypothetical. That batch ran
a verification pass at every handoff — planner checking worker, worker checking
planner, reviewer checking both — and **every single pass produced a finding.**
Notably, almost none were judgment failures. The decisions held up. What kept
being wrong were the *measurements and citations underneath them*, in ways that
looked right.

Six classes, six different defenses. They don't substitute for each other.

Classes 1–5 come from the audit batch itself. **Class 6 was found in the
post-audit follow-up work (2026-08-24)** — while fixing three test defects the
batch had flagged, in the tests and tooling that were supposed to be doing the
checking.

**Companion doc:** `tracking-integrity.md` covers a further class — the same
data tracked in several places with no designated authority, so drift becomes
undetectable rather than merely present. It also specifies the checker that
catches classes 2 and 3 mechanically.

---

## 1. Tool edges — the command answers a different question than you asked

The tool is never wrong. It answers exactly what you typed, which is not always
what you meant. Re-reading your own prose will never catch this, because the
prose accurately describes a measurement that was wrong.

### `grep -h`/`-o` silently voids a downstream path filter

```bash
# WRONG — grep -v never sees a filename, so it excludes nothing
grep -rhoE '\.Exec\(' internal/store/*.go | grep -v _test | wc -l

# RIGHT
grep -rhoE '\.Exec\(' --include='*.go' --exclude='*_test.go' internal/store/ | wc -l
```

With `-h` the filename is stripped *before* the pipe, so `grep -v _test`
matches against the text `".Exec("` and filters nothing. The count silently
includes every test file.

**This one was made twice in the same session — the second time by the person
who had just written the warning.** Intending to be careful is not a defense.

### A regex that ends at `(` counts every match, not the subset

```bash
# WRONG — matches ALL exported methods; the pattern stops at the paren
grep -rhE '^func \(s \*Store\) [A-Z][A-Za-z0-9]*\(' internal/store/*.go
```

This was reported as "371 methods lacking a context." It was **371 total
methods, of which 134 already had one** — the real figure was 237. A fabricated
"target: 505" followed from it into a task file. An executing agent caught it
only because the command was printed alongside the number.

That last sentence is the origin of this repo's most-propagated execution rule.
It is stated directly, as something an agent does rather than something an
author observed, in `agent-verification-discipline.md` §1.2 — *ship the command
next to the number*. Worth knowing where it came from: nobody designed it, and
it survived four hops between sessions by copy-paste before it was written
down.

### Recursive commands rooted at `.` descend into nested worktrees

```bash
# WRONG in this repo — .claude/worktrees/ held 88 full repo copies
gofmt -l .            # returned 10,502

# RIGHT
gofmt -l ./internal ./cmd ./pkg    # returned 105
```

### `du` reports allocated blocks, not summed file sizes

A directory reported as "~16 MB, 30 files" was **8.0 MB, 29 files** — `du -sh`
counts blocks, and the file count came from an `ls -la` listing that included
`.` and `..`.

### A `tool.`-prefixed symbol may be a local variable, not the package

`grep 'tool\.[A-Z]'` matched `agent.ID, tool.ID` where `tool` was a loop
variable, in files that don't import the package at all. Briefly produced a
false alarm that a package retirement would break the build.

**Defense: ship the command next to the number**, so verifying is a paste
rather than an investigation — and sanity-check the command against a case
whose answer you already know. A count of zero from a command that *should*
match something is the cheapest available signal that the command is wrong.

---

## 2. Derive; don't store

**Every count error in the batch came from a number that had been stored** — in
a task file, in kickoff prose, in a README, or in an agent's own context from
earlier in the same conversation. Not one came from a fresh measurement.

| Stored | Real | Where it lived |
|---|---|---|
| 371 methods without `ctx` | 237 | task file |
| "twelve tasks" | 13 | kickoff prose, 4 places |
| "six of the eight files" | 7 of 9 | kickoff prose |
| ~16 MB, 30 files | 8.0 MB, 29 | README, escalation, handoff |
| 65 task files | 67 | this batch's README, corrected on derivation |

Restating a number reads as *emphasis*, not as an independent unverified
claim — which is exactly why it doesn't feel like duplication while writing it.

**Defense:** derive at the moment of use. Never recall a number from earlier in
the same document, and never carry one across documents. Where a number
genuinely must be stored, it needs one of:

- the command that produces it, or
- a stamp: *"measured at `1d3bfd96`; re-derive before use."*

This worked every time it was applied. `13/03` (gofmt) and `08/03` (G304
sites) both carry re-derive instructions, and both counts moved before those
tasks ran.

---

## 3. Stale premises — the tool was right, the world moved

No wrapper catches this and no derivation rule helps. The command was correct,
the interpretation was correct, and the code changed afterward.

- A task file's fix snippet showed `DeleteAgentByID(id string)`. A later sweep
  had made it `(ctx context.Context, id string)`. **Copying it verbatim would
  not compile.**
- A finding described "`http.DefaultClient`, no timeout, no host restriction."
  A wave that landed *after* it was measured removed `http.DefaultClient`
  entirely and added both a timeout and a size cap. Half the finding was
  already fixed; deciding it as written would have re-added a duplicate
  timeout.
- A task warned its rename "may reach every caller across the tree." Measured:
  **11 references in four files.**

**Defense: stamp any task file or doc that cites code with the commit it was
verified against.** Right now a reader cannot distinguish a citation written an
hour ago from one written two months and forty commits ago. Both look equally
authoritative.

---

## 4. Documents are best-effort snapshots. Reality wins.

This is the class that makes the previous one dangerous rather than merely
annoying.

A task file, a design doc, an engineering guide — each is written in a
declarative voice, because that is how you write clearly. An agent reads that
voice as *ground truth* and treats disagreement with the code as its own
error to be reconciled, rather than as evidence the document is stale.

**The rule: a document records the best understanding at the time it was
written. Current source is the source of truth. When they disagree, the code
wins and the document gets corrected — not worked around.**

This must be stated, not assumed, because the failure is invisible: an agent
that quietly bends its implementation to match a stale doc produces work that
looks compliant and is wrong.

Corollary for authors: when you write something you are *not* certain of, say
so in the text. "Verified against source" and "assumed" should be
distinguishable by a reader who wasn't there.

---

## 5. Rule scope inflation

A rule written for one scope gets applied universally, because the text didn't
fence its scope.

**Real case:** the engineering guide says not to reuse terms. An agent
therefore refused to use the word "slot" anywhere. But `User.slots` and
`Skills.slots` are unambiguous — the receiver disambiguates them. The rule is
about *domains and subsystems* (an `agents/*`-style collision), not about field
names.

Nothing broke. But the agent spent effort avoiding a non-problem and produced
worse naming to satisfy a rule that didn't apply.

**Defense, for rule authors:** state the scope in the rule, and give an example
of something the rule does *not* cover. A rule with no stated boundary will be
applied at every boundary.

**Defense, for rule readers:** when a rule seems to forbid something obviously
fine, that is evidence you are outside its intended scope — not evidence the
obviously-fine thing is wrong. Ask.

---

## 6. Vacuous verification — the check ran, and confirmed nothing

Found 2026-08-24, in post-audit follow-up work rather than in the batch itself.
That timing matters: these were found *while fixing defects the audit had
already flagged*, in the tests and tooling doing the checking.

Distinct from class 1. There, a command answers a different question than you
asked and you get a wrong number. Here the check runs correctly, reports
success, and has verified **nothing** — a right answer to a question that was
never posed. Nothing looks wrong at any point. The suite stays green forever.

This is the class that most rewards a no-code-review workflow's attention: a
human skimming a diff might notice an assertion that cannot fire. With review
removed, the test is the only thing asserting correctness, and a test that
asserts nothing produces silence indistinguishable from success.

### A `range` loop over an empty collection asserts nothing

```go
// WRONG — passes when List() returns empty, which under load it sometimes did
workers := mgr.List()
for _, w := range workers {
    if w.Status != StatusCanceled { t.Errorf(...) }
}

// RIGHT
workers := mgr.List()
if len(workers) != 1 { t.Fatalf("got %d workers, want 1", len(workers)) }
if workers[0].Status != StatusCanceled { t.Errorf(...) }
```

`internal/worker/manager_test.go`, `TestShutdown`. The test had **two** defects,
not one: the flake it was filed for, and this — a second, quieter mode where it
passed having checked nothing. Reproduced under scheduler saturation: 1 run in
25 returned an empty list.

### A "still blocked" assertion with nothing reachable to block

`internal/sandbox/os_linux_test.go`,
`TestNetnsBridge_HostArbitraryPortStillBlocked`. In an unprivileged container
the namespace setup may be unavailable, so the assertion succeeds because
nothing was *ever* reachable — not because the bridge blocked it.

**A security test that passes vacuously is worse than no test**, because it
reports coverage that does not exist.

Defense: a **positive control**. Prove the harness *can* reach an allowed port
in this environment before asserting the arbitrary one is blocked. If the
control fails, `t.Skip` with a reason. A skip is honest; a vacuous pass is not.

### A ratchet that reads an empty scan as a total improvement

`scripts/quality-ratchet.py`, `lint` subcommand:

```
$ echo '{"Issues": []}' > empty.json
$ python3 scripts/quality-ratchet.py lint --report empty.json …
audit-config linters: baseline=3255 current=0
audit-config linters: reductions detected for cyclop, dupl, … unused
audit-config linters: ratchet passed
EXIT=0
```

A scan that covered nothing is reported as a 3,255-finding improvement across
18 linters. A report filtered to 12% of the codebase also exits 0.

Reachable, not hypothetical: a `golangci-lint` invocation passing import paths
where directories were expected emitted `typechecking error: … directory not
found`, then `0 issues.`, exit 0. One malformed argument between a green
nightly and a scan of nothing.

The gosec comparator in the *same file* has the defense already —
`cardinality_failed = ignored != 1` — so an empty gosec report still exits 1.
The lint side has no equivalent.

**And its own tests encode the hole.** `scripts/quality-ratchet_test.py`
contains a test asserting the empty report exits 0. The vacuous pass is
codified as intended behavior, so anyone fixing the ratchet meets a failing
test and may "fix" the fix.

### An acceptance command that cannot detect its own bug

A task file specified `go test -race -count=100 ./internal/service/` as the
gate for an event-ordering flake. `-race` instruments every memory access,
which inflates the timing gaps that ordering bugs depend on — measured
inter-event gap p50 **106 µs** without `-race`, **2,187 µs** with it.

| `-race` | count | failures |
|---|---:|---:|
| yes | 1,000 | 0 |
| yes | 20,000 | 1 |
| **no** | 20,000 | **18** |

Expected failures under the prescribed command, with the bug fully present:
**0.005**. The gate would have gone green and the defect been reported fixed.

`-race` and high `-count` are not interchangeable intensities of the same dial.
See `testing-workflow.md` §2.

### Defense

**For any assertion, ask what input would make this pass while proving
nothing** — an empty collection, a zero count, an unreachable target, a check
that never ran. If that input is reachable, the check needs a positive control.

**For any fix with a regression test: revert the fix, watch the test fail,
restore, verify the restore byte-for-byte, and report that you did.** An
assertion never observed failing is of unknown strength. Both fixes landed
2026-08-24 did this; both claims held up under independent re-verification.

Guard the restore — see `agent-verification-discipline.md` §3.4. A silently
failed restore produced one wrong verification result before being caught.

### A note on tooling

Unlike classes 2–5, this class **is** partly mechanizable. Mutation testing
(`go-mutesting`, `gremlins`) introduces small changes and reports which ones no
test catches — finding vacuous tests by construction. Worth evaluating against
`internal/service`, `internal/worker` and `internal/store` first.

Coverage tooling is the weaker cousin: it shows what is never *executed*, not
what is executed but never *asserted on*. The `range`-over-empty case above
shows full line coverage.

---

## Before you publish a task file, kickoff, or handoff

- [ ] Every number derived from a command **at the moment of writing** — not
      recalled, not carried over, not computed by mental arithmetic.
- [ ] Every stored number carries its command or a `measured at <commit>` stamp.
- [ ] Every code citation verified against current `HEAD`, and the file stamped
      with the commit it was verified against.
- [ ] Any count that must appear twice has one copy marked authoritative:
      *"count from this list; if another number appears below, this line wins."*
- [ ] Anything asserted but not verified is labeled as such.
- [ ] Any rule you restate carries its scope.
- [ ] Every assertion checked against: what input would make this pass while
      proving nothing? If that input is reachable, add a positive control.
- [ ] Every regression test observed failing without its fix, with the restore
      verified byte-for-byte.
- [ ] Every prescribed acceptance command verified to actually detect the defect
      it gates — especially where `-race` or an iteration count is involved.

## A note on tooling

Wrappers can catch class 1 — the dangerous combinations above are a short,
enumerable list. Two design constraints learned here:

- **Annotate; don't auto-correct.** Silently fixing `gofmt -l .` to skip
  worktrees makes the wrapper the *new* invisible failure for anyone who
  legitimately wants to scan them. An injected notice — *"this used `-h`, so
  filenames are stripped before your filter"* — costs nothing and would have
  caught both instances. This matches the project's existing preference for
  additive, informative interventions over silent rewriting.
- Reserve correction for forms with **no** legitimate use.

Classes 2 through 5 are not tool problems and no wrapper will help. They need
the stamps, the scoping, and the stated precedence of code over documents.
