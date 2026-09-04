# Cleanup Tasks — Agent Framework Reset

> Historical archive. Retired product and tool names below are preserved only as migration history; do not copy them into executable prompts or current documentation.

## 1. Global CLAUDE.md (~/.claude/CLAUDE.md)

**Current state:** Contains the Agent Auto-Boot (MANDATORY) block that forces every session to load .nanite/agent-boot.md + profile + bootstrap.md.

**Action: REWRITE.** Replace with minimal global instructions:
```markdown
# Global Claude Instructions

- If `.nanite/boot-prompt.md` exists, read it first for session context.
- Do not guess when uncertain. Stop and ask for clarification.
- Prefer focused, minimal output. No trailing summaries.
```

The auto-boot system was the v1 experiment. The new system uses boot-prompt (manual) and per-project CLAUDE.md for project-specific rules. No global mandatory boot sequence.

## 2. Global settings.json (~/.claude/settings.json)

**Current state:** 25 plugins enabled globally + tokf hook + skipDangerousModePermissionPrompt.

### Plugins — Keep/Remove

**KEEP (actually useful globally):**
| Plugin | Why |
|--------|-----|
| gopls-lsp | Go LSP — you write Go daily |
| typescript-lsp | TS LSP — you write TS/React |
| github | GitHub integration — always useful |
| code-review | PR review — always useful |

**REMOVE (not needed globally, install per-project if needed):**
| Plugin | Why Remove |
|--------|-----------|
| learning-output-style | **Silently activates "learning mode" without consent.** This was the source of the Insight blocks and educational framing you didn't ask for. |
| explanatory-output-style | Same issue — injects output style directives silently |
| vercel | Massive context injection. 50+ skills, SessionStart hook injects full ecosystem knowledge graph (~15K tokens). PreToolUse hook fires on every Read/Edit/Write/Bash and injects up to 3 skill docs per call. This consumed enormous context in our session. Install per-project for Vercel/Next.js work only. |
| frontend-design | Per-project, not global |
| figma | Per-project, not global |
| laravel-boost | Per-project (and you're moving away from Laravel) |
| php-lsp | Per-project (same) |
| pyright-lsp | Per-project |
| terraform | Per-project |
| atlassian | Per-project (work projects only) |
| agent-sdk-dev | Per-project |
| plugin-dev | Per-project |
| hookify | Per-project |
| skill-creator | Per-project |
| playground | Per-project |
| claude-md-management | Per-project |
| claude-code-setup | Per-project |
| pr-review-toolkit | Per-project (or keep if you do PRs frequently) |
| code-simplifier | Per-project |
| atomic-agents | Per-project |
| chrome-devtools-mcp | Per-project |

**EVALUATE (could go either way):**
| Plugin | Notes |
|--------|-------|
| pr-review-toolkit | If you do PRs across many repos, keep global. Otherwise per-project. |
| code-simplifier | Same consideration. |

### Hooks
| Hook | Action |
|------|--------|
| tokf pre-tool-use | EVALUATE — is tokf still useful? If yes, keep. |

### Settings
| Setting | Action |
|---------|--------|
| skipDangerousModePermissionPrompt | KEEP if intentional. This skips the "are you sure?" prompt for dangerous bash commands. |

## 3. Uninstall Commands

Run these to remove plugins globally:

```bash
# Output style plugins (silently inject behavior)
claude plugins uninstall learning-output-style
claude plugins uninstall explanatory-output-style

# Heavy context injectors
claude plugins uninstall vercel

# Per-project only (not needed globally)
claude plugins uninstall frontend-design
claude plugins uninstall figma
claude plugins uninstall laravel-boost
claude plugins uninstall php-lsp
claude plugins uninstall pyright-lsp
claude plugins uninstall terraform
claude plugins uninstall atlassian
claude plugins uninstall agent-sdk-dev
claude plugins uninstall plugin-dev
claude plugins uninstall hookify
claude plugins uninstall skill-creator
claude plugins uninstall playground
claude plugins uninstall claude-md-management
claude plugins uninstall claude-code-setup
claude plugins uninstall atomic-agents
claude plugins uninstall chrome-devtools-mcp
claude plugins uninstall code-simplifier
claude plugins uninstall pr-review-toolkit
```

After uninstall, the settings.json enabledPlugins should only contain:
```json
{
  "gopls-lsp@claude-plugins-official": true,
  "typescript-lsp@claude-plugins-official": true,
  "github@claude-plugins-official": true,
  "code-review@claude-plugins-official": true
}
```

## 4. Project-scoped plugins (already correct)

These are installed per-project on context-memory-service and don't affect other projects:
- superpowers, feature-dev, ralph-loop, playwright, commit-commands

No action needed.

## 5. Mentat .agentrc cleanup

| Item | Action |
|------|--------|
| `.nanite/bootstrap.md` (17.5K) | DELETE — replaced by boot-prompt skill |
| `.nanite/boot/` (7 profiles) | KEEP meta-agent and worker. DROP prime, architect, analyst, reviewer, ops-tester (Multiplexor roles). |
| `.nanite/pcc/` (global + tasks) | FREEZE — don't delete yet, but stop generating. Evaluate if Vanta Conduit replaces this. |
| `.nanite/inbox/` | DELETE — Multiplexor A2A messaging, handled by Engine |
| `.nanite/plans/` | KEEP — phase files still useful for reference |
| `.nanite/agent-boot.md` | REWRITE to match new minimal boot contract |
| `.nanite/CLAUDE.md` | REWRITE to point at new system |
| Dropped skills (19) | DELETE files from .claude/skills/ |
| Dropped commands | DELETE matching files from .claude/commands/ |
| Hook scripts (audit, worktree-check, session-end) | DELETE from .claude/hooks/ |
| settings.json hooks | UPDATE to remove dropped hooks |

## 6. Vanta Conduit tasks

| Task | Priority |
|------|----------|
| Verify/add firstOrCreate for namespaces | HIGH — doc-note depends on this |
| Register docs namespaces for known projects | After firstOrCreate works |
| Audit current Vanta Conduit data and schema | Review what agents stored vs what we want |
| Test doc-note → doc-search round-trip | Validate the new skills work end-to-end |

## 7. Follow-up sessions

| Topic | What to do |
|-------|-----------|
| B2 — Project Docs Agent | Design the structured docs maintainer (triggered by events, not ad-hoc) |
| B3 — Strategic Planner Agent | Design the roadmap/scope/planning assistant |
| Deferred skills redesign | Revisit the 17 deferred skills once B2/B3 are defined |
| Mentat project CLAUDE.md | Rewrite for new framework |
| Agent-boot.md rewrite | Align with minimal boot contract |
| Test new skills | Run doc-note, doc-search, boot-prompt in live session |
