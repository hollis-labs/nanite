# Nanite / agentrc Consolidation Design

**Date:** 2026-04-09
**Status:** Approved
**Scope:** Migrate the agentrc framework (roles, skills, commands, installer) into the Nanite binary, finish the `.nanite/` / `NANITE.md` surface, add session-scoped A2A addressing with handoff support, roll out to 14 projects, archive the agentrc repo
**Builds on:** `2026-04-08-agent-adapter-architecture-design.md`

---

## Summary

Nanite absorbs the full agentrc framework as embedded content and replaces `agentrc-install` with a native `nanite install` command (CLI + MCP). The `.agentrc/` directory convention is retired across the portfolio in favor of `.nanite/` + a thin `NANITE.md` at project root. A2A messaging is extended from unconstrained TEXT addressing to session-scoped `(session_id, agent_id)` addressing with an explicit handoff flow built on Nanite's existing `session_agents` binding table. All 14 currently-installed agentrc projects are migrated in a phased rollout with per-project archival to `~/Projects-apps/.archived/`. After rollout, the `agentrc` source repo and `~/.agentrc/` global home are archived and removed.

This completes the consolidation begun in the 2026-04-08 agent adapter architecture spec, which shipped the adapter framework and the `.agentrc` → `.nanite` rename inside Nanite. This spec addresses what was out of scope for that effort: the content library, the installer, the cross-project rollout, and session-scoped A2A with handoff.

---

## Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Content location | Embed framework content via Go `embed.FS` inside Nanite binary | Zero external dependency; content versioned with consuming binary; agentrc content is small (~8K lines) and mostly stable; archiving agentrc becomes a clean one-way operation |
| `~/.nanite/` as real dir | Installer extracts embedded content to `~/.nanite/` on first run | Humans edit files directly (files are source of truth); extract skips user-modified files via checksum compare |
| NANITE.md style | Thin boot prompt + pointers, humans own it | Single file for humans to glance at; keeps `.nanite/` as structured config zone; matches validated pattern in `~/Projects-apps/nanite/.nanite/boot-prompt.md` |
| Richer init (stack knowledge graph, context generators) | Deferred to follow-up task | Base migration scope; richer scaffolding is optional and agent-runnable |
| A2A exposure surface | Both CLI and MCP, wrapping the same internal service | Matches house pattern (engine/hadron/cortex/cerberus); MCP for discoverability, CLI for hooks/cron/pipes |
| MCP transport | Bidirectional / streaming for subscriptions | Push new messages to subscribed agents without polling |
| A2A addressing | `(session_id, agent_id)` using Nanite's existing agent IDs + `"user"` sentinel | Agents already have canonical IDs (UUID or `file-<slug>`); no new identifier system; `"user"` sentinel keeps query paths uniform |
| Session role binding | Reuse existing `session_agents.is_primary` | Already present in schema; handoff is a mutation inside one transaction |
| A2A schema migration | Clean break, no parallel writes | No current users; parallel-write + cutover is unnecessary complexity |
| Legacy CLAUDE.md loader block | Removed entirely, replaced by `<!-- nanite:start/end -->` managed section | Clean break; no transition period with two sets of instructions |
| Project registry | Keep in `~/.nanite/config.yaml` as YAML (not database) | Files are source of truth; DB sync is a deferred follow-up |
| `agentrc-manage` skill | Port as markdown skill, rename to `nanite-agent-manage` | No native command yet; the markdown skill keeps working because it just reads/writes files; first-class `nanite role create` etc. deferred |
| Meta role rename | `agentrc-dev` → `nanite-agent-manager` (not `nanite-dev`) | `nanite-dev` collides with "developer of the Nanite app itself" |
| Per-project migration strategy | Rename-and-rewire with archival to `~/Projects-apps/.archived/{project}-YYYY-MM-DD/` | Clean break; date-stamped safety net; zero ambiguity about where old content went |
| Rollout order | Nanite first (dogfood), then 13 others via parallel sub-agents | Risk is highest on first migration; dogfood resolves issues before fan-out |
| agent-workspaces | Archive-only mode (no `.nanite/` scaffolded) | It's a user-convention scratch dir, not a codebase; real fix is a future "Nanite workflows" primitive |
| Rollback capability | `nanite install --rollback` as Phase 2 deliverable | Real safety for Phase 5 parallel rollout; ~30 lines over the install service |
| Partial-install recovery | Automatic, driven by state marker file in archive dir | Sub-agent crashes leave recoverable state; TTY prompts interactive, non-TTY uses `--resume` / `--restart` flags |
| Testing cross-platform | Skipped, macOS-first | Nanite's deployment target is desktop macOS at launch |

---

## 1. Architecture Overview

Three layers after migration:

