Run the deep-review skill for a project/system assessment. Target: $ARGUMENTS (project name, system area, or "full" for portfolio-wide).

Launch 3-4 parallel sub-agents to gather: (1) Code assessment — build status, test results, LOC, last activity; (2) Volon state — epics, sprints, tasks by status, stale items; (3) Docs & context — README, architecture docs, Cortex records, PCC; (4) Integration points — MCP tools, shared modules, portfolio connections.

Synthesize into a structured report: status summary, build/test results, Volon state, key findings (working/attention/broken), integration assessment, prioritized recommendations. Present to user for review. For each accepted recommendation, create a Volon task with description, acceptance criteria, and pointers.
