# Plugin System

The primary mechanism for adding custom use-case logic, app-specific integrations, custom agents, and providers without a core code change. Genuinely well-architected — a single YAML-manifest-driven registration path (`applyManifestRegistrations`) treats compiled-in (**builtin**) and separate-process (**subprocess**) plugins identically, with a real signed install/verify/stage/commit state machine and a proper unload sweep across every registry a plugin can touch.

## What's real and already deep

**Hooks/filters reach further into the core than a first read of the plugin registration table suggests.** A real six-point filter chain spans the whole turn loop: `FilterUserMessage` → `FilterSystemPrompt` → `FilterContextWindow` → `FilterToolResult` → `FilterAssistantResponse` → `FilterEnvelopeData`. Reflexes have their own `FilterReflexState`/`FilterReflexAction` and `EmitReflexFired`/`EmitReflexActionStaged`. Plus a real spread of lifecycle events (session archived, agent switched, artifact created, bookmark changed, config changed). This is close to comprehensive already — not a gap needing a redesign.

**One real gap found**: a filter exists for tool *results* (post-execution) but not tool *selection* — a plugin can't currently add, remove, or reshape which tools get offered to the model for a turn. Add `FilterToolSelection` alongside the tool-selection filter stack (see [Steering](03-steering.md)).

**Hot-reload is real** (`POST /api/plugins/reload`, dev-mode `window.__nanite_reloadPlugin`) — genuinely avoids a restart. One asymmetry to close: CLI-based plugin install still requires a manual restart while the API-driven path and reload endpoint both hot-load live.

## Middleware — exists, but only at the HTTP layer, and not plugin-extensible

`internal/server/server.go` has a real, standard `net/http` middleware chain (recover → logging → CORS → auth → caller-identity → body-limit). **Decision: don't build a second, general "middleware" concept** — the turn-loop's filter/hook system already functionally serves that role for the chat-turn domain, and a second name for the same pattern would recreate exactly the naming-collision problem this whole review exists to fix. Instead: **make the existing HTTP middleware chain plugin-extensible** — today a plugin can register new routes but can't inject into the chain wrapping every request. Complementary to the filter/hook system, not a replacement.

## What's being wired up

**`registers.agent_profiles[]`** — accepted by the manifest schema, never acted on. Direct connection to [Agent Construction](01-agent-construction.md): plugin-provided agents were decided to stay as a real source, and this is the seam that decision needs. Loom's agents would move onto this path immediately once it lands.

**Builtin plugin enable/disable** needs a real installed/enabled state model — not the current file-rename mechanism, which is subprocess-only and doesn't even apply to the 12 currently-loaded builtins. Modeled on WordPress's plugin state (code present = installed, a separate flag = active), working uniformly across builtin and subprocess plugins via GUI/CLI/API, backed by the DB rather than a file's presence/absence.

**`registers.panels[]`** (right-rail tab entries — currently registers but the render function is a placeholder) and **`registers.crud[]`** (generic CRUD resource handlers — also unwired) are both real and worth developing, neither urgent. Build when there's a first real consumer or genuine downtime. **`registers.panels[]`'s rendering half is frontend work — deferred to the separate frontend pass, not any backend phase.** Its registration/manifest/backend half is fair game for a backend phase; the render function itself is not.

## Cut

`trigger_rules` (an early version of what became reflexes) and `custom_actions` (meant to be slash-command-triggered UI actions) — both zero-caller, zero-row. Whatever real need either was reaching for is served by reflexes and the existing, real plugin `commands[]` registration going forward.

The support-ticket reference plugin — extensively documented but no source or manifest exists anywhere in this workspace. Confirmed a demo; doc references get cleaned up alongside the Cards cuts.