1. **Nanite binary** (`~/Projects-apps/nanite/`) — embeds the role/skill/command/doc/template library via `embed.FS` at `assets/framework/`. On `nanite install`, extracts to `~/.nanite/` if absent. Source of truth for framework content.
2. **Global user home** (`~/.nanite/`) — populated by the installer from embedded assets, identical layout to today's `~/.agentrc/` (`roles/`, `skills/`, `commands/`, `docs/`, `templates/`, `config.yaml`, `agent-boot.md`). Humans edit directly; `nanite install --refresh` re-extracts missing files but never overwrites user edits (checksum compare on extract, skip if modified).
3. **Project-local** (`<project>/.nanite/` + `NANITE.md`) — thin `NANITE.md` at project root (humans own it, scaffolded once from template). `.nanite/` holds `config.yaml`, `boot-prompt.md`, `agents/*.md`, and symlinks into `~/.nanite/{roles,skills,commands}/`. CLI adapters (nanite-native, claude, codex, gemini, opencode — already shipped in the 2026-04-08 architecture spec) read this layer and write managed sections into their respective CLI files.

### Data flow: install time

```
nanite binary (embed.FS)
      │
      ├─ nanite install                    → ~/.nanite/ (skip user-modified)
      │
      └─ nanite install --project <dir>    → <dir>/.nanite/, <dir>/NANITE.md
                                              rewrites managed sections in
                                              CLAUDE.md / GEMINI.md / AGENTS.md / OPENCODE.md
                                              via AdapterRegistry.SyncAllProjectRoots()
```

### Data flow: runtime A2A

```
external agent                     Nanite binary
    │                                   │
    │ nanite a2a send --to X  ─────────▶│   CLI wrapper
    │                                   │
    │ mcp__nanite__a2a_send   ─────────▶│   MCP wrapper (bidirectional stream)
    │                                   │
    │                                   ▼
    │                          internal/service/a2a.go   (single canonical impl)
    │                                   │
    │                                   ▼
    │                          SQLite (a2a_messages + session_agents + session_handoffs)
    │                                   │
    │   ◀───── push via MCP stream ─────┘   (subscribed agents get new messages)
```

### What goes away

- `~/Projects-apps/agentrc/` → archived to `~/Projects-apps/.archived/agentrc-final-YYYY-MM-DD/`, git tagged `v2.2.0-final`
- `~/.agentrc/` global → removed after all projects migrate and a brief verification window
- Every project's `.agentrc/` + `.agentrc-legacy/` → moved to `~/Projects-apps/.archived/{project}-YYYY-MM-DD/`
- `## agentrc` loader blocks in project CLAUDE.md files → removed entirely during migration, replaced by `<!-- nanite:start/end -->` managed sections

### What stays

- The role/skill/command markdown content — verbatim (with mechanical renames), relocated to `~/Projects-apps/nanite/assets/framework/` and embedded
- The `.agentrc` → `.nanite` rename pattern from the 2026-04-08 architecture spec
- The existing nanite-native, claude, codex, gemini, opencode adapters — no changes

---

## 2. Components

Six components. One is pure content relocation, two are new code, two are extensions to existing code, one is a schema migration.

### 2.1 Embedded framework assets — `nanite/assets/framework/` (new, content relocation)

Copy the current agentrc directory structure verbatim into the Nanite repo:

```
nanite/assets/framework/
├── VERSION                  # e.g., "2.3.0" (bumped past agentrc's 2.2.0 to mark the port)
├── config.yaml              # seed global config (projects: section, roles: section)
├── agent-boot.md            # seed session boot rules
├── roles/
│   ├── domain/              # backend, frontend, code-review, project-docs, strategic-planner, auditor
│   ├── stack/               # go, react
│   └── meta/                # nanite-agent-manager (renamed from agentrc-dev)
├── skills/                  # 18 skill .md files (agentrc-install removed)
├── commands/                # command stubs (matching skills)
├── docs/                    # nanite-framework.md, nanite-setup-guide.md, ref-*
└── templates/               # NANITE.md.tmpl, backend.md, frontend.md
```

**Accessor package:** `internal/assets/framework.go`

```go
//go:embed all:assets/framework
var frameworkAssets embed.FS

// ExtractTo extracts the embedded framework tree into targetDir.
// Per-file checksum compare against any existing file: skip if modified.
func ExtractTo(targetDir string, opts ExtractOptions) (*ExtractReport, error)

// File reads a single embedded file without extracting.
func File(path string) ([]byte, error)

// Version returns the framework version from assets/framework/VERSION.
func Version() string
```

**Renames during port:**
- `roles/meta/agentrc-dev.md` → `roles/meta/nanite-agent-manager.md`
- `skills/agentrc-manage.md` → `skills/nanite-agent-manage.md`
- `docs/agent-framework-v2.md` → `docs/nanite-framework.md`
- `docs/agent-setup-guide.md` → `docs/nanite-setup-guide.md`

**Delete during port:**
- `skills/agentrc-install.md` — replaced by `nanite install` CLI

**Content find-replace during port (paths only, not prose history):**
- `agentrc_version` → `nanite_version`
- `~/.agentrc/` → `~/.nanite/`
- `.agentrc/` → `.nanite/`
- `agentrc-manage` → `nanite-agent-manage`
- `agentrc-dev` → `nanite-agent-manager`

Manual review of `docs/nanite-framework.md` and `docs/nanite-setup-guide.md` is required — prose needs rewriting beyond sed.

### 2.2 `nanite install` command (new)

**Files:**
- `cmd/nanite/install.go` — cobra subcommand, flag parsing, calls into service (~50 lines)
- `internal/service/install.go` — canonical install/migrate/rollback/cleanup logic
- `internal/service/install_test.go` — unit tests

