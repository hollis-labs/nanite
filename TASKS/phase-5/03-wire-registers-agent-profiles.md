# Wire `registers.agent_profiles[]` against the new role/scope/agent construction model

**Phase:** 5
**Status:** implemented
**Depends on:** Phase 1 in full (the `roles`/`agents` composition model this must register against)
**Touches:** `internal/plugin/registrations.go:354-357` (the deferred-skip stub), `internal/plugin/config.go:137,276-279` (`AgentProfileRegistration{ID, File}` — likely needs a richer shape, see What to do)

## Context

Architecture doc `09-plugin-system.md`: *"`registers.agent_profiles[]` — accepted by the manifest schema, never acted on. Direct connection to Agent Construction: plugin-provided agents were decided to stay as a real source, and this is the seam that decision needs. Loom's agents would move onto this path immediately once it lands."* Architecture doc `01-agent-construction.md`: *"Plugin-provided agents stay as a real source... must conform to this schema, no legacy grandfathering."*

### Exact current gap, verified

`AgentProfileRegistration{ID, File}` (`internal/plugin/config.go:276-279`) — a minimal shape: an ID and a path to a YAML file in the plugin's own on-disk format (test fixture: `agents/giphy.yaml`, an old flat agent-profile shape). Install-time validation already checks `registers.agent_profiles[].file` exists (`internal/plugin/install/validate.go:350-351,422-423`). The runtime gap is exact and self-documented: `internal/plugin/registrations.go:354-357`:

```go
if len(reg.AgentProfiles) > 0 {
    host.logger.Info("manifest agent_profiles: yaml-driven registration deferred (follow-up B.4 task)", "plugin", pluginID, "count", len(reg.AgentProfiles))
    skipped += len(reg.AgentProfiles)
}
```

Accepted by schema, validated at install, logged and silently skipped at runtime. This is literally tagged `B.4` in the code as a deferred follow-up — this task is that follow-up.

**Real Phase 1 dependency, structural not just sequencing**: `File` points at a YAML in the plugin's own old flat-shape format. The new `roles`/`agents` composition schema (role/scope/agent split, cascading override) doesn't exist until Phase 1 lands — there is nothing correct to register a plugin agent profile *into* until then. This is why the dependency is on Phase 1 in full, not just a specific table.

### Real, immediate beneficiary — confirmed live

`internal/api/loom_curator_wake.go` confirms Curator is a real, live agent with a real wake path today. Once this task lands, Curator/Weaver would move onto proper plugin-registered agent definitions conforming to the new construction model, per architecture doc's stated intent — this is not a speculative use case.

## What to do

