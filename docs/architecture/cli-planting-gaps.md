# CLI planting gaps: Tether vs Nanite

Research note, no code changed. Compares what Tether plants when it launches a CLI
agent (Claude Code, Codex, agy) with what Nanite plants for the same runtimes, and
what an API-runtime Nanite session gets for the same concerns. Sources: Tether
`internal/app/{launch_prepare,codex_plant,agent_ops,mcp_hardening}.go`,
`internal/launch/`, `internal/skills/`, and the shared planter in
`libs/harness/core@v0.13.0/adapters/provider/bootdir_*.go`; Nanite
`internal/runtime/agent/bootdir*.go`, `definition_files.go`, `skill_plant.go`,
`host_mcp.go`, `internal/agentdir`, `internal/service/cognitive_views.go`.
`bootdir-retirement-0011.md` has the byte-level inventory of Nanite's planters.

Reading the tables: "API" is a native-loop session (no boot dir; context built per
turn in slots, tools from the authority-gated native registry). Nanite's CLI
planters already use the same libs planting layer as Tether, so most files match;
the gaps are in what feeds them.

## 1. Per-runtime tables

### Claude

| Item | Tether plants | Nanite CLI | Nanite API | Gap? | Cheap close |
| --- | --- | --- | --- | --- | --- |
| Instruction file | `CLAUDE.md` (prompt + MCP note); cwd is the workspace/worktree, so the repo's own `CLAUDE.md` auto-loads | `CLAUDE.md` in boot dir (cwd); repo `CLAUDE.md` NOT loaded, boot prompt only tells the agent to read it | Workspace slot walks up `AGENTS.md`/`CLAUDE.md` from the work dir | YES (work-root instructions) | Inline the work root's instruction files into the planted file (C1) |
| settings.json | `permissions.defaultMode` (from agent permission mode), `additionalDirectories`, `apiKeyHelper` | `defaultMode: acceptEdits` fixed, `additionalDirectories` = CLI roots, 0600 | none | partial (mode fixed, no apiKeyHelper) | Map posture into `claudeProviderConfigContent` (C4) |
| MCP | `.mcp.json`: `tether mcp --proxy` (confined, `TETHER_MCP_SERVERS` allow-list) + extra servers; `--strict-mcp-config` | `.mcp.json`: `nanite` server + host `mcp_servers`; strict-MCP, `--allowedTools=mcp__nanite__*` + per-grant rules | native tools via authority grants | Tether bridge absent unless registered | Register tether in the MCP registry, name it in host `mcp_servers` (C2) |
| Skills | `.claude/skills/<id>.md` (flat) from catalog `agent.skills` | `.claude/skills/<name>/` tree, granted + hash-approved + enabled; definition skills also under `agent/skills/` | skills slot listing + `skill_get`; granted trees under `agent/skills/` in session files | no | n/a |
| Hooks | none (daemon launches); only agent-boot plants them | none; mechanism wired (`DefaultBootDirHooks` empty, claude only) | n/a | no (policy, empty by design) | see section 3 |
| Allow rules | `--allowedTools`/mode only | allow rules on argv only (settings allow ignored in untrusted boot dir) | permission engine | no | n/a |
| Identity | `TETHER_*` env, session/task ids | `.sandbox/tether-identity.md` when URN known (`ActorURN` or profile `TetherURN`) | actor binding in the harness | maybe (C3) | see C3 |
| Env | `TETHER_TOKEN`, `TETHER_MCP_SERVERS`, task/project ids | `NANITE_*` in the MCP entry; none via `AmendEnv` | n/a | no | n/a |
| Extra files | launch-catalog `native_files` and `boot_dir_overlay` at provider paths | only `agent/<pinned resources>`, never provider files, by design | `agent/` files in workspace | intentional | none |

### Codex

