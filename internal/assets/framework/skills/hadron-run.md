# Hadron Run (:hadron-run)

Run a Hadron blueprint via MCP tools, with CLI fallback.

## When to use

- When a task can be automated via a Hadron blueprint
- Before manual execution — always check for an applicable blueprint first
- When the user explicitly requests a blueprint run

## Procedure

1. **Validate blueprint.** Use `mcp__hadron__hadron_blueprint_validate`. If unavailable, fall back to CLI: `hadron validate <blueprint>`. If validation fails, print error and exit.

2. **Review inputs.** Read the blueprint's `inputs:` section. Verify all required inputs are provided or have defaults.

3. **Execute blueprint.** Use `mcp__hadron__hadron_run_enqueue` with blueprint name and inputs. If MCP unavailable, fall back to CLI: `hadron run <blueprint> --input key=value`. Capture the run ID.

4. **Monitor execution.** Poll `mcp__hadron__hadron_run_get` every 5 seconds, up to the blueprint's timeout (default: 120s). Check for terminal status (completed, failed, canceled).

5. **Handle failure.** If the run fails:
   - Read run events via `mcp__hadron__hadron_run_events` for error details
   - Use the `:escalate` skill to create a repair task with the last ~50 lines of output as evidence
   - Emit failure signal

6. **Emit signal.**
   - Success: `**[blueprint]** <name> — completed (<run-id>)`
   - Failure: `**[blueprint]** <name> — FAILED (<run-id>)`

## Output

- Console: blueprint signal (success or failure)
- On failure: escalation via `:escalate` skill

## Invariants

- Never run a blueprint that fails validation
- Never run without verifying required inputs
- Prefer MCP tools over CLI when Hadron MCP server is available
- On failure, always escalate — never silently fail
- Poll interval: 5 seconds. Max wait: blueprint timeout or 120s default