1. Design the new `AgentProfileRegistration` shape (or a new manifest field, if the old flat-YAML shape is too far from the new composition model to extend cleanly) — it must express enough to construct a real `roles`/`agents` composition: persona/system_prompt (→ `roles`), scope/tool/skill grants (→ `agents` composition + `agent_tools`/`agent_skills`), `consumer_id` (a plugin-registered agent is a natural `consumer_id` candidate — the plugin's own identity, or an explicit consumer tag in the manifest).
2. Implement the registration path in `applyManifestRegistrations`/`registrations.go`, replacing the current deferred-skip stub: parse the plugin's agent-profile file(s), construct/upsert the corresponding `roles`/`agents` rows, per the "no legacy grandfathering" instruction — a plugin manifest with an old flat shape should either be rejected with a clear error or require updating to the new shape, not silently coerced.
3. Migrate Loom's giphy-style test fixture (and any other real plugin currently declaring `agent_profiles[]`) to the new shape as part of this task's verification, not left on the old format.
4. Confirm uninstall/unload correctly tears down plugin-registered `roles`/`agents` rows (the "proper unload sweep across every registry a plugin can touch" architecture doc `09` already credits the plugin system with elsewhere — this task must extend that sweep to cover the new registration).

## Done means

- `registers.agent_profiles[]` is a real, working registration path — a plugin declaring an agent profile in the new shape gets a real, functioning `roles`/`agents` composition on load.
- Plugin uninstall/unload correctly removes what it registered.
- At least one real plugin (Loom's, if feasible within this task's scope, or a test plugin otherwise) is verified end to end: install → agent appears and is dispatchable → uninstall → agent is gone.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log

### The new `AgentProfileRegistration` shape -- design

`AgentProfileRegistration{ID, File}` in the manifest itself is **unchanged** in shape. The manifest-level entry only ever needed to be a pointer at a file; what changed is what that file's *content* is now required to be. Defined `internal/plugin/agent_profiles.go`'s `PluginAgentProfileDocument`:

```yaml
role:                       # -> a `roles` row
  slug: <required>
  name: <required>
  system_prompt: <required> # the role -> agent -> task cascade's broadest layer
  class: advisor|process|template|harness   # optional

agent:                      # -> the `agent_profiles` composition row (still
                             #    agent_profiles under the hood, decision log
                             #    Section 6 -- not renamed to `agents`)
  slug: <required>
  name: <required>
  description / class / activation_mode / runtime_kind / default_model /
  default_provider / system_prompt_override: all optional
  tools: [known_tools.name, ...]   # -> agent_tools grants (granted_via="plugin")
  skills: [skills.slug, ...]       # -> agent_skills
  consumer_slug: <optional>        # -> consumers row; defaults to the plugin's own id
```

No legacy grandfathering: `ParsePluginAgentProfileFile` strict-decodes with `yaml.Decoder.KnownFields(true)` and `Validate()` requires the new shape's mandatory fields. A file in the old flat shape (no `role:`/`agent:` split -- e.g. the now-cut giphy reference plugin's implied shape) is rejected with a specific, clear error identifying exactly what's missing, never silently coerced. Covered by `TestPhase5AgentProfiles_NoLegacyGrandfathering`.

Scope (`agent_projects`) is deliberately **not** part of this v1 shape: `internal/store/projects.go`'s `Project` has no slug or other stable natural key a plugin manifest could reference declaratively, only an opaque DB-minted ID a plugin author can't know ahead of time. A plugin-registered agent is therefore global/unscoped by default (same as Curator today); real per-project scoping can be added by an operator afterward via the existing `agent_projects` API. Documented in `agent_profiles.go`'s package doc comment as a deliberate limitation, not an oversight.

### What was done

