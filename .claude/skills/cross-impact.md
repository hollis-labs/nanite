# cross-impact

Analyze how a change in one project affects other projects in the Fragments Engine portfolio. Checks shared modules, MCP contracts, config dependencies, and API surfaces.

## Usage
`/cross-impact [scope]`

**scope** (optional): What to analyze. Defaults to uncommitted changes in the current project.
- `staged` — analyze staged changes only
- `branch` — analyze all changes on current branch vs main
- `<file-path>` — analyze impact of changes to a specific file
- `<module-name>` — analyze impact of changes to a shared module (e.g., "core/otel", "mcp-helpers")

## Examples
- `/cross-impact` — analyze uncommitted changes
- `/cross-impact branch` — analyze full branch impact
- `/cross-impact internal/provider/anthropic.go` — impact of provider changes
- `/cross-impact core/otel` — impact of otel module changes

## Instructions

1. **Identify the change set**:
   - Use git diff (appropriate to scope) to get changed files
   - Categorize changes: Go code, config, MCP tools, API endpoints, shared modules

2. **Load the project registry**:
   - Read `config/repos.yaml` to get all managed projects and their paths
   - For each project, check `go.mod` for shared module dependencies

3. **Analyze impact by category**:

   ### Shared Module Changes
   If changed files are in a shared module (tiamat-otel, tiamat-mcp-helpers, tiamat-tool-broker, or future core/*):
   - Find all projects that import this module (check `go.mod` and `go.sum`)
   - List specific packages imported
   - Check if the change is breaking (function signature changes, removed exports)
   - Check if `replace` directives need updating

   ### MCP Tool Contract Changes
   If changes affect MCP tool definitions (tool names, parameter schemas, return types):
   - Identify which MCP server owns the tool
   - Find all projects that call this tool (search for tool name in code and skills)
   - Check if parameter changes are backwards-compatible
   - Flag skills/commands that reference the changed tool

   ### API Endpoint Changes
   If changes affect HTTP API endpoints:
   - Check if other services call these endpoints
   - Check if the frontend consumes them
   - Flag breaking changes (removed endpoints, changed request/response schemas)

   ### Config Changes
   If changes affect config files (yaml, json, toml):
   - Check if other projects share or reference this config
   - Check if environment variables changed
   - Flag changes to ports, URLs, or service names

   ### Database Schema Changes
   If changes affect migrations or schema:
   - Check if other services share the database
   - Flag column renames, type changes, or dropped tables

4. **Risk assessment**:
   - **No impact**: Changes are fully internal to this project
   - **Low impact**: Changes affect shared modules but are backwards-compatible
   - **Medium impact**: Changes require updates in other projects but won't break them
   - **High impact**: Breaking changes that will cause failures in other projects

5. **Generate the report**:

```
=== CROSS-IMPACT ANALYSIS ===
Source: <project_id> (<scope description>)
Changed files: <N>

IMPACT LEVEL: <None | Low | Medium | High>

AFFECTED PROJECTS:
  <project_id> — <impact level>
    Reason: <what's affected and why>
    Files at risk: <specific files in that project>
    Action needed: <what to do>

  <project_id> — <impact level>
    ...

SHARED MODULE IMPACT:
  <module_name>:
    Imported by: <list of projects>
    Change type: <backwards-compatible | breaking>
    Details: <what changed>

MCP CONTRACT IMPACT:
  <tool_name>:
    Server: <which MCP server>
    Consumers: <list of projects/skills using this tool>
    Change: <parameter added/removed/renamed>

CONFIG IMPACT:
  <config_key>:
    Used by: <list of projects>
    Change: <what changed>

RECOMMENDED ACTIONS:
  1. <action for highest-impact item>
  2. <action for next item>
  ...

NO IMPACT:
  <list of projects confirmed unaffected>
=== END ANALYSIS ===
```

## When to Use
- Before committing changes to shared modules
- Before merging PRs that touch cross-project code
- When modifying MCP tool schemas or API contracts
- When the user asks "will this break anything?"
