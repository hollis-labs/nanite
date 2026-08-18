# Boot-Launched Agent Transcripts (Nanite boot-profile CLI harness + Torque agent-launch)

Evidence-gathering only. These are raw, headless Claude Code CLI transcripts recorded when Nanite's boot-profile CLI harness and Torque's agent-launch mechanism spun up an agent session against a compiled boot prompt, with no human in the loop. No recommendations below — findings and verbatim excerpts only.

## Directories/files reviewed

### `nanite-boot-claude` (6 directories found — task estimate said "about 8"; enumeration below is exhaustive for the glob)

| # | Directory | Transcript file | Size | Lines |
|---|---|---|---|---|
| 1 | `nanite-boot-claude-8d3f76b0-...-r0-3445639172` | `b796242a-5f53-4ce7-a622-3c4913ca362c.jsonl` | 72,457 B | 26 |
| 2 | `nanite-boot-claude-8d9058c8-...-r0-240300269` | `7144785b-be7f-441e-a03e-e65a072c18ee.jsonl` | 57,554 B | 21 |
| 3 | `nanite-boot-claude-a40f03f6-...-r0-3243107124` | `2543b042-5a66-4cee-92af-44f152f03aa5.jsonl` | 64,867 B | 20 |
| 4 | `nanite-boot-claude-af9afb0f-...-r0-2013075417` | `b9762f54-98cc-47c5-80d0-66ee1efaf4e1.jsonl` | 56,552 B | 21 |
| 5 | `nanite-boot-claude-edd88c59-...-r0-3755281925` | `e2defaee-73dd-49d5-8152-a9814034f48f.jsonl` | 55,189 B | 21 |
| 6 | `nanite-boot-claude-f10603cf-...-r0-2378048118` | `ca7a7bcd-70a8-4ea1-925f-8138084df11d.jsonl` | 52,933 B | 18 |

