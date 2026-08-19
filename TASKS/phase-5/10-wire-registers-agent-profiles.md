# Wire `registers.agent_profiles[]` against the new role/scope/agent construction model

**Phase:** 5
**Status:** not-started
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
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
