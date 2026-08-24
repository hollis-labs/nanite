# End of Session (:end-of-session)

**Status:** beta (user-local, not yet canonical)

Run the deterministic end-of-session handoff checklist: reconcile the working tree, surface anything the user didn't author, confirm boot-prompt and agent context freshness, and produce a brief wrap-up report. Invoked automatically when the user signals end-of-session or explicitly via `/end-of-session`.

## When to use

- Explicitly via `/end-of-session` or when the user says "run end of session"
- NEVER invoke mid-task to "check where we are" — this skill is for wrap-up, not status. Use `/qstatus` for mid-task snapshots.

## Background (why this exists)

A 90-day chat-history analysis found that 66% of exited sessions had no boot-prompt update, 37% had neither a commit nor a boot-prompt update at end, and only 17% wrapped cleanly with both. The same analysis found repeated "discover-and-defer" moments where the agent silently classified something as "separate task" and moved on instead of surfacing it to the user. This skill is the deterministic pause that fixes both failures. It runs a checklist and uses a fixed three-option template to surface anything it finds — no agent opinions, no baked-in dispositions.

## Invoker precedence

If the invoker's task prompt explicitly contradicts this skill's defaults (e.g., "skip the commit step, I'll do it myself" or "just update the boot-prompt, nothing else"), the invoker's instructions win. This skill's defaults apply when the invoker is silent.

## The three-option surfacing template

Whenever this skill finds something it needs the user to decide on, it uses exactly this template. No variations, no embellishments, no "I think you should...".

> Hey, noticed **{thing}** — {1–3 lines of context, facts only}. Want me to:
> - **A) Go into detail** (I'll dig in now and we'll decide together)
> - **B) Capture for later review** (saves to the NANITE inbox via `/nanite`)
> - **C) Add to this session's tasks for follow-up** (stays in TodoWrite or the plan doc)

**Rules:**
- State the discovery as a fact. 1–3 lines of context. No opinion, no preferred disposition, no recommended pick.
- Exactly three options, in this order, labeled A / B / C.
- Never substitute a binary yes/no ("want me to fix it?") — that's Pattern B2, the anti-pattern this skill trains against.
- Never bury the surface inside a status block — stop, pose the menu, wait.
- If more than one thing needs surfacing, pose them one at a time unless they're tightly related. A menu per discovery is clearer than a bulk list.

## Workspace mode (strict vs multi-session)

Before classifying files, detect the workspace's mode — the behavior around USER/AMBIGUOUS files differs sharply between the two.

**Strict mode** — the workspace is a single-owner code repository. Uncommitted files that the session didn't author are anomalies worth surfacing.
- Detection: at least one of `go.mod`, `package.json`, `Cargo.toml`, `pom.xml`, `pyproject.toml`, `composer.json`, `Gemfile`, `mix.exs`, `build.gradle`, `CMakeLists.txt`, `Makefile` exists at the workspace root.

**Multi-session mode** — the workspace is a long-lived collaborative or knowledge-base dir where multiple agents (parallel, interleaved, or asynchronous) accumulate state. Uncommitted non-SESSION files are the normal steady state, not signal.
- Detection (any of):
  - No code-project marker at the root (see strict-mode list above).
  - `.nanite/multi-session` marker file exists.
  - `.nanite/config.yaml` has a top-level `workspace_type: multi-session` field.
  - The root contains any of: `knowledge/`, `agents/`, `execution/`, `planning/`, `boot/<project>/` pattern, `.nanite/agents/` with 2+ files — these are agent-ops workspace conventions.
- The user's explicit preference for such workspaces (captured as memory `feedback_kb_repo_commit_freely.md` in at least one profile): commit accumulations wholesale when the user asks; otherwise leave untouched. Never surface pre-existing USER accumulation file-by-file — it's baseline noise.

When uncertain between the two modes (e.g., a code repo that happens to also have `.nanite/agents/`), default to **strict**. Users are better served by one-too-many surfaces in a code repo than by silently skipping a real discovery.

