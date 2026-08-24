# Tracking integrity: designated sources and the checks that catch drift

**Companion to `failure-modes.md`.** That doc covers wrong *measurements*; this
one covers the same data being **tracked in more than one place with no
designated authority**, so drift is not merely present but undetectable — you
cannot tell which copy is wrong.

This is the data-model form of "derive, don't store." **Duplicated tracking
data is stored derivable data.**

Every assertion below caught real drift in `TASKS/audit-remediation/`
(Waves 0–4). None is theoretical. They are ordered by how often they fired.

---

## The duplication, as it actually exists

| Data | Copies | Drift observed |
|---|---|---|
| **Task status** | task file `**Status:**`, `findings.json.task_status`, `TASKS/INDEX.md` table, batch `README.md` table | **All four.** A task read `complete` (not a legal value); a whole wave sat at `implemented` after real reviews had passed; a landed task showed `not-started` in two trackers |
| **Decision status** | AD queue table, AD body sections, task-file banners, `findings.json.disposition` | Two findings kept `needs-architect-decision` after their decision landed — the note was appended, the field never flipped |
| **Finding → task** | `findings.json.task_file`, `FINDING-INDEX.md` | A finding pointed at a task file that explicitly disclaimed owning it, and the real task was never written |
| **Counts** | README dispatch table, README wave sections, INDEX, kickoff prose | 63 vs 65 vs 67; "twelve tasks" while listing thirteen; "six of the eight files" for seven of nine |
| **Wave membership** | dispatch table, task-sequence section, INDEX section, kickoff | A wave was inserted before its predecessor and omitted from the dispatch table entirely |

## Designate one source; derive the rest

The fix is not "stop duplicating" — some duplication is genuinely useful for
readers. The fix is that **exactly one copy is authoritative and the others are
checked against it.**

| Data | Source of truth | Why |
|---|---|---|
| Task status | the task file's `**Status:**` line | It is where the worker actually writes, at the moment the state changes |
| Decision status | `ARCHITECT-DECISIONS.md`'s queue table | One row per decision; the body sections are prose elaboration |
| Finding → task | `findings.json.task_file` | Machine-readable, and the only copy a tool can consume |
| Counts | **nothing** — always derived | See `failure-modes.md` §2 |
| Wave membership | the batch README's dispatch-unit table | The kickoff author's entry point |

Write the source down in the batch README. Ambiguity about which copy wins is
what converts a small inconsistency into an unresolvable one.

---

## The checks

### 1. Status agreement
Every task file's `**Status:**` equals `task_status` on every finding whose
`task_file` points at it.
> *Caught:* a wave's six tasks at `implemented` with PASS verdicts already in
> their Review notes; a landed sweep still showing `not-started` in two
> trackers.

### 2. Enum legality
Every `task_status` ∈ `allowed_task_statuses`; every `disposition` ∈
`allowed_dispositions`.
> *Caught:* a task file reading `complete`, which is not a legal value. Trivial
> to check, and nothing else would have found it.

### 3. Flagged findings have a decision, or don't need one
A finding with `requires_architect_decision: true` must **either** appear in
`ARCHITECT-DECISIONS.md` **or** have `disposition != needs-architect-decision`.
> *Caught:* four separate missing decisions (AD-25 through AD-28), one per wave,
> each found by hand at kickoff time. Running it across *all* remaining waves
> rather than just the next one found a fifth gap — seven flagged findings in the
> cleanup wave — **before** its kickoff, the first time that happened.

### 3b. …but the catalog's flag is not the only source. Scan task text too.
**This is a blind spot in check 3, found the hard way.** Check 3 trusts
`findings.json`'s `requires_architect_decision` as the authority on whether a
decision is needed. **Task files can flag decisions the catalog does not.**

In Wave 8, six findings carried task text explicitly saying "decision needed, do
not resolve unilaterally" — and **four of the six had
`requires_architect_decision: false`**, so check 3 reported them clean. A
kickoff author reading the task bodies found all six; the automated check found
two. The planner had already declared the wave "fully ungated" on the strength
of check 3 alone.

So: grep task bodies for decision-needed language and cross-reference against
the queue. **The union of the two sources is the real set**, and a disagreement
between them is itself a finding — it means one of them is wrong.

### 4. Decided decisions don't leave stale dispositions
A decision marked decided ⇒ no finding it covers is still at
`needs-architect-decision`.
> *Caught:* two findings whose notes said DECIDED while the field never moved.

### 5. Referenced paths resolve
Every `task_file` value points at a file that exists.

### 6. Task files map to findings — with a first-class exception list
Every task file maps to ≥1 finding, **or** is on an explicit exception list.
> **Get this one right or it becomes noise.** Legitimate non-finding task files
> exist and will keep appearing: a fix task for a regression a sweep exposed; a
> guide-derived standards task; a classification-table placeholder whose real
> work lives elsewhere; an evidence document that shares a numeric prefix with
> a real task. Four in one batch. The exception list must be **data with a
> reason attached**, not something a future tidy-up deletes.

### 7. Counts match reality
Every count in a wave table equals files on disk.
> *Caught:* three separate count errors. Note the checker must exclude the
> class-6 exceptions or it will disagree with correct tables.

### 8. Citation staleness — the one discipline can't catch
Any task file or doc citing code carries a `verified-at: <commit>` stamp. Warn
when the cited files have changed since.
> This is the `failure-modes.md` §3 class. A reader cannot currently tell a
> citation written an hour ago from one written forty commits ago — both look
> equally authoritative. *Would have caught:* a fix snippet that no longer
> compiled; a finding half-resolved by a later wave; a scope warning claiming
> tree-wide reach for what was 11 references.
>
> Warn, don't fail. A stale citation is usually still directionally right; the
> point is to make staleness visible so the reader re-verifies instead of
> trusting.

---

## Implementation notes

**Warn loudly; don't auto-correct.** Same rule as the tool wrappers in
`failure-modes.md`. A checker that silently rewrites `task_status` to match a
task file will eventually propagate a wrong value into the machine-readable
catalog with no trace. Report the disagreement and name both sides; let a human
decide which is wrong.

**Report the pair, not the verdict.** `06/04 says "complete"; GO-X-001 says
"reviewed"` is actionable. `06/04 status invalid` is not.

**Run it at handoffs, not on every commit.** These drift at role boundaries —
worker → reviewer, wave → kickoff author — not continuously. Wave close and
kickoff authoring are the two moments that matter.

**Checks 1–7 are cheap and exact**; a few dozen lines over one JSON file, a
directory listing, and a markdown table. Check 8 needs git history and is worth
building second.

## Why this is worth tooling rather than discipline

The batch that produced these examples ran a verification pass at every handoff
and **every single pass found something.** Not because anyone was careless —
the decisions held up throughout. What kept being wrong was bookkeeping that a
person has to remember to update in four places, at the exact moment they are
finishing something else and want to be done.

That is not a discipline problem. It is a job for a checker.
