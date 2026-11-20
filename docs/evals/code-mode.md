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
`ANSWER: 30 days; Data Council; 2026-03-04; corpus/policy-2026.md`. The graders split that line on
`;` and check each field. An answer may name a decoy in its explanation to rule it out; a decoy on
the ANSWER line fails it, and so does an answer with no such line. Each field accepts the
reasonable ways to say it:
- **`research` period:** "30 days", "30-day", "30 calendar days" or "thirty days".
- **`council`:**
  - The loop bug needs line 17 (or L17) and one of: off-by-one, `<= len`, out of range, out of
    bounds.
  - The refund bug needs a line from 23 to 26 and one of: negative, below zero, `< 0`. A refund
    field calling it fine, or only asking for tests, does not count.
- **`inventory` owner:** must be exactly the top owner.

A subtask run that did not test what its arm is for is marked **invalid**, not a success. The report
says why:
- **Required variant:** a scenario can require one, and the run never ran it. `council` requires
  `council`, `history` requires `scribe`, and `inventory` requires `research`.
- **Facts already given (`history`, `last_n` 6 and 1):** a subtask's first message already held one
  of the facts it was meant to find, for example because the parent put them in the goal. Every
  request is checked as it goes to the model.
- **Context chosen by the parent (`history` arms only):** a `subtask` call set `context.last_n` or
  `context.full`, so the arm's `last_n` never applied. Other scenarios may set context freely.
- **Nothing stored (`inventory`, small cap):** no subtask result was stored behind a pointer.
  Whether the parent then fetched it is reported as data (parent fetches), not a validity rule.

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

**Stub tools:** a scenario's `Stubs` are host tools offered in every arm (group `eval-stubs`), so a
case can exercise an agent that depends on host tools without calling them. Each stub has a name
(1-64 letters, digits, `_` or `-`; not a built-in or reserved name, so an MCP tool cannot be stubbed
under its `mcp__` name), a description, an input schema and canned results. A call gets the first
result whose `when` fields equal its input's, otherwise the next ordered result (the last repeats).
Calls in one model step may run at once in any order, so give those a `when` to keep a run
deterministic. `read_only` works as for any host tool (read-only variants are offered it; whether a
call asks for approval depends on the permission mode). `Result.StubCalls` (JSON only) counts each
stub's calls in the task turn, subtasks' included. Bad stubs are refused before any run starts. The
checked-in scenarios use none.

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

**Before it starts,** it prints the provider, the model, the prices and the caps. It also prints a
loud warning when a price is below the model's list price (known for `claude-fable-5-2`,
`claude-opus-5-5`, `claude-sonnet-5-5` and `claude-haiku-5-5`), or when no list price is known for
the model. A low price makes the dollar cap stop late.

**The dollar cap:**
- Every model request, compaction summaries and argument repair included, first reserves its most
  expensive outcome. That is its input, estimated generously at three characters a token, plus all
  of its output allowance (`max_tokens`, or 16,384 when the request names none).
- A stream that finishes settles to its reported usage.
- A stream that is cut off, cancelled or reports no usage is charged at least its reservation. A
  provider may bill more than it reported before the cut.
- A failed request, or a failed summary or repair, is charged its reservation. A successful summary
  or repair reports no usage, so it is charged an estimate: its input at three characters a token
  and its answer at four.
- A request that does not fit beside other requests' reservations waits for them to settle (they
  usually settle well below their worst case). One that would pass the cap on its own is refused.
  That ends the run it belongs to, and the runs not yet started are skipped.
- Once the cap has refused a request, every later request is refused too, so runs in flight at
  that moment end at their next request.

**Results:** on any exit (finished, capped, Ctrl-C or an error), it writes what ran and the spend
so far. Error text is shortened, and anything that looks like a credential is removed, before it
goes into a report.

**Credentials:** it reads them the way `nanite` does: `NANITE_GATEWAY_URL` and
`NANITE_GATEWAY_KEY_REF`, then the keychain, then `NANITE_PROVIDER_KEY_ENV=1`.

