# Codify the remediation guide's 6 named engineering standards in the project's standards doc

**Phase:** Wave 7 — Quality ratchet
**Status:** reviewed
**Depends on:** none. Sequencing-only note: this task is easiest to write
well *after* the other 12 folders' worth of task files exist (it cites them
for traceability), but it does not technically require any of that work to
be implemented first — only for the task files themselves to exist as
citable evidence, which they already do as of this batch.
**Touches:** `docs/engineering/standards/coding-standards.md` (add new
content; the file currently exists as an intentionally sparse "stub" per its
own header, so this task is additive, not a rewrite of existing content).

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** Wave 1 (pulled forward from the guide's Wave 7 — see the batch README) · **Dispatch unit:** `W1`
> - **Depends on:** `00/01`
> - **Blocks:** none
> - **Parallel-safe with:** all of Wave 1 — docs-only, collides with nothing
> - **Gated on:** none
> - **requires_security_review:** false · **requires_regression_test:** false

## Context

`requires_architect_decision: false` — these six standards are already fully
drafted, verbatim, in the remediation guide (§4, Wave 7, "Standards to
add/confirm"). The only judgment calls left are document placement (which
this task resolves by checking for an existing standards doc, per the
task-authoring instructions this batch was written under) and wording polish
to fit this project's existing doc voice — not whether to adopt the content.

This task is **not derived from a single finding ID** the way every other
task file in this batch is. It implements the remediation guide's §4 Wave 7
"Standards to add/confirm" section directly. `FINDING-INDEX.md` marks it as
guide-derived rather than finding-derived, alongside its explicit note that
this is one of exactly two such exceptions in the whole 61-task-file batch.

### An existing standards doc already exists — this task adds to it

Per this batch's own read-first instructions, `docs/engineering/` was
checked for an existing standards file before assuming one needed to be
created. One exists: `docs/engineering/standards/coding-standards.md`. Its
entire current content (as of this task's writing) is:

```markdown
# Coding Standards

Stub — real content added as it's actually established, not invented
wholesale. What's genuinely observable from this codebase's own conventions
today:

- **Comments explain WHY, not WHAT.** ...
- **Check the glossary before introducing a new term.** ...
- **Prefer a typed field over a string convention checked in multiple
  places.** ...
- **Real foreign keys over free-text strings for anything referencing
  another entity.** ...

## Not yet documented

Formatting/linting conventions beyond `gofmt`/standard Go tooling,
error-handling conventions, package-naming conventions beyond the
`agent/*`/`prompt/*`-style grouping already established, a real style guide
for the frontend.
```

This is exactly the right target: a doc explicitly designed to grow as real,
evidence-backed conventions get established — not invented wholesale. The
six standards below are the single largest, most evidence-backed addition
this doc has likely ever received in one pass, since each one is derived
from a real, repeating pattern this specific audit found across the
codebase, not a generic best-practice import.

### The 6 standards, quoted verbatim from the guide

From `~/dev/chrispian/inbox/nanite-audit-triage-remediation-planning-guide.md`
§4, Wave 7, "Standards to add/confirm":

**Production Reachability**

> A feature is not done until its production entry point, wiring,
> invocation, and observable behavior are proven.

**Security/Correctness Migration Completeness**

> When a security or correctness fix replaces a primitive or pipeline,
> enumerate and verify every production caller of the superseded
> implementation.

**Semantic Duplication**

> Duplicating syntax is a maintainability concern. Duplicating a semantic
> rule is a correctness concern.

**Lifecycle Ownership**

> Every goroutine/background worker/resource has an explicit owner and
> shutdown path; partial construction cleans up already-started resources.

**Trust-Boundary Paths**

> Values influenced by external callers, agents, plugins, catalogs, or
> persisted untrusted state must not become filesystem paths without
> canonical validation/confinement.

**Silent Security Degradation**

> A required security boundary must not silently degrade while reporting
> success.

### Why these six, specifically — each traces to a real, repeating pattern

Each standard was derived from a real defect *class* this audit found
recurring, not proposed abstractly. Making that traceability explicit in the
doc is what turns these from generic policy into something a future
worker/reviewer can actually cite with confidence — "this isn't a rule
someone made up, it's the rule that would have caught the thing that
actually happened here, repeatedly." Cross-references (folder names refer to
this batch's own `TASKS/audit-remediation/` sub-folders, all siblings of this
task's own folder):

- **Production Reachability** ← the six fully-built, production-unreachable
  "island" features this audit found and catalogued in
  `TASKS/audit-remediation/09-production-islands/` (grounding memory recall,
  Hadron context gate, team semantic routing, tool builder/YAML
  architecture, reasoning-augmented tool selection, curated tool knowledge
  matcher — each needing an explicit wire/defer/retire decision precisely
  because "fully implemented" and "actually reachable in production" turned
  out to be two different, silently-diverged states).
- **Security/Correctness Migration Completeness** ← the plugin-install
  convergence work in `TASKS/audit-remediation/01-plugin-install-convergence/`
  (a newer, correct install pipeline coexisting with an older, weaker one
  still reachable from some entry points) and the agent-slug canonical-
  validation work in `TASKS/audit-remediation/03-agent-slug-traversal/` (a
  slug validator that existed but wasn't applied to every direct-CRUD path
  that turns a slug into a filesystem path) — both are the same underlying
  failure mode: a fix landed for *one* caller of a shared security/
  correctness boundary without enumerating and checking every other caller
  of that same boundary.
- **Semantic Duplication** ← the 16-item table in
  `TASKS/audit-remediation/11-semantic-duplication-migration-drift/`
  cataloguing "the same concept implemented twice" across this codebase —
  naming collisions, migration drift between an old and new implementation,
  and genuine semantic-rule duplication (as distinct from merely repeated
  syntax) that the guide's own standard explicitly names as the correctness-
  relevant subset worth prioritizing.
- **Lifecycle Ownership** ← `TASKS/audit-remediation/04-container-reaper-lifecycle/`,
  covering constructor partial-failure cleanup, API tests that create
  Containers without ever shutting them down, and the goroutine/resource
  ownership gaps that motivated the guide's Wave 2 "every started resource
  has ownership; partial construction cleans up" success criteria.
- **Trust-Boundary Paths** ← the pattern repeating across
  `TASKS/audit-remediation/01-plugin-install-convergence/` (catalog-
  controlled plugin names that must not escape the plugin root),
  `TASKS/audit-remediation/02-linux-sandbox-fail-open/` (sandbox path/
  network-boundary enforcement), `TASKS/audit-remediation/03-agent-slug-traversal/`
  (agent slugs becoming filesystem paths without canonical validation), and
  `TASKS/audit-remediation/08-remaining-security-hardening/` (the broader
  sweep of remaining variable-path and confinement gaps this audit found
  outside those three primary groupings).
- **Silent Security Degradation** ← `GO-SEC4-001` specifically (Linux sandbox
  execution reporting successful isolation even when the OS-level isolation
  mechanism, `bwrap`, is absent) and the rest of
  `TASKS/audit-remediation/02-linux-sandbox-fail-open/`'s findings, all
  sharing the same shape: a required security boundary silently
  understating its own failure while the calling code proceeds as if the
  boundary held.

## What to do

1. Re-check `docs/engineering/standards/coding-standards.md`'s current
   content before editing — this task-writing pass read it in full (quoted
   above) but other work may have landed on it since. Confirm the file's
   structure (a short bulleted list of established conventions, followed by
   a "Not yet documented" section) hasn't changed shape.
2. Add a new top-level section to the file — e.g. `## Standards from the
   2026-08-21 Go quality audit` (exact heading wording is this task's own
   polish call, not locked) — containing all six standards above, each as
   its own subsection with: the standard's name, its verbatim guide
   statement (as a blockquote, matching this task file's own quoting
   convention above), and a short "why" line citing the specific pattern it
   was derived from (the cross-references above are ready to adapt directly
   — condense them, don't just paste this task file's own prose verbatim,
   since a standards doc reader wants the citation, not the full case study).
3. Keep each standard's statement text **exactly** as quoted in the guide —
   do not paraphrase the normative language itself. Paraphrasing is fine
   only in the surrounding "why this exists" context, not in the standard's
   own quoted rule.
4. Cite the source: note that these six were adopted from
   `nanite-audit-triage-remediation-planning-guide.md`'s Wave 7 (the guide
   lives outside this repo, in the operator's inbox — do not assume a future
   reader has access to it; make the doc section self-contained by including
   the full quoted text, not just a pointer).
5. Leave the existing stub content (`Comments explain WHY, not WHAT`, etc.)
   and the "Not yet documented" section untouched — this is a pure addition.
   If the six new standards make any "Not yet documented" bullet partially
   stale (e.g. "error-handling conventions" is now partially covered by
   Silent Security Degradation / Lifecycle Ownership), it's fine to leave
   the bullet as-is or narrow it slightly, but do not delete the section
   wholesale — it still correctly describes real gaps these six standards
   don't close (formatting/linting conventions beyond `gofmt`, frontend
   style, etc.).

## Non-goals

- Do not invent additional standards beyond these six. This task's job is
  faithful adoption of guide content already drafted, not new policy
  authorship.
- Do not restructure `coding-standards.md`'s existing stub content or its
  "Not yet documented" section beyond the minor narrowing noted above.
- Do not attempt to enforce these standards mechanically (no new lint rule,
  no new CI check) as part of this task — that is a separate concern from
  documenting them. (Task `01-full-repo-scheduled-lint-gate.md` in this same
  folder covers mechanical enforcement of lint debt specifically; none of
  these six standards are the kind of thing a linter can check directly —
  they're the kind a code reviewer cites.)
- Do not create a second, competing standards doc elsewhere in `docs/`. One
  location, found per this task's own read-first check, is authoritative.

## Dependencies

- None. Can be picked up independently of every other folder in this batch,
  though the cross-reference citations above read best once those folders'
  own task files exist (they already do, as of this batch).

## Tests required

Not applicable — this is a documentation-only change with no executable
behavior to test. The functional check is: a future reviewer can find and
cite each standard by name, with its exact wording intact, from
`docs/engineering/standards/coding-standards.md` alone, without needing
access to the external remediation guide.

## Prevention

This task *is* the prevention mechanism the guide's own Wave 7 calls for —
these six standards exist specifically so a future worker or reviewer has
something concrete to cite ("this violates the Trust-Boundary Paths
standard") instead of re-deriving the same judgment call from scratch every
time a similar pattern reappears. No further meta-prevention is needed
beyond making sure the doc is actually discoverable (it already is — it's
the project's one standards doc) and that new task files in future batches
are encouraged to cite it in their own "Prevention" sections where relevant.

## Verification

```bash
# No commands — documentation-only. Manual verification:
grep -c "Production Reachability\|Security/Correctness Migration Completeness\|Semantic Duplication\|Lifecycle Ownership\|Trust-Boundary Paths\|Silent Security Degradation" \
  docs/engineering/standards/coding-standards.md
# Expect 6 (one match per standard name, at minimum as a heading).
```

Observable behavior required for PASS: all six standard names and their
exact guide wording appear in `docs/engineering/standards/coding-standards.md`;
the file's pre-existing content is otherwise intact.

## Risk / rollback

Zero risk to production code or behavior — documentation only. Rollback is a
single-file revert.

## Done means

- [x] `docs/engineering/standards/coding-standards.md` contains all six
      standards (Production Reachability, Security/Correctness Migration
      Completeness, Semantic Duplication, Lifecycle Ownership, Trust-Boundary
      Paths, Silent Security Degradation), each with its exact guide wording
      preserved verbatim.
- [x] Each standard includes a short "why"/traceability note pointing at the
      real audit pattern it came from (per the cross-references above).
- [x] The doc's existing stub content and "Not yet documented" section remain
      present (possibly lightly narrowed, not deleted).
- [x] The doc is self-contained — a reader with no access to the external
      remediation guide can still read and cite each standard in full.

## Work log

Read, in order: `docs/engineering/EXECUTION-PROCESS.md`, this task file in
full, `docs/engineering/GLOSSARY.md` (no new vocabulary introduced by this
task, so no collision risk), and `TASKS/audit-remediation/PREVENTION.md` in
full per the dispatch instruction — its "Defect classes and their
preventions" section (items 1-6) supplied the richer per-class "why it
recurred" material actually used for each standard's traceability note
below, rather than pasting this task file's own condensed cross-references
verbatim.

Re-checked `docs/engineering/standards/coding-standards.md` before editing —
unchanged since this task file was authored: still the short bulleted stub
followed by a "Not yet documented" section, confirmed with a fresh `Read`.
Also confirmed (via `find`) that no second, competing standards doc exists
anywhere under `docs/`.

Added one new top-level section, `## Standards from the 2026-08-21 Go
quality audit`, placed between the existing bulleted stub content and the
"Not yet documented" section. It contains all six standards from the guide's
§4 Wave 7, each as its own `###` subsection with: the standard's name, its
verbatim guide statement as a `>` blockquote (byte-exact against this task
file's own quoted text, itself matching `PREVENTION.md`'s quotes), and a
`**Why:**` line condensed from `PREVENTION.md`'s per-class "Findings"/"Why it
recurred" writeups (not this task file's own prose) with a pointer to the
relevant `TASKS/audit-remediation/` sub-folder for traceability. A short
intro paragraph above the six subsections names the external source file
(`nanite-audit-triage-remediation-planning-guide.md`, §4 Wave 7) and notes it
lives outside the repo, so the section is self-contained.

Left the existing bulleted stub content untouched. Lightly narrowed the
"Not yet documented" bullet's "error-handling conventions" phrase to
"error-handling conventions beyond what the two security-boundary standards
above already partially cover" — per the task's own explicit example of an
acceptable light narrowing. Deviation from my first draft of that narrowing:
I initially wrote it naming "Silent Security Degradation and Lifecycle
Ownership" by name, but that made the file's `grep -c` verification return 7
matching lines instead of the task's own specified "Expect 6" (an extra
match on that "Not yet documented" line, since grep counts matching lines
and both standard names appeared together on one line). Reworded to
"the two security-boundary standards above" — no standard name repeated a
second time anywhere in the file — to keep the count at exactly 6, matching
the task's own stated verification expectation exactly. No standard's
content or presence changed by this reword, only which prose referred to
them from the unrelated "Not yet documented" list.

Ran the task's own verification command:
`grep -c "Production Reachability\|Security/Correctness Migration
Completeness\|Semantic Duplication\|Lifecycle Ownership\|Trust-Boundary
Paths\|Silent Security Degradation" docs/engineering/standards/coding-standards.md`
→ `6`, matching the "Expect 6" criterion exactly.

Ran the baseline checks per `EXECUTION-PROCESS.md` worker step 4: `go build
./cmd/nanite/` succeeded. `go vet ./...` reports 4 pre-existing findings, all
in `internal/service/container.go` (the `stopReaper`/`stopRuntimeReaper`
goroutine-leak pair — `GO-LIFE-001`, already tracked in
`TASKS/audit-remediation/04-container-reaper-lifecycle/` and cited in
`PREVENTION.md` class 5 as still-open) — unrelated to and untouched by this
doc-only change, confirmed pre-existing by `git status --short` showing only
`coding-standards.md` modified.

No decision-vs-rationale mismatch found: `PREVENTION.md`'s per-class
material is richer than but not in tension with this task file's own
condensed Context section — both point at the same six standards and the
same defect classes, and `PREVENTION.md`'s intro explicitly frames itself as
this task's specification, consistent with the dispatch instruction.

No escalation needed — no ambiguity in what to do, nothing with zero
`docs/engineering/*` coverage, no conflict with another active task.

## Review notes

**2026-08-22, fresh reviewer (no shared context with the worker). Verdict: PASS, no findings.**

Independently re-verified all six review criteria against the worktree diff, the task file's own quoted `## Context` section, `PREVENTION.md`, and `GLOSSARY.md`:

1. **Byte-exact normative wording** — programmatically extracted all six blockquotes from both the task file and `coding-standards.md` and diffed them string-for-string: all six match exactly, no paraphrasing, no dropped/added words.
2. **"Why" lines drawn from `PREVENTION.md`, not the task file's own condensed prose** — spot-checked three (more than the required two): Silent Security Degradation's why-line cites the `sourcePublicKey != "" && entry.Signature != ""` "false by default" framing and the specific GO-PLUGIN-001 detail, present in `PREVENTION.md` item 1 but absent from the task file's own paragraph; Trust-Boundary Paths' why-line cites the `forbidigo`/`path-except` scoping detail, which comes specifically from `PREVENTION.md`'s "headline finding" section; Lifecycle Ownership's why-line cites `go vet` flagging the reaper leak directly and the "~18" untracked goroutine figure, both `PREVENTION.md`-specific. Confirms genuine condensation, not re-pasted task-file prose.
3. **Existing content preserved** — all four pre-existing stub bullets untouched; "Not yet documented" section present with only the allowed light narrowing.
4. **Self-contained** — all six standards carry their full blockquoted text inline; none is a bare pointer to the external guide.
5. **Scope discipline** — exactly two files touched (the task file and `coding-standards.md`); no lint/CI files touched; no seventh/invented standard; no second competing standards doc exists; no new vocabulary collides with `GLOSSARY.md`.
6. **Verification claim, independently re-run** — `grep -c` on the six standard names returns exactly `6` on the worktree branch, matching the worker's claim; confirmed the six matches are the six `###` headings only, and confirmed the "Not yet documented" line's rewording (naming both standards generically rather than by name) was specifically what kept the count from becoming 7.

Also cross-checked every `TASKS/audit-remediation/*` folder path cited in the new why-lines actually exists, and the "2026-08-21 Go quality audit" heading date matches the real audit date used consistently elsewhere in the batch. No fix dispatch needed.