**Invocations:**

```
nanite install                              # extract embedded assets to ~/.nanite/
nanite install --project <dir>              # scaffold .nanite/ + NANITE.md in project
nanite install --project <dir> --migrate-from-agentrc
                                             # project has .agentrc/ → archive + migrate
nanite install --project <dir> --archive-only
                                             # archive .agentrc/ without scaffolding .nanite/
                                             # (used for agent-workspaces-style scratch dirs)
nanite install --project <dir> --rollback   # restore from most recent archive
nanite install --project <dir> --resume     # continue partial install from state marker
nanite install --project <dir> --restart    # reverse archive, then migrate fresh
nanite install --project <dir> --cleanup    # remove .nanite/, leave backups
nanite install --refresh                    # re-extract ~/.nanite/ assets, skip user-modified
nanite install --refresh --force            # re-extract everything, overwrite
nanite install --print-diff --project <dir> # dry-run, show what would change
```

**MCP surface:**
- `mcp__nanite__install_home` — extract to `~/.nanite/`
- `mcp__nanite__install_project` — scaffold + migrate
- `mcp__nanite__install_rollback` — rollback from archive
- `mcp__nanite__install_diff` — dry-run diff

All MCP tools are thin wrappers over `internal/service/install.go`.

### 2.3 Project migration logic — `internal/service/install.go` (new, complex)

Pseudocode for `InstallProject`:

```
InstallProject(projectDir, opts):
  1. detect state:
       - has .agentrc/ ?
       - has .agentrc-legacy/ ?
       - has .nanite/ ?
       - has NANITE.md ?
       - has CLAUDE.md with agentrc loader block?
       - has CLAUDE.md with <!-- nanite:start/end --> markers?
       - has recent archive dir for this project with .install-state.json ?

  2. guard:
       if partial state detected (.install-state.json present with phase != complete):
           → branch to ResumeInstall / RestartInstall
       if .nanite/ exists AND is complete (has config.yaml, agents/, symlinks, and
           NANITE.md exists at project root AND CLAUDE.md has valid nanite:start/end markers):
           → no-op, report "already installed" (unless --refresh passed)
       if .nanite/ exists but some pieces are missing (e.g., PR #11 manual rename left
           .nanite/ but no NANITE.md and no managed section):
           → "adopt" path: fill in the missing pieces, write state marker marked complete
       if .nanite/ AND .agentrc/ both exist (should not happen, but defensive):
           → refuse with error, ask user to manually resolve
       if .agentrc/ exists and not --migrate-from-agentrc → refuse with hint

  3. if --migrate-from-agentrc:
       archiveDir = ~/Projects-apps/.archived/{basename}-{YYYY-MM-DD-HHMMSS}/
       write .install-state.json (phase: "starting")
       mv projectDir/.agentrc       → archiveDir/.agentrc
       mv projectDir/.agentrc-legacy → archiveDir/.agentrc-legacy    (if exists)
       snapshot projectDir/CLAUDE.md pre-edit → archiveDir/CLAUDE.md.pre-edit
       update .install-state.json (phase: "archived")

  4. ensure ~/.nanite/ exists; if not, run ExtractTo()
     update .install-state.json (phase: "global-extract-complete")

  5. scaffold projectDir/.nanite/:
       - config.yaml (from archiveDir/.agentrc/config.yaml if migrating, else template)
                     (field rename: agentrc_version → nanite_version)
                     (unknown keys preserved verbatim via yaml round-trip)
       - boot-prompt.md (copy from archive if present; else empty scaffold)
       - agents/ (copy from archive/.agentrc/agents if present)
       - roles, skills, commands → symlinks into ~/.nanite/{roles,skills,commands}/
     update .install-state.json (phase: "scaffold-nanite-dir")

  6. scaffold projectDir/NANITE.md from template — only if not present
     update .install-state.json (phase: "scaffold-nanite-md")

  7. CLAUDE.md surgery:
       - remove any "## agentrc" (or any heading level) section
         snapshot removed content → archiveDir/removed-claude-section.md
       - write/replace managed section between <!-- nanite:start --> ... <!-- nanite:end -->
       update .install-state.json (phase: "claude-sync")

  8. fire AdapterRegistry.SyncAllProjectRoots()
     update .install-state.json (phase: "adapter-sync")

  9. mark .install-state.json phase: "complete"
     return InstallReport { actions[], warnings[], archivePath }
```

**Idempotency:** Running twice on a clean project is a no-op on run 2 (everything exists, checksums match). Running after a user edits `NANITE.md` preserves the edit (checksum skip on template write).

**Error handling:** Each phase is reversible up to the archival step. If a phase fails, the state marker records the last completed phase, enabling `--resume` on re-run.

### 2.4 A2A session-scoped addressing — extension

**Schema migration (`005_a2a_session_scoping.sql`):**

