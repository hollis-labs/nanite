# User Workflow — Observed From Claude Code Transcripts

Empirical study of Chrispian's working style, extracted from ~/.claude/projects/*.jsonl session logs. Goal: ground the proposed five-phase workflow (ideation → tech exploration → planning → execution → review) in real behavior before formalizing it in Nanite.

## Session Inventory

**Corpus window:** 2026-03-06 to 2026-04-05 (last 30 days).

**Nanite project (primary focus):** 26 JSONL session files in `~/.claude/projects/-Users-chrispian-Projects-apps-nanite/`. 25 had substantive assistant activity (≥2 turns) and were analyzed. Size range: 23 KB to 1.9 MB per session.

**Aggregate stats across 25 Nanite sessions:**
- **User turns:** 121 total, mean 4.8 per session (median lower — several single-turn sessions)
- **Assistant turns:** ~3,250 total, mean 130 per session
- **User:assistant ratio ≈ 1:27** — extreme delegation density
- **Sessions opening with "Boot <role>":** 21/25 (84%) — remaining 4 are test/trivial sessions or continuations
- **Sessions spawning subagents via Agent tool:** 18/25 (72%), total 45 spawns
- **Sessions using TaskCreate/TaskUpdate (Nanite's engine backlog tools):** 12/25 (48%)
- **Sessions with explicit git commit:** 7/25 (28%) — lower than expected; most commits happen in dedicated commit-flow or via Cerberus sessions
- **Sessions using cerberus MCP tools directly:** 6/25 (24%)
- **Sessions using Plan Mode (Enter/ExitPlanMode):** 3/25 (12%) — nearly unused
- **Sessions using TodoWrite (built-in):** 0/25 — TodoWrite is entirely replaced by Engine TaskCreate/TaskUpdate

**Tool use aggregate (top 15):**
```
Read              529   Write              103
Bash              434   Glob                55
Edit              406   Agent               45
Grep              242   ToolSearch          34
TaskUpdate        155   cerberus_rebuild    10
TaskCreate         83   Skill                6
                        cerberus_logs        5
                        EnterPlanMode        3
                        ExitPlanMode         3
```

**Non-Nanite comparison sample (5 sessions each):**
- `hadron/`: 13 sessions in window, same Boot-<role> opener, similar tool distribution, heavier frontend-rebuild focus
- `mentat/`: 77 sessions in window — highest volume project, includes an explicit `Boot orchestrator` role and heavy `mcp__engine__engine_task_transition` usage (53 calls in one session), ~22 Agent spawns/session peak
- `cerberus/`: 10 sessions, same opener pattern, heavier Bash/Write ratio (daemon work)

The pattern described below is consistent across all four projects.

## Actual Workflow Observed

The user's self-described five-phase flow exists, **but most of it is collapsed, implicit, or redistributed across multiple sessions**. The phases rarely live inside a single session. Instead, each session tends to specialize as *one* phase of a larger multi-session workflow, and the transitions between phases happen across sessions via a shared artifact: the **boot prompt**.

### Phase 1 — Ideation (Exploration/Feature Framing)

Present in roughly 15–20% of sessions, usually explicitly declared. Canonical example, `b0a1b9a6-63f3-4a20-88e9-95b42c94659b.jsonl` turn 1:

> "This session we will focus on some ideas/task exploration. If we decide it's good, we'll add it to the plan/boot prompt. Here is a feature that would be SUPER handy…" (followed by a ~400-word feature sketch for a per-chat shell)

Duration: 4–8 user turns, ~30 assistant turns. The user drops a wide-scope problem, the agent explores shape/alternatives, and the conversation converges via numbered-list corrections. Output is **never code** — it's task entries written to the boot prompt ("go ahead and write the tasks to the boot prompt", same session, turn 3).

Ideation is the only phase that consistently surfaces *novelty*. Once a feature is in the plan doc, it stops being ideation and becomes executable work.

### Phase 2 — Technical Exploration (Research/Audit)

Present in ~25% of sessions, frequently delegated to a subagent. Typical pattern: a "research-only" session with explicit "do not write code" framing. Example from `dd8ef370-32b8-46a2-8265-b12880d09161.jsonl` turn 1:

> "TASK: Phase C Investigation — Cortex Type/View Registry Audit / This is a RESEARCH ONLY task. Do not write any code. Produce a findings document."

Subagent spawn descriptions confirm this is a recurring primitive (from 45 observed `Agent` spawns):
- `Explore:Build current-state picture for boot-prompt`
- `Explore:Nanite architecture deep read`
- `Explore:Audit plugin events system`
- `Explore:Audit plugin UI components`
- `Explore:Read all migration files`
- `Explore:Review all plugins`
- `Explore:Review known issues`

Output: a markdown doc under `docs/` or `docs/research/`, referenced from the boot prompt. The main session synthesizes the findings into a single paragraph for the user to read; raw subagent output is rarely dumped verbatim into the main stream.

### Phase 3 — Planning

This is the phase that is most *distributed*. There is no single "planning session" in the corpus — planning happens in two places:
1. **Tail end of an ideation or exploration session**, where the last 2–3 turns convert findings into boot-prompt task entries.
2. **Top of an execution session**, where the first turn is "Boot <role> and review the boot prompt and see docs/post-mvp-plan.md — you will be working on B2."

The **boot prompt is the plan.** It is a shared markdown file that the user treats as a durable, multi-session backlog. The built-in Claude `TodoWrite` tool is **not used at all** (0/25 sessions). Instead the Nanite Engine's `TaskCreate`/`TaskUpdate` tools carry the work — 155 `TaskUpdate` calls across the corpus vs 0 `TodoWrite` calls.

Plan Mode (EnterPlanMode/ExitPlanMode) appears in only 3 sessions. It is not part of the user's regular flow — he plans in prose and commits the plan to a file.

### Phase 4 — Execution

This is the dominant phase by volume. It accounts for the vast majority of assistant turns. Typical execution session:
- **1–2 user turns total.** User says "Boot nanite-backend / TASK: Phase 4 — TaskBackend Abstraction" (`7d8a6c5c-1676-4c98-9dba-693c214897de.jsonl`, 112 assistant turns, 2 user turns), then goes silent until the agent finishes.
- The second user turn is usually "Let's update the boot prompt and do a commit." — i.e. the handoff to review.
- Assistant runs Read→Edit→Bash→Grep in long loops. Median tool-use per execution session: ~80 calls.

Quotes that mark the execution phase entry:
- "Let's proceed." (`ade2105d`, `575ddd42`)
- "I'm ready." (`7b30df89`)
- "Perfect, yes. Let's proceed." (`575ddd42`)

These are the cleanest phase-transition signals in the entire corpus and they all mean the same thing: *planning is closed, start executing*.

### Phase 5 — Review

Review happens in two distinct forms, and **neither is a dedicated session**. Both compress into 1–3 turns at the end of an execution session:

**Form A — Visual/functional smoke test** (frontend work): User runs the app, reports concrete defects. `ecf137f0` turn 3: "Looks good! The only issue with the 1hr ago is that the 'you' part doesn't show up by default… Let's make the 'You' text always…" `aa02f50c` turn 2: "So far so good! I see the plugin listed in the plugins page but it's grayed out and I can't interact with it."

**Form B — PR/copilot review loop** (after commit): `02a8cedd` turn 18: "There are a few items of feedback on the PR. Please take a look and address and we'll merge, sync and close out." `575ddd42` turn 19: "There are just a few bits of feedback on the PR. Please take a look and address."

There is no evidence of git-diff walkthroughs, no "show me what you changed", no explicit test-run-and-review step. Tests are run by the agent autonomously during execution; the user reviews by *using the software* or *reading the Copilot comments on a PR*.

**Phase duration distribution (rough, in user turns):**

| Phase | Typical turns | % of corpus volume |
|---|---|---|
| Ideation | 4–8 | ~10% |
| Tech exploration | 2–4 (main) + subagent | ~15% |
| Planning | 0–2 (usually embedded) | ~5% |
| Execution | 1–2 user, 80–300 asst | ~60% |
| Review | 1–3 | ~10% |

## Phase Transition Patterns

Transitions are **almost always explicit and terse**, but not via the five-phase vocabulary the user described. The real vocabulary is shorter:

- **Into execution:** "Let's proceed." / "Perfect, yes." / "I'm ready." / "Hit me with the boot prompt for the first phase." (`02a8cedd` turn 10)
- **Into review:** "Looks good!" / "Perfect." / "Looks great." (nearly every frontend session ends with one of these before the commit request)
- **Into next task/phase:** "Let's do the next phase." / "Please provide me with the next prompt." (`02a8cedd` turns 13, 14)
- **Commit/close:** "Let's do a commit here." / "Let's get everything committed and do a PR." (`ecf137f0`, `02a8cedd`)

The user does **not** say "let's plan this" or "let's review." Those phases are either skipped-as-implicit or handled by artifact manipulation (updating the boot prompt).

**Cross-session transitions** are the most interesting signal. The parent session explicitly coordinates boot prompts for child sessions. From `02a8cedd` (a meta-orchestration session):

- Turn 13: "Phase 3 just finished, they are updating boot prompt and plan. Give me the next prompt please."
- Turn 15: "I'm booting up an agent for B/User shell. Can you give me a prompt for an agent to do the Cortex investigation? I'll boot them too."

The user is running multiple child Claude sessions in parallel and this meta-session is his dispatch console. This is the most distinctive, non-obvious pattern in the corpus.

## Subagent Usage Patterns

45 Agent spawns across 18 sessions (72% of substantive sessions). Spawn descriptions cluster into three clear types:

1. **Exploration/audit** (majority): `Explore:Audit hardcoded color usage`, `Explore:Review all plugins`, `Explore:Nanite architecture deep read`. These are bounded, read-only. Results are synthesized into a paragraph, not dumped.
2. **Bounded destructive task**: `general-purpose:Delete marvel plugin entirely` — a narrow, well-scoped write action offloaded to preserve main-session context.
3. **Meta/research** (rare but present): `general-purpose:Claude Code transcripts pattern analysis`, `general-purpose:Anthropic harness + tools posts digest`. Used for reading external corpora.

**Observed quality:** synthesis is generally good — the main session summarizes subagent findings in 3–10 lines and the user accepts them. There are no examples in the corpus of the user re-scoping a subagent after it ran (which would indicate bad delegation). The user *does* occasionally re-scope **before** spawning, e.g. `aa937fe0` turn 2 where he answers 5 questions about scope/tier/depth before letting the agent kick off research. This pre-spawn clarification is a recurring pattern.

## Artifact Creation Patterns

Artifacts, in decreasing order of frequency and importance:

1. **Boot prompt** (highest leverage). Updated at the end of ~every session. Single source of truth for plan state. Transitions "tasks in flight" → "tasks done" → "next tasks proposed". Lines like "Let's update the boot prompt and do a commit." (`7d8a6c5c`) and "Let's update the boot prompt to reflect reality. That'll be our first task." (`ecb7c5f8`) establish it as a living document.
2. **Plan docs under `docs/`** — `docs/post-mvp-plan.md`, `docs/plugin-extraction-plan.md`, phase docs. Referenced by name across sessions ("see docs/post-mvp-plan.md - you will be working on B2", `7b30df89`). These are the persistent multi-session planning artifacts.
3. **Research findings docs** — produced by exploration subagents, written to `docs/research/` or referenced from boot prompt.
4. **Engine Tasks (TaskCreate/TaskUpdate)** — 238 combined calls. The *structured* backlog, separate from boot prompt prose. Used inside sessions for in-session TODO tracking.
5. **Git commits + PRs** — terminal artifact. Chrispian delegates the commit message writing to the agent and immediately routes PR feedback (from GitHub Copilot reviewer) back into a short review loop.
6. **ADRs / decision docs** — mentioned occasionally ("check the decision doc", `575ddd42`) but not frequently created.

**Notably absent:** No memory/save commands (no `/remember`, no explicit memory captures in the corpus). Feedback is captured by *writing it into the boot prompt* for the next session, not by saving to a memory layer.

## Friction Points

Specific places where the tool and the workflow don't match:

1. **No native multi-session orchestration.** The parent-orchestrator session in `02a8cedd` and `575ddd42` works only because Chrispian manually relays state: "A1 and A2 agents are working now" / "B1 & B2 are in progress now" / "They just finished. Please provide me with the next prompt." This is visible human-in-the-loop polling. Nanite has no native way for a parent session to see child session state.

2. **Boot-prompt drift.** Multiple sessions open with complaints about stale boot prompts: `ecb7c5f8` turn 2: *"I think Current work is somewhat stale. ! exec is done, for example. Also, we eliminated all those connectors except for github."* And turn 3: *"1. Upcoming. 2. Debug, TaskBackend are both done too. I think you should check recent commits/docs, etc. This should have all been documented."* The boot prompt is the hub, but nothing is validating that the prose in it reflects repo state.

3. **Skill injection noise.** Several sessions have their first real user message buried under 2–3 auto-injected Vercel plugin skill messages (`dd8ef370`, `1648e065`). This pollutes the planning context even for obviously-Go projects. The user never refers to these skills, confirming they are pure noise.

4. **TodoWrite bypassed.** Claude Code's built-in TODO tracking is used zero times. The user has built Engine/TaskCreate as a replacement, but the harness keeps reminding the agent to use TodoWrite (visible in the system-reminder stream). Friction between built-in assumptions and the user's actual backlog model.

5. **No structured transition between exploration and execution.** In `575ddd42` turn 15, the user has to *manually* keep track of which research has been done vs which execution items are ready: *"The dependencies for all the P1 tasks are done. I think the following order is good."* This kind of dependency-ordering is done in prose, in his head, and dictated to the agent. Error-prone.

6. **Review is entirely out-of-band.** The only review signal in the corpus is the user opening the app or reading a PR. The agent has no hook to request review, the user has no structured "review mode." A fix for this would require harness awareness of "this task is ready for user verification."

## Patterns Worth Formalizing

Five concrete primitives that recur enough and are friction-heavy enough to deserve first-class support:

1. **Boot-prompt as canonical plan state.** Make it a structured document type in Nanite with:
   - Explicit `in-flight`, `done`, `proposed` sections
   - Automatic staleness detection against git log / Engine task state
   - A "refresh against reality" command (the exact task Chrispian opens stale sessions with)
   - Per-role variants (`nanite-backend.boot.md`, `nanite-frontend.boot.md`) since the corpus shows distinct FE/BE boot prompts

2. **Parallel child-session orchestration ("Orchestrator Mode").** The `02a8cedd` meta-session is already an orchestrator pattern — it just lacks tooling. First-class support would include:
   - A `spawn_child_session(role, boot_prompt)` primitive
   - Status visibility into child sessions (running / awaiting input / done / blocked)
   - Automatic boot-prompt-fragment generation for next-task handoff
   - The mentat project already has `Boot orchestrator` as a role — this is a proven pattern worth lifting to the harness

3. **Exploration subagent as a named primitive.** 72% of sessions use it; spawn descriptions are repetitive ("Explore:X", "Audit:Y"). Define an `/explore` skill that:
   - Enforces read-only scope
   - Requires a `docs/research/<topic>.md` output path
   - Auto-references the result from the active boot prompt
   - Pre-prompts the user with the 3–5 scoping questions he already answers every time (tier, depth, time budget, audience, followups)

4. **Ideation-mode session.** Currently identified only by the user manually declaring "This session we will focus on some ideas/task exploration." Make it a session type:
   - Suppresses code edits entirely (agent cannot Write/Edit)
   - Biases toward sketch + numbered-list alignment UX
   - Terminal artifact must be one or more appended tasks on a boot prompt
   - Exits cleanly into a commit of the updated boot prompt

5. **Review handoff signal.** When an execution session is "done but unverified", the agent should emit a structured "ready-for-review" envelope listing: files changed, tests run, screenshots if frontend, and suggested verification steps. The user's existing review flow ("looks good"/"the issue with X is…") becomes a structured response to that envelope rather than free-form.

Ordered by ROI: #1 (boot-prompt structure) and #2 (orchestrator mode) are highest leverage because they're the two places where the workflow is actively hacked together in prose. #3 is low risk and high repetition. #4 and #5 are smaller but would reduce the implicit-mode problem.

## Patterns to NOT Formalize

Things that look like a pattern on first read but are improvisation or one-offs:

1. **The literal five-phase flow as a single-session state machine.** The user's self-described flow is real as a *mental model*, but **it does not live inside one session**. Encoding it as a session-level FSM (Ideation → Exploration → Planning → Execution → Review with forced transitions) would be fighting the evidence. Planning is mostly ≤2 turns; review is mostly ≤3; several sessions have no ideation at all. Cross-session workflow is the correct scope, not intra-session.

2. **Plan Mode (EnterPlanMode/ExitPlanMode).** Used 3 times in 25 sessions. The user has deliberately routed around it. Don't build on top of it.

3. **TodoWrite.** Zero uses. Already replaced by Engine tasks. Don't rebuild.

4. **Explicit "review phase" blocking.** The user's review is either "I used it and it works" or "PR copilot feedback." Building a forced review checkpoint would add friction. The review *signal* (item #5 above) is worth formalizing; a review *phase* is not.

5. **Structured backtracking / undo workflow.** Only 6/121 user messages (~5%) contain backtrack markers, and most are context interrupts ("while we wait, since the rename…"), not course corrections. Building extensive undo/rewind tooling would be solving a non-problem. The reason backtracks are rare is that the boot-prompt + ideation-first flow catches disagreements *before* execution.

6. **TaskCreate/TaskUpdate as a user-facing primitive.** The user almost never creates tasks directly — the agent does. Exposing a `/task add` slash command would be redundant; what the user needs is better boot-prompt structure (#1), and the task tracking falls out of that.

7. **Memory/"remember this" captures.** Absent from the corpus. Feedback flows into the boot prompt instead. Don't build a separate memory capture layer until there's evidence the boot-prompt channel is overloaded.

## Summary

The user operates a **cross-session, artifact-centered workflow** where the boot prompt is the plan, execution sessions are near-autonomous, and one session per working block acts as a meta-orchestrator over parallel child sessions. The five phases exist, but only ideation, execution, and review are consistently visible inside individual sessions; exploration is delegated to subagents; planning lives in files, not in dialog. The biggest harness-level opportunity is first-class support for boot-prompt state management and parent→child session orchestration — both currently held together by manual prose relay.