**Working directory:** each run works in its own copy of the workspace through
`agent.Options.WorkDir`, so relative paths in the file tools resolve there. The harness never
changes the process's working directory. `Config.Parallel` (`-parallel`, default 1, at most 8)
runs that many at once; `Run` itself is safe to call concurrently, and results keep scenario and
arm order. The dollar cap is shared by every run in one `Run`. When a run fails with an error, the
runs in flight beside it are cancelled and reported as `stopped: another run failed`, and no
further run starts. MCP stdio servers start in the process's working directory, not the copy.

## Live results, 2026-11-20

One live run: `claude-sonnet-5-5` as parent and subtasks, with the $10 cap and 150,000 tokens a
run. The full table is in [live-2026-11-20/results.md](live-2026-11-20/results.md) (and `.json`).

**Spend.** The estimate at the conservative price was $4.83, under the $10 cap, and nothing was
skipped. At Sonnet 5.5's list price ($2 input and $10 output per million tokens, both 7.5 times
below the conservative price, with cache reads cheaper still), the run cost about $0.64 at most.
The estimate is above the sum of the per-run costs ($3.90), because requests that reported no
usage are charged their whole reservation.

**Outcomes.** 15 runs:
- 9 valid successes;
- 1 failure: the direct council review found the loop bug but missed the negative refund;
- 5 invalid: all three `history` subtask arms, and both `inventory` subtask arms.

**What the numbers show:**
- **Small tasks were not delegated.** In `code-search`, `research` and `chore`, the model had
  `subtask` and did not use it: no subtasks, and the same work as the direct run. The parent's
  context was 10–12% larger, which is the cost of offering the tool (its schema and the base
  prompt's delegation section). All six runs succeeded. These tasks fit easily in one context, so
  not delegating them was a reasonable choice.
- **The council worked, and costs what it buys.** Told to review through the `council` variant,
  the model started the lead, and the lead started three reviewers (4 subtasks). The review found
  both bugs where the direct review missed one. It used 8.2 times the tokens (60,728 against
  7,402) and took 25 seconds against 7.
- **`history` did not test `last_n`.** The model delegated to `scribe` in all three arms, but every
  call set the subtask's context itself (`context.last_n` or `context.full`), so the variant's
  `last_n` never applied. The harness marks these runs invalid: the 1, 6 and 20 arms ran the same
  context and used the same tokens (about 37,900). The subtasks read and wrote files; none needed
  `parent_history_search`.
- **`inventory` did not test the result cap.** Despite the instruction to collect long lists
  through `research`, the model read the files itself in both subtask arms. No subtask ran, so
  nothing was stored or fetched (0 parent fetches anywhere in the run).
- **Approvals** were as expected: 6 in each `chore` run, all the parent's own because no subtask
  ran, and 1 in each `history` run.
- **Compactions:** none.

## Recommendations

These are recommendations only; nothing here changes the defaults.

- **`last_n`: keep the default of 6.** The live run gives no evidence either way. When Sonnet 5.5
  delegated work that depended on history, it chose the subtask's context itself instead of
  relying on the variant's `last_n`. So the default matters less than how well the parent picks
  context, and that is what the next `history` run should measure. Either count a parent's own
  context choice as data rather than invalid, or add an arm where the call cannot set context.
- **`result_cap`: keep the default of about 5,900 tokens.** No live run returned a large result
  through a subtask, so the cap was never exercised. The scripted check shows the mechanism works:
  at 1,500 tokens the parent fetches the stored result. Whether a smaller default saves anything
  needs a scenario the model actually delegates.
- **For the evals themselves:**
  - Give `inventory` a task too big to do in one context, so delegation is the natural choice.
  - Make `history` require delegation without suggesting a context.
  - Keep the council scenario as the reference case: it is where Code Mode changed the outcome.
