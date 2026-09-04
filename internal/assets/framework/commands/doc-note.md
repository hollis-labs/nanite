Store a project documentation note in Tesseract. Takes arguments: [project] [type] [content].

Launch an Agent tool (subagent_type: general-purpose, model: haiku) that:
1. Resolves the current user id, agent id, and session id; asks if any required identity is unavailable
2. Validates type is one of architecture, decision, api, data, procedure, constraint, goal, note, or summary
3. Generates a stable timestamp key (YYYYMMDD-HHMMSS-4random)
4. Writes with `mcp__tesseract__knowledge_write` using namespace `user/{user}/knowledge/{project}`, kind `note`, source `manual`, pointer scheme `nil`, locator `{project}/docs/{key}`, summary and body, required author/session fields, and JSON-encoded tags `["project:{project}","doc-type:{type}"]`

Input: $ARGUMENTS

If project, type, content, or a required identity is missing, ask for it. Do not guess.

The agent must return ONLY:
✓ Stored to user/{user}/knowledge/{project}/{key} as {type}

Display the agent's response directly. No additional commentary.
