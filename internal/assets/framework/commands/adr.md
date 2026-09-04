Run the adr skill to capture an architectural decision. Takes an argument describing the decision.

Launch an Agent tool (subagent_type: general-purpose, model: sonnet) that:
1. Discovers the ADR directory relative to the current project as described in skills/adr.md, then globs ADR-*.md there to find the next ADR number
2. Writes a formal ADR file with sections: Status, Date, Context, Decision, Consequences, References
3. Optionally registers the file in Tesseract via `mcp__tesseract__knowledge_write` using kind `doc`, source `filesystem`, pointer scheme `file`, and the required summary/author/session fields

The decision to capture: $ARGUMENTS

If no arguments provided, ask the user what decision to capture.

The agent must return ONLY:
✓ ADR-NNN written: Title
→ path/to/file

Display the agent's response directly. No additional commentary.