- **Migration 122** (`internal/store/migrations/122_agent_profiles_roles_plugin_owner.sql`): adds nullable `agent_profiles.plugin_id` / `roles.plugin_id` (+ indexes), mirroring the existing `artifacts.source_plugin_id` precedent (no FK, same rationale). This is the DB-authoritative ownership tag the unload sweep reads.
- **Store** (`internal/store/agents.go`, `roles.go`): added `AgentProfile.PluginID` / `Role.PluginID` fields (threaded through `agentColumns`/`scanAgent`/`CreateAgent`/`UpdateAgent` and `roleColumns`/`scanRole`/`CreateRole`/`UpdateRole`), plus `ListAgentsByPluginID`, `ListRolesByPluginID`, `CountAgentsByRoleID`.
- **`internal/plugin/agent_profiles.go`** (new): `PluginAgentProfileDocument`/`Role`/`Agent` types, `ParsePluginAgentProfileFile`, `Validate`, `registerManifestAgentProfiles` (the registration entrypoint, replacing the deferred-skip stub), `applyPluginAgentProfile` (constructs/upserts role + agent + tool/skill grants), `resolveOrCreatePluginRole`, `resolveOrCreateConsumer`, and `Host.SweepPluginAgentProfiles` (the exported teardown method -- see below).
- **`internal/plugin/registrations.go`**: replaced the `B.4 deferred` log-and-skip stub with a call to `registerManifestAgentProfiles`.
- **`internal/plugin/host.go`**: `UnloadPlugin`'s step 18 now calls `h.SweepPluginAgentProfiles(id)`. Also fixed `NewHostWithStore`'s parameter type from `interface{}` to `*store.Store` and made it populate `h.store` (previously only `h.services["store"]`) -- see Deviations.
- **`cmd/nanite/plugin_cmd.go`**: `pluginUninstall`/`pluginDisable` now resolve the plugin's manifest and call `host.SweepPluginAgentProfiles(manifest.Identifier())` against a `buildMinimalHost()`-backed host, before `triggerRestart()` -- see Deviations for why this was necessary.
- **`internal/api/plugins.go`**: `runPluginUninstallCleanup` now also calls `SweepPluginAgentProfiles` (belt-and-suspenders alongside `unloadPluginFromHost`'s live-host `UnloadPlugin` sweep -- covers the branch where a plugin is uninstalled while already disabled, where `unloadPluginFromHost` is never called).
- **`docs/plugin-yaml-reference.md`**: expanded the `registers.agent_profiles[]` section with the new `role:`/`agent:` shape (a plugin author reference this task's Done-means requires be real, not just code-level).
- **Tests**: `internal/plugin/agent_profiles_test.go` -- `TestPhase5AgentProfiles_EndToEnd` (the Done-means verification) and `TestPhase5AgentProfiles_NoLegacyGrandfathering`.

### Deviations from plan, and why

1. **No real, currently-installed plugin declares `registers.agent_profiles[]`.** The task's Context cites the giphy reference plugin's `agents/giphy.yaml` as the old-shape example -- but giphy was cut in full by `TASKS/phase-0/15a-cut-giphy.md` before this task started. No plugin.yaml anywhere in this workspace (confirmed by grep across `internal/plugin/builtin/*/plugin.yaml`) declares `agent_profiles[]` today; the only surviving references are manifest-string unit-test fixtures (`config_manifest_v1_test.go`, `schemas_test.go`) that never had real file *content* to migrate. "What to do" item 3 ("migrate Loom's giphy-style test fixture... to the new shape") therefore has nothing real to migrate -- there is no committed old-shape file anywhere in this repo. Verification instead used the Done-means' explicit fallback: "a test plugin otherwise," run through the exact same `Host.LoadPlugin` -> `applyManifestRegistrations` -> `Host.UnloadPlugin` pipeline `loader.go`'s `LoadDiscovered` uses for a genuine on-disk plugin (not a shortcut).
2. **Real gap found and fixed: the CLI uninstall/disable path never went through a live `Host.UnloadPlugin` call at all.** `cmd/nanite/plugin_cmd.go`'s `pluginUninstall`/`pluginDisable` apply their effect via a subsequent `cerberus restart`, not an in-process unload -- the next boot simply never (re)loads a removed/disabled plugin, so a sweep implemented only inside `UnloadPlugin` would never fire for that path, leaking every plugin-owned `agent_profiles`/`roles` row forever. Fixed by extracting the sweep into an exported, DB-only `Host.SweepPluginAgentProfiles(pluginID)` method (not tied to a live, loaded plugin) and calling it explicitly from both CLI commands via `buildMinimalHost()`. This in turn surfaced a second, smaller bug: `NewHostWithStore(store interface{})` only ever populated `h.services["store"]`, never the typed `h.store` field my sweep (and `applyManifestRegistrations`'s enabled-gate) actually reads -- so a CLI-built host's store was invisible to both. Fixed by widening the parameter to `*store.Store` and assigning both. Verified safe: every real call site (`internal/api/plugins.go`, `cmd/nanite/plugin_cmd.go`, two test call sites) already passed either `nil` or a `*store.Store`, confirmed by grep.
3. **Real bug found and fixed in my own draft during testing: reload silently blanked several `agent_profiles` columns.** `UpdateAgent` (unlike `CreateAgent`) does not backfill empty-string fields to their column defaults (`status`, `tags`, `modes`, etc. are written verbatim). My first draft constructed a fresh, mostly-zero `store.AgentProfile{}` on every registration call; on the *second* load (the reload/upsert path), that blanked `status` (and would have blanked several JSON-array columns) on the existing row. Caught by extending the end-to-end test to assert `Status == "active"` after a second `applyManifestRegistrations` call, not just after the first. Fixed by starting from the existing row (`agentRow = *existing`) when one exists and overwriting only the fields this registration path is authoritative for, rather than constructing from scratch -- generalizing `UpsertAgentBySlug`'s own existing, narrower precedent (it already selectively preserves `ID`/`AgentHash`/`Version`/`URN`).
4. **"Dispatchable" is verified at the composition layer, not via a live LLM turn.** The test confirms the constructed `agent_profiles` row resolves to a role with a real `system_prompt` (with `agent.SystemPrompt` deliberately left empty so `internal/service.ResolveAgentCascade`/`applyScalarCascade` -- confirmed, by direct read, to be wired into the live `resolveForSession` path -- supplies it at boot), and carries real `agent_tools`/`agent_skills` grants of the exact shape `internal/service/tool.go`'s `filterToolsByAgentTools` reads at dispatch time (confirmed live via `TASKS/phase-4/05-wire-select-for-agent-to-read-agent-tools.md`, already `implemented` on main). Exercising an actual provider/LLM turn is out of this backend-only task's scope (no live credentials in this harness) and isn't necessary to prove the registration path is real and correct.
5. **Consumer rows are deliberately not swept on unload.** Per architecture doc's "Ownership and instancing" section, a consumer is a lightweight, durable ownership tag (the existing Loom seed row is never torn down either) -- reused across reinstalls/reloads. `resolveOrCreateConsumer`'s doc comment and the end-to-end test both make this explicit (asserts the consumer survives `UnloadPlugin`).
6. **`docs/engineering/architecture/09-plugin-system.md`'s "What's being wired up" section now describes this feature inaccurately** ("accepted by the manifest schema, never acted on" is no longer true). Left unedited: this task's prerequisite, Phase 5 item 02, landed on main without updating that same doc's adjacent "Builtin plugin enable/disable needs a real installed/enabled state model" bullet either, despite also being fully implemented -- confirming these architecture docs are treated as a snapshot from the original design review, not a continuously-maintained changelog. Noted here for the record rather than silently diverging from that established precedent.

### Verification

- `go build ./cmd/nanite/`, `go build ./...`: pass.
- `go vet ./...`: passes except 4 pre-existing warnings in `internal/service/container.go` (confirmed via `git diff --stat` that this task never touches that file -- pre-existing baseline noise, not introduced here).
- `go test ./...`: full suite green, no failures.
- **Migration 122 tested against a real production backup** (`~/.local/share/nanite/workspaces/default/backups/main.db.pre-execution-backup-20260818-132726`), copied to an isolated scratch location first (per process doc's safety note -- this migration doesn't touch `AgentConfigService`/`writeManaged`, but the isolation was applied anyway). Migrated cleanly: 28 pre-existing `agent_profiles` rows and 0 pre-existing `roles` rows, all correctly `plugin_id IS NULL` post-migration; a live `CreateRole`/`UpdateRole`/`ListRolesByPluginID`/`DeleteRole` round-trip against that real data succeeded. `git status --short` was run immediately after and showed only this task's intended files -- no accidental write to any tracked file. The temporary verification test file was deleted afterward and is not part of this change.
- **End-to-end (Done-means):** `TestPhase5AgentProfiles_EndToEnd` (`internal/plugin/agent_profiles_test.go`) -- install (a real plugin.yaml + on-disk agent-profile file, loaded through `Host.LoadPlugin` + `applyManifestRegistrations`, the exact pipeline `LoadDiscovered` uses) -> agent appears (`GetAgentBySlug` resolves; `RoleID`/`ConsumerID` set; role's `system_prompt` non-empty; `agent_tools`/`agent_skills` grants present) and is dispatchable (composition-layer verification, see Deviation 4) -> reload is upsert, not duplicate-create, and does not corrupt `status` (Deviation 3's regression guard) -> uninstall (`Host.UnloadPlugin`) -> agent and role gone, consumer intentionally survives. `TestPhase5AgentProfiles_NoLegacyGrandfathering` confirms the old flat shape is rejected with zero partial rows created. Both pass.
- Nothing was escalated; no genuine ambiguity or doc/code contradiction was hit that met the stop conditions.

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
