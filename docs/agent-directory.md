# Agent directory

An authored agent directory contains `agent.md` and `host.yaml`. It can also
include `.mcp.json`, `permissions.yaml`, `skills/`, and `context/`.
Nanite reloads these inputs at the start of each turn.

## Shell environment

`host.yaml` can define literal, non-secret shell variables under `runtime.env`.
These values are passed to approved Bash commands.

```yaml
runtime:
  env:
    PROJECT_MODE: development
```

Keep credentials out of `host.yaml`. Use a secret reference in the setting that
consumes the credential, for example an MCP server's environment entry in
`.mcp.json`. `runtime.env` values are passed literally.