## Agent self vs user attribution

When reconciling the working tree, each changed/untracked file falls into one of three buckets:

1. **Session authored** — the current session touched this file via `Edit`, `Write`, or a `Bash` command that clearly modifies it (e.g., `git mv`, `mv`, code generators run by the agent). Use the session's tool-call history to confirm.
   - **Action (both modes):** commit it without asking. The user implicitly authorized the change by giving the task. Use a conservative commit message describing what the session did (see Commit message rules below).
2. **User authored** — the file is in the working tree but the session never touched it. Common causes: the user edited it in another editor, a parallel agent modified it, a pre-session uncommitted change was already there, or tooling (linter auto-fix, generated file) touched it.
   - **Action (strict mode):** do NOT auto-commit. Surface via the three-option template. Example: *"Hey, noticed `internal/foo/bar.go` has uncommitted changes I didn't make. Want me to: A) show you the diff, B) capture it to NANITE, C) add a follow-up task to decide later?"*
   - **Action (multi-session mode):** do NOT auto-commit AND do NOT surface file-by-file. Mention the count + a one-line category summary in the wrap-up report ("12 pre-existing files from parallel-agent activity, not touched this session") so the user has situational awareness, but treat the accumulation as expected baseline. If the user explicitly asks for a wholesale housekeeping commit, route that as a separate explicit action.
3. **Ambiguous** — the session ran a broad command (`go fmt ./...`, `pnpm install`, a codegen script, a rebase) that could have touched files the agent didn't explicitly `Edit`. Cannot definitively attribute.
   - **Action:** treat as user-authored and follow the mode-appropriate rule above. In multi-session mode, roll into the baseline summary; in strict mode, surface via the three-option template with a note on why attribution is ambiguous.

When uncertain, err toward the user. Auto-committing a file the user was mid-editing is worse than one extra question.

## The deterministic checklist

Run these in order. Do not skip steps. If a step cannot be completed (e.g., no git repo), note it and continue to the next.

### 1. Snapshot the working tree

Run these commands (inline, not via sub-agent — the main context needs the results):

```bash
git status --porcelain -u          # -u is mandatory — catches untracked files
git diff --name-only HEAD          # tracked files modified since last commit
git log --oneline -1               # current HEAD for the report
git branch --show-current          # current branch
```

The `-u` flag on `git status` is non-negotiable. Untracked files (new files not yet `git add`ed) are invisible without it and are exactly where parallel-agent work and user-generated files sit.

### 2. Classify changes by authorship

First, determine workspace mode per the "Workspace mode" section above — strict or multi-session. This controls how the USER/AMBIGUOUS lists are handled downstream (steps 8 and 9).

For each file in `git status --porcelain -u`, classify as session-authored, user-authored, or ambiguous using the rules in the "Agent self vs user attribution" section above. Build three lists:

- **SESSION** — files the current session touched via Edit/Write/Bash
- **USER** — files changed but not touched by this session
- **AMBIGUOUS** — files potentially touched by broad session commands

Tool-call history is the source of truth for SESSION attribution. If in doubt, mark AMBIGUOUS.

In multi-session mode, expect the USER list to be substantial (often a dozen or more files from parallel-agent activity). That's the normal steady state, not a red flag. The classification still happens so the SESSION list is accurate — only the downstream handling differs.

### 3. Check boot-prompt freshness

Locate the boot-prompt:

- If `.nanite/boot-prompt.md` exists, that's the target.
- Else if `./boot-prompt.md` exists at the workspace root, that's the target.
- Else there is no boot-prompt yet.

Compare the boot-prompt's last-modified time against the session start time. If substantive work happened this session (SESSION list is non-empty, or a plan was advanced, or a PR was opened, or any commit was made) AND the boot-prompt was not written during this session, it's stale. Fail closed: surface via the three-option template.