```sql
-- Drop unconstrained columns (clean break, no current users)
ALTER TABLE a2a_messages DROP COLUMN from_agent;
ALTER TABLE a2a_messages DROP COLUMN to_agent;

-- Add session-scoped addressing
ALTER TABLE a2a_messages ADD COLUMN from_session_id TEXT NOT NULL DEFAULT '';
ALTER TABLE a2a_messages ADD COLUMN from_agent_id   TEXT NOT NULL DEFAULT '';
ALTER TABLE a2a_messages ADD COLUMN to_session_id   TEXT NOT NULL DEFAULT '';
ALTER TABLE a2a_messages ADD COLUMN to_agent_id     TEXT NOT NULL DEFAULT '';

CREATE INDEX idx_a2a_to_session_agent ON a2a_messages(to_session_id, to_agent_id, status);
CREATE INDEX idx_a2a_from_session_agent ON a2a_messages(from_session_id, from_agent_id);

-- Handoff audit trail (session_agents handles the binding itself)
CREATE TABLE session_handoffs (
    id                    TEXT PRIMARY KEY,
    session_id            TEXT NOT NULL REFERENCES sessions(id),
    from_agent_id         TEXT,
    to_agent_id           TEXT NOT NULL,
    requested_by          TEXT NOT NULL,    -- "departing" | "incoming" | "user"
    status                TEXT NOT NULL DEFAULT 'pending'
                              CHECK(status IN ('pending','approved','rejected','completed')),
    requested_at          TEXT NOT NULL,
    approved_at           TEXT,
    approved_by_user      INTEGER NOT NULL DEFAULT 0,
    context_message_count INTEGER,
    notes                 TEXT
);
CREATE INDEX idx_handoffs_session ON session_handoffs(session_id, status);
```

**No new binding table.** Handoff mutates `session_agents.is_primary` inside one transaction:

```sql
BEGIN;
UPDATE session_agents
   SET is_primary = FALSE
 WHERE session_id = ? AND is_primary = TRUE;

INSERT INTO session_agents (session_id, agent_id, mode, is_primary, joined_at)
VALUES (?, ?, 'default', TRUE, CURRENT_TIMESTAMP)
ON CONFLICT(session_id, agent_id) DO UPDATE SET is_primary = TRUE;

UPDATE session_handoffs
   SET status = 'completed', approved_at = CURRENT_TIMESTAMP, approved_by_user = 1
 WHERE id = ?;
COMMIT;
```

**The `"user"` sentinel.** Messages addressed to the human in a session use `to_agent_id = "user"`. User UI reads `WHERE to_agent_id = 'user' AND to_session_id = <current>`. Unambiguous because Nanite's agent ID scheme is UUID or `file-<slug>` — neither can collide with the literal `"user"`. Enforced at write path: any attempt to create an agent with `id = "user"` or `slug = "user"` is rejected at the service layer.

**Validation at send time:** The service layer rejects sends where `to_agent_id` isn't one of: a known DB UUID, a `file-<slug>` referencing a discovered file agent, or the `"user"` sentinel.

**Service methods (`internal/service/a2a.go`):**

```go
SendMessage(ctx, msg A2AMessage) error
Inbox(ctx, sessionID, agentID, status string) ([]A2AMessage, error)
Thread(ctx, threadID string) ([]A2AMessage, error)
Ack(ctx, sessionID, agentID, msgID string) error
Resolve(ctx, sessionID, agentID, msgID string) error
RequestHandoff(ctx, sessionID, fromAgentID, toAgentID, reqBy string) (handoffID string, err error)
ApproveHandoff(ctx, handoffID string) error
RejectHandoff(ctx, handoffID, reason string) error
RecentForSession(ctx, sessionID string, limit int) ([]A2AMessage, error)
SubscribeSessionAgent(ctx, sessionID, agentID string) (<-chan A2AMessage, error)
```

**CLI surface:**

```
nanite a2a send --session <sid> --to <agent_id> --subject "..." --body "..." [--from-agent <aid>]
nanite a2a inbox --session <sid> --agent <aid>
nanite a2a thread <threadID>
nanite a2a ack --session <sid> --agent <aid> <msgID>
nanite a2a resolve --session <sid> --agent <aid> <msgID>
nanite a2a catch-up --session <sid> --last 20
nanite a2a handoff request --session <sid> --to <agent_id> [--from <agent_id>]
nanite a2a handoff approve <handoffID>
nanite a2a handoff reject <handoffID> --reason "..."
```

**MCP surface (bidirectional):**

```
mcp__nanite__a2a_send                  (req/resp)
mcp__nanite__a2a_inbox                 (req/resp)
mcp__nanite__a2a_thread                (req/resp)
mcp__nanite__a2a_ack                   (req/resp)
mcp__nanite__a2a_resolve               (req/resp)
mcp__nanite__a2a_catch_up              (req/resp)
mcp__nanite__a2a_subscribe             (streaming — pushes new messages)
mcp__nanite__a2a_handoff_request       (req/resp)
mcp__nanite__a2a_handoff_approve       (req/resp, user approval in Nanite UI or CLI invocation)
mcp__nanite__a2a_handoff_reject        (req/resp)
```

