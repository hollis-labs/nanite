# Code Mode evals

These evals measure what the `subtask` tool buys. Each scenario runs twice: once with direct tool
use (the file tools, no `subtask`) and once with `subtask` offered as well. The two runs are then
compared from the turns' trace records.

## Scenarios

The scenarios live in `evals/testdata/scenarios`. Each one has an agent directory, a
fixture workspace and a task (`task.md`). Its grader is in `evals/scenario.go`.

| Scenario | Task | Grader checks |
|---|---|---|
| `code-search` | Find the functions in `src/` that read `APP_MODE`, and the modes they check. The answer is in two of eight files. | file and function names, and the three modes |
| `research` | Answer a question from a local corpus of seven documents, one of them superseded, and cite the source. | period, decider, date, cited file |
| `chore` | Append a line to each of six notes. Tool-heavy, and every write asks for approval. | every file changed exactly, and the count reported |
| `council` | Review a file for bugs. The subtask run uses an authored lead variant (`council`, `can_spawn`) that starts three read-only `reviewer` subtasks. | both planted bugs, with line numbers |

The graders are deterministic: they check facts in the answer and the state of the workspace, not
the style of the answer. The live run uses the same graders, with no model grading.

## Metrics

All of these come from the trace records (libs `agent/trace` v0.2.0), except approvals and wall
time.

- **Parent context tokens:** the input plus cache tokens of the root turn's own requests. This is
  what Code Mode is meant to shrink.
- **Total tokens:** the whole tree, which is the root turn's own usage plus each `subtasks[].usage`.
- **Estimated cost:** total tokens at one price. The price defaults to a deliberately high one
  (`evals.ConservativePrice`), so read these figures as a comparison between modes, not as a bill.
- **Wall time.**
- **Approvals:** how many times the gate was asked, including a subtask's asks relayed to the root.
- **Parent tool calls** and **subtasks:** how much the root did itself, and how many subtask
  conversations ran anywhere in the tree.

## Running

**Offline.** `go test ./evals` runs every scenario in both modes, against a scripted model
and the real engine and tools. CI runs it with the rest of the tests.
- The scripted model's usage is estimated from the size of each request, at four characters a
  token. So the offline numbers compare how much context each mode sends, and say nothing about
  model quality.
- The checked-in report is `offline.md` (`offline.json`). The test fails if the report goes stale.
  Regenerate it with `go test ./evals -run TestOfflineRun -update`.
- `go run ./cmd/nanite-eval` makes the same run and prints the table.

**Live.** This is opt-in and spends money. It never runs in CI.

```sh
go run ./cmd/nanite-eval -live -max-usd 10 -turn-tokens 150000 -out docs/evals/live-YYYY-MM-DD
```

- **Credentials:** the live run reads the provider key the way `nanite` does: from
  `NANITE_GATEWAY_URL` and `NANITE_GATEWAY_KEY_REF`, then the keychain, then
  `NANITE_PROVIDER_KEY_ENV=1`.
- **Model:** the scenarios' `host.yaml` names no model, so the provider's default chat model runs
  both the parent and its subtasks.
- **`-turn-tokens`:** caps each run, subtasks included, through `agent.Options.TurnTokenBudget`.
- **`-max-usd`:** caps the whole run. Every model response's usage is priced as it arrives. When the
  total reaches the cap, the run in progress is cancelled and the rest are skipped. The pricing
  flags (`-price-in`, `-price-out`, `-price-cache-read`, `-price-cache-write`) default to the
  conservative price, so the cap stops early rather than late.

## Offline results

From `offline.md`:
- With the subtask tool, the parent's context is 44–62% of the direct run's for the three
  single-subtask scenarios. The parent no longer carries the search results and file contents.
- The tree's total tokens are higher, 161–186% of the direct run's. A subtask starts with its own
  system prompt and part of the parent's history, and the parent then reads the subtask's result.
- The council run costs about seven times the direct review, and its parent context is slightly
  larger. That is expected: it buys three independent reviews, not a smaller parent.

## Recommendations

**`last_n` (default 6) and `result_cap` (default about 5,900 tokens): keep both for now.** Offline,
every subtask result was far under the cap, and none of these tasks needed the parent's history.
Neither default was exercised enough to justify a change. The live run will settle it. Things to
look at there:
- whether real results approach the cap (then consider a lower default for `research`, whose
  result is a short answer);
- whether subtasks ask for history with `parent_history_search` (if they never do, a smaller
  default `last_n` would cut each child's start-up tokens).

These recommendations will be updated with the first live comparison. Nothing here changes the
defaults.