> Hey, noticed the boot-prompt at `.nanite/boot-prompt.md` hasn't been updated this session, but we committed {N} files / advanced {plan-name} / opened PR #{num}. Want me to: A) draft an updated boot-prompt now (invokes `/boot-prompt`), B) capture a wrap-up note to NANITE for the next session, C) add "update boot-prompt" as a follow-up task?

Do not auto-invoke `/boot-prompt` — that skill is manual by design. Only invoke it if the user picks option A.

If no substantive work happened (empty SESSION list, no commits, no plan advances), skip the boot-prompt check. Trivial sessions don't need handoffs.

### 4. Reconcile plans — Clockwork (authoritative) + local files (legacy)

**Plans now live in Clockwork.** Local plan files (`plan.md`, `docs/plans/*.md`, etc.) are legacy and should be migrated. Check Clockwork first; fall back to local files for any that haven't been migrated yet.

**4a. Clockwork plan reconciliation (authoritative).**

Search Clockwork for tasks/sprints worked on this session. Cross-reference against commit messages, tool-call history, and task IDs mentioned in the session.

For any Clockwork task the session advanced:
- If status is still `todo` or `doing` after completion → surface via the three-option template (same shape as Step 5 below; defer to Step 5b for actual transitions — avoid double-surfacing the same task).
- If status is correct but no comment records the work → note in Step 11b (the `#end-session` comment will cover it).

**4b. Local plan file reconciliation (legacy).**

If plan files exist at any of:
- `docs/superpowers/plans/*.md`
- `docs/plans/*.md`
- `PLAN.md` at workspace root
- `.nanite/plans/*.md`

AND the session referenced or advanced tasks in those files, diff the checkbox state against the session's task history.

- If checkboxes are stale → surface via the three-option template.
- If the project uses Clockwork, also surface: *"Hey, this plan file could be migrated to Clockwork — want me to: A) migrate tasks to Clockwork now, B) leave the file as-is, C) add a follow-up task?"*

**Convention for new work:** agents MUST create plans in Clockwork, not as plan files. Use `mcp__clockwork__clockwork_task_create` or the sprint tools. Local plan files are read-only legacy — do not create new ones.

### 5. Reconcile against task tracker — Clockwork

**Clockwork is the task system of record.** Use `mcp__clockwork__*` tools.

**During the session (not just at close):** agents should add progress comments to Clockwork tasks as milestones complete. This makes step 5 cheaper — the task already has context.

> **Legacy fallback:** a small number of pre-migration projects still hold tickets in the legacy Engine tracker. If the session cited a `TASK-20YYMMDD-NNN`-style ID and Clockwork has no match, swap `mcp__clockwork__clockwork_*` for `mcp__engine__engine_*` (read-only — never auto-transition). Do not reach for Engine by default; Clockwork first, every time.

---

**5a. Reverse lookup — session-cited tickets.**

Grep tracking files and commit messages for Clockwork IDs:

```bash
grep -rnE "CW-20[0-9]{6}-[0-9]{4}" <tracking files this session touched>
```

For each `CW-` ID found:
- Fetch via `mcp__clockwork__clockwork_task_get id=<id>`.
- If status is `todo` or `doing` and session work matches → surface via the three-option template (see below).

**5b. Forward lookup — in-progress tasks possibly advanced.**

```
mcp__clockwork__clockwork_task_list status=doing
```
(no project_id filter available yet — scan returned tasks for title/description matches against session work)

For each task returned that plausibly matches session work:

> Hey, noticed **CW-XXXX** (`<title>`) is still `doing`, but this session committed {N} files / advanced {plan-name} / closed related work. Want me to: A) transition it now (I'll ask which status), B) capture a reconciliation note to NANITE, C) add a follow-up task?

On option A:
- `mcp__clockwork__clockwork_task_transition id=<id> status=<chosen>`

After transition, if session work warrants a summary comment, offer (not assume) to add one:
- `mcp__clockwork__clockwork_comment_add task_id=<id> body=<summary>`

**5c. Sprint / Epic check.** If any task transitioned to `done`, check whether sibling tasks are also done:
- `mcp__clockwork__clockwork_task_list parent_id=<sprint_id>`

If all siblings are done or paused, offer to transition the sprint/epic. Never silently decide.

**Backlog verification:** if the session created tasks or logged to Clockwork mid-flight, verify those calls completed in tool-call history. Silent failures → surface via three-option template.

**Tracker unreachable:** note in wrap-up report under `Clockwork: unreachable — reconciliation skipped`. Continue checklist. Do not retry aggressively.

### 6. Check stale agent context files

Look in `.nanite/agents/*.md`. For each agent context file:

- Read the `## Scope` or equivalent section (the area the doc covers).
- If the session worked in files covered by that scope AND the agent context file's mtime predates the session start, it's stale.

Surface via the three-option template: *"Hey, noticed we worked in `internal/chat/` this session (covered by `.nanite/agents/backend.md`), but that doc wasn't updated. Want me to: A) review the doc and patch anything that drifted, B) capture a drift note to NANITE, C) add a follow-up task?"*

If agent scope can't be parsed from the doc, skip it rather than guess.

### 7. Commit SESSION-authored changes

For the SESSION list from step 2:

- Stage the files explicitly by name: `git add <file1> <file2> ...`. Never `git add -A` or `git add .` — those pick up USER and AMBIGUOUS files.
- Generate a conservative commit message using the house style (check recent commits via `git log -5 --oneline` for tone — e.g., kebab-case prefix like `boot-prompt:` or `chat: fix foo` per recent commits in the repo).
- Commit. Do NOT push. Pushing is out of scope (see Out of scope).
- If no SESSION-authored files exist, skip this step — nothing to commit.

**Commit message rules:**
- One line, imperative mood, <72 chars for the subject.
- Describe what the session did, not why (why goes in the boot-prompt).
- Prefix with a scope when the repo's recent commits use prefixes.
- If the session spans more than one logical change, make multiple commits, one per logical change. Do not squash unrelated work.
- Include the Co-Authored-By trailer if the repo's recent commits do (check `git log -5` for the pattern).

### 8. Handle USER and AMBIGUOUS changes (mode-dependent)

Behavior forks by workspace mode (determined in step 2):

**Strict mode.** For every file in the USER and AMBIGUOUS lists, surface via the three-option template. Batch tightly-related files (e.g., three files in the same package modified together) into one surface. Separate unrelated files.

**Multi-session mode.** DO NOT surface file-by-file. Roll the USER/AMBIGUOUS lists into a one-line summary in the wrap-up report for situational awareness only:
- Count of files in each list.
- A one-line category tag if obvious (e.g., `"2 agent-config, 4 prior-session docs, 3 tool-runtime artifacts"`).
- No diffs, no questions, no decisions unless the user asks. This is the default state of a multi-session workspace — surfacing it on every wrap creates noise that trains the user to ignore the skill.

**Do not commit USER or AMBIGUOUS files in either mode.** Ever. Even if the user says "yeah include everything", route that through explicit confirmation — prefer to stage them and have the user review the diff before committing. The one exception: if the user explicitly invokes a "housekeeping commit" flow (e.g., "commit accumulated KB state wholesale"), follow their directive, but still stage by explicit file list (never `-A`/`.`/`-u`), exclude paths that should be gitignored (tool runtime state, lockfiles, temp databases), and make it a separate commit with a clearly labeled scope (`housekeeping:` or similar).

### 9. Produce the wrap-up report

After the checklist completes, emit the end-of-session report (see Output contract below).

### 10. Vanta sweep — capture uncaptured session findings

Two passes: (a) history-based, (b) tracking-file heading scan.

**10a. Tool-call / conversation history.** Review the session's tool-call and edit history for findings that match `capture-to-vanta` triggers (cross-project pattern, non-obvious decision, surprise/workaround, user feedback, reusable insight) but were not captured mid-flight.

**10b. Tracking-file heading scan.** For every tracking-root file written or modified this session (anything under `execution/<project>/.../<YYYY-MM-DD>/`, `planning/<topic>/`, `boot/<project>/`), grep for these section headings:

```
^## Decisions locked|^## Decisions$|^## Decisions-locked
^## Follow-up candidates|^## Follow-ups|^## Pending follow-ups
^## Known limitations|^## Preserved tech debt
^## Out of scope|^## Out-of-scope
```

Each matched section is a capture candidate. The sub-bullets under the heading are the content (strip Markdown list syntax; preserve the rationale).

**For each candidate**, invoke the appropriate capture skill (they dedup via `conduit_lookup` before writing):

- Decisions → `capture-to-vanta` with tag `decision` (or the `/capture-decision` slash command)
- Follow-ups → `capture-to-vanta` with tag `followup` (or `/capture-followup`)
- Known limitations + preserved tech debt → `capture-to-vanta` with tag `limitation` (or `/capture-limitation`)
- Out of scope (WITH rationale; skip bare lists) → `capture-to-vanta` with tag `out_of_scope`

The skill emits one-line `Captured: <title> → <namespace/key>` per write so the wrap-up report can summarize.

**Skip this step** when: trivial session (no commits, no plan advances), or tracking files were not touched, or all candidates were already captured mid-flight. No narration needed in the wrap-up report if the sweep produced zero writes.

**10c. Draft review.** After the sweep, recall all `draft`-status memories from the current session:

```
mcp__vanta__memory_recall namespaces=["user/chrispian/memory"] statuses=["draft"] since=<session_start_time>
```

If any drafts exist, present them as a compact list (key + one-line summary) and ask the user to decide on each:

> **Draft memories from this session — {N} items need a decision:**
>
> 1. `decisions.foo.bar` — _"summary"_
> 2. `followups.nanite.baz` — _"summary"_
>
> For each: **promote to canonical**, **leave as draft** (needs more info), or **discard** (no longer relevant).

**Important — memory records use write-time status only.** `context_status_promote` and `context_status_deprecate` operate on context-domain records and will return `not_found` for memory records. Use these patterns instead:

- **Promote to canonical:** call `mcp__vanta__memory_write` with the same namespace + key, `status: canonical`, and `supersedes: <revision_id>` (the revision_id from the recall result). This writes a new canonical revision superseding the draft.
- **Discard:** call `mcp__vanta__memory_write` with the same namespace + key, `status: draft`, a `ttl_seconds: 1` to expire it immediately, and `supersedes: <revision_id>`. Alternatively, simply leave it — drafts have lower activation weight and won't surface prominently.

**Skip 10c** when: no drafts exist for this session, or the session_id is unknown (cannot filter by session). In that case note "no session drafts found" in the report.

**Memory-key normalization.** Vanta keys require `a-z 0-9 _` per segment — the capture skills normalize hyphens → underscores automatically. Direct `memory_write` calls must pre-normalize.

Vanta-primary transition note (`vanta-primary-since: 2026-04-19`): this step is Wave 1 of the memory migration. Wave 3 will fold this into Step 5 with Clockwork reconciliation and full file-based deprecation.

**10d. Marker sweep.** Invoke the `marker-parser` skill's sweep mode to scan session message history for unresolved inline markers (`:decision`, `:adr`, `:memory`, `:draft`, `:note`, `:todo`, `:defer`, `:promote`, `:archive`, `:review`, `:research`, `:finding`, `:preference`). Each marker routes per the marker-parser table; memory-domain markers dedupe-and-write to Vanta; process markers (`:promote`, `:archive`) surface via 3-option template rather than auto-resolving. Record the count + destinations for the report's `Markers resolved` line.

**Skip 10d** when: the session contained no inline markers (the sweep is cheap — this is mostly a belt-and-suspenders step catching markers that weren't resolved mid-flight). If marker-parser isn't installed, note "marker-parser not available — skipped" and continue.

### 11. Write the session-close record (closeout packet)

Write a single structured `session_close` record to Vanta (knowledge domain) as the durable continuity signal for the next session's boot. This is the primary input the boot compiler reads — do not rely on the chat report alone.

**Scope key is required.** Derive the scope key from:
1. Role file's `lineage_alias` field if present
2. Boot profile identity block if the session used one
3. Fallback: `<project>.<role>.main` (e.g., `nanite.backend.main`, `agent-ops.steward.main`)

**Call:**
```
mcp__vanta__knowledge_write
  namespace = user/chrispian/knowledge/session-close/<project>
  kind = session_close
  source = agent
  pointer_scheme = file
  pointer_locator = <boot-prompt path if present, else workspace root>
  key = <session_id>
  summary = <one-line session headline: what shipped + what's still open>
  body = <structured payload, see below>
  tags = ["session-close", "scope:<scope_key>", "vanta-primary-since:2026-04-19",
          "<project>", "<role>", "phase:<phase-if-applicable>"]
  session_id = <CLAUDE_SESSION_KEY or SESSION: value from hook>
```

**IMPORTANT — scope tag is required.** Every session-close record MUST include `scope:<scope_key>` (e.g., `scope:nanite.backend.main`). This is what the boot compiler uses to filter records for a specific agent identity. Without it, the record exists but won't be found.

**Body payload (structured; markdown-fenced sections):**

```
## session
- id: <session_id>
- project: <project-slug>
- scope_key: <scope_key>
- start: <RFC3339 if determinable, else "unknown">
- end: <RFC3339 now>
- branch@head: <branch>@<short-sha>
- workspace_mode: <strict|multi-session>
- clockwork_comment_id: <id from Step 11b, if written>

## segments
- <short chunk description — big topic shifts, each ~1 line>
- ...

## artifacts
- commits: <list of short-sha + subject>
- files_authored: <list of session-authored paths>
- plan_advances: <list of Clockwork task IDs completed>
- tickets_touched: <list of Clockwork IDs with transitions (and any read-only legacy Engine IDs cited)>
- prs: <list of PR numbers/urls>
- adrs: <list of new ADR paths>
- vanta_captures: <list of memory_id/revision_id from Step 10 captures>

## boot_delta_candidates
- <one-line description of a change the next boot-prompt should reflect>
- ...
(what changed in the world that the active boot-prompt doesn't yet reflect)

## markers_resolved
- :decision <summary> → <destination>
- :todo <summary> → <destination>
- ...
(from Step 10d)

## next_session_hints
- <one-line description of what the next session should pick up>
- ...
(boot-prompt "Next Actions" / "Resume Now" candidates)

## known_limitations
- <from tracking-file sections scanned in Step 10b, filtered to this session>

## out_of_scope
- <what this session deliberately didn't do, with rationale>

## narrative
<3–5 sentence prose written by the closing agent while context is hot.
Intended for direct insertion into §7 Session Narrative of the next boot prompt.
Write as if you're a few minutes ahead of the next agent — what do they need to
feel oriented? What's the structural risk or forward-looking flag? What was the
vibe of this session? Keep it dense and honest; don't summarize what's already
in the structured sections above.>
```

**The `narrative` field is the prose boot entry.** Write it with §7 in mind — it goes verbatim into the next session's boot prompt. This is the one part where judgment and voice matter; the rest of the record is compiler input.

**Project slug resolution:**
1. If `boot/<project>/boot-prompt.md` was touched this session, use `<project>`.
2. Else if the working directory is inside `~/Projects-apps/<project>/`, use `<project>`.
3. Else use `agent-ops`.

**Skip step 11 when:** the session is trivial (no commits, no plan advances, no captures). A one-line report is sufficient for trivial sessions.

**Failure mode:** Vanta write fails → surface via 3-option: A) retry, B) dump to `execution/<project>/session-close-fallback/<date>.md`, C) skip. Never hard-fail.

