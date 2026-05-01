# Audit — Hardcoded paths and system-specific values

> **Ticket:** CW-20260430-0009 — Trust-agent permission redesign.
> **Scope:** `internal/`, `cmd/`, `pkg/`, `config/`, `plugins/`.
> **Patterns swept:** literal `~/X`, `/Users/`, `/home/`, `$HOME/X`, hardcoded
> user names, default fallback paths used when config is unset.
> **Author note:** the SP5 hot-fix (commit `151fc7b`) already removed the four
> personal entries (`~/Projects-apps`, `~/Projects`, `~/.nanite`, `~/.claude`)
> from `defaultDevToolsAllowedPaths` before this audit ran. The remaining
> exceptions found below are either legitimate (home-dir join) or
> well-bounded (org-default, env-overridable) — no Part 2 redesign blocker
> surfaces from this sweep.

## Classification taxonomy

| Class | Meaning | Disposition |
|---|---|---|
| `runtime-derived` | Uses `os.UserHomeDir()` + brand-namespaced subdir; auto-resolves per machine. | **Keep.** Correct pattern. |
| `org-default` | Convention shared by the user's org (e.g. `~/Projects-apps`) baked in as a template/default but env-overridable. | **Keep with env knob** (already exists for the load-bearing case). |
| `system` | Unix path conventions (`/etc/hosts`, `/var`) used in non-runtime contexts (test, comment, doc). | **Keep.** |
| `personal` | One specific developer's path that won't exist anywhere else. | **Delete or move to config.** |
| `test-fixture` | `_test.go` literal — runs only against synthetic state. | **Keep.** No-op. |

## Findings — runtime code

| File:Line | Current value | Class | Disposition | Notes |
|---|---|---|---|---|
| `cmd/nanite/main.go:543-553` (`defaultDevToolsAllowedPaths`) | `cfg.ProjectRoot()` → `os.Getwd()` → `nil` | `runtime-derived` | **Replace per Q4.** Drop the `os.Getwd()` fallback. New default = project root only. | Cwd-as-baseline risks scope leak when user starts in `~/`. The trust-agent redesign moves wider grants to explicit-mention parsing. |
| `internal/service/install/archive.go:13` (`ArchiveBase`) | `"~/Projects-apps/.archived"` | `org-default` | **Keep.** Already env-overridable via `NANITE_ARCHIVE_BASE` (`migrate.go:177`). | Audit 2026-04-10-installer/09 already classified this. The `~/` is tilde-expanded at use sites; missing dir is created on first archive. |
| `internal/assets/framework/config.yaml:100,107,108,112,131-...` | Various `~/Projects-apps/...` project entries | `org-default` (template) | **Keep.** | These are the *embedded sample* `~/.nanite/config.yaml` shipped via `nanite install`. Users edit/replace as part of project setup. Not runtime decisions. |
| `internal/muxproxy/client.go:60-62` | `expandHome(path)` — same tilde expansion | `runtime-derived` | **Keep.** | Generic expansion helper. Mirrors `internal/mcp/dev_tools.go:65-83`. |
| `internal/mcp/dev_tools.go:65-83` (`expandHome`) | Tilde expansion of input | `runtime-derived` | **Keep.** SP5 fix. | Required for LLM-supplied `~/X` paths and config-supplied entries. |
| `internal/config/config.go:108,279-285` | `filepath.Join(home, ".nanite", "nanite.yaml")` + `expandHome` | `runtime-derived` | **Keep.** | User-config canonical location. |
| `internal/config/config.go:135` (`ProjectRoot`) | `expandHome(c.Project.Root)` | `runtime-derived` | **Keep.** | Reads from yaml config; tilde-expand at the boundary. |
| `internal/skill/loader.go:27,45` | `filepath.Join(home, ".nanite", "skills")` | `runtime-derived` | **Keep.** | Brand-namespaced. |
| `internal/skill/discovery.go:41,52,59` | Project `.nanite/skills`, user `~/.nanite/skills`, project `.claude/skills` | `runtime-derived` | **Keep.** | Brand-namespaced; `.claude` is the cross-tool convention. |
| `internal/agent/loader.go:32` | `filepath.Join(home, ".nanite", "agents")` | `runtime-derived` | **Keep.** | |
| `internal/agent/discovery.go:53,60` | Project `.nanite/agents`, user `~/.nanite/agents` | `runtime-derived` | **Keep.** | |
| `internal/cli/installcmd/installcmd.go:51,73,255` | `filepath.Join(home, ".nanite")` | `runtime-derived` | **Keep.** | Install target. |
| `internal/service/install/install.go:74,256,292,377` | `filepath.Join(home, ".nanite")` and overrideable `archiveBaseOverride()` | `runtime-derived` (+ env override) | **Keep.** | |
| `internal/service/install/migrate.go:24,107,191` | `.nanite` paths | `runtime-derived` | **Keep.** | |
| `internal/service/install/scaffold.go:34` | `filepath.Join(projectDir, ".nanite")` | `runtime-derived` | **Keep.** | |
| `internal/service/install/resume.go:72,130` | `.nanite` under projectDir | `runtime-derived` | **Keep.** | |
| `internal/service/install/adopt.go:45` | `.nanite/config.yaml` under projectDir | `runtime-derived` | **Keep.** | |
| `internal/plugin/builtin/adapter-claude/plugin.go:130` | `filepath.Join(projectDir, ".claude", "agents")` | `runtime-derived` | **Keep.** | Cross-tool integration. |
| `internal/plugin/builtin/adapter-nanite-native/plugin.go:307,461,465,479,485,502` | `.nanite` and `.agentrc` joins | `runtime-derived` | **Keep.** | Includes legacy `.agentrc` fallback. |
| `internal/sandbox/sandbox.go:38` | `filepath.Join(home, baseDirName)` | `runtime-derived` | **Keep.** | `baseDirName` is brand-namespaced. |
| `internal/truncate/truncate.go:111` | `filepath.Join(home, ".nanite", "tool-output")` | `runtime-derived` | **Keep.** | |
| `internal/toolclient/skills.go:235` | `filepath.Join(home, DefaultSkillsDir)` | `runtime-derived` | **Keep.** | |
| `internal/tool/yaml_loader.go:57` | `filepath.Join(home, ".nanite", "tools")` | `runtime-derived` | **Keep.** | |
| `internal/reflex/loader.go:61` | `filepath.Join(home, ".nanite", "reflexes")` | `runtime-derived` | **Keep.** | |
| `internal/brand/brand.go:58` | `filepath.Join(home, "."+ID)` | `runtime-derived` | **Keep.** | Single source of truth for brand-namespaced home. |
| `cmd/nanite/plugin_logs.go:21` | `filepath.Join(home, "plugin-logs", id+".stderr.log")` | `runtime-derived` | **Note.** Could move under `.nanite/plugin-logs` for namespacing, but out-of-scope for this ticket. | |
| `cmd/nanite/plugin_install_flow.go:39,50` | `filepath.Join(home, "plugin-staging")`, `"plugin-catalog"` | `runtime-derived` | **Note.** Same observation — could move under `.nanite/`. Out-of-scope. | |

