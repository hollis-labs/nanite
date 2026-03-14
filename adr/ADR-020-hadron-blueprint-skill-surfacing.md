# ADR-020: Surface Hadron Blueprints as Conversational Skills via Tool Broker Rules

**Status:** Accepted
**Date:** 2026-03-14
**Deciders:** chrispian, Mentat

## Context

Hadron auto-registers every blueprint in `~/.hadron/blueprints/` as an MCP tool at startup. There are currently 78 blueprints covering builds, tests, audits, backups, deployments, and game orchestration. Each blueprint's inputs automatically derive the MCP tool schema.

However, Mentat's tool broker **blanket-excludes all `hadron_bp_*` tools** via a default rule in `tiamat-tool-broker/default-rules.yaml`. This means 78 ready-to-use capabilities are invisible to the chat experience.

## Decision

Replace the blanket exclusion with **intent-based inclusion rules** that surface relevant blueprints when the user's intent matches.

### Rule Examples

| Intent Keywords | Blueprints Surfaced |
|----------------|-------------------|
| "run tests", "test project" | `hadron_bp_test_go_project`, `hadron_bp_lint_go_project` |
| "build", "compile" | `hadron_bp_build_*` |
| "check drift", "dependency" | `hadron_bp_dependency_drift_detector` |
| "backup", "snapshot" | `hadron_bp_backup_*`, `hadron_bp_time_machine_snapshot` |
| "audit", "compliance" | `hadron_bp_*audit*`, `hadron_bp_otel_compliance_checker`, `hadron_bp_api_contract_validator` |
| "release", "tag" | `hadron_bp_tag_release`, `hadron_bp_release_notes_compiler` |
| "docker", "container" | `hadron_bp_docker_*` |
| "health", "status" | `hadron_bp_project_health_dashboard`, `hadron_bp_port_conflict_scanner` |
| "standup", "report" | `hadron_bp_sprint_standup_generator` |
| "cleanup", "nightly" | `hadron_bp_nightly_cleanup`, `hadron_bp_dotfile_backup` |

### Exclusion retained for
- SUDS game blueprints (`hadron_bp_suds_*`) — surfaced only on explicit game intent
- Clone/scaffold blueprints — rarely needed conversationally

## Consequences

### Positive
- 45+ existing blueprints become conversationally accessible with zero new code
- Users can say "run the tests for cortex" and it works
- Hadron's value is multiplied — blueprints become first-class skills

### Negative
- More tools in the selection pool may increase noise (mitigated by intent-based rules)
- Blueprint execution still happens via Hadron daemon — must be running

### Risks
- Intent keyword collisions (e.g., "build" matching too many blueprints)
- Tool broker token budget may need adjustment if many blueprints are selected simultaneously