---

### 11b. Post session-close comment to Clockwork

After Step 11, post a comment to every Clockwork task that was substantially worked on this session. This is the **work journal entry** — prose, human-readable, tied to the task.

**For each Clockwork task worked this session:**

```
mcp__clockwork__clockwork_comment_add
  task_id = <CW-task-id>
  body = <see format below>
```

**Comment format:**
```
#end-session session:<session_id>

**What happened:** <2–4 sentences: what was built/changed, key decisions made>

**Commits:** <short-sha list with subjects>

**Next:** <what the next session should pick up on this task>

**Flags:** <any structural risk, blocker, or "watch for this" — optional>
```

**The `#end-session` tag** marks this as the canonical session narrative for this task. The boot generator and any future tooling can query Clockwork comments filtered by `#end-session` to get the work log entry for a given task.

**Agents may also add comments mid-session** (not just at close) to capture milestone completions, decisions, or blockers as they happen. These intermediate comments don't need `#end-session` — just write natural prose. Example: *"Finished P8 CompactionContract disclosure prompt (commit abc1234). Handoff stash ID now wires into boot disclosure block. Next: chat_search MCP tool."*

**Skip 11b when:**
- The session was trivial (no Clockwork tasks were advanced).
- No Clockwork tasks are identifiable for the session.
- Note in the wrap-up report: `Clockwork comment: skipped (no tasks advanced)`.