**Handoff approval semantics:** In the Nanite chat UI, approval is an explicit user action (button click → `approved_by_user = 1`). In CLI-only contexts, `nanite a2a handoff approve <id>` counts as user approval by default. Cross-session handoff (transferring a live session's context to a newly-booted session) is not supported in MVP but the schema does not preclude it — see Future Work.

### 2.5 Content updates inside embedded assets (mechanical)

All skills and docs inside `assets/framework/` get the find-replace pass from 2.1. Manual review of prose docs.

### 2.6 Documentation updates

- `docs/nanite-framework.md` — rename from `agent-framework-v2.md`, rewrite references, add section on A2A session-scoped addressing and handoff protocol
- `docs/nanite-setup-guide.md` — rename, rewrite install instructions to use `nanite install`
- New: `docs/nanite-install.md` — command reference, flags, migration guide
- New: `docs/nanite-a2a.md` — CLI + MCP reference, session-role addressing, `"user"` sentinel reservation, handoff protocol

---

## 3. Migration Flow

Six phases, with Phases 2 and 3 running in parallel.

```
Phase 1 ──┬──▶ Phase 2 (install CLI + service + MCP + rollback)
          │
          └──▶ Phase 3 (A2A session scoping + CLI + MCP bidirectional)
                      │
                      ▼
              Phase 4 (dogfood on Nanite itself)
                      │
                      ▼
              Phase 5 (rollout to 13 + archive-only for agent-workspaces)
                      │
                      ▼
              Phase 6 (archive agentrc repo + remove ~/.agentrc/)
```

### Phase 1 — Embed framework content

1. Copy `~/Projects-apps/agentrc/{config.yaml,agent-boot.md,roles,skills,commands,docs,templates,vendor}/` → `~/Projects-apps/nanite/assets/framework/` verbatim.
2. Apply renames and content sed pass from section 2.1.
3. Delete `skills/agentrc-install.md` from the copy.
4. Manual prose review of `docs/nanite-framework.md` and `docs/nanite-setup-guide.md`.
5. Add `assets/framework/VERSION` with `2.3.0`.
6. Add `internal/assets/framework.go` with `//go:embed all:assets/framework` and helper functions.
7. `go build ./...` + smoke test listing the embedded tree.

**Checkpoint:** Nanite binary compiles with full framework content embedded. No user-facing command yet. `agentrc` repo untouched.

### Phase 2 — `nanite install` (parallel with Phase 3)

1. `internal/service/install.go` — canonical install logic with full unit tests.
2. `cmd/nanite/install.go` — cobra subcommand, flag parsing.
3. MCP tool registration: `install_home`, `install_project`, `install_rollback`, `install_diff`, `install_cleanup`.
4. New template: `assets/framework/templates/NANITE.md.tmpl`.
5. Retire existing `CLAUDE.md` loader template (managed section is adapter-generated).
6. Adapter integration: after scaffolding, call `AdapterRegistry.SyncAllProjectRoots()`.
7. Integration test: tempdir with fake `.agentrc/`, run full migration, verify invariants, verify idempotent second run.
8. Implement `--rollback` (reads archive dir, reverses migration).
9. Implement `--resume` and `--restart` based on `.install-state.json`.
10. Interactive-TTY detection for partial install prompt; non-TTY fails with exit code 3.

**Checkpoint:** `nanite install` works end-to-end against a test directory. Migration with archival works. Rollback works. Partial install recovery works. `agentrc` repo untouched, project state untouched.

### Phase 3 — A2A session scoping (parallel with Phase 2)

1. Migration `005_a2a_session_scoping.sql` — drop old columns, add new columns, create `session_handoffs`, add indexes.
2. Rewrite `internal/store/a2a.go` — all store methods use new columns; validation enforces canonical agent IDs.
3. Implement `internal/service/a2a.go` with all methods from section 2.4.
4. Rewrite `internal/api/a2a.go` HTTP endpoints to match new addressing.
5. MCP bidirectional subscription: implement `a2a_subscribe` using MCP streaming.
6. `cmd/nanite/a2a.go` — CLI subcommands wrapping the service layer.
7. Handoff end-to-end test: request, approve, verify binding moves, verify messages route correctly after handoff.
8. Subscribe/unsubscribe test: verify push delivery, verify no replay on resubscribe.

**Checkpoint:** A2A works with session-scoped addressing. CLI + MCP both functional. Handoff flow round-trips. Catch-up returns recent messages. Existing chat still works (primary-agent resolution unchanged).

### Phase 4 — Dogfood on Nanite itself

**State note:** Nanite already has `.nanite/` from the 2026-04-08 agent adapter architecture work (PR #11 renamed `.agentrc/` → `.nanite/` inside the Nanite repo). It does NOT have `.agentrc/` or a symlink structure or a `NANITE.md` at project root or a CLAUDE.md managed section written by the new installer. This is exactly the "adopt existing" guard branch from section 2.3.

1. In `~/Projects-apps/nanite/`, run `nanite install` (extract embedded assets to `~/.nanite/`).
2. Run `nanite install --project ~/Projects-apps/nanite`:
   - Detects existing `.nanite/` from PR #11 → takes the adopt path
   - Creates missing symlinks into `~/.nanite/{roles,skills,commands}`
   - Scaffolds `NANITE.md` at project root (from template)
   - Writes CLAUDE.md managed section (removes any stale `## agentrc` heading if one was left behind)
   - Writes state marker marked `phase: complete`
3. Start a Claude Code session in `~/Projects-apps/nanite`, boot an agent, verify context loads.
4. Force context compaction, verify role + context re-load from the new managed section.
5. A2A smoke test: send, inbox, handoff request, handoff approve, catch-up — all from inside the Nanite repo.

**Checkpoint:** Nanite is running on the new installer against its own partially-pre-migrated state. All other projects still on `.agentrc/`. `agentrc` repo untouched.

### Phase 5 — Rollout to the remaining projects

Dispatched in parallel via sub-agents, one per project. Each sub-agent:

1. Runs `nanite install --project <root> --migrate-from-agentrc` (or `--archive-only` for agent-workspaces).
2. Verifies archive dir created with `.agentrc/` (and `.agentrc-legacy/` if present).
3. Verifies `.nanite/` exists (except agent-workspaces), `NANITE.md` exists, symlinks resolve.
4. Verifies CLAUDE.md has `<!-- nanite:start -->` markers and no `## agentrc` section (except agent-workspaces, which is unchanged).
5. Verifies `.install-state.json` shows `phase: complete`.
6. Reports success/failure with archive path.

**Projects:** the table below reflects the portfolio state as of this spec. Nanite itself was handled in Phase 4. The exact count depends on what's still on `.agentrc/` when Phase 5 runs; treat the table as the starting checklist, not a fixed number.

| Project | Mode |
|---|---|
| agent-workspaces | archive-only |
| cerberus | full migration |
| clockwork-manifold | full migration |
| engine (monorepo child) | full migration |
| conduit (monorepo child) | full migration |
| cortex (monorepo child) | full migration |
| libs (monorepo child) | full migration |
| hadron | full migration |
| carrier | full migration |
| nexus | full migration |
| sigil | full migration |
| lnklst | full migration |
| suds-v2 | full migration |
| fragmentsengine.com | full migration |

**Sub-agent failure handling:** Exit code 3 (partial install) triggers automatic retry with `--resume`. If that also fails, project is flagged for manual attention.

**Manual verification after completion:** Spot-check one project per language (Go monorepo child, Go standalone, TypeScript, static-site) by launching a Claude Code session and booting an agent.

**Checkpoint:** Every portfolio project is on `.nanite/` (or archive-only for scratch dirs). `agentrc` repo still exists as safety net.

### Phase 6 — Archive agentrc repo and clean up global state

1. Commit any pending work in `~/Projects-apps/agentrc/`.
2. `git tag v2.2.0-final` in the agentrc repo.
3. `mv ~/Projects-apps/agentrc ~/Projects-apps/.archived/agentrc-final-YYYY-MM-DD`.
4. Remove `~/.agentrc/` (symlink or directory).
5. User manually deletes the GitHub repo once comfortable.
6. Portfolio-wide grep for stragglers (`agentrc`, `.agentrc`, `~/.agentrc`) excluding `.archived/`. Any hits go into a cleanup followup task.

**Checkpoint:** `agentrc` no longer exists anywhere on the system. Nanite owns the full framework. All projects run on `.nanite/`. `~/Projects-apps/.archived/` contains dated safety-net backups.

### Rollback at any phase

- **Phases 1-3:** trivial — nothing outside the Nanite repo has changed. `git reset`.
- **Phase 4:** `nanite install --project ~/Projects-apps/nanite --rollback`, restores pre-migration state.
- **Phase 5:** per-project `nanite install --rollback` (same as Phase 4).
- **Phase 6:** `mv ~/Projects-apps/.archived/agentrc-final-YYYY-MM-DD ~/Projects-apps/agentrc`, restore `~/.agentrc/` symlink.

---

## 4. Error Handling & Edge Cases

### 4.1 Installer — filesystem edge cases

| Case | Behavior |
|---|---|
| `.nanite/` already exists, no `--force` | Refuse, print what's there, exit non-zero |
| `.nanite/` exists + `--migrate-from-agentrc` | Refuse, hint to use `--rollback` first |
| `.agentrc/` exists, no `~/.nanite/` global | Auto-run `nanite install` (home extract) first |
| Archive dir collision for same timestamp | Append `-{sequence}` suffix |
| Symlink target missing after extract | Abort project install with clear error |
| `CLAUDE.md` missing | Create with just the managed section + one-line header |
| `CLAUDE.md` malformed / not UTF-8 | Write managed section to `.nanite/claude-managed-section.md`, warn, continue |
| `CLAUDE.md` has mismatched `<!-- nanite:start/end -->` markers | Same fallback as above |
| `## agentrc` section at unexpected heading level | Remove anyway (case-insensitive), snapshot to `removed-claude-section.md` |
| Project dir is a symlink | Resolve, operate on real path |
| Project dir read-only / permission denied | Fail fast, no partial state |

### 4.2 Installer — config migration

| Case | Behavior |
|---|---|
| Legacy `agentrc_version < 2.2.0` | Copy `agents:` block verbatim, regenerate the rest from template, warn |
| Unknown YAML keys (voice_profile, permissions, etc.) | Preserve verbatim via yaml round-trip |
| `.agentrc/agents/*.md` contain `~/.agentrc/` refs | Find-replace during copy, record modified files in install report |

### 4.3 A2A — addressing edge cases

| Case | Behavior |
|---|---|
| `to_agent_id` doesn't exist | Reject send at API boundary |
| Agent exists but not bound to `to_session_id` | Allow — target may be waiting for work |
| `from_agent_id` mismatch with caller | Trusted at send time (MVP). See backlog task for multi-tenant impersonation check. |
| Session deleted, A2A messages reference it | No cascade, messages remain for audit |
| `"user"` collides with a future agent_id | Prevented at agent create (rejected at service layer) |
| Message to `"user"` with no UI connected | Stays in inbox, UI picks up on load |

### 4.4 Handoff edge cases

| Case | Behavior |
|---|---|
| Handoff requested with no current primary | `from_agent_id` can be NULL; approve sets new as primary |
| Handoff to nonexistent agent | Reject at request time |
| Double handoff race (two pending) | Both allowed; approving one auto-rejects the other |
| Approve on already-completed handoff | No-op, success |
| Approve on rejected handoff | Error |
| Transaction mid-flight failure | All-or-nothing (single BEGIN/COMMIT) |
| Subscription dropped mid-stream | Re-subscribe is safe; starts emitting from point of re-subscribe, no replay. Clients should call `catch-up` if they need history. |

### 4.5 Rollback edge cases

| Case | Behavior |
|---|---|
| Rollback on a never-migrated project | Error: no matching archive dir |
| Rollback called twice | Second call: `.agentrc/` already restored, exit clean ("already rolled back") |
| Archive dir manually manipulated | Detect missing snapshot files, refuse rollback, report which files are missing |
| User edited CLAUDE.md post-migration | Preserve content outside `<!-- nanite:start/end -->` markers; restore `## agentrc` section from snapshot; content inside the managed markers is lost (documented) |

### 4.6 Partial install recovery (Phase 5)

**State marker file** at `{archive_dir}/.install-state.json`:

```json
{
  "original_project_basename": "hadron",
  "original_project_path": "/Users/chrispian/Projects-apps/hadron",
  "started_at": "2026-04-09T14:30:22Z",
  "phase": "archived",
  "completed_phases": ["archive"],
  "archive_path": "/Users/chrispian/Projects-apps/.archived/hadron-2026-04-09-143022",
  "rollback_snapshot_path": "/Users/chrispian/Projects-apps/.archived/hadron-2026-04-09-143022/rollback",
  "nanite_version": "2.3.0"
}
```

Phases tracked: `archive` → `global-extract` → `scaffold-nanite-dir` → `scaffold-nanite-md` → `claude-sync` → `adapter-sync` → `complete`.

**Detection on re-run:** `.agentrc/` missing in project, `.nanite/` missing or partial, matching archive dir with `phase != "complete"`.

**Interactive TTY:**

```
$ nanite install --project .
⚠ Partial install detected from 2026-04-09 14:30:22 (phase: archived)
  Archive: /Users/chrispian/Projects-apps/.archived/hadron-2026-04-09-143022

How do you want to proceed?
  [r] Resume from last completed phase
  [s] Start over (restore .agentrc/ from archive, then re-run migration)
  [c] Cancel (leave state as-is)
>
```

**Non-interactive:** Exit code 3 with hint to use `--resume` or `--restart`.

**Resume semantics:** Reads state marker, continues from next phase. For the `archive → scaffold` gap, automatically copies `agents/*.md`, `config.yaml`, `boot-prompt.md` from the archive dir. No user intervention required.

**Restart semantics:** Reverses the archive move (`.agentrc/` back to project), deletes partial `.nanite/`, removes the archive dir, runs fresh migration.

**Sub-agent harness** auto-retries with `--resume` on exit code 3. Only flags for manual attention after `--resume` also fails.

### 4.7 Catastrophic recovery

| Case | Behavior |
|---|---|
| User deletes `~/.nanite/` | `nanite install` re-extracts; user customizations lost (document) |
| User deletes archive dir | No forward recovery; document as safety net |
| Nanite binary updated, VERSION bumped | `nanite install --refresh` re-extracts, skips user-modified; `--refresh --force` overwrites |

---

## 5. Testing & Verification

### 5.1 Unit tests (code we're building)

**`internal/assets/framework.go`:**
- `ExtractTo` against empty tempdir: full tree extracted, checksums match
- `ExtractTo` against tempdir with modified file: modified file skipped, others extracted
- `ExtractTo` against tempdir with extra file: extra file untouched
- `File(path)` returns bytes for known file, error for missing
- `Version()` returns string from VERSION file

**`internal/service/install.go`:**
- Fresh install on empty project: `.nanite/`, `NANITE.md`, CLAUDE.md managed section. Idempotent.
- `--migrate-from-agentrc` on fake `.agentrc/`: archive dir, field renames, copied agents, CLAUDE.md cleanup
- Migration with `.agentrc-legacy/` present: both archived
- Archive collision same second: sequence suffix appended
- Unknown YAML keys round-trip preserved
- CLAUDE.md with no `## agentrc` section: managed section appended cleanly
- CLAUDE.md with unexpected heading level: removed, snapshot written
- CLAUDE.md with mismatched markers: fallback to `.nanite/claude-managed-section.md`
- Project symlink: resolves to real path
- Rollback: bit-for-bit equivalent to pre-migration (except mtime)
- Rollback on never-migrated project: clean error
- Partial install + `--resume`: completes successfully
- Partial install + `--restart`: reverses archive, migrates fresh
- Non-TTY + partial state + no flag: exit code 3

**`internal/store/a2a.go` + `internal/service/a2a.go`:**
- Send with valid `(session_id, agent_id)`: row inserted
- Send with `to_agent_id = "user"`: accepted
- Send with unknown `to_agent_id`: rejected
- Attempt to create agent with `slug = "user"`: rejected
- Inbox query: matching rows only, correct ordering
- Thread query: full chain via reply_to linkage
- Ack by bound agent: status → `read`
- `RecentForSession`: returns last N across both sides of session
- `RequestHandoff`: pending row created
- `ApproveHandoff`: single transaction flips `session_agents.is_primary` and updates handoff row
- Transaction failure injection: neither change persists
- `ApproveHandoff` on completed: no-op success
- `ApproveHandoff` on rejected: error
- Two pending handoffs: approving one auto-rejects the other
- Subscribe + insert: message delivered within 100ms
- Drop + re-subscribe: new subscriber gets new messages, no replay

### 5.2 Integration tests (installer round-trip)

**Full migration round-trip:**
1. Build fake project mirroring real agentrc install
2. Run `--migrate-from-agentrc` via service layer
3. Verify archive, `.nanite/` contents, `NANITE.md`, CLAUDE.md, user content byte-identical
4. Run `--rollback`
5. Verify bit-for-bit equivalent to original

**Home extract + refresh:**
1. Extract to tempdir home
2. Modify one skill file
3. Run `--refresh`
4. Verify modified file untouched, new files added, would-be-overwritten files skipped

**A2A + handoff end-to-end:**
1. Create two agents
2. Create session, bind agent A as primary
3. A → `(session, "user")`, verify user inbox
4. A → `(session, B)`, verify B's inbox
5. Request handoff A → B, approve
6. Verify `session_agents.is_primary` → B
7. Verify prior messages to B still visible
8. `RecentForSession` returns messages from both
9. Subscribe to `(session, B)`, verify push delivery

### 5.3 Manual verification (rollout checkpoints)

**Phase 4 (Nanite on itself):**
- `ls ~/Projects-apps/nanite/.nanite` shows expected structure
- `cat NANITE.md` shows thin boot prompt
- CLAUDE.md: no `## agentrc`, has `<!-- nanite:start -->`
- Archive dir present with `phase: complete` in state marker
- Boot a Claude session, verify context loads
- Force compaction, verify recovery
- A2A round-trip (send, inbox, handoff, catch-up)

**Phase 5 (rollout):**
- Per-project sub-agent reports aggregated
- Spot-check 4 projects across languages
- `agent-workspaces` verification: `.agentrc/` gone, no `.nanite/`, rest of dir untouched

**Phase 6 (archive agentrc):**
- Every project has `.nanite/` (except agent-workspaces) and no `.agentrc/`
- `git status` clean in agentrc repo, tag created, moved to `.archived/`
- `~/.agentrc/` removed
- Portfolio-wide grep for stragglers

**Post-rollout smoke test (one week later):**
- Multi-agent session from two projects
- A2A across fresh Nanite restart
- `.archived/` safety net still present

### 5.4 Out of scope

- Cross-platform install (macOS-first)
- Concurrent `nanite install` runs on same project
- Schema rollback for A2A migration (clean break, forward-only)
- Binary-upgrade path from old Nanite (pre-release, single consumer)
- A2A performance/load testing (SQLite, single-digit agents)

---

## 6. Future Work (Deferred Tasks)

| Task | Reason for deferral |
|---|---|
| Richer `nanite init` scaffold (stack knowledge graph, backend/frontend context generators, enriched boot prompt) | Base migration scope; agent-runnable later, optional |
| Config ↔ DB bidirectional sync | Files are source of truth for MVP; DB sync is a separate design |
| Skill → CLI subcommand mapping review (`nanite build`, `nanite lint`, etc.) | Ties into the broader post-migration skill review |
| Cross-session handoff (transfer live session to newly-booted session) | Current handoff is single-session; extension adds `source_session_id` to `session_handoffs` |
| Multi-tenant A2A impersonation check | MVP is single-user desktop; becomes relevant with hosted version |
| Nanite workflows (named scratch workspaces, pinned context manifest, output dirs) | Missing primitive surfaced during agent-workspaces verdict; user's ad-hoc workaround is the current workflow |
| Git-based rollback for install | `.archived/` approach works universally; git-based is a nice-to-have for users who commit agent config |

---

## 7. Open Questions

None. All decisions captured in the Decisions table.

---

## 8. References

- `2026-04-08-agent-adapter-architecture-design.md` — parent spec; defines `CLIAgentAdapter`, managed section protocol, `.agentrc` → `.nanite` rename
- `~/Projects-apps/agentrc/` — source repository being consolidated (to be archived)
- `~/.agentrc/` — current global home (to be replaced by `~/.nanite/`)
- `~/Projects-apps/nanite/internal/agent/adapter.go` — core adapter interface
- `~/Projects-apps/nanite/internal/store/a2a.go` — current A2A store layer
- `~/Projects-apps/nanite/internal/store/agents.go` — `AgentProfile`, `SessionAgent`, `session_agents` table
