# Boot-Profile CLI Harness

**Status:** Active
**Introduced:** SP-20260514-0002 (tickets CW-20260514-0045 → CW-20260514-0050)
**Example catalog:** `examples/boot-profiles/`
**Smoke test:** `internal/service/chat_bootprofile_smoke_test.go`

The boot-profile system lets an operator register a shared catalog of agent
"boot profiles" (identity + named-slot prompt sources + launch contract) and
have them appear in the chat composer's provider/model dropdown alongside the
DB-seeded providers. Selecting a boot-profile-backed row from the dropdown
spins up a headless chat session against the configured CLI adapter
(`claude`, `codex`, `opencode`, ...) with the compiled boot prompt threaded
into the agent's first turn.

This is the **headless chat harness**, not a terminal emulator. The agent
runs in a long-lived PTY-backed subprocess; chat turns are delivered via
`SendInput` and streamed back as `StreamEvent` deltas. There is no `xterm.js`
front-end. See [Boundary: chat harness vs. terminal card](#boundary-chat-harness-vs-terminal-card)
below.

---

## Architecture overview (how the pieces compose)

```
   nanite.yaml: boot_profile_catalog_path
            │
            ▼
   bootprofile.LoadCatalog ── reads YAML from disk ──► bootprofile.Catalog
            │
            ▼
   bootprofile.Compile     ── pure compiler ──────────► bootprofile.LaunchSpec
            │                  (identity vars; text/static slots resolve here;
            │                   cmd/http/role_summary/skill_index defer as
            │                   Requirement entries)
            ▼
   bootprofile.Registry    ── caches one LaunchSpec per profile, atomic Reload
            │
            ├─► api.handleListProviders ── surfaces "bootprofile:<id>" rows
            │                              in the dropdown (additive — DB-seeded
            │                              providers still appear)
            │
            └─► chatServiceImpl.resolveBootProfile
                     │
                     │  on every user turn against a "bootprofile:<id>" session:
                     │
                     ├─► Registry.CompileFor(id, session-scoped vars)
                     ├─► ResolveRequirements   (drains the Requirement list;
                     │                          stubbed types fail here)
                     ├─► stash on activeSessionLaunchSpecs[sessionID]
                     └─► return CLI-routable provider alias ("pty-claude", …)
                              │
                              ▼
                      chat.IsCLIProvider(alias) → driveBootSession
                              │
                              ▼
                      applyLaunchSpecToBootOpts → runtimeagent.Options
                              │                   (Workdir / Env / ExtraArgs /
                              │                    BootPromptOverride)
                              ▼
                      runtimeagent.Boot ── spawns the CLI process
```

The six tickets in SP-20260514-0002 land each layer:

| Ticket | Adds |
|---|---|
| CW-20260514-0045 | `chat.NormalizeCLIProvider` (single source of truth for `pty-*` aliases) and `chat.classifyNilProvider` routes `prov == nil` CLI flows into `driveBootSession`. |
| CW-20260514-0046 | `internal/bootprofile/` pure compiler + loader. Defines the `LaunchSpec` data contract. |
| CW-20260514-0047 | Boot profiles surface in the dropdown as `bootprofile:<profile_id>`. New config key `boot_profile_catalog_path`. `Registry` provides atomic Reload + Lookup. |
| CW-20260514-0048 | Wires the dropdown selection into headless `agent.Boot` via `applyLaunchSpecToBootOpts`. Compiles fresh per Boot for session-scoped vars. |
| CW-20260514-0049 | Crash recovery + session restart. `recoveryPreBootHook` re-resolves via `Registry.CompileFor` before every broker-dispatched relaunch (fresh-catalog policy). `RestartAgentSession` exposes the explicit restart entry. |
| CW-20260514-0050 | Operator docs (this file), example catalog (`examples/boot-profiles/`), and the smoke test. |

---

## Configuration

### Step 1: point Nanite at a catalog

In `~/.config/nanite/config.yaml` (or the project-level `./nanite.yaml`):

```yaml
boot_profile_catalog_path: ~/dev/hollis-labs/apps/nanite/examples/boot-profiles
```

The path is tilde-expanded at load time. **Leaving it empty disables the
feature entirely** — the dropdown shows only DB-seeded providers and the
chat-resolve layer never invokes the registry. This is the documented
"no catalog → no behavior change" branch.

Restart `nanite-api` after changing the path.

### Step 2: catalog layout

```
<boot_profile_catalog_path>/
├── boot-profiles/
│   ├── <profile-id>.yaml      ── one file per profile
│   └── ...
├── launches/
│   ├── <launch-id>.yaml       ── one file per launch
│   └── ...
└── <any other files>          ── used by static-slot `path:` entries,
                                  resolved relative to this root
```

`LoadCatalog` ignores subdirectories under `boot-profiles/` and `launches/`
and any file not ending in `.yaml`. Duplicate IDs (across either subdir)
abort the load with an actionable error so a misconfigured catalog can't
silently shadow a previous entry.

### Step 3: profile schema

```yaml
# boot-profiles/my-agent.yaml
id: my-agent                        # required; unique across the catalog
display_name: "My Agent"            # optional; falls back to id in the UI
launch: my-agent                    # references launches/<id>.yaml

identity:                           # required block
  lineage_alias: my-agent           # required
  role: backend                     # optional
  project: my-project               # optional
  work_root: /path/to/work          # optional
  tracking_root: /path/to/tracking  # optional
  vanta_primary: "true"             # optional

vars:                               # optional profile-inline vars
  agent_label: "Backend Agent"

slots:                              # named-slot map (see below)
  agent:
    type: text
    content: "You are {{agent_label}} on {{project}}."
  rules:
    type: static
    path: rules.md
```

### Step 4: launch schema

```yaml
# launches/my-agent.yaml
id: my-agent                        # required; unique across launches
provider: pty-claude                # required; CLI adapter alias
workdir: /path/to/project           # optional
ui_label: "My Agent (Claude PTY)"   # optional; falls back to profile display_name → id
env:                                # optional; overlays runtime env at Boot
  MY_KEY: my_value
args:                               # optional; appended to adapter argv
  - --add-dir
  - /path/to/project
boot_mode: ""                       # optional; "" / "file" / "stdin" / "inline"
```

### Slot source types

| Type | Resolves at | Status | Notes |
|---|---|---|---|
| `text` | Compile time | Works today | `content:` inline body with `{{var}}` substitution. |
| `static` | Compile time | Works today | `path:` is a file or directory relative to the catalog root. Directories scan via `glob:` (default `*.md`) up to `limit:` files. |
| `cmd` | Boot time | **Stubbed** | Surfaces as `bootprofile.ErrRequirementUnsupported`. Booting a session whose profile uses `cmd` will fail at the resolve step with a pointed error naming the slot. |
| `http` | Boot time | **Stubbed** | Same disposition as `cmd`. |
| `role_summary` | Boot time | **Stubbed** | Same disposition. |
| `skill_index` | Boot time | **Stubbed** | Same disposition. |

If you need cmd/http resolution today, you can pre-render the value into the
catalog as a `static` file and regenerate it externally (e.g. a cron writes
`<catalog-root>/git-recap.md` and the profile references it as a `static`
slot). The four deferred resolvers are scheduled for a follow-up ticket.

### Variable substitution

`{{var}}` literals in `text` slot bodies are resolved against a merged map
built per Compile call:

```
caller-supplied (session-scoped) > profile.vars > identity-derived
```

The compiler always pre-populates the map with the profile's `identity`
fields under both flat keys (`{{work_root}}`) and dotted keys
(`{{identity.work_root}}`). Catalog authors can use either style.

**Session-scoped vars** (caller-supplied during a real session) are:

| Key | Source |
|---|---|
| `{{session_id}}` | The chat session UUID. |
| `{{session_provider}}` | The session row's provider (the encoded `bootprofile:<id>`). |
| `{{session_model}}` | The session row's model id. |
| `{{agent_slug}}` | The session's primary agent profile slug. |
| `{{agent_name}}` | The session's primary agent profile display name. |
| `{{agent_provider}}` | The session's primary agent profile default provider. |

Unknown variables are a hard error: the compile aborts with the missing
variable names sorted into the error message. The motivation is to keep
half-rendered `{{typo}}` literals out of the LLM context.

**Important caveat for dropdown visibility:** `Registry.Reload` compiles
every profile with **empty caller vars** to populate the dropdown cache.
A profile whose `text` slot references `{{session_id}}` will fail at
Reload and be skipped from the dropdown — but the same profile will
compile fine via `Registry.CompileFor` once a session boots against it.
The pragmatic guidance is: use only identity / profile-inline vars in
slot bodies if you want the profile to appear in the dropdown. If you
need session-scoped vars, the chat-resolve layer's `CompileFor` call
will still pick the profile up if you select it some other way (e.g.
programmatically constructed session row), but the operator-facing
dropdown surface won't show it.

---

## End-to-end happy path

1. **Configure** `boot_profile_catalog_path` (Step 1 above) and restart
   `nanite-api`.

2. **Reload** the chat UI. The provider/model dropdown enumerates the
   DB-seeded providers plus one row per successfully-compiled boot profile,
   labeled `<ui_label> (boot profile)`.

3. **Select** a boot-profile row (provider + model both encode the same
   `bootprofile:<id>` string).

4. **Send a chat turn.** The chat-resolve layer:
   - Decodes the encoded id.
   - Calls `Registry.CompileFor(id, session-scoped vars)` — compiles
     against the **current** catalog state with the session's caller vars
     merged in.
   - Drains the (now empty) `Requirements` list via
     `ResolveRequirements`.
   - Stashes the spec on `activeSessionLaunchSpecs[sessionID]`.
   - Returns the CLI-routable provider alias (e.g. `pty-claude`) so
     `chat.IsCLIProvider` matches and routing reaches `driveBootSession`.

5. **`driveBootSession` boots the runtime:**
   - On first turn, builds `runtimeagent.Options` and overlays the spec
     via `applyLaunchSpecToBootOpts` (Workdir / Env / ExtraArgs /
     BootPromptOverride land on the options).
   - Calls `runtimeagent.Boot` — the layout writes `CLAUDE.md` /
     `agent-context.md` into the boot dir, the adapter spawns the CLI
     subprocess, and a session-lifetime `Wait` observer is registered
     for crash recovery.

6. **Stream the response.** The runtime fans out `StreamEvent` deltas
   through the agent-event bridge; the chat harness wires them to the
   session's SSE stream.

7. **Stop** with the existing stop affordance. `CloseAgentSession`
   tears down the runtime process and clears the per-session entries
   in `activeSessions`, `activeSessionSlots`, and
   `activeSessionLaunchSpecs`.

---

## Session management

| Action | Method | Behavior |
|---|---|---|
| First boot | `driveBootSession` first turn | Reads stashed spec, applies onto `Options`, calls `Boot`. |
| Subsequent turns | `driveBootSession` subsequent turns | Reuses the live session, delivers per-turn payload via `SendInput`. |
| Slot regen | `driveBootSession` when slot hash changes | Rewrites `CLAUDE.md` + `agent-context.md` in the boot dir; sends a re-read instruction. |
| Switch session | UI swaps to another session row | Each session has its own runtime + stashed spec; no cross-talk. |
| Stop | `CloseAgentSession` | Stops the runtime; clears stash; chat row stays alive. |
| Restart | `RestartAgentSession` | Calls `CloseAgentSession`; next user turn re-resolves against the **current** catalog. Internal-only today — no HTTP route. |

---

## Crash recovery

The recovery broker dispatches replacement sessions when a runtime process
exits unexpectedly. For boot-profile-backed sessions, the
`recoveryPreBootHook` runs **before every relaunch**:

1. Looks up the chat session row via the store.
2. If `session.Provider` is not a `bootprofile:<id>` id → pass through
   unchanged (legacy CLI/API recovery flow stays untouched).
3. Otherwise, decodes the profile id and calls
   `Registry.CompileFor(profileID, fresh recovery vars)`. The
   registry's **cached catalog** is consulted, not a serialized
   snapshot from the original boot.
4. Drains requirements; overlays the fresh spec onto the broker-
   assembled `Options`; re-stashes on `activeSessionLaunchSpecs`.

### Fresh-catalog policy

The hook deliberately runs against the **current** catalog state, not
the snapshot the dead session booted with. If the operator edits
`<catalog-root>/boot-profiles/<id>.yaml` between the original boot and
a recovery relaunch, the replacement runs against the edited definition.

Rationale: the recovery use case is "process died, get me back to the
configured state", not "preserve the dead process's exact env". A
future "preserve dead session env exactly" variant would need a new
code path; the seam is documented in `chat_bootprofile_recovery.go`.

### Profile-deleted is session-fatal

If the operator deletes a profile YAML while a session is in flight,
`CompileFor` returns `ErrProfileNotFound`. The recovery hook surfaces
this as:

```
recovery resume failed: profile "my-agent" not in current catalog (was deleted?)
```

The broker escalates to `Permanent` and the session enters the
escalated state. **Recovery is the operator's signal to either
re-create the profile or archive the session** — the system cannot
heal a deleted profile on its own.

### Resume IDs not yet flowing

The hook does NOT set `Mode=ModeResume` or `ResumeFromCheckpoint`.
The broker preserves the session id across relaunches, so chat history,
lineage, and path grants survive structurally — but there's no
provider-level resume protocol behind any current CLI adapter. The
seam (`opts.Mode = runtimeagent.ModeResume`, `opts.ResumeFromCheckpoint =
<id>`) is marked in the code for a future ticket that wires e.g.
claude-code's session_id resume protocol.

The structural guarantee from CW-20260514-0048 remains: **normal
launches never carry resume fields.** The smoke test
`TestBootProfileSmoke_ResolveAndApplyPipeline` pins this on the normal
path; the recovery test suite pins it on the recovery path.

---

## Known limitations

| # | Limitation | Workaround / status |
|---|---|---|
| 1 | `cmd` / `http` / `role_summary` / `skill_index` slot sources are stubbed with `bootprofile.ErrRequirementUnsupported`. | Use `static` slots and regenerate the file externally; or restrict to `text` + `static`. Real resolvers are a follow-up. |
| 2 | Profiles whose slot bodies reference caller-only vars (e.g. `{{session_id}}`) fail `Registry.Reload` and are skipped from the dropdown. | Stick to identity / profile-inline vars in slot bodies if you want dropdown visibility. |
| 3 | `RestartAgentSession` is internal-only — there is no `POST /api/sessions/{id}/restart` HTTP route today. | A follow-up ticket will surface the route. |
| 4 | Resume IDs do not flow into `runtimeagent.Options`. Recovery means "boot fresh with the same chat history", not "resume the provider-level conversation". | Adapter-side resume protocol wiring is a follow-up ticket. |
| 5 | `session.Provider` is the load-bearing recovery routing pin — modifying it on a live boot-profile session will route the next recovery as a non-boot-profile session. | Don't rewrite `session.Provider` outside the documented restart path. |
| 6 | `bootprofile.Compile` does not execute the optional `Profile.Template` override; it only surfaces the path on `LaunchSpec.TemplatePath`. | Authors that need a custom template render must implement the renderer in the chat layer before consuming the spec. The default renderer (`renderDefaultPrompt`) produces a stable 7-section layout. |
| 7 | The compiler is filesystem-bound: relative `static` paths resolve from the catalog root. There is no remote-fetch mode. | Use absolute paths or pre-stage content into the catalog directory. |
| 8 | `Registry.Reload` is best-effort: per-profile compile errors are collected into the returned error, but successfully-compiled profiles still land in the cache. | An operator should treat reload errors as actionable rather than fatal. |

---

## Boundary: chat harness vs. terminal card

This system is the **headless chat harness**. Distinguishing it from the
forthcoming **Terminal Card / xterm path** (CW-20260514-0043) matters
because the two solve different problems and conflating them produces
the wrong design pressure.

| Aspect | Boot-profile CLI harness (this doc) | Terminal Card / xterm (CW-20260514-0043) |
|---|---|---|
| User-facing surface | Chat composer in the regular chat UI. | An interactive terminal card embedded in chat — full keyboard + screen. |
| Input model | One user message → one `SendInput` → streamed deltas. | Raw stdin / stdout; user types into the terminal, sees ANSI output. |
| Boot prompt | Compiled from the boot-profile catalog. | TBD — the terminal card is for seeing the live CLI TUI, not for shaping a boot prompt. |
| Streaming envelope | `StreamEvent` deltas; structured envelope cards. | Raw bytes through an xterm.js front-end. |
| Recovery | `recoveryPreBootHook` re-resolves the catalog. | Out of scope here. |
| Use case | "I want to chat with the Claude CLI through Nanite." | "I want to see the live Claude CLI TUI." |

**If you find yourself wanting to expose terminal control codes, raw
keystrokes, or the live TUI through this harness, stop.** That belongs
on the Terminal Card path. The boot-profile harness deliberately stays
on the chat-message abstraction so it can compose cleanly with the
rest of Nanite (envelopes, MCP tools, slot system, recovery broker).

---

## Smoke testing

### Automated (preferred)

```bash
go test ./internal/service/ -run TestBootProfileSmoke_
```

The smoke loads `examples/boot-profiles/` from disk, builds a
`Registry`, drives `resolveBootProfile` against the encoded id, and
asserts the resulting `runtimeagent.Options` would carry the spec's
env / args / workdir / boot-prompt to `agent.Boot`. It does **not**
launch a real CLI process — the test stops at the
`runtimeagent.Options` shape.

Three sub-tests pin the load-bearing seams:

| Test | What it pins |
|---|---|
| `TestBootProfileSmoke_CatalogLoadsAndCompiles` | YAML schema is intact; the example compiles with empty vars (dropdown-visible). |
| `TestBootProfileSmoke_ResolveAndApplyPipeline` | Encoded id → `resolveBootProfile` → `CompileFor` → requirements drain → `applyLaunchSpecToBootOpts` → `runtimeagent.Options`. Includes the no-resume-on-normal-launch pin. |
| `TestBootProfileSmoke_RegistryListSurface` | `Registry.List()` populates the dropdown surface with the example. |

### Manual against a live daemon

For when you have a real `claude` binary on `$PATH` and want to verify
the actual spawn:

1. Set `boot_profile_catalog_path` to `examples/boot-profiles/` (with
   the path tilde-expanded).
2. `cerberus_rebuild nanite-api --reason "smoke boot-profile catalog"`.
3. Open the chat UI; confirm `Claude CLI (smoke) (boot profile)` is in
   the provider dropdown.
4. Create a new chat session against that row.
5. Send a chat turn (e.g. "Print 'hello from smoke'").
6. Confirm a streamed response arrives.
7. Stop the session via the UI.
8. Verify `cerberus_logs nanite-api` shows `recovery: boot-profile
   relaunch resolved against current catalog` is **absent** for a
   clean stop (the recovery hook only fires on unexpected exits).

For the recovery half:

1. Send a chat turn against the smoke session.
2. While the turn is in flight, `kill -9` the `claude` subprocess
   `nanite-api` spawned.
3. Confirm the recovery broker dispatches a replacement and the next
   turn succeeds.
4. Confirm `cerberus_logs nanite-api` shows `recovery: boot-profile
   relaunch resolved against current catalog`.

To exercise the **profile-deleted is session-fatal** path:

1. With a live smoke session, move
   `examples/boot-profiles/boot-profiles/claude-smoke.yaml` aside.
2. Hit the registry reload (today: restart `nanite-api`).
3. Send another chat turn → resolve fails with `boot profile not
   found: "claude-smoke"`. The session row stays alive; restoring
   the YAML and reloading recovers the dropdown entry.

---

## Cross-references

- `CW-20260514-0043` — Terminal Card / xterm path (the real-TUI variant
  this harness is deliberately not).
- `internal/bootprofile/INVARIANTS-or-equivalent` — not present today;
  developer docs live in package-level `doc.go` and the per-file
  comments. Operator changes that need to alter the data contract
  should update both the schema in `profile.go` and this doc.
- `docs/mcp-trust-model.md` — boot-profile-backed sessions still
  inherit the MCP trust model; the boot prompt's MCP server allowlist
  is passed through via `MCPServers` on the spec.
- `docs/tool-naming-convention.md` — tools surfaced inside the CLI
  agent follow the standard `<concept>_<verb>` convention. Boot
  profiles do not register tools; that is the plugin system's job.
