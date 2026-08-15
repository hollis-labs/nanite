# Product Recipe Packs

**Status:** Shipped in Nanite  
**Primary service:** `internal/service/durable_agent_recipes.go`

Nanite's durable-agent recipe catalog has two layers:

- stable substrate IDs that older callers can keep using;
- richer product recipe IDs for the main operator-facing setup stories.

Recipes are setup paths only. They compile into the existing durable-agent
instance, launch policy, wake payload, runtime kind, work root, metadata, and
planned non-secret injections. They do not introduce a second runtime stack.

## Stable IDs

These remain backward compatible:

- `project-advisor`
- `managed-cli-harness`
- `process-monitor`
- `template-worker`

## Product IDs

These are the richer operator-facing recipes:

- `architect-advisor`
- `orchestrator`
- `planner`
- `project-manager`
- `proxima-relay`
- `reviewer`
- `system-monitor`
- `task-writer`
- `web-chat-agent`
- `external-company-agent`

## Recipe Summary

| ID | Lifecycle | Runtime | Creates |
|---|---|---|---|
| `project-advisor` | `advisor` | `api` | Reusable project/topic advisor durable agent |
| `architect-advisor` | `advisor` | `api` | System design / architecture partner |
| `project-manager` | `advisor` | `api` | Work-coordination advisor: monitors task/dependency state, surfaces blockers, recommends dispatch timing |
| `proxima-relay` | `advisor` | `api` | Operator-facing concierge / relay durable agent |
| `reviewer` | `template` | `api` | One-shot, freeform acceptance-criteria review pass with checkpoint escalation |
| `managed-cli-harness` | `harness` | `streaming-stdio` | Managed headless CLI durable session |
| `orchestrator` | `harness` | `api` | Long-lived executive: polls Torque, dispatches ready work via workflow_run/subagent_spawn |
| `planner` | `template` | `api` | One-shot sequencing pass: goal/design doc → dependency-ordered Torque tasks |
| `process-monitor` | `process` | `api` | Substrate process monitor with fresh wake sessions |
| `system-monitor` | `process` | `api` | Product monitor with explicit wake-pass scheduling stance |
| `template-worker` | `template` | `api` | Substrate one-shot worker template |
| `task-writer` | `template` | `api` | One-shot task brief / plan writer |
| `web-chat-agent` | `advisor` | `api` | Durable advisor prepared for a web chat integration |
| `external-company-agent` | `advisor` | `api` | Durable advisor prepared for a company-system integration |

## What They Start Or Wake

- `advisor` recipes reuse or create an attached session through the normal durable-agent start path.
- `process` recipes compile to fresh-per-wake session policy and rely on explicit wake controls.
- `template` recipes compile to fresh one-shot run sessions.
- `harness` recipes compile to managed harness session reuse.

## What They Plant Or Preview

Recipes can describe planned non-secret injections such as:

- task briefs
- workspace or architecture notes
- relay or network notes
- monitor facts
- persona briefs

These are previews and planning artifacts in the current implementation. They
do not execute arbitrary callbacks and they do not bypass the existing runtime
bootdir and boot-plan seams.

## Catalog Loading

Nanite loads built-in recipes first, then applies configured catalog sources
from app config:

```yaml
recipes:
  catalog_paths:
    - /path/to/recipe/catalog-or-directory
```

Configured recipes override built-ins by matching `id`. Duplicate IDs across
configured sources fail service startup loudly. JSON files, YAML files, and
directories containing those files are supported.

## External-Facing Recipes

`web-chat-agent` and `external-company-agent` are intentionally conservative.

They do:

- create a durable agent with product-story metadata;
- point at `/api/harness/v1` as the integration surface;
- preserve operator-supplied display, scope, endpoint-purpose, and posture notes.

They do not:

- provision a web widget;
- provision Teams, Copilot, Microsoft 365, or company adapters;
- invent a second external protocol;
- create a second runtime launch path.

## System Monitor Stance

`system-monitor` makes the current wake and scheduling stance explicit:

- due work can be inspected;
- an explicit run-due pass can be triggered by an operator;
- no background poller is provisioned by the recipe.

## Example Dry Run

```json
{
  "name": "Web Support",
  "slug": "web-support",
  "profile_id": "profile-123",
  "metadata": {
    "public_display_name": "Support Concierge",
    "persona_scope": "Help users navigate the product"
  },
  "start": true
}
```

Applied to:

```text
POST /api/durable-agent-recipes/web-chat-agent/dry-run
```

This returns the compiled durable-agent instance, launch policy, wake payload,
session policy, planned injections, and any missing requirements.
