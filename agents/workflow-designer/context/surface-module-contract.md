# Surface module contract

The target checkout's `module/` is authoritative. A module imports `github.com/hollis-labs/surface/module` and permitted helpers `module/tetherlink`, `module/collab`, `module/moduletest`, `module/collab/collabtest`. It does not reach into core internals. Register in `init`; `Manifest()` is immutable boot-time data; `Start` receives the scoped Host after migrations; `Stop` joins owned background work.

## Manifest and shared actions

- `ID`: lowercase letters, digits and dashes, matching `module.ValidID`; `Title`, `About`, `Version` describe the module.
- `Views`: globally unique IDs (module ID for main, module.name for others), title/icon, dimensions, Menu/Bare/Multi and `Render: module.Component("module/main")`. That entry must match the web export. `Multi` plus a stable key opens/focuses a record window. Frame rendering is reserved, not a working dynamic-module system.
- `Commands`: IDs module.verb; palette, shortcut, page, CLI and agent can share them. Keep business validation in shared server operations used by commands, routes and tools.
- `Tools`: names module_verb, model-facing descriptions, JSON Schema input, explicit level, optional call description and server-side Run. Read changes nothing; act changes reversible screen state; write changes data/reaches outside and asks with a possibly standing answer; confirm asks each time. Omitted/unknown levels default to write. Profiles cannot loosen levels.
- `Store`: embedded numbered SQL migrations and directory; Host.Store is module-local SQLite. Add new migrations instead of editing shipped ones. Host.DataDir holds module files. An external source of truth may need a local working copy and revision checks; do not silently turn it into independent authoritative state.
- `Settings`: local keys, type/default/options, optionally Owner. Read Host.Config at use time; watch if a side effect must follow a change. Page keys are module.key. Runtime/model/permissions configuration belongs to agent files, not app settings.

## Context and references

Views call `desk.report(win.id, state)` with the complete state (kind, ref, url, selection). Reports replace previous state and are untrusted page reports. `View.Describe` adds server-known Kind/Ref/Extra. Stable refs such as module:record connect context and `Host.Marks()`; use shared marks rather than a new comment store.

Slots declare Name/About, selecting Views/Modes, top-of-mind Tools, Instructions, Where and optional Knowledge. Tools listed by a slot are not an access-control grant. Knowledge is bounded, fast current state, from local/cached data; avoid network calls during context assembly.

`Manifest.Refs` declares reference kinds, anchored ID pattern, optional readiness, batched Resolve, optional full Record. Resolve is read-only; omit missing IDs and return errors for failed lookups. Record may return `module.ErrRefNotFound`. RefEntity can include body, opening command and URL. Register detection in `web/src/cite/core` as well as the Go RefKind. Core batches up to 50 and caches previews. Views use SDK CiteText/CiteRef/RecordBody. This is separate from marks' stable ref string.

## Routes, events and desk ownership

Register `Host.Handle("GET /items", handler)` in Start; it is scoped to `/api/m/<id>/items`. Publish module-prefixed events after successful writes, e.g. `Host.Events().Publish("module.changed", payload)`. Views subscribe with useEvent and reload; no frontend polling. Validate routes as well as tool inputs.

The server owns windows and layout. Page code sends desk actions and renders returned state. Agent tools run in Go so API/MCP/CLI and the page see the same outcome. Report dirty/composing state. Before a delegated data edit, inspect windows' Dirty/Composing state and refuse to overwrite owner work; desk navigation has its own held-change behavior. A failed/held action must not be reported as completed.

## Specialist authoring versus runtime

An embedded profile is declared with `Profile: module.ProfileFS(profileFiles, "profile")`. It has agent.md plus context/, skills/, variants/, tools/ and optional steering.json/evals. No host.yaml, .mcp.json, permissions.yaml, providers.yaml or identity.yaml: profiles share the app runtime, permissions, identity and conversation. Profile instructions/helpers change the role, not authority. Variants narrow tools and cannot select a provider.

At this pack's source revision, selection is a conversation pin, then focused module profile, then app seat. Surface stages the overlay without rewriting the owner's seat; the run log records the profile. Steering is validated but not dynamically applied by profiles, and scripted runner tests do not prove live profile selection. External module-agent binding is being added separately: verify its landed API and folder resolution before configuring it; do not invent a Manifest field.
