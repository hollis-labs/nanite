# Doc Note (:doc-note)

Store a durable project documentation note in Tesseract through a sub-agent.

## Input

```text
/doc-note [project] [type] [content]
```

The supported types are `architecture`, `decision`, `api`, `data`, `procedure`, `constraint`, `goal`, `note`, and `summary`. Types are stored as tags; the canonical Tesseract knowledge kind is `note`.

## Procedure

If project, type, content, current user id, agent id, or session id is unavailable, ask for it. Do not infer required identity.

Launch a sub-agent that validates the input, generates a key in `YYYYMMDD-HHMMSS-4random` form, and calls:

```json
mcp__tesseract__knowledge_write {
  "namespace": "user/{USER}/knowledge/{PROJECT}",
  "key": "{KEY}",
  "kind": "note",
  "source": "manual",
  "pointer_scheme": "nil",
  "pointer_locator": "{PROJECT}/docs/{KEY}",
  "summary": "{ONE-LINE SUMMARY}",
  "body": "{CONTENT}",
  "author_agent_id": "{AGENT_ID}",
  "session_id": "{SESSION_ID}",
  "tags": "[\"project:{PROJECT}\",\"doc-type:{TYPE}\"]"
}
```

The `nil` pointer scheme is intentional: the stored body is the artifact, so pointer health is `not_applicable`. `source`, both pointer fields, `summary`, `author_agent_id`, and `session_id` are required by Tesseract v0.9. Array-valued MCP arguments such as `tags` are JSON-encoded strings.

Return only:

```text
✓ Stored to user/{USER}/knowledge/{PROJECT}/{KEY} as {TYPE}
```

## Invariants

- Always use a sub-agent and `mcp__tesseract__knowledge_write`.
- Preserve content in `body`; do not expand or reinterpret it.
- Use the writable knowledge namespace shape `{user|app}/{id}/knowledge[/...]`.
- Reuse a stable key and set `supersedes` only when deliberately replacing a known revision.
- Tasks and execution state belong in Torque, not Tesseract.