**Why both Vanta and Clockwork:**
- **Vanta session_close** = structured compiler input for the boot generator; scope-filtered by agent identity; drives §2/§3/§7 of the next boot prompt.
- **Clockwork comment** = task-scoped work narrative; searchable by task/ticket; audit trail visible in the Clockwork UI; extractable for task-level review.

They're complementary, not redundant. The `clockwork_comment_id` in the Vanta record links the two.

## Output contract

**Default output.** A single compact report, rendered in chat:

```
End of session — {branch} @ {short-sha}

Committed this session:
  • {commit-sha-short} {commit-subject}
  • {commit-sha-short} {commit-subject}

Surfaced to user (awaiting decision):
  • {file-or-topic} — {A/B/C pending}
  • {file-or-topic} — {A/B/C pending}

Boot-prompt: {updated this session | stale, flagged | n/a}
Plans: {Clockwork reconciled | local files (legacy): <path> | n/a}
Clockwork tasks: {reconciled | {N} surfaced | unreachable | n/a}
Agent context: {current | stale: <path> | n/a}
Markers resolved: {N markers → destinations | none found | n/a}
Session-close packet: {written <knowledge-id> scope:<scope_key> | skipped (trivial) | fallback <path> | unreachable}
Clockwork comment: {posted #end-session on <CW-IDs> | skipped (no tasks advanced) | unreachable}

Nothing outstanding. / {N} items awaiting user decision.
```

