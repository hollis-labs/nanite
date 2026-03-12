Run the pcc-sync skill. Synchronize Project Context Cache to Cortex:

1. Read PCC from the target project's `.agentrc/pcc/global/` directory (5 files: 01_overview through 05_decisions)
2. Write to Cortex via MCP `context_write` tool (namespace: `app/mentat/pcc/<project-id>`, key: `pcc.<filename>`)

If called with `--all`, sync all projects from `config/repos.yaml`. If called with `--project <id>`, sync just that project. If called with no args, infer the project from the current working directory.

Cortex sync is best-effort — warn but don't fail if Cortex MCP is unavailable.

$ARGUMENTS
