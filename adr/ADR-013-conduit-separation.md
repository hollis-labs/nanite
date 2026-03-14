# ADR-013: Separate Chat App (Conduit) from Mentat Agent

**Status:** Accepted
**Date:** 2026-03-13
**Context:** Mentat identity crisis

## Problem

"Mentat" currently means 6 different things:
1. The GUI chat application (binary, repo)
2. A cognitive AI agent persona (agent profile in DB)
3. A CLI orchestration workflow (meta-agent boot profile)
4. A system prompt mode in Volon's GUI server
5. An envelope type (`type: "mentat"`)
6. Tool name prefixes (`mentat_open_sprint_planning`)

This creates confusion for users ("is Mentat the chat app or the agent?"), for agents (they conflate the app with the persona), and for developers (code search for "mentat" returns 6 contexts).

## Decision

**Separate the chat application from the Mentat agent.**

### Conduit (the chat app)
- Rename the chat application to **Conduit**
- Conduit is a provider-agnostic, multi-agent chat harness
- It is infrastructure — it doesn't think, agents think
- Any Special Agent can run inside it (Mentat, project-specific agents, etc.)
- Repo: `hollis-labs/conduit`, binary: `conduit`, directory: `~/Projects-apps/conduit/`

### Mentat (the Special Agent)
- Mentat becomes a defined Special Agent with: role, responsibilities, domain, capabilities
- It runs inside Conduit (GUI) or via agentrc boot profiles (CLI)
- Responsibilities: context continuity, cognitive aid, planning, cross-project coordination
- Selecting the "Mentat" agent profile in Conduit loads its specific system prompt, tools, and defaults

### Central Agent Directory
- User-level: `~/.agentrc/` — shared config, boot profiles, global settings
- Project-level: `<project>/.agentrc/` — project-specific overrides
- Agent boot includes `project_root` to scope file operations to the target project
- Agents no longer need to run from inside each project directory

## Consequences

- Clear separation: Conduit = harness, Mentat = agent, agentrc = config spec
- Less confusion when agents boot (they know what project they're working on)
- Chat app becomes reusable for any agent profile, not tied to Mentat
- Provider abstraction can be extracted to fe-core for all apps
- Requires rename (mentat → conduit) similar to previous mentat-chat → mentat rename
- Volon's hardcoded "mentat mode" in GUI server needs refactoring
- Envelope types and tool prefixes need genericizing

## References

- ADR-011 (planned): Unified Session Context — the Mode/Scope/Lens system Conduit will implement
- ADR-012 (planned): Agent Spec — the schema Mentat's agent profile will follow
- PRD: `docs/planning/PRD.md` — original vision (update needed)