In multi-session mode, the `Surfaced to user` section is replaced with a one-line accumulation tally rather than a list of decisions:

```
Pre-existing accumulation: {N} file(s), not session-authored (baseline noise in multi-session workspace; ignored per mode)
```

Sections with nothing in them are omitted rather than shown empty. If the entire session is clean (no commits, no surfaces, no drift), the report collapses to:

```
End of session — {branch} @ {short-sha}
All clean. Nothing to commit, nothing to surface.
```

**Override.** If the invoker explicitly asks for a longer narrative (e.g., "give me a full wrap-up with what we did"), produce a short narrative paragraph above the structured report. Keep it factual — what was done, not what was interesting.

## Invariants

- NEVER auto-commit USER or AMBIGUOUS files. Only SESSION-authored files get committed without asking. (Exception: explicit user-invoked "housekeeping commit" flow — see step 8.)
- **Mode-aware surfacing.** In strict mode, every USER/AMBIGUOUS file is surfaced via the three-option template. In multi-session mode, pre-existing accumulation is rolled into a one-line tally in the report — NOT surfaced file-by-file. The general anti-pattern ("I noticed X, moving on" / "X is a separate task" / "X is out of scope, continuing") still applies to novel discoveries in either mode — those always go through the three-option template. What's relaxed in multi-session mode is ONLY the expected-baseline file accumulation from parallel-agent activity, not actual discoveries.
- ALWAYS run `git status --porcelain -u` (the `-u` is load-bearing — catches untracked files, which is where parallel-agent and user-generated work lives).
- ALWAYS stage commits by explicit file name. NEVER `git add -A` / `git add .` / `git add -u`.
- NEVER push to remote. That's a separate, explicit action.
- NEVER squash or amend prior commits. New commits only.
- NEVER invoke `/boot-prompt` automatically — it's manual-only by design. Only invoke it if the user picks option A on the boot-prompt surface.
- NEVER auto-transition legacy Engine tasks. Treat Engine as read-only at session-close; if a `TASK-` ID needs movement, surface via the three-option template and let the user decide.
- NEVER auto-resolve destructive markers (`:promote`, `:archive`). Step 10d surfaces them via 3-option — user picks.
- NEVER skip Step 11 for substantive sessions. The session-close record is the compiler input; its absence breaks continuity for the next boot. Only skip on trivial sessions (no commits, no plan advances, no captures).
- ALWAYS include `scope:<scope_key>` tag on session-close records. Records without scope tags won't be found by the boot generator.
- ALWAYS write the `narrative` field in Step 11 — this is the prose §7 content written while context is hot. Don't leave it empty.
- ALWAYS post the `#end-session` Clockwork comment (Step 11b) for substantive sessions that advanced Clockwork tasks. The comment is the task-scoped work journal entry.
- NEW PLANS go in Clockwork. Do NOT create new plan files on disk. Migrate existing local plan files to Clockwork when the opportunity arises (surface via three-option template in Step 4b).
- Mid-session Clockwork comments are encouraged (not just at close) — capture milestone completions, decisions, blockers as prose comments on the task.
- If the workspace isn't a git repo, skip steps 1–2 and 7, and note the limitation in the report.
- If the checklist reveals a gap the skill doesn't cover, STOP and surface it via the three-option template rather than improvise.