All six are `boot-profile CLI harness` launches (per this project's `docs/boot-profile-cli-harness.md`). All six are functionally identical: a one-shot session that receives `Boot @./boot.md`, self-orients by listing the boot dir, reads `.sandbox/agent-context.md` (and in some cases `.sandbox/envelope-schema.md`), and ends with a "ready for a task" message — with no task ever actually delivered, because the mode is `one_shot` and the session terminates there. Each run took 6–10 assistant turns and roughly 15–30 seconds of wall-clock time (e.g. run 1: 21:01:45.585Z → 21:02:02Z).

### `torque-boot-agentlaunch` (2 directories, as expected)

| # | Directory | Transcript file(s) | Size | Lines | Duration |
|---|---|---|---|---|---|
| 7 | `torque-boot-agentlaunch-bootdir-34e6d12b-1630373851` | `5b0adfe6-...jsonl` (main) + `5b0adfe6-.../subagents/agent-a065d752803ca7ec3.jsonl` (nested Explore sub-agent) | 1,205,309 B + 657,702 B | 433 + 201 | 17:47:37Z → 18:23:13Z (~35.5 min) |
| 8 | `torque-boot-agentlaunch-bootdir-da9ea7b4-3304430636` | `a9593482-3ce8-49e5-948f-e532d0f3977c.jsonl` | 305,136 B | 70 | 18:23:26Z → 18:26:29Z (~3 min) |

Both are `torque agent-launch` sessions. Directory 7 (`agent_profile: implementer-long`) is the **implementer** run for Torque task `CW-20260519-0068` ("Agent should narrate progress during long / subagent-heavy turns"); it produced PR #234. Directory 8 (booted with prompt `"Disposition audit for CW-20260519-0068. V1 reviewer (CW-20260503-0019)."`) is the **reviewer/end-agent** run that audited and closed out that same PR. Together they form one complete, sequential implement → review → merge → close cycle on the same ticket, captured from two different headless launches ~3 hours apart (17:47Z implementer start, 18:23Z reviewer start, i.e. the reviewer launched immediately after the implementer's AAR landed).

---

## Setup / boot issues

### Boot prompt for `nanite-boot-claude` carries no task
**Source:** all six `nanite-boot-claude` transcripts (e.g. `b796242a-...jsonl`, turn 1).
Every one of the six sessions' `boot.md` is an 8-line stub containing only agent-profile/session metadata and the sentence "This is a one-shot invocation. Complete the task in a single response and exit." — no task content is present anywhere in the boot dir. The agent has nothing to do but confirm it booted and stop, because `Mode: one_shot` closes the session immediately after.
> `"# Boot\n\n**Agent profile:** default\n**Session id:** 8d3f76b0-...\n**Mode:** one_shot\n\nThis is a one-shot invocation. Complete the task in a single response and exit.\n"`
All six runs end on some variant of "Ready for your task — what would you like help with?" which then goes unanswered (one-shot mode terminates there). This same shape repeats verbatim across all 6 directories.

### Full personal skill/agent catalog loaded into a "cannot execute" default-profile sandbox
**Source:** `nanite-boot-claude-8d3f76b0-...` `skill_listing`/`agent_listing_delta` attachments, turn ~5.
The `skill_listing` attachment injected into every `nanite-boot-claude` session (`skillCount: 24`) is the operator's full personal Claude Code skill catalog — `adr`, `blg`, `capture-decision`, `schedule`, `loop`, `keybindings-help`, `security-review`, etc. — the same catalog available in interactive sessions, not a curated Nanite-specific subset. `agent_listing_delta` likewise surfaces `laravel-react-crud-builder` and `statusline-setup`, agents unrelated to Nanite. This is loaded even though the booted agent identifies itself (from `.sandbox/agent-context.md`) as `"# Agent: Default … Description: General-purpose chat agent … Can Execute: false"`.

### `.nanite/` directory absent in every CLI-boot sandbox
**Source:** all six `nanite-boot-claude` transcripts, turn 2–3.
Every run's first or second tool call is a directory listing that discovers `.nanite/` does not exist in the boot sandbox (`ls: .../.nanite/: No such file or directory`), so the agent concludes there is no `boot-prompt.md`/`config.yaml` to load and falls back to the bare `.sandbox/agent-context.md` profile. This is consistent across all 6 runs — it is either expected behavior for this launch shape or a systematic absence, but every run independently rediscovers it via a live `ls` rather than being told directly.

### Host-machine hook (`tokf`) fails non-blocking on every Bash call across all sandboxes
**Source:** all six `nanite-boot-claude` transcripts and the `torque-boot-agentlaunch-34e6d12b` transcript.
Every `PreToolUse:Bash` hook invocation in every `nanite-boot-claude` sandbox fails with:
> `"Failed with non-blocking status code: /Users/chrispian/Library/Application Support/tokf/hooks/pre-tool-use.sh: line 2: exec: tokf: not found"` (exit code 127)
This fires twice per `nanite-boot-claude` session (once per Bash call) and is silently absorbed (`hook_non_blocking_error`, non-fatal) — the agent never sees or reacts to it. By contrast, in the much longer `torque-boot-agentlaunch-34e6d12b` implementer session, `tokf` **is** present and used successfully as a real wrapper around `go test` (e.g. `tokf run --baseline-pipe 'tail -60' go test ./internal/subagent/... -race -timeout 120s`, exit 0, and inline output tagged `[tokf] output filtered — to see what was omitted: 'tokf history show --raw 38171'`). So the tool exists and works in the Torque worktree environment but its hook script cannot find the binary in the `nanite-boot-claude` sandbox's `PATH` — the two launch mechanisms provision the shell environment differently.

### Session identity is tracked under three different IDs simultaneously
**Source:** `nanite-boot-claude-8d3f76b0-...` transcript, turns 1 and 10.
The same boot produces: the temp-dir UUID in the folder name (`8d3f76b0-4b25-4d72-a06d-9947677ec423`), the Claude Code `sessionId` (`b796242a-5f53-4ce7-a622-3c4913ca362c`), and a third key minted by `~/.claude/hooks/session-start-key.sh` (`session-20260813-7bbf8750`). The final assistant message in a sibling run (`edd88c59-...`) reports the third form ("session `session-20260813-96fb0997`") rather than the sessionId or folder UUID, so which identifier is "the" session id is inconsistent even within the transcript itself.

---

## Tool-calling issues

### `ScheduleWakeup` called with a missing required parameter, self-corrected one turn later
**Source:** `torque-boot-agentlaunch-34e6d12b` (implementer), 18:12:19Z–18:12:34Z.
While a background Copilot-review poll was running, the agent called:
> `ScheduleWakeup {"delaySeconds":660,"reason":"Copilot PR-review poll running in background...","noop":true}`
which returned the validation error `` `prompt` is required when `stop` is not true. `` The very next `ScheduleWakeup` call (14 seconds later) added the missing `prompt` field and succeeded ("Next wakeup scheduled for 13:24:00 (in 686s)."). No further retries, no confusion — a one-shot parameter-schema miss, immediately fixed.

### Stray `cd` into a stale relative path after a background/async gap
**Source:** `torque-boot-agentlaunch-34e6d12b` (implementer), 18:22:31Z.
Immediately after the ~10-minute background PR-review poll returned and the agent stopped its `ScheduleWakeup` fallback, its next Bash call (`gh pr view 234 --json reviews,state,mergeable ...`) failed:
> `"Error: Exit code 1\nfailed to run git: fatal: not a git repository (or any of the parent directories): .git"`
The shell's working directory had reverted to the boot root rather than the worktree it had `cd`'d into earlier in the same logical turn. The agent diagnosed and fixed this on the next call by prefixing an explicit `cd` to the work root, and the command then succeeded. A smaller instance of the same class of error occurs earlier in the same session at 18:09:06Z (`cd internal/chat` fails with `no such file or directory: internal/chat` because the shell was already inside that directory from a prior command); the agent ran `pwd` to check state and adjusted.

### Minor script-level bash errors during self-directed diff/gofmt inspection
**Source:** `torque-boot-agentlaunch-34e6d12b` (implementer), 18:10:08Z and 18:11:09Z.
Two throwaway diagnostic one-liners failed outright and were abandoned in favor of a simpler follow-up command, both apparently used only to double check formatting rather than to make a required decision:
- `awk: syntax error at source line 1` while trying to bucket a `gofmt -d` diff into per-hunk temp files (`/tmp/gofmt_hunk_*.txt`) — the agent switched immediately to just re-running the plain `gofmt -d` diff.
- An `env | grep -i git` / `git config --get ...` probe for a pager/diff-alias explanation returned exit code 1 on the alias lookup but usable output on the rest; the agent used the partial output and moved on.

---

## Hallucinations

### Claimed envelope schema was "reviewed" without reading it
**Source:** `nanite-boot-claude-af9afb0f-...` transcript, final turn (20:59:16Z).
This run's tool-call sequence is: list boot dir → list `.sandbox/` → `Read .sandbox/agent-context.md`. It never calls `Read` (or any tool) on `.sandbox/envelope-schema.md`. Its closing summary nonetheless states:
> `"No .nanite directory exists here, so there's no boot-prompt.md or agent config to load — just the default profile in .sandbox/agent-context.md (general-purpose chat agent, execute disabled) and the envelope schema reference."`
This mentions "the envelope schema reference" as loaded/available context, but the transcript shows no tool call that actually retrieved its contents in this run — contrast with runs 1 and 3 (`8d3f76b0-...`, `a40f03f6-...`), which do `Read`/`cat` the file before summarizing it.

---

## Steering issues

No clear steering issues (off-task wandering, unproductive loops, or under-corrected drift) were observed in either torque-boot-agentlaunch session. Both stayed tightly scoped to their assigned Torque task end-to-end: the implementer session broke its own work into 4 tracked sub-tasks via `TaskCreate`/`TaskUpdate` (investigate → design → implement+test → commit/PR/AAR) and executed them in strict order without detours; the reviewer session ran a fixed audit checklist (task state checks → PR alignment check → design check → follow-up filing → tag/merge/transition) with no backtracking. The `nanite-boot-claude` sessions are too short (one-shot, no assigned task) to exhibit steering behavior either way.

---

## Crash / recovery signs

No abrupt terminations, hangs, or retry-storms were found in any of the 8 reviewed sessions. All six `nanite-boot-claude` sessions end cleanly on an explicit "ready" message consistent with `one_shot` mode. Both `torque-boot-agentlaunch` sessions end on a clean, deliberate final message (implementer: task self-transitioned to `review` after AAR filed; reviewer: task transitioned to `done` after PR merge, closing comment posted). The one background-task wait pattern present (implementer polling for a Copilot PR review) was handled via `run_in_background` + `ScheduleWakeup`, not busy-polling, and resolved cleanly when the 10-minute cap expired with zero reviews found — the agent explicitly noted the cap-out and proceeded per its stated contract rather than stalling.

---

## Other

### A full implement → review → merge → close life cycle is reconstructable from just these two Torque sessions
**Source:** both `torque-boot-agentlaunch` transcripts, cross-referenced via Torque task `CW-20260519-0068` / PR #234.
The reviewer session (`da9ea7b4-...`) opens by fetching the exact same task, PR, comments, and AAR the implementer session (`34e6d12b-...`) produced three hours earlier, and its own audit comment explicitly narrates the task's history across two prior implementation rounds:
> `"...a first (2026-05-19) [round] closed by the V1 reviewer... but a 2026-05-26 post-flight audit found no landed code and reopened it. The second round (2026-08-14) shipped a harness-level heartbeat..."`
The reviewer filed a new follow-up ticket (`CW-20260814-0009`) for a gap the implementer's own AAR had flagged (no live/manual verification of the narration feature), tagged the original task `agent-closed`, squash-merged PR #234, and transitioned the task to `done` — all autonomously, all cited with a captured merge-commit SHA (`2243c94d7c11386a8ed3b9515a11dd8bc1a27a6b`).

### `ToolSearch` used deliberately and narrowly at the start of both Torque sessions and again mid-session
**Source:** both `torque-boot-agentlaunch` transcripts.
Both sessions open by calling `ToolSearch` with a `select:` query naming exactly the 4–5 `mcp__loopback__torque_*` tools needed for that phase of work (e.g. `select:mcp__loopback__torque_task_get,mcp__loopback__torque_comment_list,mcp__loopback__torque_artifact_list,...`), rather than a broad keyword search or loading the entire MCP surface. The implementer session repeats this pattern a second time mid-session (`select:mcp__loopback__torque_task_create,mcp__loopback__torque_task_update,mcp__loopback__torque_task_transition,mcp__loopback__torque_task_checkpoint_emit`) right before it needs task-mutation tools it hadn't loaded initially.

### Implementer session self-imposed a token budget on its own prompt-text change and iterated against it
**Source:** `torque-boot-agentlaunch-34e6d12b`, 18:08:51Z–18:09:24Z.
While editing `internal/chat/universal_rules.go`, the agent ran a throwaway Go test three times in a row (`TestPrintBlockLen`) after each successive edit, watching the reported `EST TOKENS` value fall from over-budget toward the target: no explicit value shown before but shows 587 → then trimmed further, final measurement:
> `"BYTE LEN: 2158\nEST TOKENS: 540"` against a stated `"550 SlotUniversal budget"`
and stopped exactly once it had "a small margin," rather than trimming further or stopping short.

### `gofmt` pre-existing-issue check via `git stash`
**Source:** `torque-boot-agentlaunch-34e6d12b`, 18:10:01Z.
When `gofmt -l` flagged a struct-field alignment issue in a file the agent had edited, it did not silently reformat or ignore it — it ran `git stash` to check whether the misalignment existed in the file *before* its own change, confirmed it predated the change, then `git stash pop` and left the pre-existing misalignment untouched, citing "smallest complete change" as the rationale in its later verification comment.

---

## Patterns observed

- The two launch mechanisms produce categorically different transcripts: all six `nanite-boot-claude` (boot-profile CLI harness) sessions are short, task-less one-shot boots that end in "ready for a task" with the session already over; both `torque-boot-agentlaunch` sessions carry a real assigned task from a planted task bundle and drive it to a concrete, verifiable end state (PR merged, task transitioned, AAR/audit comment posted).
- Every `nanite-boot-claude` session independently re-discovers the same facts via live shell calls (no `.nanite/` dir, `.sandbox/agent-context.md` shows a `Default`/`Can Execute: false` profile) rather than being told this in the boot prompt directly — the same 3–5 tool-call sequence repeats nearly verbatim across all six directories.
- The non-blocking `tokf` hook failure is systemic to every `nanite-boot-claude` sandbox (12 total occurrences across 6 runs, 2 per run) but does not occur in the `torque-boot-agentlaunch` sandbox, where the same `tokf` binary is present and actively used to wrap long-running test commands.
- Both `torque-boot-agentlaunch` sessions make heavy, structured use of the Torque `loopback` MCP surface (task-scoped tools requiring no `task_id`), planted local task-bundle files (`tasks/README.md`, `task.md`, `task.json`, `process.md`) as the primary source of task identity, and a git-worktree-per-run workspace (`work_root` distinct from `repo_root`) — consistent with the `work_root`/worktree pattern documented for this project's own Cerberus/worktree conventions.
- All observed tool-calling errors in the Torque sessions (missing `ScheduleWakeup` prompt, stray `cd` into a stale relative path, an `awk` syntax error, a nonzero-exit git-alias probe) were self-diagnosed and corrected within one to two follow-up tool calls, with no narrated confusion, no repeated failed retries of the identical call, and no visible loss of task state.
- The one clear hallucination-shaped finding — claiming the envelope schema was "reviewed" in a run where no tool call actually read it — appeared in exactly one of the six otherwise-identical `nanite-boot-claude` runs; the other five either read the file or omitted any claim about it.
- The reviewer (`torque-boot-agentlaunch-da9ea7b4`) session functions as a closed loop against the implementer (`torque-boot-agentlaunch-34e6d12b`) session's own output: it re-derives task history purely from Torque/GitHub state (comments, AAR, PR), including detecting and citing an earlier (2026-05-26) reopening of the same ticket that a prior V1 reviewer round had missed.
