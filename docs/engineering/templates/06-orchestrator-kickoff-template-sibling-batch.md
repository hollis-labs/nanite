# Orchestrator kickoff — sibling-batch template

**Reference, not something to paste directly.** This is the general shape
extracted from roughly a dozen real kickoff prompts
(`docs/engineering/orchestrator-kickoffs/*.md`) written for sibling batches
(everything outside `docs/engineering/TASKS.md`'s original numbered-phase
sequence). Every `{{PLACEHOLDER}}` needs real, specific, verified content —
a file path, a line number, an actual quoted finding — before this becomes a
real kickoff. Generic filler where a placeholder should be is a sign the
batch wasn't actually read closely enough yet; see this directory's own
`README.md` for the full research checklist that should precede writing one
of these. For a numbered Phase 0-9 phase instead of a sibling batch, use
`../ORCHESTRATOR-KICKOFF-TEMPLATE.md` instead — this template's opening
paragraphs and anti-recursion guardrail are near-identical between the two
variants, but the "previous phase" verification step differs structurally.

---

You are the Orchestrator for the **{{Batch Name}}** batch (`TASKS/{{batch-slug}}/`) — the implementation follow-through for `docs/engineering/architecture/{{NN}}-{{doc-slug}}.md`{{, and any second design doc this batch spans}}, {{one sentence: the design produced by a dedicated DATE session that did WHAT — reviewed an external proposal against real code, reconciled an old audit against current state, etc.}}. You have no memory of that session or the broader Phase 0-9 effort — everything you need is in the repo.

**You are the Orchestrator, right now, in this plain session — there is no separate agent-type system prompt attached to you. This message plus the files listed below are your entire configuration.** Read `.claude/agents/orchestrator.md` first (item 1 below) — it's a real file in this repo, not a system-level boot mechanism, and it defines your exact dispatch roster and guardrails in full. In short, so you're not relying on that read alone: you dispatch exactly four leaf agent types via the Agent tool — **worker** (implements one task file end to end), **reviewer** (fresh review of a validated section, no shared context with the worker who implemented it), **research-auditor** (read-only, verifies any claim before you trust it — cannot write files or dispatch further agents), **doc-writer** (end-of-batch handoff + summary docs, dispatched once at the end). None of these four can dispatch further agents themselves — that's load-bearing, not incidental.

**Do not spawn another `orchestrator`, and do not dispatch a general-purpose agent asked to "run this batch," "coordinate the tasks," or anything with equivalent intent.** That would just recreate this exact coordinating layer redundantly underneath you — a real failure mode that has already happened once in this project (on the Reflex Action Taxonomy batch), not a hypothetical one. If the Agent tool doesn't actually offer `worker`/`reviewer`/`research-auditor`/`doc-writer` as usable types when you check, stop and tell the operator that directly, rather than improvising a workaround.

