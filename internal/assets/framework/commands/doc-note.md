Store a documentation note in Vanta Conduit. Takes arguments: [project] [type] [content].

Launch an Agent tool (subagent_type: general-purpose, model: haiku) that:
1. Validates project is known (engine, conduit, vanta-conduit, hadron, nexus, cerberus, carrier, nanite, sigil, suds-v2, lnklst, _shared)
2. Maps type to Conduit record type (architecture→system/map, decision→decision/adr, api→contract/api, data→contract/data, procedure→runbook, constraint→strategy/constraints, goal→strategy/goal, note→note/volatile, summary→brief/summary)
3. Generates a timestamp key (YYYYMMDD-HHMMSS-4random)
4. Writes to Cortex via mcp__vanta__context_typed_write with namespace={project}/docs, status=draft

Input: $ARGUMENTS

If any of project, type, or content is missing, ask for the missing fields. Do not guess.

The agent must return ONLY:
✓ Stored to {project}/docs/{key} as {type} (draft)

Display the agent's response directly. No additional commentary.