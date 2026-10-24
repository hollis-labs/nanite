# Retiring the per-runtime boot-dir planters (CW-20261024-0011)

Status: analysis and verification only. No production code changed. The harness
(`internal/runtime/agent/bootdir_retire_golden_test.go`, golden
`internal/runtime/agent/testdata/bootdir_retire_golden.txt`) is the only code in
this change, and it is test-only. No model turn was made and no CLI was launched.

Base: `origin/main` at `5437abf3` (includes #569 agy farm HOME, #574 agy path rules).
Library: `github.com/hollis-labs/libs/harness/core` v0.15.0, the version Nanite pins.
v0.16.0 was also run against the projection path and is byte-identical (its change
log adds only card events); this analysis holds for both.

## 1. Bottom line

* Do not retire the four planters by pointing them at `planting.Plant` /
  `launcher.Prepare`. That path (the "projection" path) silently loses the Claude
  permission mode, the Claude instruction body, hooks, and plants an empty
  `auth.json` placeholder. It also forces the library's boot-dir naming over Nanite's
  forensic naming.
* Do retire the per-runtime `*PlantSpec` builders in favor of one call to the
  layout-table renderer: `plan.Resolve` per field, then
  `workspace/render.Render` (the renderer `boot.Plan` and Tether use), and feed the
  returned `artifact.Tree` to the planting call Nanite already uses
  (`plant.SharedPlanter{Authorize}` inside the `makeAuthorizedBootDir` custody
  port). The harness materialized exactly that, through Nanite's own custody
  port, and read the disk back. With Nanite's native settings declared as host
  defaults, Codex `config.toml`, Claude `settings.json` and every instruction, kickoff,
  sandbox, skill and definition file are byte-identical to today's.
* The planter is the smaller half of a boot. What is outside it (argv extras, env
  amendments, links, the agy HOME farm, the instruction body composers, the hooks
  refusal, the host-MCP gate) is not replaced and must stay (section 6). Retirement
  deletes roughly the PlantSpec builders, `bootdir_provider_config.go` and
  `bootDirArtifactTree`; it does not delete `Layout`.
* Recommended order: Codex, Claude, OpenCode, agy (agy only partially). One
  live smoke per runtime is still wanted before flipping Claude and OpenCode
  (section 9). Codex and agy can be flipped on offline evidence if the stated
  conditions hold.

## 2. What the shared planner is (and is not)

There are two shared paths in the libs. They give different bytes.

| Path | Entry point | Who uses it | Posture/settings source |
| --- | --- | --- | --- |
| projection | `launcher.Compile` + `Prepare` + `planting.PrepareExecution` / `Plant` (`ProjectExecution` is its pure half) | go-agent-sessions / legacy BootDirSpec consumers | the adapter's own fields; `Provider.Permission` only reaches argv/env |
| canonical | `plan.Resolve(Request{Key, Posture, Components})` per field, then `render.Render(Request{Inputs, Native, Overlays, Roots})` | `boot.Plan`, Tether | typed native slots, bound posture, host-declared defaults |

The canonical path is pure (no I/O, no credential reads), takes caller-supplied
instruction bodies verbatim, and refuses combinations it cannot make safe
(`host_policy_mixture`, `posture_required`, `reserved_destination`). It returns a
tree plus launch deltas (`Binding.Argv/Environment/CWD`), credential link effects, and
diagnostics. It does not return argv for the executable, the prompt, `--allowedTools`,
`--strict-mcp-config`, or `HOME`.

Existing Nanite use: `skill_plant.go` already reads the same table
(`plan.Table()`), and `bootdir_parity_test.go` checks that every table path is planted.
That test compares paths and the table's compatibility mode bits only. It does not
compare bytes, canonical modes, settings keys or launch bindings, which is why the
differences below were not visible before.

## 3. Inventory: what each planter plants today

Boot root is `0700` (`os.MkdirTemp`). Files default to `0644`; Nanite forces `0600`
when the bytes contain `NANITE_AUTH_TOKEN` (`bootDirArtifactTree`), and Codex
`config.toml` is always `0600`. Directories are not planted explicitly.
Common to all four: `boot.md` (kickoff), `.sandbox/agent-context.md`,
`.sandbox/envelope-schema.md`, optional `.sandbox/tether-identity.md`,
definition files under `agent/` (keys outside `agent/` dropped), skills from
`ResolvePlantableSkills` (granted, approved hash current, `Enabled`), delivered
`agent/skills/<name>/` copies at the runtime's skill root, `.mcp.json` mirror
(Claude's real MCP file; unread by the others).

| | Claude | Codex | OpenCode | agy |
| --- | --- | --- | --- | --- |
| Instruction file | `CLAUDE.md` = `BuildCLAUDEMD(name, desc, prompt)` | `AGENTS.md` = `provider.AgentsMD(...)` | `agents/<slug>.md` = `# Name\n\n<prompt>\n` | `AGENTS.md` = `provider.AgentsMD(...)` |
| Settings | `.claude/settings.json` 0644: `permissions.defaultMode: acceptEdits`, `permissions.additionalDirectories` = `CLIWritableRoots` | `config.toml` 0600: `approval_policy = "never"`, `sandbox_mode = "workspace-write"`, `[sandbox_workspace_write] writable_roots`, `[mcp_servers.nanite]` | `opencode.json` 0644/0600: `agent.<slug>.prompt = {file:./agents/<slug>.md}`, `mcp.nanite` (type local, enabled) | none in the boot dir; private `HOME/.gemini` (below) |
| MCP shape | `.mcp.json` `{"mcpServers": {nanite, host...}}` | in `config.toml`; `.mcp.json` mirror also planted (unread) | in `opencode.json`; `.mcp.json` mirror also planted (unread) | `.agents/plugins/tether/mcp_config.json` 0600 + `plugin.json` |
| Other files | hooks: `hooks/claude/<name>` 0700 + `settings.hooks`; none by default | links: `auth.json` -> host login, `sessions` -> `~/.nanite/codex-sessions/<id>` (Long-lived/Resume) | `agents.json` | `agy-home/` symlink farm, private `~/.nanite/agy-gemini/<slug>/antigravity-cli/settings.json` 0600 (MCP allow rules, path allow rules) and `nanite-owned-rules.json` |
| Skills at | `.claude/skills/<n>/` | `skills/<n>/` | `skills/<n>/` and `.opencode/skills/<n>/` (Nanite overlay) | `.agents/skills/<n>/` |
| Hooks | wired | refused (`hooksUnsupportedError`) | refused | refused |
| Env (`AmendEnv`) | none | `CODEX_HOME=<boot>` | `OPENCODE_CONFIG_DIR=<boot>`, `XDG_CONFIG_HOME=<boot>/xdg-config` | `HOME=<boot>/agy-home` |
| Spawn cwd | boot | boot | project dir (boot when empty) | boot |
| Argv extras (`selectAdapter`) | `--mcp-config <boot>/.mcp.json --strict-mcp-config`, `--allowedTools=mcp__nanite__*[,host rules]`, `--setting-sources project,local` (host-MCP gated), `--add-dir <work root> <dirs...>` | none | none | repeated `--add-dir <root>`, `--dangerously-skip-permissions` in yolo |

Content sources that stay Nanite's: `BuildCLAUDEMD`, `codexAgentsMD`, `agyAgentsMD`,
`opencodeAgentMD`, `sandboxFiles`, `tetherIdentityFile`, `definitionFilesFor`,
`naniteMCPEntry` / `renderMCPJSONWith`, `HostMCP` projection.

## 4. The harness and its results

`TestBootdirRetireGolden` plants every runtime x scenario with the CURRENT planter
into a temp dir and reads the tree back (paths, modes, bytes, symlink targets), then
plants the same `SetupParams` through four shared variants and diffs. Normalised for
temp dir names, home, project, roots and the session credential. Compared against
`testdata/bootdir_retire_golden.txt`, so a planter or dependency change that moves any
difference fails; regenerate with `-update-bootdir-golden` after reading this doc.

Scenarios (13): minimal; mcp; mcp-no-credential; host-mcp-and-grants;
skills-with-disabled (one enabled, one disabled); definition-files (including a
rogue key outside `agent/` and a delivered skill); directories (work root + 2 roots);
hooks; posture-absent; posture-default; posture-accept-edits; posture-plan;
posture-yolo (posture applies to the shared plan only: the planters have no posture
input).

Variants:

* `projection-default`: `planting.ProjectExecution`, library resolver's adapter.
* `projection-pinned`: same with `WithAdapter` carrying what the planters hard-code
  (Claude permission mode and additional directories, Codex sandbox mode and writable
  roots, OpenCode agent slug).
* `canonical-strict`: `plan.Resolve` + `render.Render`, posture bound exactly as the
  scenario says, no host-declared native defaults.
* `canonical-hostdefaults`: same, but Nanite's own native settings declared as host
  defaults (Claude `defaultMode acceptEdits` and `additionalDirectories`; Codex
  `never` / `workspace-write` / `writable_roots`) and no posture bound (the renderer
  refuses to mix the two). Materialized through `makeAuthorizedBootDir` +
  `plant.SharedPlanter` and read back from disk.

What the shared side cannot express was built as the closest honest equivalent and is
marked in the test header: Nanite's own files, skills and definition files ride
`NativeFiles` (projection) or `Overlays` / skill `Package` inputs (canonical); the
Nanite MCP server rides `MCPSpec.Servers` / `Native.Servers`; Claude hooks ride a
`hooks` slot plus a 0700 overlay. Anything left over shows as "only in CURRENT".

### Results (sum over 13 scenarios; `-` only current, `+` only shared, `M` mode, `B` bytes)

```
claude/projection-default       identical=0  -1  +6  M1   B33
claude/projection-pinned        identical=0  -1  +6  M1   B21
claude/canonical-strict         identical=0  -0  +6  M14  B19
claude/canonical-hostdefaults   identical=0  -0  +6  M14  B7
codex/projection-default        identical=0  -12 +6  M13  B32
codex/projection-pinned         identical=0  -12 +6  M13  B31
codex/canonical-strict          identical=0  -8  +0  M0   B4     (8 more scenarios refuse: posture_required)
codex/canonical-hostdefaults    identical=0  -30 +0  M0   B1
opencode/projection-default     identical=0  -28 +18 M8   B18
opencode/projection-pinned      identical=0  -16 +6  M8   B30
opencode/canonical-strict       identical=0  -24 +0  M7   B24
opencode/canonical-hostdefaults identical=0  -24 +0  M7   B24
antigravity/projection-default  identical=0  -84 +6  M0   B18
antigravity/projection-pinned   identical=0  -84 +6  M0   B18
antigravity/canonical-strict    identical=0  -84 +6  M0   B6
antigravity/canonical-hostdefaults identical=0 -84 +6 M0   B6
```

`identical=0` everywhere is expected: every shared variant differs in at least the
mode or JSON shape of one file in every scenario. The counts that matter are which
differences are real, listed next. Hooks on Codex, OpenCode and agy: CURRENT refuses
(`hooksUnsupportedError`), the shared API has no hook input at all (6 scenarios).

### Reading the counts per runtime

* Claude, canonical-hostdefaults: 0 files only in CURRENT, `.claude/settings.json`
  bytes identical, the only byte diff left is `.mcp.json` (`"type": "stdio"`), plus a
  mode tightening on `settings.json` in 13/13 scenarios and an extra `.mcp.json` where
  CURRENT plants none (6).
* Codex, canonical-hostdefaults: `config.toml`, `AGENTS.md`, `boot.md`, `.sandbox/*`,
  skills and definition files byte-identical. What differs is only what the renderer
  does not plant: `.mcp.json` (6 scenarios) and the two links `auth.json` (12) and
  `sessions` (12), which stay Nanite's.
* OpenCode: real differences in every scenario (agent file format, `opencode.json`
  keys, `agents.json`, `.opencode/skills`), and the isolation env is gone.
* agy: 84 entries are the HOME farm and private settings (7 per scenario x 12; the
  hooks scenario errors on both sides). Only `mcp_config.json` (a trailing newline),
  an extra plugin file when no MCP is configured, and (projection path) the `AGENTS.md`
  description line differ in the planted files.

## 5. Difference table

Class: IMP intentional improvement, NEU neutral, REG regression unless handled,
HOLD must stay outside the planter. Rating is the risk if left unhandled:
H high, M medium, L low. "Evidence" is where in the golden file.

### Claude

| # | Difference | Current -> shared | Class | Risk | Notes / evidence |
| --- | --- | --- | --- | --- | --- |
| C1 | settings.json permission mode (projection-default, canonical-strict with no posture) | `{"permissions":{"defaultMode":"acceptEdits"}}` -> `{}` (3 bytes) | REG | H sandbox posture | Headless Claude with no `defaultMode` denies edits it cannot approve. Nanite must bind posture `accept-edits` (canonical emits `defaultMode: acceptEdits`, bytes identical, scenario `posture-accept-edits`) or declare host defaults. Absent posture is the default Nanite path today. |
| C2 | settings.json mode | 0644 -> 0600 | IMP | L creds-in-files | File can carry env and apiKeyHelper. Canonical table mandates 0600. 13/13. |
| C3 | `additionalDirectories` with a bound posture | `["<PROJECT>","<ROOT1>","<ROOT2>"]` -> refused (`host_policy_mixture`) or dropped | REG, low impact | L | Claude ignores `permissions.additionalDirectories` in an untrusted workspace and every boot dir is one (see `launchExtra.AddDirs`), so the grant that holds is argv `--add-dir`. Keep argv. |
| C4 | CLAUDE.md body (projection path) | 1167 -> 37 bytes | REG | H behaviour | The projection path renders its own short body. Canonical takes the body verbatim, so `BuildCLAUDEMD` must stay and be passed in. Envelope rules, agent context pointers are in that body. |
| C5 | `.mcp.json` server entries | gain `"type": "stdio"` | NEU | L | Valid Claude MCP shape; unverified against the live CLI (cheap check, section 9). |
| C6 | `.mcp.json` with no MCP configured | not planted -> planted 0600 (6) | NEU | L | `--mcp-config` then names a file with no servers; same effect as strict-with-nothing. |
| C7 | `.mcp.json` mode when there is no credential in it | 0644 -> 0600 | IMP | L | It still names the DB path. |
| C8 | launch argv | CURRENT extras vs table `--add-dir <P> --mcp-config <boot>/.mcp.json` | HOLD | H security | The table gives neither `--strict-mcp-config`, `--allowedTools=mcp__nanite__*,<host rules>`, `--setting-sources project,local`, nor the extra `--add-dir` roots. Dropping them re-opens user-level MCP servers (CW-20261001-0221) or refuses every MCP call (CW-20261001-0411). The table's two variadic flags also need a delimiter (render doc). |
| C9 | hooks | settings `hooks.Stop[...]` + `hooks/claude/stop.sh` 0700 -> projection: lost; canonical: reproduced byte-for-byte | HOLD (projection) | M | `DefaultBootDirHooks` is empty, so no production impact today. Canonical: `hooks` slot + 0700 overlay verified identical. |
| C10 | yolo | settings `acceptEdits` + argv developer variant (`--dangerously-skip-permissions`) -> canonical settings `defaultMode: "bypassPermissions"` (`66` vs `60` bytes), projection argv `--permission-mode bypassPermissions` | REG | M | `errHostMCPNoGate` exists because the developer variant drops the host-MCP gate; binding yolo into settings must keep that refusal. |

### Codex

| # | Difference | Current -> shared | Class | Risk | Notes |
| --- | --- | --- | --- | --- | --- |
| X1 | posture absent | `approval_policy = "never"` + `sandbox_mode = "workspace-write"` -> `posture_required` refusal (8 scenarios) | REG | H | The earlier unverifiable finding, now verified: with no bound posture and no host-declared default, the renderer refuses. Not a silent drop in 0.15.0: it fails the plan. |
| X2 | nearest bound posture `accept-edits` | `approval_policy = "never"` -> `"on-request"` | REG | H sandbox posture | `on-request` is the setting that deadlocks a headless codex (the live bug `bootdir_provider_config.go` fixed). The table has no posture that maps to `never` + `workspace-write`; `plan` is `never`/`read-only`, `yolo` is `never`/`danger-full-access`. |
| X3 | posture `yolo` | `sandbox_mode = "workspace-write"` -> `"danger-full-access"` (62 vs 59 bytes) | REG | H sandbox | Only if Nanite maps its own yolo to the shared yolo. |
| X4 | `writable_roots` with a bound posture | `[sandbox_workspace_write] writable_roots = ["<PROJECT>","<ROOT1>","<ROOT2>"]` -> refused `host_policy_mixture`; absent in canonical-strict | REG | H sandbox | Roots and posture cannot be combined; the roots must ride as a host-declared default. |
| X5 | host-declared defaults path | `config.toml`, `AGENTS.md`, `.sandbox/*`, skills identical | IMP | -- | The only path that preserves today's bytes: declare `never`/`workspace-write`/roots as host defaults and bind no posture. Diagnostic `posture_absent: host-declared native policy retained`. |
| X6 | `.mcp.json` mirror | planted 0600 with the session token -> not planted | IMP | L | One fewer copy of the agent credential in the boot dir. Codex never reads it. |
| X7 | `auth.json` | symlink to host login (after plant) -> projection: empty 0600 regular file placeholder; canonical: a link effect, no file | HOLD | H credentials | `linkCodexHostAuth` must keep running after the plant (it removes then links). Forget it and every call 401s (CW-20261001-0021). |
| X8 | `sessions` link | persisted symlink for Long-lived/Resume -> not produced | HOLD | H function | `linkCodexSessions`; resume of a codex thread fails without it (CW-20261024-0034). |
| X9 | `AGENTS.md` description line (projection) | 118 -> 83 bytes | NEU | L | Canonical: body is caller-supplied, identical. |
| X10 | hooks | refused -> shared API has no input | HOLD | M | The refusal must run before planning or a gate is silently skipped. |
| X11 | host MCP servers | not in `config.toml` (admission refuses Codex) -> appended as `[mcp_servers.<name>]` if passed | HOLD | M | Keep `HostMCPSupported` admission; a server passed in would be planted without a per-tool gate. |

### OpenCode

| # | Difference | Current -> shared | Class | Risk | Notes |
| --- | --- | --- | --- | --- | --- |
| O1 | isolation env | `OPENCODE_CONFIG_DIR=<boot>` + `XDG_CONFIG_HOME=<boot>/xdg-config` -> only `OPENCODE_CONFIG_DIR` | HOLD | H security | Without the XDG redirect the operator's own `~/.config/opencode` MCP servers load, among them one that opens Nanite's database inside the sandbox (CW-20261001-0239). `AmendEnv` stays. |
| O2 | agent file | `# Golden Agent\n\n...` -> front matter `---\ndescription: Launch agent golden-agent\nmode: primary\n---` and no H1 | IMP/NEU | M | Matches the table's "native primary-agent header". Needs one `opencode` check that `--agent <slug>` finds it. |
| O3 | agent name | display name (`agents/Golden Agent.md`, projection-default) vs slug | REG | M | The `{agent}` component must be the slug (`agentSlug`) or `render` refuses the component (`ValidateComponent`, from code, not exercised) and `--agent` mismatches. |
| O4 | `opencode.json` keys | `agent.<slug>.prompt = {file:./agents/<slug>.md}` -> absent; `permission` slot per posture | NEU/REG | M | Per posture: default `edit ask, bash ask`; accept-edits `edit allow, bash ask`; plan `edit deny, bash ask`; yolo allows `bash`, `edit`, `webfetch`, `external_directory`, `doom_loop`. Absent posture -> no `permission` key, as today. Headless behaviour of `ask` is unmeasured. |
| O5 | `agents.json` | planted 0644 -> not planted | NEU | L | Unread legacy index (table doc: "removals describe the legacy table"). |
| O6 | `opencode.json` mode | 0644 (no credential in it) -> 0600 | IMP | L | 7 scenarios; with MCP+token it is already 0600. |
| O7 | `.mcp.json` mirror | planted 0600 with token -> not planted | IMP | L | Unread. |
| O8 | `.opencode/skills/<n>/` | Nanite overlay copy -> not produced | NEU | L | Kept "because it is not settled which of the two OPENCODE_CONFIG_DIR reaches"; the table evidence (`O2`) says `skills/<n>/`. Can ride an overlay if wanted. |

### agy

| # | Difference | Current -> shared | Class | Risk | Notes |
| --- | --- | --- | --- | --- | --- |
| A1 | HOME farm, private `.gemini`, `settings.json` allow rules, `nanite-owned-rules.json`, `HOME` env | 7 entries per scenario -> none | HOLD | H | Headless agy auto-denies every MCP and path tool without `permissions.allow` rules (`mcp(tether_<server>/<tool>)`, `read_file(<root>)`, `write_file(<root>)`), and the only safe place for them is a private HOME (PR #569, #574). The table models agy permissions as `runtime-binding` only (diagnostic `native_permission_omitted`). Stays entirely outside the planter. |
| A2 | `mcp_config.json` | trailing newline added (333 -> 334 bytes) | NEU | L | |
| A3 | plugin files with no MCP configured | not planted -> planted 0600 (6) | NEU | L | |
| A4 | argv | one `--add-dir` per root -> table `--add-dir <P>` only (projection path with posture yolo also adds `--dangerously-skip-permissions`) | HOLD | M | Extra roots stay on argv (`addDirArgs`). |
| A5 | `AGENTS.md` description line (projection) | 118 -> 83 bytes | NEU | L | Canonical identical (caller body). |
| A6 | hooks | refused -> no input | HOLD | M | As Codex. |

### Cross-runtime

| # | Difference | Result | Class |
| --- | --- | --- | --- |
| K1 | `.sandbox/*`, tether identity, `boot.md` | byte-identical in every variant | -- |
| K2 | skill files | same paths and bytes at `.claude/skills`, `skills`, `.agents/skills` (OpenCode except O8); disabled skill absent in every variant, asserted per runtime/variant/scenario | -- |
| K3 | delivered definition skills (`agent/skills/<n>/...`) | identical when modelled as skill packages; an overlay at a skill root is refused by `render` (`reserved_destination`, from `reserved()` in render.go, not exercised) | HOLD |
| K4 | file modes inside skills | Nanite's vendor `FileMap` has no modes, every vendored file is 0644 (scripts not executable). The table preserves declared modes (setuid and group/other-write stripped). Taking the table's mode needs the vendor store to record modes | OPEN |
| K5 | credential-bearing file modes | today: 0600 only if the bytes contain `NANITE_AUTH_TOKEN`; canonical: 0600 for every MCP/settings/config row | IMP |
| K6 | boot dir naming | `nanite-boot-<provider>-<session>-r<run>-*` (forensic, CW-20260515-0025) vs `launcher.Prepare`'s plan-hash naming | HOLD, avoided by the render-only path |

## 6. What lives outside the planter and would be lost

None of this is produced by any shared variant (the harness shows each as "only in
CURRENT" or in the `launch:` lines).

* argv: `strictMCPArgs`, `claudeMCPAllowArgs`, `hostMCPSettingSourcesArgs`,
  `addDirArgs`, `agyPermissionArgs`, `modelArgs` (selection extras in `selectAdapter`).
* `AmendEnv`: `CODEX_HOME`, `OPENCODE_CONFIG_DIR` + `XDG_CONFIG_HOME`, agy `HOME`.
* `SpawnWorkdir` (OpenCode runs in the project dir), `BootPrompt`, `BootMode`.
* Post-plant effects: `linkCodexHostAuth`, `linkCodexSessions`, `plantAgyHome`
  (`writeAgySettings`, `agyAllowRules`).
* `Populate` and `RegenerateSystemPromptSlot` return typed `ArtifactRefreshUnavailable`;
  they are not planter code and must stay on `Layout`.
* Custody: `makeAuthorizedBootDir` (authorizer + seal) and `bootArtifactEvidence` /
  `BootArtifactFailure`. Verified compatible: the render tree materializes through
  `plant.SharedPlanter{Authorize}` unchanged (every canonical run that rendered also materialized).
* `hooksUnsupportedError` refusals for Codex, OpenCode and agy.
* The host-MCP gate (`HostMCPSupported`, `ValidateHostMCPServer`, `errHostMCPNoGate`).
* Instruction body composers (`BuildCLAUDEMD`, `codexAgentsMD`, `agyAgentsMD`,
  `opencodeAgentMD`) and every other content source listed in section 3.
* `makeBootDir` naming and `mkdirTempCanonical`.
* File-mode overrides: the agy `0600` override and the token-content `0600` rule
  disappear into the table's fixed modes (improvement, K5); nothing else relied on them.

## 7. Skills

* Install from the agent directory, not only list. Today there are two sources. (1)
  Granted skills: `ResolvePlantableSkills` (grant present, approved hash equals the
  catalog hash, catalog row present, `Enabled`) then `SkillPlantFiles` from the
  vendor store at the runtime's table root (`sharedSkillRoot`, so Nanite and Tether
  agree). (2) The definition's `agent/skills/<name>/...` files (the "agent
  directory" of the definition, `agentpolicy.FilesRoot/SkillsDir`) are copied to the
  runtime's skill root by `definitionFilesFor` on a fresh boot. Mid-session
  `PlantAgentSkillFiles` returns `ArtifactRefreshUnavailable`: installation happens
  at boot only. `cognitive_views.go` lists them. If "agent directory" means an
  on-disk directory such as `~/.agents/skills`, that is not implemented and agy
  deliberately does not link the real `~/.agents`: see open question 8.
* Disabled skills. The skip is in `ResolvePlantableSkills` (`!sk.Enabled`). The
  shared planner has no notion of enabled/approved/granted: a skill handed to it is
  planted. So the filter must stay caller-side and the test now asserts, for every
  runtime, variant and scenario, that `disabled-skill` is never planted. Scenario
  `skills-with-disabled`; also covered by `TestResolvePlantableSkills`.
* Table roots confirmed against the harness: Claude `.claude/skills/<n>/`,
  Codex `skills/<n>/` (boot; the table says the `.agents/skills` path is only for the
  installed layer, measured `--cd` probe loses it), OpenCode `skills/<n>/`, agy
  `.agents/skills/<n>/`. All identical to what Nanite plants except the OpenCode
  `.opencode/skills` copy (O8).

## 8. Staged migration

Mechanism, for every stage: a per-runtime switch read where `bootdirLayoutFor` builds
the layout, `NANITE_BOOTDIR_SHARED=<comma list of runtimes>` (default empty means all
legacy), in the same style as `NANITE_CLAUDE_STRICT_MCP` and
`NANITE_OPENCODE_ISOLATE_CONFIG`. A runtime on the list calls the new
`renderPlan(runtime, params)`; off the list it calls today's `*PlantSpec`. Rollback
per runtime is removing it from the list and restarting Nanite; no data change,
because boot dirs are per session and ephemeral, and a resumed session replants
(inactive-only contract). Legacy code is deleted only one release after the last
runtime has run on the new path without a rollback.

0. This change: harness and golden, no behaviour change. Add the "must stay
   identical" subset as plain assertions in the migration PR (Codex `config.toml`,
   Claude `settings.json`, instruction/kickoff/sandbox/skill files).
1. Codex first. Byte-identical on the host-defaults path, smallest surface, loud and
   quick failure mode (a headless hang), and the thing the rewrite touches least
   (the links and `CODEX_HOME` stay). Declare `never` / `workspace-write` /
   `writable_roots` as host defaults, bind no posture. Do not bind posture
   `accept-edits` (X2). Keep `linkCodexHostAuth` and `linkCodexSessions` after the plant.
   Side effect: `.mcp.json` mirror disappears (X6).
2. Claude second. Identical bytes with host defaults except `settings.json` 0600 and
   `.mcp.json` `"type": "stdio"`; most used, so after Codex proves the mechanism. Keep
   all argv extras (C8) and pass `BuildCLAUDEMD` as the body (C4). Needs the live
   smoke below. Hooks via slot + 0700 overlay.
3. OpenCode third. Real format changes (O2, O4, O5), so it needs both `opencode debug
   config` (no model turn) and one live turn. Keep `AmendEnv` (O1). Pass the slug as
   the agent (O3). Decide O8.
4. agy last, and only the planted files (plugin, MCP config, `AGENTS.md`, skills).
   `plantAgyHome`, `agy_mcp_allow.go`, `agy_path_allow.go` and `AmendEnv` stay as they
   are. The `HOME` farm and settings are 84 of agy's differences and nothing in the
   shared planner models them. It is acceptable for agy never to move.
5. Cleanup, one release later: delete the four `*PlantSpec` builders and their planter
   types, `bootdir_provider_config.go`, `bootDirArtifactTree`/`plantSpec` (replaced by
   one plant of the render tree), the `.mcp.json` mirrors' code for Codex/OpenCode.
   Keep `Layout`, `bootdir_authority.go`, `bootdir_hooks.go` (declaration), the codex
   and agy link files, `bootdir_common.go`.

## 9. Recommendation and live-launch conditions

Proceed, in the order above, via the canonical path only. Do not adopt
`planting.Plant`/`launcher.Prepare` for Nanite boot dirs. Land the harness first.

Conditions under which a real CLI launch is still required (nothing was launched
here; estimates assume one trivial prompt per turn, cached system prompt, small
tool output):

| Runtime | Live launch needed when | Turns | Spend estimate |
| --- | --- | --- | --- |
| Claude | before flipping (C2, C5: 0600 `settings.json` and `"type": "stdio"` in `.mcp.json` untested against the real CLI); after any Claude version bump that touches settings or MCP loading | 2: one that calls a Nanite MCP tool, one that writes a file in the work root (proves `acceptEdits` and `--allowedTools`) | about 25-40k tokens, mostly input |
| Codex | only if a bound posture is chosen instead of host defaults (X2, X3: `on-request` can hang headless, `danger-full-access` is a sandbox change); or after a codex bump that changes how `CODEX_HOME` config or `auth.json` links behave. Not needed on the host-defaults path: `config.toml` is byte-identical offline | 0, or 2 if posture is bound (write in a writable root, write outside it) | 0, or about 20-35k tokens |
| OpenCode | before flipping (O2, O4): `--agent <slug>` resolving the front-matter agent file, and what `ask` does headless. `opencode debug config` answers the first with no model turn | 1-2 | about 20-35k tokens, provider dependent |
| agy | only if the planted plugin files change (A2/A3 are cosmetic) or the farm moves; not for the flip itself because the farm is untouched | 0, or 2-3 | 0, or about 30-50k tokens |

Total if everything is required: about 8 turns, roughly 100-160k tokens. Everything
above that is a CLI-version regression check, not a migration check.

## 10. Open questions needing a human decision

1. Posture ownership. Bind Nanite's permission mode to the shared posture (Codex
   gets `on-request` for accept-edits, X2) or declare today's native settings as host
   defaults forever (keeps bytes, permanent exception to the table)? Recommendation:
   host defaults for Codex and Claude until the posture table has a `never` +
   `workspace-write` cell (needs a libs change).
2. Accept 0600 on `settings.json` / `opencode.json`, and Claude `"type": "stdio"` in
   `.mcp.json` (C2, C5, O6)?
3. Drop the unread `.mcp.json` mirrors for Codex and OpenCode (removes a copy of the
   agent credential from the boot dir, X6, O7)?
4. OpenCode: move to the table's `agents/<slug>.md` front matter, no `agents.json`,
   no `agent` key in `opencode.json`; keep the `.opencode/skills` overlay or drop it
   (O2-O8)?
5. Skill exec bits: let the vendor store record file modes so the table's mode rule
   applies (K4)? Today vendored scripts are not executable; honouring modes adds an
   execution surface.
6. Library: the canonical renderer is public in harness/core v0.15.0 but its
   `workspace/render` and `adapters/*/nativefiles` packages are new imports for the
   runtime package; confirm they are stable enough to pin, and whether the layout
   owner will add a `never`/`workspace-write` posture cell.
7. Claude yolo: settings `bypassPermissions` plus the developer variant, or keep
   today's `acceptEdits` settings and argv-only yolo (C10)? The host-MCP refusal
   `errHostMCPNoGate` must survive either way.
8. What "install skills from the agent directory" means: the definition's
   `agent/` files root (already installed at boot, section 7) or an on-disk
   directory (not implemented; agy hides `~/.agents` on purpose). Also: is boot-only
   installation acceptable, given `PlantAgentSkillFiles` mid-session is
   `ArtifactRefreshUnavailable`?
9. Is agy allowed to stay on the legacy planter indefinitely (stage 4)?
10. Authorization for the live smokes in section 9 and their spend.

## 11. Conflicts with concurrent work

* agy. `feat/agy-non-mcp-0110` (path allow rules, `agy_path_allow.go`,
  `plantAgyHome` calling `agyAllowRules`) merged as #574 before this analysis was
  finalized, so the harness runs against it. The golden compares agy's private
  `settings.json` by path and mode only, never by bytes, so further rule changes do
  not fail it. It does call `agyPrivateGeminiDir`, `agyAgentsMD` and
  `plantAgyHome` indirectly through `Setup`; a rename of those, or a new file in the
  private gemini dir, changes the golden or breaks the build, so whoever merges second
  regenerates. This plan keeps agy last and leaves `bootdir_agy_home.go`,
  `agy_mcp_allow.go`, `agy_path_allow.go` and `AmendEnv` untouched, so it does not
  conflict with further agy settings work.
* Skills: PR #558/#554 work on `skill_plant.go` and `definition_files.go` is
  compatible; the harness calls `ResolvePlantableSkills`, `definitionFilesFor` and
  `sandboxFiles` and will need a mechanical update if they are renamed.

## 12. Reproduce

```
GOWORK=<go.work that uses this worktree> go test ./internal/runtime/agent/ -run TestBootdirRetireGolden
GOWORK=... go test ./internal/runtime/agent/ -run TestBootdirRetireGolden -update-bootdir-golden   # after reading this doc
```

The test needs no network, no CLI and no credentials; it isolates `HOME`, sets the
Nanite switches it reads, and uses a fixed non-secret session credential string that
is replaced by `<TOKEN>` in the golden.