## Out of scope

This skill does one thing: the end-of-session handoff checklist. It does NOT:

- Run tests, linters, or builds — use `/go-test`, `/go-lint`, `/go-build` or project equivalents before invoking this skill if you want a green-check first.
- Push commits to remote — push is explicit, separate, and often gated on PR workflow. The user pushes.
- Open, update, or merge pull requests — use `gh pr create` / `gh pr merge` or a dedicated skill.
- Squash, rebase, or amend prior commits — those are history-rewriting operations and need explicit user authorization.
- Generate the boot-prompt automatically — that's `/boot-prompt`'s job, and it's manual-only.
- Archive or clean up worktrees — separate concern.
- Write to Vanta Conduit / NANITE directly — if the user picks option B on a surface, invoke `/nanite` or `/doc-note` from there. This skill is the checklist, not the capture mechanism.
- Analyze code quality or flag technical debt — use `/deep-review` for that.
- Update `~/.nanite/docs/nanite-framework.md` or any framework doc — that's the agentrc-manager role's job.

If the user asks for any of the above during an end-of-session run, complete the checklist first, then address the request as a separate step.

## Notes for the invoking agent

- **The checklist is the point.** The value of this skill is that it runs in a fixed order every time. Don't "optimize" by skipping steps that feel unnecessary for this session — the user's complaint is that agents silently skip steps.
- **Surfacing is a pause, not a narration.** When you pose the three-option template, actually stop and wait for the user's pick. Don't immediately follow it with "I'll probably do A" or "while you decide, let me also check Y". One surface, wait, next step.
- **The wrap-up report is a receipt, not a recap.** The user already knows what happened in the session — they were there. The report exists to confirm what was committed, what was surfaced, and what's still open so the next session can pick up without archeology.
- **If the hook fires on a false positive** (user said "wrap this function up" meaning something totally different), the checklist still runs but will find a clean tree and produce a one-line "all clean" report. That's a cheap false-positive cost and worth keeping the hook permissive.