| Item | Tether plants | Nanite CLI | Nanite API | Gap? | Cheap close |
| --- | --- | --- | --- | --- | --- |
| Instruction file | `AGENTS.md` (frontmatter + prompt); skills inlined as sections | `AGENTS.md` (`codexAgentsMD`); repo `AGENTS.md` not loaded | as Claude row | YES (work root) | C1 |
| config.toml | `approval_policy`/`sandbox_mode` from posture, `writable_roots`, `[mcp_servers.*]`, protected-path sandbox | same keys, fixed `never`/`workspace-write`, `[mcp_servers.nanite]`, 0600 | none | partial (posture fixed) | C4 |
| MCP | tether proxy + extra servers in config.toml | nanite only; host `mcp_servers` REFUSED (no per-tool gate) | native tools | YES, structural | not cheap (section 3) |
| Skills | inlined into `AGENTS.md` | `skills/<name>/` under `CODEX_HOME` | as Claude | no | n/a |
| Auth / env | `auth.json` copy or link, `CODEX_HOME` | `auth.json` link, `sessions` link, `CODEX_HOME=<boot>` | n/a | no | n/a |
| Hooks | none | refused (`hooksUnsupportedError`) | n/a | no | n/a |

### agy (Antigravity)

| Item | Tether plants | Nanite CLI | Nanite API | Gap? | Cheap close |
| --- | --- | --- | --- | --- | --- |
| Instruction file | `AGENTS.md` | `AGENTS.md` (`agyAgentsMD`) | as Claude | YES (work root) | C1 |
| MCP | `.agents/plugins/tether/mcp_config.json` (+ loopback, extra servers) | same file; nanite + host servers; allow rules in private `agy-home/.gemini/.../settings.json` | native tools | Tether bridge absent unless registered | C2 |
| Skills | `.agents/skills/<id>/SKILL.md` | same path, from granted skills | as Claude | no | n/a |
| Home / settings | never relocates HOME, never writes `~/.gemini`; browser shim planted | private HOME farm + per-agent `.gemini` (allow rules, path rules) | n/a | Nanite ahead; no browser shim | none |
| Allow rules | `--dangerously-skip-permissions` in bypass, else accept-edits | MCP and path allow rules from grants; `cli_allow` proposed, not built | permission engine | partial | not cheap |
| Hooks | none | refused | n/a | no | n/a |

## 2. Ranked cheap closes

All use Nanite's own store: the definition body and host settings that
`nanite agent load` publishes, the work root `agent run` resolves, the MCP registry
and grants. No new config format.

1. **C1. Work-root instruction files (all three runtimes).** `BuildCLAUDEMD`
   (`bootdir_claude.go`), `codexAgentsMD` (`bootdir_codex.go`), `agyAgentsMD`
   (`bootdir_agy.go`), fed from `composeBootdirParams` (`bootdir.go`), where
   `params.ProjectDir` and `CLIWritableRoots[0]` are the work root that `agent run`
   or `host.yaml directories[0]` set. Read `AGENTS.md`/`CLAUDE.md` there
   (regular file, size cap, not in the control plane, re-checked by
   `CLIRootDenyReason`) and append under a "Project instructions" heading. Snapshot
   at boot, refreshed by the existing re-plant path. Cost: one helper, three call
   sites. (A `--add-dir` env switch for Claude exists in newer versions but is
   unverified here, and does not help Codex or agy.)
2. **C2. Tether bridge for CLI agents, no code.** `host_mcp.go` already projects
   registry servers named in `host.yaml mcp_servers` into Claude `.mcp.json` and
   agy `mcp_config.json`, gated per granted tool. `internal/mcpconfig/tether.go`
   (`TetherMessagingConfig`, token-file check) builds the registry entry. Register
   it once, name it in `host.yaml`, grant the tools. Cost: a documented recipe
   (and a test that a loaded agent boots with it), not planter changes.
3. **C3. Identity for `agent run` sessions.** `tetherIdentityFile`
   (`tether_identity.go`) is a no-op without `ActorURN` or profile `TetherURN`.
   Check that `deps.ActorURN` is wired for sessions started from a loaded
   definition with an enrolled actor, and plant the file for them. Cost: wiring plus
   a test, once the binding is confirmed (section 4).
4. **C4. Posture from the host's existing profile.** `bootdir_provider_config.go`
   hard-codes Claude `acceptEdits` and Codex `never`/`workspace-write`. Tether
   derives both from the agent's permission mode. Map the host settings'
   `harness_profile` posture (admission already refuses what a CLI cannot enforce,
   e.g. read-only) into `claudeProviderConfigContent` and the Codex config
   renderer. Medium cost: needs a posture field on `SetupParams`
   (`bootdir-retirement-0011.md` notes the planters have none).
