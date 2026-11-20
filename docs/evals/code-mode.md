# Code Mode evals

These evals measure what the `subtask` tool buys. Each scenario runs in several arms:
- **`direct`:** the file tools, no `subtask`.
- **`subtask`:** the same tools plus `subtask`, with default settings.
- **Settings arms:** for some scenarios, extra `subtask` arms that vary one setting.

The arms are compared from the turns' trace records. The harness is the `evals` package and
`cmd/nanite-eval`. It is experimental, and its API may change while the N6-10 evals work builds on it.

## Scenarios

The scenarios live in `evals/testdata/scenarios`. Each one has:
- an agent directory;
- a fixture workspace;
- a task (`task.md`);
- for one scenario, earlier messages sent first in the same conversation (`prelude.txt`).

The graders are in `evals/scenario.go`.

| Scenario | Task | Arms | Grader checks |
|---|---|---|---|
| `code-search` | Find the functions in `src/` that read `APP_MODE`, and the modes they check. Two of the nine files read it; a third mentions it and reads `APP_MODES` instead. | direct, subtask | both functions and all three modes, as whole words; neither decoy function |
| `research` | Answer from a local corpus of seven documents, one of them superseded, and cite the source. | direct, subtask | period, decider, date, cited file; not the superseded decider |
| `chore` | Append a line to each of six notes. Every write asks for approval. | direct, subtask | every file changed exactly; the count stated as 6 or "six", not the 6 in a date |
| `council` | Review a file for bugs. The agent is told to review through the authored `council` lead variant, which starts three read-only `reviewer` subtasks. | direct, subtask | both planted bugs, with line numbers |
| `history` | Write a file from facts given earlier: a codename that was later corrected, a date and an owner. They sit in the first three of eight earlier messages. The agent is told to delegate writing to the `scribe` variant. | direct, subtask (`last_n` 6), `last_n` 1, `last_n` 20 | the file has exactly three lines with the corrected codename (never the old one, anywhere in the file), the date and the owner |
| `inventory` | Collect all 150 TODO comments in `src/`, then report the total and the top owner. The full list is about 2,700 tokens. The agent is told to collect long lists through the `research` variant. | direct, subtask (cap about 5,900 tokens), `result_cap` 1,500 | the total and the top owner |

The prompts are the same in every arm. Only whether `subtask` is offered, and the arm's setting,
differ.

Every task asks for a final line in a fixed form, such as
`ANSWER: 30 days; Data Council; 2026-03-04; corpus/policy-2026.md`. The graders check facts and
decoys on that line only, so an answer may name a decoy in its explanation to rule it out. An
answer with no such line fails.

A subtask run that did not test what its arm is for is marked **invalid**, not a success. The report
says why:
- **Required variant:** a scenario can require one, and the run never ran it. `council` requires
  `council`, `history` requires `scribe`, and `inventory` requires `research`.
- **Facts already given (`history`, `last_n` 6 and 1):** a subtask's first message already held one
  of the facts it was meant to find, for example because the parent put them in the goal. Every
  request is checked as it goes to the model.
- **Context chosen by the parent:** a `subtask` call set `context.last_n` or `context.full`, so the
  arm's `last_n` never applied.
- **Nothing stored (`inventory`, small cap):** no subtask result was stored behind a pointer and
  fetched by the parent.

The graders are deterministic and check facts. The live run uses them too; there is no model
grading.

**`last_n` and `result_cap`:** only `history` and `inventory` test these. The first four scenarios
need no conversation history, and none of their results comes near the cap, so they say nothing
about either setting.
- `history` varies the `scribe` variant's `last_n`. A variant cannot set 0, which means the default
  of 6, so the smallest arm is 1. With 1 or 6, the facts fall outside what the subtask is given: it
  has to find them with `parent_history_search`. With 20 they are in its first message.
- `inventory` varies `SubtaskResultCap`. At 1,500 tokens the list is over the cap: the parent
  receives a pointer and has to fetch the stored result.

## Metrics

All of these come from the task turn's trace records (libs `agent/trace` v0.2.0), except approvals
and wall time. A prelude's turns are left out.
- **Parent context tokens:** the input plus cache tokens of the root turn's own requests.
- **Total tokens:** the whole tree, which is the root turn's own usage plus each `subtasks[].usage`.
  - A background subtask still running at the end of the turn has no usage in the record, so the
    harness leaves background subtasks out of its totals. No scenario starts one.
