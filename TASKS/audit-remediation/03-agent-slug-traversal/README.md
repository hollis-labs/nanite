# Agent slug traversal — Wave 1

This folder is Wave 1 (release-blocking trust boundaries) per the remediation
guide's §4: `GO-AGENT-001` is a HIGH-confidence, HIGH-severity path-traversal/
arbitrary-file-overwrite gap reachable from the direct agent-CRUD HTTP API,
with `GO-AGENT-002` (0.0% test coverage on the exact files a fix would touch)
as its directly-tied testing-gap sibling — the same "GUI/API path never
back-ported a newer/correct primitive that a sibling caller already uses"
pattern the audit's cross-cutting synthesis (REPORT.md §9.4) names as the
recurring cause behind this wave's other two groups (plugin-install
convergence, Linux sandbox fail-open). It belongs this early because the
blast radius is a filesystem write, not merely a read, and because the
mitigating primitives it needs (`internal/pathsafe.ResolveUnder`, the slug
regex in `internal/builders/agent_builder.go`) already exist in the
codebase — this is a "wire the existing lock," not a new-primitive design
task.

One task file: **`01-canonical-slug-path-validation.md`**.
