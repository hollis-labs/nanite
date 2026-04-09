# Doc Note (:doc-note)

Store a documentation note in Vanta Conduit. Runs via sub-agent to keep main context clean.

## When to use

- When the user types `:doc-note` or `/doc-note` with content to record
- When any agent needs to persist a documentation note during work
- Example: `/doc-note engine architecture "Engine uses a plugin-based runner with provider strategy"`
- Example: `/doc-note nexus decision "ADR-034 established Nexus as the agent infrastructure lib"`

## IMPORTANT: Run in Sub-Agent

This skill MUST be executed via the Agent tool (subagent) to keep the main context clean. The sub-agent validates, maps, writes to Vanta Conduit, and returns a one-line confirmation.

## Input Format

```
/doc-note [project] [type] [content]
```

**project** — Target project namespace. Must be one of the known projects or `_shared` for cross-project docs.

**type** — Document type. One of:
| Input       | Conduit Type          | Use for                              |
|-------------|-----------------------|--------------------------------------|
| architecture | system/map           | Structure, topology, how things connect |
| decision    | decision/adr          | Choices made and why                  |
| api         | contract/api          | API contracts, interfaces             |
| data        | contract/data         | Data models, schemas                  |
| procedure   | runbook               | How-to, step-by-step processes        |
| constraint  | strategy/constraints  | Design rules, invariants              |
| goal        | strategy/goal         | Objectives, success criteria          |
| note        | note/volatile         | Ephemeral rough thought (14-day TTL)  |
| summary     | brief/summary         | Session/sprint summary (90-day TTL)   |

**content** — The note text. Can be quoted or unquoted.

## Known Projects

engine, conduit, vanta-conduit, hadron, nexus, cerberus, carrier, nanite, sigil, suds-v2, lnklst, _shared

## Procedure

Parse the user's input for project, type, and content. If any field is missing or ambiguous, ask — do not infer.

Launch an Agent with this prompt (fill in from parsed input):

```
You are a documentation clerk for Fragments Engine. Your only job is to store a note in Vanta Conduit.

## Input:
- Project: {PROJECT}
- Type: {TYPE} (maps to Conduit type: {CONDUIT_TYPE})
- Content: {CONTENT}

## Steps:

1. Validate the input:
   - Project must be one of: engine, conduit, vanta-conduit, hadron, nexus, cerberus, carrier, nanite, sigil, suds-v2, lnklst, _shared
   - Type must map to a known Conduit type (see table above)
   - Content must be non-empty
   - If any validation fails, return: ✗ Missing or invalid: {field}. Provide {what's needed}.

2. Generate a key:
   - Format: {timestamp}-{4-char-random}
   - Example: 20260321-143022-a7f2

3. Write to Vanta Conduit:
   - Use mcp__cortex__context_typed_write with:
     - namespace: {PROJECT}/docs
     - key: {generated-key}
     - record_type: {CONDUIT_TYPE}
     - status: draft
     - payload: the content text
     - actor: doc-note

4. Return ONLY this format:
   ✓ Stored to {PROJECT}/docs/{key} as {CONDUIT_TYPE} (draft)
```

## Output

Display the sub-agent's one-line confirmation. No additional commentary needed.

## Invariants

- ALWAYS run via sub-agent
- ALWAYS write with status: draft — never canonical
- If project or type is unclear, ASK. Do not guess or infer.
- Namespace pattern: {project}/docs — keeps all project context together
- Keys are auto-generated timestamps, never human-meaningful
- This skill writes only. It does not retrieve, search, or summarize.
- Content is stored as-is. The clerk does not edit, expand, or interpret.
