# Specialist agent template

A starter for an agent that owns one process, capability or knowledge area
and stays around: other people and agents come to it for that area.

```sh
cp -r examples/specialist ./my-specialist
nanite validate ./my-specialist
```

## What to change

| File | Change |
|---|---|
| `agent.md` | `definition_id`, `name`, `description`, `behavior.purpose`, and the whole charter below the frontmatter. |
| `host.yaml` | `application_id`; `daily_token_budget` to suit the agent. |
| `context/area.md` | The stable facts the agent needs on every turn. |
| `skills/` | One skill per procedure the agent follows; keep `memory`. |

## Charter conventions

The charter is the Markdown body of `agent.md`. Every specialist charter has
the same five sections, so people and other agents can read any of them the
same way:

1. **Role**: one sentence on the area and what owning it means.
2. **You own**: the processes, capabilities and artifacts the agent is
   responsible for.
3. **Not yours**: the neighbouring areas and who owns them, so the agent
   hands work over instead of doing it.
4. **How you work**: recall first, ask only when the answer changes the work,
   verify, record what matters.
5. **Standards**: brevity, honesty about what was not verified, and no
   secrets.

Keep the charter under a page. Longer procedures and reference material go
in `skills/` (fetched on demand with `skill_get`); facts needed on every turn
go in `context/`.

## Memory

The agent's own history is always searchable (`history_search`,
`history_get`). Shared, durable memory comes from the memory service's tools;
the `memory` skill says how to recall and what to record, and keeps writes
in the agent's own namespace.

## Where it runs

- **Inside a host app** (for example a Surface module bound to this folder):
  the host supplies the model, gateway and tools, including memory. The host
  uses `agent.md`, `context/`, `skills/`, `variants/`, `steering.json` and
  the `runtime` keys in `host.yaml`; `.mcp.json` is ignored.
- **Standalone** with the `nanite` CLI: add a `.mcp.json` registering your
  MCP gateway, so the memory tools appear as `mcp__gateway__*`:

  ```json
  {"mcpServers": {"gateway": {"type": "http", "url": "https://mcp-gateway.example.test/mcp"}}}
  ```

  and configure the model gateway as described in
  [the agent directory reference](../../docs/agent-directory.md). To use
  Warden tokens instead of static credentials, add an `identity.yaml`
  ([Identity and Warden auth](../../docs/agent-directory.md#identity-and-warden-auth)).
