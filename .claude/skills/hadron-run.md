# Hadron Run

Run a Hadron blueprint via MCP tools, with CLI fallback.

## When to use

- When a task can be automated via a Hadron blueprint
- Before manual execution — always check for an applicable blueprint first
- When the user explicitly requests a blueprint run

## Procedure

1. **Validate blueprint.** Use MCP `hadron_blueprint_validate` (or CLI `hadron validate`). If validation fails, print error and exit.

2. **Review inputs.** Read the blueprint's `inputs:` section. Verify all required inputs are provided or have defaults.

3. **Execute blueprint.** Use MCP `hadron_run_enqueue` with blueprint path and inputs. Capture the run ID.

4. **Monitor execution.** Poll `hadron_run_get` or `hadron_run_events` for completion status.

5. **Handle failure.** If the run fails:
   - Use the escalate skill to create a repair task
   - Include last ~50 lines of output as evidence
   - Emit failure signal

6. **Emit signal.**
   - Success: `**[blueprint]** <name> — completed (<run-id>)`
   - Failure: `**[blueprint]** <name> — FAILED (<run-id>)`

## Output

- Console: blueprint signal (success or failure)
- On failure: escalation via escalate skill

## Invariants

- Never run a blueprint that fails validation
- Never run without verifying required inputs
- Prefer MCP tools over CLI when Hadron MCP server is available
- On failure, always escalate — never silently fail