5. **C5. Skill authoring friction.** `agent load` only plants skills `agent.md`
   requests, at the pinned digest, with a host grant (`--grant-same-as`). The
   mismatch error already prints the packed digest. Cheap polish: make
   `agent load --dry-run` print the ready `content.digest` line for each
   `skills/<name>` folder. File: `internal/agentdir/agentdir.go` (`packSkill`),
   `internal/service/agent_load.go` notes.

## 3. Not worth closing now

- **Hooks.** Tether's daemon plants none; Nanite's mechanism is wired but empty by
  policy (soft-by-default doctrine). Codex and agy refuse hooks because an allow
  from a workspace hook is advisory. Nothing to copy.
- **Codex and OpenCode host `mcp_servers`.** They have no per-tool allow Nanite can
  set, so a projection would be ungated; refusing is the safe behavior.
- **Injected native files / boot-dir overlay.** Tether's launch catalog can write
  provider paths; Nanite deliberately limits definitions to `agent/` so a definition
  cannot reach `CLAUDE.md` or `.mcp.json`.
- **agy browser shim.** Tether plants it for headless login; Nanite shares the
  operator's auth by symlink, so no login runs.
- **Claude `apiKeyHelper`, workspace-trust seed.** Tether's trust seed is a legacy
  host-effect path; Nanite avoids writing `~/.claude.json`, and allow rules ride
  argv instead.
- **agy `cli_allow` list.** Already a proposed follow-up in `agent-runtime.md`;
  needs a host-settings field, so not a cheap close.
- **Tether's flat `.claude/skills/<id>.md` and Codex inlined skills.** Nanite's
  per-skill directory trees are the better shape; no gap.

## 4. Could not determine

- Whether `ActorURN` is bound for sessions created by `agent run` (C3); read only
  the planter side, not the enrollment wiring.
- Whether API hosts honor `host.yaml mcp_servers` (the loader comment implies only
  CLI hosts refuse them); not traced through the native registry.
- Tether's exact per-launch file set for agents launched by `agent-boot` (the
  sysadmin boot script): it plants its own charter, `settings.json` with hooks,
  Codex `config.toml` with `[projects]` trust, and plugin hooks. That is the
  operator path, not the daemon's, and was not compared line by line.
- Tether pins `libs/harness/core v0.13.0`, Nanite v0.15.0; the planter bytes may
  differ between them (the retirement note says v0.16.0 matches v0.15.0).
- `context-assembly.md` says `CLAUDE.md` is rewritten in a running CLI session on
  a prompt edit; the code returns `ArtifactRefreshUnavailable` for bound roots.
  The doc looks stale; not changed here.

## 5. Closure status (MVP push, 2026-10-29)

| Item | Status |
|---|---|
| C1 work-root instruction files | **Closed.** `workRootInstructions` (`internal/runtime/agent/workroot_instructions.go`) appends the work root's `AGENTS.md` (else `CLAUDE.md`) to `params.SystemPrompt` in `composeBootdirParams`, so all three CLIs plant it. 16 KiB cap, regular files only, skipped for roots `CLIRootDenyReason` refuses. |
| C3 identity for host-started sessions | **Closed.** A host-started session's profile id is the host settings id while an enrolled actor's `profile_id` is its binding URI, so `DurableActorURNForSession` never matched. It now also matches through the enabled binding of that host settings id (`internal/store/durable_agents.go`). |
| C2 Tether bridge for CLI agents | **Closed as a recipe**: `docs/guides/cli-tether-mcp.md`. For replies from an enrolled agent the MVP path is the Nanite-hosted `tether_send` tool instead, which needs no per-agent credential. |
| C4 posture from the host profile | **No cheap content today.** Admission already refuses anything a CLI cannot enforce (for example a `read-only` profile), so every admitted CLI host has the one `default` posture the planters hard-code. A posture field becomes worth adding only when a second enforceable posture exists. Left as is on purpose. |
| C5 skill digest friction | **Served by the existing error**: a digest mismatch already prints the packed digest (`agentdir.go`, "folder packs to ..."). Workflow: `agent load --dry-run`, copy the printed digest into `agent.md`. A dedicated dry-run listing was not built. |