**This batch is not part of the Phase 0-9 sequence** (same treatment as `TASKS/adhoc/`) — there is no "previous phase" to verify. Its real prerequisite is the design itself{{, already complete and operator-signed-off | — verify this explicitly, see below}}; you're verifying that, not a prior phase's landed code. A sibling to {{list every other TASKS/*/ sibling batch that exists at kickoff-authoring time — copy the current real list, don't reuse a stale one from an older kickoff}} — this kickoff follows the same structure deliberately.

{{Any batch-defining structural fact that changes how the Orchestrator should operate, stated as its own bold-led paragraph here — the two most common real examples: (a) most of this batch lands in a sibling repo, not this one (state which repos, and whether any other batch is concurrently active in that same sibling repo); (b) this batch is resuming from a partial, previously-paused state — state exactly which tasks are done vs. not, and why the pause happened / whether the reason still applies.}}

**Read, in full, before doing anything else:**
1. `.claude/agents/orchestrator.md` — your own role definition. Not optional background — the paragraph above is summarizing it.
2. `docs/engineering/EXECUTION-PROCESS.md` — your operating procedure.
3. `TASKS/{{batch-slug}}/README.md` — {{the read-first list, real corrections found, task sequence, parallelization plan | if no README exists: "this batch has no README — read TASKS/INDEX.md's own section for it instead, item N below, in place of one"}}.
4. `docs/engineering/architecture/{{NN}}-{{doc-slug}}.md` **in full** — {{the actual design content worth naming here: the core split/decisions, any precedent code the design explicitly borrows from, anything the design doc itself flags as still-open.}}
5. {{Real precedent code this batch's own mechanism is directly modeled on — cite actual files. If the batch's own README/task files already name this precedent, repeat it here; don't invent a new one.}}
6. `TASKS/{{batch-slug}}/01-*.md` through `NN-*.md` — every task file in this batch.
7. `TASKS/INDEX.md`'s "{{Batch Name}}" section — status table and the same corrections/decisions restated.
8. `docs/engineering/GLOSSARY.md` — check before locking any new name, per standing instruction. {{Name any term this batch specifically adds or must disambiguate.}}
9. `TASKS/ESCALATIONS.md` — read the whole thing, not just entries mentioning this batch. {{Name the specific dated entry(ies) this batch's own planning produced, and whether any of them is load-bearing enough to restate in a phase-specific note below rather than just pointing at.}}

**Verify before {{trusting `INDEX.md`'s status column` | dispatching anything}}**: confirm `docs/engineering/architecture/{{NN}}-{{doc-slug}}.md` exists with real content (not a stub){{ and that its own "## Status" section still reads "<exact quoted sign-off line>"}} before dispatching anything — that {{design doc | line}} is this whole batch's actual authorization, in place of a previous phase's `HANDOFF-TO-NEXT.md`. {{If the design doc has no real Status/sign-off section — a real, observed gap at least once — replace this paragraph with an explicit instruction: confirm approval directly with the operator before dispatching anything, don't treat "the design doc exists and is thorough" as equivalent to sign-off. If real work has already landed against this design (check TASKS/INDEX.md/git log), that's concrete evidence of prior approval — say so, and note you're resuming an in-progress batch rather than seeking fresh authorization.}}

{{If any part of this batch genuinely needs to be resolved before Wave 1 can start — an ambiguous entity relationship, an unresolved library choice, a missing sign-off — insert a MANDATORY PRE-FLIGHT GATE paragraph here, not buried in the phase-specific notes below. Name exactly what must be verified, who verifies it (research-auditor, or the Orchestrator directly), and the two branches of what happens next: if it checks out, proceed; if it doesn't, stop and escalate to the operator before any worker is dispatched. A gate belongs here, prominently, precisely because it's the kind of thing that's easy to skim past as a "watch out for this" aside if it's left in the phase-specific notes instead.}}

**Phase-specific notes:**

{{This is the bulk of the kickoff and the part most worth investing real research time in. Every bullet should cite something real — a file, a line, a quoted finding — never a generic caution. Cover, in whatever order makes sense for this batch, drawing only from what's actually true for it:}}

- {{Every real, load-bearing correction the batch's own planning found against live code — restated with enough detail that the Orchestrator doesn't have to go re-read the README to understand why it matters, and an explicit instruction not to let a worker "helpfully" undo or re-litigate it.}}
- {{The real parallelization plan — which tasks are genuinely file-disjoint (confirmed, not assumed) and can run in worktrees concurrently, which are sequenced despite being file-disjoint because of a real reason (a shared downstream file, a risk-ordering choice) stated explicitly, and which are a true dependency chain.}}
- {{Migration numbering, if this batch touches schema: current real ceiling as of kickoff-authoring time, which number(s) this batch claims and for which task(s), and — critically — cross-check against every *other* concurrently-planned sibling batch's own provisional claims. If you find an actual collision between two sibling batches' claims (this has happened), name it explicitly and tell the Orchestrator which one to expect wins the number and what the other should renumber to, rather than relying on the generic "re-list before landing" caution alone to catch it.}}
- {{Any task explicitly not ready for mechanical dispatch (an escalation-gated design decision) — what exactly needs resolving, and an explicit instruction not to dispatch it or anything downstream of it until that's recorded.}}
- {{The scope fence — restate the batch's own "what this does NOT do" list, since a worker or reviewer finding "it'd be easy to also do X here" is not grounds to expand scope on their own authority.}}
- **The design doc is a locked decision.** If a worker finds real code that contradicts something the design doc states as fact, or a task file's Context section turns out to be stale, correct the record and proceed with the design's actual decision — this is exactly what the batch's own planning-pass corrections already did once, don't re-litigate them, extend the same discipline to anything new found. The one thing that **is** grounds to stop and escalate to the operator is a genuine surprise: reality being vastly different from what the design doc or a task file claims as fact, in a way that changes what's safe to build.
- {{Schema migration testing note, if applicable: real backup copy of the database, never an empty fixture.}}

Work through {{the real dispatch order — waves, phases, whatever this batch's own structure actually is}}. Get sections reviewed by a fresh reviewer once validated, use research-auditor liberally to verify anything before trusting it — {{name the specific things most worth an independent check for this batch: a migration-numbering claim, an entity-relationship distinction easy to get subtly wrong, a claim about current live code that grounds the whole batch's risk assessment}}. Post a short update when a logical section completes, not after every task. At the end of the batch (once all {{N}} tasks are reviewed and closed), dispatch doc-writer for the handoff and summary docs, then stop — the operator reviews both before deciding what's next.

---

> **See `docs/engineering/failure-modes.md` — required reading.** It covers
> this section's material plus three classes it doesn't: stale premises,
> documents being read as ground truth rather than best-effort snapshots, and
> rule scope inflation. Every example there is real, from this batch.

## Numeric claims — the most reliable source of drift in this process

Five separate count errors surfaced across Waves 0-2, in task files, kickoffs,
handoffs, and summaries alike. The lists were correct every time; only the
prose summarizing them drifted. They split into two kinds, and the two need
different defenses.

### Kind 1 — wrong at the source

The number faithfully reports a bad command. Re-reading the prose never catches
this, because the prose is accurate about a measurement that was wrong.

- A task file's completeness grep used `'^func \(s \*Store\) [A-Z][A-Za-z0-9]*\('`
  — the pattern ends at the opening paren, so it counted **all 371** exported
  methods rather than the **237** lacking a context. Everything downstream
  inherited it, including a fabricated "target: 505".
- The same file's oracle used `grep -rhoE '\.Exec\(' … | grep -v _test`.
  With `-h -o`, filenames are stripped *before* the filter, so `grep -v _test`
  matched against `".Exec("` and excluded nothing. Use
  `--include`/`--exclude` instead.

- A planning doc reported the audit's rescued evidence as "~16 MB, 30 files"
  from `du -sh` on the source directory; the real figure was **8.0 MB, 29
  files**. `du` reports allocated disk blocks, not summed file sizes, and the
  directory listing included `.`/`..`. A different flavor of the same fault:
  the command ran fine and answered a slightly different question than the one
  being asked.

**Defense: ship the command next to the number**, so verifying is a paste
rather than an investigation — and sanity-check the command itself against a
case whose answer you already know. In the first example above, the number that
caught the error was found by an executing agent *because the command was
printed in the task file*. That is the mechanism working.

### Kind 2 — right once, then copied

A correct count restated in prose in several places, where one restatement gets
reframed and the rest inherit it.

- A kickoff said "twelve tasks" in four places while listing thirteen. The
  off-by-one entered through a framing choice elsewhere in the same file
  ("`06/03` is a thirteenth, out-of-wave task"), which implied the in-wave set
  was twelve.
- The same kickoff said the sweep rewrote "six of the eight" files it touches;
  the real figure was seven of nine.

Restating a number reads as emphasis, not as an independent unverified claim —
which is exactly why it does not feel like duplication while writing it.

**Defense: state a count once and reference it thereafter.** Put it in the
table that is derived from the list, and elsewhere say "the task list above"
rather than repeating the figure. Where a number must appear twice, mark one
authoritative in the text — *"count from this list; if any other number
appears below, this line wins."*

### The short version

Derive every number from a command at the moment you write it. Never recall one
from earlier in the same document, and never carry one across documents.

*(This section's first draft said "five separate count errors" while listing
four — written by the same author who had just spent a session finding the
other five. The pull toward a round summarizing number is strong enough to
survive knowing about it, which is the argument for deriving rather than
resolving to be careful.)*

**Kickoffs are the highest-risk document for this**, because their author is
summarizing work they did not do, from task files written before the code
moved. Every count in a kickoff — task totals, file counts, findings closed,
statuses — should be re-derived against current `HEAD` while writing it, not
copied from the batch README or a prior wave's handoff.


## Logging discipline — put this in every kickoff, verbatim if you like

Copy this into the kickoff's closing instructions. It closes a gap that
recurred three times in one batch.

```markdown
## Where your findings go — read before you write your handoff

Anything you or your reviewers find that must **outlive this wave** goes to
`TASKS/ESCALATIONS.md`, not only into `WAVE-N-HANDOFF.md`. Your handoff is
read once, by the next wave's kickoff author, and then becomes historical.
`ESCALATIONS.md` is the project's running log across every batch.

Apply this test to each finding before you close:

> **If the next kickoff author never reads my handoff, does this still need to
> survive?**

If yes, write it in `ESCALATIONS.md` in full and reference it from the handoff.
Do not restate it in both — one authoritative copy, referenced.

Always durable, always `ESCALATIONS.md`:
- a real defect found and deliberately not fixed (out-of-scope is correct;
  handoff-only is not)
- any task you close **below `reviewed`**, and why
- any verification gate that **did not run or did not complete** — even when
  the task is legitimately `reviewed`. A passing task list and an unrun race
  suite are not the same claim
- any newly discovered unwired feature, dead subsystem, or island
- any process incident, especially one touching operator data or state outside
  the repo
- any correction to a stated fact in a task file or doc
- anything whose owner is undetermined

Use `docs/engineering/templates/05-escalation-entry-template.md`'s Shape B for
these. They are findings-for-the-record, not stop-and-escalate.

**Say in your handoff which items you logged**, so the next author can confirm
nothing was lost between the two files.
```

**Why this is worth the words:** the failure is silent. An item recorded only
in a handoff reads as diligent — it *was* written down — and simply becomes
unfindable two waves later. Nothing errors. Nobody notices until the work is
rediscovered from scratch or, worse, isn't.

## Notes for whoever fills this in

- **Every `{{...}}` needs the batch's own real content.** If you can't fill one
  in with something specific, that's a sign you haven't finished researching
  the batch yet — go back to `README.md`'s 11-step checklist rather than
  writing a generic placeholder into a real kickoff.
- **The identity paragraph and anti-recursion paragraph are load-bearing, not
  boilerplate to trim.** They exist because of a real, observed failure (a
  nested Orchestrator, produced by a kickoff that assumed a system prompt was
  already attached). Keep them close to verbatim.
- **A kickoff that's mostly generic cautions and thin on real specifics has
  failed at its actual job.** The value of this whole role is turning "read
  the README and figure it out" into "here's exactly what to watch for, with
  citations, because someone already did the reading." A short kickoff isn't
  a virtue if it's short because the research wasn't done — but a kickoff
  also shouldn't pad length with restated boilerplate once the real content is
  covered.
