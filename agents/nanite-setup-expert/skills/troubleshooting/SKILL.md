---
name: troubleshooting
description: Symptom-to-cause table for Nanite agents that will not open, wait, refuse work or lose context.
---
# Troubleshooting

Start from the symptom. Ask for the exact error text and the log lines
around it; never ask for keys or tokens.

| Symptom | Likely cause | Fix |
|---|---|---|
| `nanite validate` fails naming a file or key | Unknown or malformed field (the loader is strict) | Fix the named key; compare with `docs/agent-directory.md` |
| Agent will not open: lock held | Another process has the folder open | Stop the other process; one process per agent folder |
| Turn waits "for approval" in the chat | A missing Warden grant | Ask Cerberus about the grant; in auto mode it fails fast instead |
| Tool package asks again after an upgrade | Approvals moved out of the agent folder in Nanite 0.8; a changed package digest also asks again | Approve once more; set the host's data dir so approvals persist |
| "daily token budget" refusal | The per-day cap was reached | Wait for the UTC day or raise `daily_token_budget` (owner's call) |
| Everything refused after a stop | `StopAll` / `nanite stop --all` latches until reopen | Reopen the agent (in a host: close and open it) |
| Agent forgets recent tool results | Compaction thresholds too low for its tool output | Raise `compact_at` / `compact_keep_tokens`; Nanite 0.9+ keeps the latest tool step |
| Context files missing from turns | The host's context budget cut them | Shorten `context/`; move detail into skills |
| Turn stops at the step limit | `max_steps` reached | Raise it, or split the work into subtasks; the turn ends with a closing reply |
| Model call 401 / 403 | Gateway key or token wrong, or the grant lacks the scope | Check the key file mode and path, or the Warden grant |

If none fits, collect: Nanite version, host (CLI or app), the folder's
`host.yaml`, and the first error, then hand it to the Nanite lead.