- **Estimated cost:** total tokens at one price. The default price is deliberately high
  (`evals.ConservativePrice`): read it as a comparison, not as a bill.
- **Wall time.**
- **Approvals:** how often the gate was asked, including a subtask's asks relayed to the root.
- **Parent tool calls, subtasks and variants:** how much the root did itself, how many subtask
  conversations ran anywhere in the tree, and which variants they ran.
- **Subtask tools and parent fetches:** the tools the subtasks called, such as
  `parent_history_search`, and how many times the root called `fetch_tool_result`. They show
  whether a run did what its arm is about.
- **Compactions:** compaction passes anywhere in the tree.

For a live run, the report adds one line per successful pair of a direct run and a subtask arm.
The line gives the subtask arm's parent context and total tokens as a share of the direct run's.

## Scripted harness check (not evidence)

`go test ./evals` runs every scenario in every arm against a scripted model, using the real engine,
tools and trace records. CI runs it with the rest of the tests.
- **What the script does:** it makes the same calls in every arm.
- **Token counts:** these are estimates from request size. The run's directory, the platform, IDs
  and numbers are normalised, so the report is identical on every machine.
- **What it shows:** that the harness, the engine and the trace records fit together. It says
  nothing about Code Mode or any model.
- **The report:** it gives the model as `scripted` and makes no comparisons. The checked-in copy is
  `offline.md` (`offline.json`). The test fails if it goes stale; regenerate it with
  `go test ./evals -run TestOfflineRun -update`.

`go run ./cmd/nanite-eval` makes the same run and prints the table.

## Live run

This is opt-in and spends money. It never runs in CI.

```sh
go run ./cmd/nanite-eval -live -model claude-sonnet-5-5 -max-usd 10 -turn-tokens 150000 -out docs/evals/live-YYYY-MM-DD
```

**Model:** `-model` (default `claude-sonnet-5-5`) is written into every run's `host.yaml`, so the
parent and its subtasks use it whatever the environment's default. A model that doesn't belong to
`-provider` is refused.

**Bounds,** each required:
- `-max-usd` above 0 and at most 50.
- `-turn-tokens` above 0. It caps one run's task turn, subtasks included, through
  `TurnTokenBudget`.
- Every `-price-*` at least $0.01 per million tokens. The defaults are deliberately high.
- A `-model` made of lower-case letters, digits, dots and dashes.

**Before it starts,** it prints the provider, the model, the prices and the caps.

**The dollar cap:**
- Every model request, compaction summaries and argument repair included, first reserves its most
  expensive outcome. That is its input, estimated generously at three characters a token, plus all
  of its output allowance (`max_tokens`, or 16,384 when the request names none).
- A stream that finishes settles to its reported usage.
- A stream that is cut off, cancelled or reports no usage is charged at least its reservation. A
  provider may bill more than it reported before the cut.
- A failed request, or a failed summary or repair, is charged its reservation. A successful summary
  or repair reports no usage, so it is charged an estimate of four characters a token.
- A request that would pass the cap is refused. That ends the run in progress, and the rest are
  skipped.

**Results:** on any exit (finished, capped, Ctrl-C or an error), it writes what ran and the spend
so far. Error text is shortened, and anything that looks like a credential is removed, before it
goes into a report.

**Credentials:** it reads them the way `nanite` does: `NANITE_GATEWAY_URL` and
`NANITE_GATEWAY_KEY_REF`, then the keychain, then `NANITE_PROVIDER_KEY_ENV=1`.

**One run at a time:** each run moves the process into its own copy of the workspace, because the
file tools resolve relative paths against the working directory. The harness therefore runs
scenarios one at a time. Nothing else in the process may rely on the working directory while it
runs.

## Recommendations

**None yet.**
- The scripted check can't support one.
- No live run has been made.
- Only `history` and `inventory` exercise `last_n` and `result_cap`.

The first live comparison will report on:
- **`history`:** whether the subtask finds early facts with `parent_history_search` when `last_n` is
  small, or needs a larger default.
- **`inventory`:** what a small cap costs when the parent has to fetch the stored result.

Nothing here changes the defaults.
