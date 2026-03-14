# project-onboard

Full new-project setup for the Fragments Engine portfolio in one command. Creates Volon project, PCC files, agentrc scaffolding, and Cortex registration.

## Usage
`/project-onboard <project_id> <project_path> — <description>`

**project_id**: Short identifier (e.g., "nanite", "sigil")
**project_path**: Absolute path to the project directory
**description**: Brief description of the project

## Examples
- `/project-onboard nanite ~/Projects-apps/nanite — Knowledge vault and RAG system for Fragments Engine`
- `/project-onboard lnklst ~/Projects-apps/_pre-dev/lnklst — Link curation and bookmarking app`

## Instructions

1. **Validate inputs**:
   - Confirm the project directory exists
   - Check that `project_id` doesn't already exist in Volon (`volon_projects_list`)
   - Check `config/repos.yaml` to see if the project is already registered

2. **Create Volon project**:
   - This is the canonical registration. All other steps reference this.
   - Note: If Volon project creation is not available via MCP, flag this as a manual step.

3. **Create agentrc scaffolding** in the project directory:
   ```
   <project_path>/.agentrc/
   <project_path>/.agentrc/pcc/global/     (PCC directory)
   <project_path>/.agentrc/inbox/          (A2A messaging)
   <project_path>/.agentrc/inbox/broadcast/
   <project_path>/.agentrc/inbox/lead/
   <project_path>/agentrc.yaml             (agent config)
   ```

4. **Generate agentrc.yaml**:
   ```yaml
   project_id: <project_id>
   project_name: <project_id capitalized>
   description: <description>
   version: 1
   ```

5. **Generate PCC files** (6-file standard):
   - `00_project.md` — Project overview, purpose, tech stack
   - `01_architecture.md` — High-level architecture (placeholder with structure)
   - `02_conventions.md` — Coding conventions, file structure
   - `03_current_state.md` — Current status, recent changes
   - `04_tasks.md` — Active tasks/priorities (links to Volon)
   - `05_decisions.md` — Key architectural decisions (links to ADRs)

   Each file should have frontmatter:
   ```yaml
   ---
   type: pcc
   project_id: <project_id>
   file: <NN_name>
   updated_at: <ISO date>
   ---
   ```

6. **Register in config/repos.yaml**:
   - Add an entry for the new project with path and project_id

7. **Register Cortex namespace**:
   - Use `context_namespace_register` with namespace `app/<project_id>`
   - Write initial context record: project description, path, tech stack

8. **Create CLAUDE.md** if it doesn't exist:
   - Reference the agentrc boot sequence
   - Include basic build/test commands if detectable (check for Makefile, go.mod, package.json)

9. **Copy boot profiles** from a reference project (e.g., mentat):
   - `.agentrc/agent-boot.md`
   - `.agentrc/boot/meta-agent.md` (at minimum)
   - `.agentrc/bootstrap.md` (template)

10. **Summary output**:

```
=== PROJECT ONBOARDED ===
Project: <project_id>
Path: <project_path>
Volon: registered
PCC: 6 files created
Cortex: namespace app/<project_id> registered
Config: added to repos.yaml
Boot: agentrc.yaml + boot profiles created

Next steps:
1. Review and customize PCC files with actual project details
2. Run /pcc-refresh to sync to Cortex
3. Add project-specific skills to .claude/skills/ if needed
=== END ===
```

## When to Use
- When adding a new project to the Fragments Engine portfolio
- When onboarding an existing repo that doesn't have agentrc scaffolding
- After creating a new repository
