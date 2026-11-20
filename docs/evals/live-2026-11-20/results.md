Model: claude-sonnet-5-5. Price per million tokens: $15 input, $75 output, $1.5 cache read, $18.75 cache write. Estimated spend: $4.8326.

| Scenario | Arm | Success | Parent context tokens | Total tokens | Est. cost (USD) | Wall | Approvals | Parent tool calls | Subtasks | Variants | Subtask tools | Parent fetches | Compactions |
|---|---|---|---:|---:|---:|---:|---:|---:|---:|---|---|---:|---:|
| code-search | direct | yes | 16056 | 16553 | 0.2232 | 7.3s | 0 | 3 | 0 | - | - | 0 | 0 |
| code-search | subtask | yes | 17970 | 18432 | 0.2345 | 6.3s | 0 | 3 | 0 | - | - | 0 | 0 |
| research | direct | yes | 19166 | 19515 | 0.1667 | 3.5s | 0 | 2 | 0 | - | - | 0 | 0 |
| research | subtask | yes | 21073 | 21582 | 0.1789 | 5.7s | 0 | 3 | 0 | - | - | 0 | 0 |
| chore | direct | yes | 25553 | 27403 | 0.2933 | 12.2s | 6 | 15 | 0 | - | - | 0 | 0 |
| chore | subtask | yes | 28743 | 30578 | 0.3079 | 12.4s | 6 | 15 | 0 | - | - | 0 | 0 |
| council | direct | no: no field names the negative refund (lines 23-26) | 6654 | 7402 | 0.0951 | 7.0s | 0 | 1 | 0 | - | - | 0 | 0 |
| council | subtask | yes | 13036 | 60728 | 0.8237 | 25.4s | 0 | 2 | 4 | council, reviewer | read_file, return_result, search_files, subtask | 0 | 0 |
| history | direct | yes | 10510 | 10861 | 0.0530 | 4.4s | 1 | 2 | 0 | - | - | 0 | 0 |
| history | subtask | no: invalid: a subtask call set context.last_n or context.full, so the arm's last_n did not apply | 18792 | 37874 | 0.2315 | 8.1s | 1 | 2 | 1 | scribe | read_file, return_result, write_file | 0 | 0 |
| history | subtask last_n=1 | no: invalid: a subtask call set context.last_n or context.full, so the arm's last_n did not apply | 18716 | 37891 | 0.2060 | 12.1s | 1 | 2 | 1 | scribe | read_file, return_result, write_file | 0 | 0 |
| history | subtask last_n=20 | no: invalid: a subtask call set context.last_n or context.full, so the arm's last_n did not apply | 18660 | 37883 | 0.2177 | 11.2s | 1 | 2 | 1 | scribe | read_file, return_result, write_file | 0 | 0 |
| inventory | direct | yes | 24969 | 26331 | 0.3413 | 9.4s | 0 | 3 | 0 | - | - | 0 | 0 |
| inventory | subtask | no: invalid: the research variant did not run | 26883 | 28084 | 0.3322 | 8.9s | 0 | 3 | 0 | - | - | 0 | 0 |
| inventory | subtask result_cap=1500 | no: invalid: the research variant did not run | 12770 | 13647 | 0.1970 | 7.2s | 0 | 1 | 0 | - | - | 0 | 0 |

- code-search, subtask: parent context 112% of the direct run's; total tokens 111%.
- research, subtask: parent context 110% of the direct run's; total tokens 111%.
- chore, subtask: parent context 112% of the direct run's; total tokens 112%.