## Findings — non-runtime / docs / tests

| File:Line | Class | Notes |
|---|---|---|
| `internal/context/compaction_test.go:62,82,134` | `test-fixture` | Synthetic input. Keep. |
| `internal/sandbox/os_darwin_test.go:40,44` | `test-fixture` | Synthetic input. Keep. |
| `internal/service/install/state_test.go:14` | `test-fixture` | Synthetic input. Keep. |
| `internal/service/ingest_test.go:35,132` | `test-fixture` | Synthetic input. Keep. |
| `internal/plugin/install/install_test.go:165` | `test-fixture` | Synthetic input. Keep. |
| `internal/chat/sanitize_test.go:24` | `test-fixture` | Synthetic input. Keep. |
| `internal/skill/loader_test.go:26,29` | `test-fixture` (error message) | Keep. |
| `internal/agent/loader_test.go:26,29` | `test-fixture` (error message) | Keep. |
| `internal/config/config_test.go:86,87,147,150,153,165,210,211,233,275,277` | `test-fixture` | Keep. |
| `internal/mcp/dev_tools_test.go:331-440` | `test-fixture` | Tilde-expansion regression coverage. Keep. |
| `internal/service/install/archive_test.go:25` | `test-fixture` | Keep. |
| `docs/**` | `documentation` | Multiple references to `~/Projects-apps/...` in user-facing docs. Keep — these are explanations / examples / references to the user's own workspace. |
| `plugins/repos.yaml` | `org-default` | `hollis-labs/...` repo refs. Keep — this is the authoritative plugin catalog for the org. |

## Findings — config files

| File:Line | Class | Notes |
|---|---|---|
| `internal/assets/framework/config.yaml:90-180` | `org-default` (template) | Embedded sample shipped by `nanite install`. The `~/Projects-apps/...` entries are the user's environment; they end up in the user's `~/.nanite/config.yaml` and are user-editable from then on. |

## SP5 backout — already landed

Per Q7 the SP5 hot-fix (`151fc7b`) removed the four hardcoded user-specific
entries from `defaultDevToolsAllowedPaths`:

```diff
-     "~/Projects-apps",
-     "~/Projects",
-     "~/.nanite",
-     "~/.claude",
```

…and preserved both:

- The tilde-expansion bug fix (`expandHome` in `dev_tools.go`).
- The configurable `dev_tools_allowed_paths` allow-list infrastructure.

The current state of `defaultDevToolsAllowedPaths(cfg)` is:

```go
1. cfg.ProjectRoot()  →  if set
2. os.Getwd()         →  fallback
3. nil                →  no implicit access
```

## Q4 follow-up — drop the cwd fallback

Decision Q4 in CW-20260430-0009 says *"Always-granted baseline: project root,
NOT cwd."* The current code still has cwd as a fallback (line 549). The
trust-agent redesign drops this:

- Default = project root (or empty if unset).
- Wider scope comes from explicit-mention parsing (Q1-Q3) or
  `dev_tools_allowed_paths` config.

This is the only structural change indicated by the audit. The implementation
in Part 2 makes that drop and adds the explicit-mention + notify-pause layers.

## Checkpoint decision

**Proceeded to Part 2.** No surprise findings — every non-runtime-derived
hardcoded path in the runtime code path is either:

1. Already env-overridable (`NANITE_ARCHIVE_BASE`).
2. An embedded sample/template intended to be replaced by the user.
3. Slated for removal by the locked design (cwd fallback in `defaultDevToolsAllowedPaths`).

No findings change the redesign.
