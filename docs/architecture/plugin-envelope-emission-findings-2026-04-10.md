# Plugin Envelope Emission — Gap Findings (2026-04-10)

> **Status:** discovery only. Implementation work is deferred to a future plugin-focused agent session.
>
> **Scope:** what was found while investigating whether the `plugins/fragments-engine` and `plugins/support-ticket` in-tree plugins could be removed from the nanite repo. Investigation expanded beyond the original scope when it became clear the plugin envelope-emission story is broken system-wide, not just for those two plugins.
>
> **Purpose of this document:** give a future plugin-architecture agent enough file:line pointers, raw findings, and verification evidence to pick up the work cold without re-running the same investigation. No recommendations or fix proposals are included — those are explicitly out of scope for this document.

---

## 1. Executive summary of the facts

1. **Only one mechanism exists today for emitting an envelope that survives into the persisted message stream and reaches the frontend:** a tool handler returns output text containing an `<!--ENVELOPE_DATA:{json}:ENVELOPE_DATA-->` marker, which is extracted by `captureEnvelopeData` in `internal/service/chat_generate.go`.
2. **Envelope type registration via `chat.RegisterEnvelopeType` is advisory, not blocking.** An envelope whose `type` is not in the `registeredTypes` map is logged as an `unregistered_type` error, but is still appended to the parsed envelopes slice, still persisted to the `messages.envelope` column, and still streamed to the frontend.
3. **The `event.Data["envelope"]` path used by the `giphy` and `oembed` builtin plugins is dead code.** There is zero code anywhere in the repository that reads `event.Data["envelope"]`. Envelopes written there by plugin event hooks are silently dropped from the message flow.
4. **Nothing in the backend reads the `registers.envelopes:` section of `plugin.yaml`.** That section is consumed only by the frontend TypeScript codegen script (`scripts/generate-plugin-imports.mjs`). Plugins that declare envelope types via `plugin.yaml` without also calling `chat.RegisterEnvelopeType` from Go code are not registered with the backend at all.
5. **Several envelope type names that appear in `plugin.yaml` files (e.g. `kb-result`, `ticket-confirmation`, `task-complete-notification`, `task-disposition`, `giphy-modal`) are actually emitted by core nanite code**, not by the plugins that claim to own them. The only plugin-declared type names that are also registered in Go code via `chat.RegisterEnvelopeType` are `sprint-planning-review`, `task-disposition`, and `task-complete-notification` — and that registration happens in `plugins/fragments-engine/plugin.go:50-52`, which imports `internal/chat` directly.
6. **Envelope JSON schemas under `internal/envelope/schemas/*.json` are used only by tests (`internal/envelope/contracts_test.go`).** No runtime code loads, compiles, or validates envelopes against these schemas. The only runtime validation is the `ValidateEnvelope` function, which checks type-name membership in `registeredTypes` and three structural fields (`kind`, `version`, `type`).

## 2. Runtime evidence

### 2.1 Live database verification

Session: `bf0a2fbe-f7b6-4860-9b43-c53bed226991` ("Finding Birthday Balloons Gif"). This was a user test of the `!giphy` command with no `GIPHY_API_KEY` set, which exercises the demo-mode code path.

Messages table query:

```sql
SELECT envelope FROM messages
WHERE session_id = 'bf0a2fbe-f7b6-4860-9b43-c53bed226991'
  AND role = 'assistant';
```

Result:

```json
[{
  "kind": "envelope",
  "version": 1,
  "type": "giphy-modal",
  "data": {
    "gif_url": "https://media.giphy.com/media/ZVik7pBtu9dNS/giphy.gif",
    "query": "balloons",
    "source": "GIPHY (demo mode)",
    "title": "Here's your balloons!"
  }
}]
```

The strings `"GIPHY (demo mode)"` and `"Here's your balloons!"` are hardcoded at `internal/mcp/self_tools_transport.go:445-456` (the `!giphy` self-tool demo-mode handler). They do not appear in the giphy builtin plugin code.

**Conclusion from runtime evidence:** the envelope that reached the user's frontend was emitted by core nanite self-tools code via the `<!--ENVELOPE_DATA:-->` marker path, not by the giphy plugin's hook handler. The giphy plugin's envelope-emission code at `internal/plugin/builtin/giphy/plugin.go:161` was not involved in producing this envelope.

### 2.2 `ValidateEnvelope` is advisory

`internal/chat/envelope.go:163-200` (`ParseEnvelopes`):

```go
for _, match := range matches {
    // ...
    var env Envelope
    if err := json.Unmarshal([]byte(jsonContent), &env); err != nil {
        errors = append(errors, EnvelopeError{Raw: jsonContent, Reason: "invalid_json"})
        continue
    }
    if verr := ValidateEnvelope(env, jsonContent); verr != nil {
        errors = append(errors, *verr)
        // Still include the envelope if it parsed — only invalid_json is fatal.
        if verr.Reason != "invalid_json" {
            envelopes = append(envelopes, env)
        }
        continue
    }
    envelopes = append(envelopes, env)
}
```

`internal/service/chat_generate.go:612-617` (caller behavior):

```go
envelopes, cleanContent, envErrors := chat.ParseEnvelopes(responseContent)
for _, envErr := range envErrors {
    log.Printf("chat-service: envelope error (%s): %s", envErr.Reason, chat.TruncateStr(envErr.Raw, 200))
    s.store.LogEvent(sessionID, "envelope_error", "warning",
        envErr.Reason, fmt.Sprintf(`{"raw":%q}`, chat.TruncateStr(envErr.Raw, 500)))
}
```

**Conclusion:** `unregistered_type` errors are logged to `stdout` and to the `event_log` table with level `warning`, but the envelope is still passed through in the returned `envelopes` slice. The only fatal error is `invalid_json`. Unregistered envelope types render correctly as long as the frontend has a registered React component for the type.

### 2.3 `event.Data["envelope"]` has no reader

Search commands and results:

```
grep -rn 'event\.Data\["envelope"\]\|Data\["envelope"\]' --include='*.go'
```

Two matches, both writers, zero readers:

- `internal/plugin/builtin/giphy/plugin.go:161` — writes `event.Data["envelope"] = string(envJSON)` inside the `message.sent` hook handler.
- `internal/plugin/builtin/oembed/plugin.go:138` — writes `event.Data["envelope"] = string(envJSON)` inside the `message.sent` hook handler.

`Host.EmitEvent` flow (`internal/plugin/host.go:1078-1110`):

```go
func (h *Host) EmitEvent(event plugin.Event) {
    // 1. Dispatch to registered event hooks (parallel, wg.Wait until done)
    // 2. Dispatch to trigger rules (fire-and-forget goroutine, sends to connectors)
    // 3. Broadcast to SSE subscribers via h.broadcastEvent(event)
}
```

`h.broadcastEvent(event)` at `internal/plugin/event_stream.go:35-46` simply forwards the event struct to every channel in `h.eventSubs`. It does not extract `event.Data["envelope"]` and does not inject envelopes into the message stream or the `messages.envelope` column.

The callers of `Host.EmitMessageSent` (at `internal/service/chat.go:155` and `internal/service/events_composite.go:55`) invoke it via `go s.pluginHost.EmitMessageSent(...)` — fire-and-forget — and do not inspect `event.Data` after the call.

**Conclusion:** any envelope written to `event.Data["envelope"]` by a plugin hook handler is effectively silently discarded. The write compiles, the log message `"oembed-card envelope attached"` fires, but nothing downstream consumes the value for message rendering.

**Unverified in this session:** whether any frontend SSE subscriber on `/api/events` (or similar) reads `event.Data["envelope"]` out of the broadcast event stream and renders it independently of the `messages.envelope` path. This was not traced. If that path exists, it would represent a secondary (undocumented) envelope delivery mechanism distinct from the `messages.envelope` column path.

### 2.4 Envelope JSON schemas are test-only

Grep for `schemaFS` and `jsonschema.NewCompiler` (via `internal/envelope/contracts_test.go:14, 261-303, 306-336`):

- `var schemaFS embed.FS` — declared only in the test file.
- `jsonschema.NewCompiler()` — called only in `TestEnvelopeSchemas_ValidJSON` and `TestEnvelopeSchemas_ExamplePayloads`, both test-only.

No runtime code in `internal/chat/`, `internal/service/`, `internal/plugin/`, or `cmd/nanite/` references the schemas FS, imports the jsonschema compiler, or performs schema-level validation on envelopes.

**Conclusion:** the 24 `*.schema.json` files under `internal/envelope/schemas/` exist exclusively to drive the contracts test suite. They do not constrain runtime behavior in any way.

### 2.5 No plugin.yaml → backend envelope registration

Grep command:

```
grep -rn 'envelopes\|RegisterEnvelopeType\|UIComponentTypeEnvelope' \
  internal/plugin/loader.go internal/plugin/host.go internal/plugin/registry.go \
  internal/plugin/types.go internal/plugin/manage.go internal/plugin/config.go
```

Only match:

- `internal/plugin/host.go:24` — `plugin.UIComponentTypeEnvelope: true` (appears as a key in an allowed-types set for `RegisterUIComponent`, unrelated to envelope-type registration).

No code in the plugin loader, registry, host, or lifecycle reads the `registers.envelopes:` section of a plugin's `plugin.yaml` and calls `chat.RegisterEnvelopeType` for each declared type.

The only consumer of `plugin.yaml` `registers.envelopes:` is `scripts/generate-plugin-imports.mjs`, which is a Node.js build-time codegen script that produces `ui/src/generated/plugin-envelopes.ts` for frontend component discovery. It has no runtime effect on the Go backend.

### 2.6 `chat.RegisterEnvelopeType` call sites

Grep across entire repo (including the in-tree `plugins/` directory):

```
grep -rn 'chat\.RegisterEnvelopeType\|\.RegisterEnvelopeType' --include='*.go'
```

Results (excluding function definition and test files):

- `plugins/fragments-engine/plugin.go:50` — `chat.RegisterEnvelopeType("sprint-planning-review")`
- `plugins/fragments-engine/plugin.go:51` — `chat.RegisterEnvelopeType("task-disposition")`
- `plugins/fragments-engine/plugin.go:52` — `chat.RegisterEnvelopeType("task-complete-notification")`

These are the only non-test call sites. The `plugins/fragments-engine` package achieves these calls by importing `github.com/hollis-labs/nanite/internal/chat` directly, which is only possible because it lives as a sub-package of the `github.com/hollis-labs/nanite` Go module (not as a proper external module).

`plugins/support-ticket/plugin.go` does not call `chat.RegisterEnvelopeType` at all. Its envelope types (`kb-result`, `ticket-form`, `ticket-confirmation`, `resolution-capture`) are declared only in `plugins/support-ticket/plugin.yaml:21-30`, which the backend never reads.

## 3. Envelope type ownership — full map

### 3.1 Core envelope types (loaded at startup)

Registered by `chat.InitCoreTypes` in `cmd/nanite/main.go:122-125`, which reads `config/envelopes.yaml`. Full list (from the manifest, 84 lines):

- Generic primitives: `document-viewer`, `report-card`, `error-report`, `approval-card`, `proposal-card`, `question-form`
- Work system: `todo-list`, `plan-review`
- Phase 7 primitives: `info-card`, `list-card`, `metric-card`, `progress-card`, `confirmation-card`, `table-card`, `timeline-card`, `diff-card`
- Backend-only: `session-task`

### 3.2 Types registered at plugin init() via `chat.RegisterEnvelopeType`

Exactly three, all from `plugins/fragments-engine/plugin.go:50-52`:

- `sprint-planning-review`
- `task-disposition`
- `task-complete-notification`

### 3.3 Types declared in plugin.yaml but NOT registered in Go

The following types appear in `plugin.yaml` files but have no `chat.RegisterEnvelopeType` call anywhere:

- `giphy-modal` (from `plugins/giphy/plugin.yaml:17`)
- `oembed-card` (from `plugins/oembed/plugin.yaml:12`)
- `kb-result` (from `plugins/support-ticket/plugin.yaml:21`)
- `ticket-form` (from `plugins/support-ticket/plugin.yaml:24`)
- `ticket-confirmation` (from `plugins/support-ticket/plugin.yaml:27`)
- `resolution-capture` (from `plugins/support-ticket/plugin.yaml:30`)

These types are not in the backend `registeredTypes` map at runtime. They rely on the advisory behavior documented in §2.2 to reach the frontend successfully.

### 3.4 Types emitted by core code that "own" a plugin name

The following envelope types are written into the chat stream by core nanite code despite their type names being declared in plugin.yaml files (or registered by a plugin's `init()`):

- **`kb-result`** — emitted by `internal/chat/envelope.go:97-136` (`BuildKBEnvelope`), called from `internal/service/chat_generate.go:869` whenever a tool name ends in `__search_kb`. Special-case hardcoded suffix match.
- **`ticket-confirmation`** — emitted by `internal/chat/envelope.go:139-155` (`BuildTicketConfirmationEnvelope`), called from `internal/service/chat_generate.go:602` when user-message content contains a `<!--TICKET_DATA:{json}:TICKET_DATA-->` marker.
- **`giphy-modal`** — emitted by `internal/mcp/self_tools_transport.go:445-456` (demo-mode handler) and `:519-524` (live-API handler) as part of the `!giphy` self-tool. Both paths wrap the envelope in an `<!--ENVELOPE_DATA:-->` marker.
- **`task-complete-notification`** — emitted by `internal/mcp/self_tools_transport.go:575-586` as part of the crossapp "report ready" handler. Wrapped in an `<!--ENVELOPE_DATA:-->` marker.
- **`task-disposition`** — emitted by `internal/mcp/self_tools_transport.go:698-707` as part of the task-disposition self-tool. Wrapped in an `<!--ENVELOPE_DATA:-->` marker.

Additional emission sites in core chat/envelope code:

- `internal/chat/errors.go:95` — builds an envelope with `kind: "envelope"` (type not traced during this investigation).

### 3.5 Types emitted by a plugin's own code (not via tool output)

- **`oembed-card`** — emitted by `internal/plugin/builtin/oembed/plugin.go:131-139` in the `message.sent` hook handler. Assigned to `event.Data["envelope"]`. Per §2.3, this assignment is dead code.
- **`giphy-modal`** (second emission site) — emitted by `internal/plugin/builtin/giphy/plugin.go:154-161` in the `message.sent` hook handler. Also assigned to `event.Data["envelope"]`. Also dead code per §2.3.

### 3.6 Types referenced in adapter-claude system prompt

`internal/plugin/builtin/adapter-claude/plugin.go:216-322` includes the following envelope type names as part of a system prompt passed to the Claude CLI adapter, instructing Claude which envelope types are available for emission:

- `task-disposition`
- `giphy-modal`
- `document-viewer`
- `report-card`
- `task-complete-notification`
- `sprint-planning-review`
- `kb-result`
- `ticket-confirmation`
- `ticket-form`
- `resolution-capture`
- `error-report`

These references are plain string content in a prompt template, not function calls.

### 3.7 Types referenced in test fixtures (non-exhaustive)

- `internal/chat/envelope_test.go:11` — `InitCoreTypes([]string{"kb-result", "session-task", "document-viewer"})` (test setup; registers kb-result for the scope of that test only).
- `internal/chat/envelope_test.go:179,197` — `kb-result` test payloads.
- `internal/chat/structured_test.go:81,83` — `kb-result` test payloads.
- `internal/tool/tool_test.go:214,219` — `kb-result` envelope-type assertion.
- `internal/service/chat_test.go:412,417` — `kb-result` marker parsing test.
- `internal/sandbox/sandbox_test.go:111` — asserts `ticket-confirmation` appears in a generated envelope markdown file.
- `internal/mcpserver/server_test.go:43,47` — `giphy-modal` envelope-data marker parsing test.
- `internal/envelope/contracts_test.go:37-48` — `knownTypes` list includes all plugin envelope types.
- `internal/envelope/contracts_test.go:169-246` — `examplePayloads` map includes example JSON for all plugin envelope types.

## 4. Working emission path (the only one verified)

### 4.1 Tool output with `<!--ENVELOPE_DATA:-->` markers

The sequence that produces a persisted, rendered envelope end-to-end:

1. A tool handler returns result text containing `...<!--ENVELOPE_DATA:{envelope JSON}:ENVELOPE_DATA-->...`.
2. `internal/service/chat_generate.go:862-878` (`captureEnvelopeData`) scans the tool result for the start/end markers, extracts the JSON payload, and appends it to a `pending` slice. If the tool name ends in `__search_kb`, the payload is wrapped via `chat.BuildKBEnvelope` before appending; otherwise the raw payload is appended.
3. The `pending` envelopes are concatenated into the assistant response content as `\n\n\`\`\`nanite-envelope\n{json}\n\`\`\`` fenced blocks.
4. `internal/chat/envelope.go:163-200` (`ParseEnvelopes`) re-parses those fenced blocks from the full response content into `[]Envelope`.
5. The parsed envelopes are JSON-marshaled and persisted to the `messages.envelope` column at `internal/service/chat_generate.go:634-639`.
6. The response stream emits the envelopes to the frontend as SSE `delta` events; the frontend's codegen'd `plugin-envelopes.ts` maps the envelope `type` to a React component and renders it.

### 4.2 Prerequisites for a plugin to use this path

- The plugin must register a tool whose handler produces the output.
- Tool registration currently requires access to `mcp.Manager.AddServer`. The `Manager` type is in `internal/mcp/`, which is not importable from an external Go module.
- `mcp.MCPTransport` (the interface a tool must implement) is also in `internal/mcp/`.
- There is no `Host.RegisterMCPServer` method or equivalent on the public plugin host surface.

### 4.3 Examples of this path in use in the repository

- `plugins/fragments-engine/tools.go:98` — formats `<!--ENVELOPE_DATA:...-->` in the sprint-planning tool result.
- `plugins/support-ticket/kb.go:239` — formats `<!--ENVELOPE_DATA:...-->` in the KB search tool result.
- `internal/mcp/self_tools_transport.go` lines 456, 524, 584, 627, 665, 705 — core self-tools formatting `<!--ENVELOPE_DATA:...-->` in tool results.

Both in-tree plugin examples reach the path via direct `internal/mcp` imports.

## 5. Gap list (facts only)

This section enumerates the gaps observed. No fix proposals are included.

### Gap 1 — No public `RegisterEnvelopeType` on Host

The `Host` type in `internal/plugin/host.go` does not expose a method for plugins to register envelope types. The only way to register a type today is `chat.RegisterEnvelopeType`, which requires importing `internal/chat`.

### Gap 2 — No public SQL database handle on Host

Plugins needing their own tables must access `*store.Store` via `host.GetService("store")` and either (a) assert to a `hasDB` interface to call `GetSQLDB()` (not currently present on `*store.Store`), or (b) assert directly to `*store.Store` and access the `.DB` field. Both require importing `internal/store`.

Observed in `plugins/support-ticket/plugin.go:46-58`.

### Gap 3 — No public `ExecuteMCPTool` on Host

Plugins that need to invoke an MCP tool from their own Go code (e.g., to call a sibling tool from within a handler) must access `*mcp.Manager` directly and call `mgr.ExecuteTool(...)`. Observed in `plugins/fragments-engine/plugin.go` lines 178, 198, 238, 258, 284, 324, 349 (seven call sites).

### Gap 4 — No public `RegisterMCPServer` on Host; `MCPTransport` is internal

Registering a custom MCP tool server requires calling `mcp.Manager.AddServer(name, transport)`, where `transport` must implement `mcp.MCPTransport`. Both `mcp.Manager` and `mcp.MCPTransport` live in `internal/mcp/`, so they cannot be imported from an external module.

Observed in `plugins/fragments-engine/plugin.go:68` and `plugins/support-ticket/plugin.go:107`.

### Gap 5 — Plugin-scoped agent profiles require direct store access

Plugins that want to seed an agent profile must construct a `store.AgentProfile` struct and call `store.Store.CreateAgent`. Both are in `internal/store/`. Observed in `plugins/support-ticket/seed.go:14-56`. The project's own architecture note (boot-prompt) states "agents/skills: file-based (MD + YAML frontmatter), DB = runtime state only" — there is currently no loader that reads plugin-shipped agent YAML files (e.g., `plugins/support-ticket/agents/it-support.yaml`) and materializes them into agent profiles at plugin load time.

### Gap 6 — `plugin.yaml` envelope declarations are not consumed by the backend

Documented in §2.5. The `registers.envelopes:` section of every `plugin.yaml` is read only by `scripts/generate-plugin-imports.mjs` for frontend codegen. The backend has no loader that reads this section and calls `chat.RegisterEnvelopeType` for each entry. Consequently, every plugin that declares envelope types in `plugin.yaml` without also calling `chat.RegisterEnvelopeType` in Go code (i.e., every plugin except `fragments-engine`) has its envelope types unregistered at runtime, and relies on the advisory validation behavior documented in §2.2.

### Gap 7 — No general-purpose plugin envelope injection path

Documented in §2.3. A plugin that wants to emit an envelope in response to an event (rather than as a reply to a tool call) has no working mechanism on the backend side. The `event.Data["envelope"]` pattern used by the giphy and oembed builtin plugins is not read by any downstream consumer in the message-persistence path. There is no `Host.EmitEnvelope(sessionID, envelope)` or equivalent method on the public plugin host surface.

**Unverified:** whether the SSE event stream broadcast by `h.broadcastEvent` is consumed by any frontend client in a way that would cause `event.Data["envelope"]` to be rendered independently of the `messages.envelope` column. This path was not traced.

### Gap 8 — No plugin-side rendering of the giphy / oembed built-in plugin envelopes

Documented in §2.3. The `internal/plugin/builtin/giphy/plugin.go:161` and `internal/plugin/builtin/oembed/plugin.go:138` writes to `event.Data["envelope"]` are not currently functional. Whether these plugins' envelope cards ever render in production depends on whether any undocumented path (e.g., SSE-side) picks them up — unverified.

## 6. File pointers for the future agent

### 6.1 Core registration and validation

- `internal/chat/envelope.go` — `registeredTypes` map, `RegisterEnvelopeType`, `InitCoreTypes`, `ValidateEnvelope`, `ParseEnvelopes`, `BuildKBEnvelope`, `BuildTicketConfirmationEnvelope`
- `cmd/nanite/main.go:121-125` — `InitCoreTypes` call site
- `config/envelopes.yaml` — source of truth for core envelope types (84 lines)
- `scripts/generate-plugin-imports.mjs` — frontend codegen script (reads `config/envelopes.yaml` and `plugins/*/plugin.yaml`)

### 6.2 Emission path

- `internal/service/chat_generate.go:595-640` — `BuildTicketConfirmationEnvelope` invocation, `ParseEnvelopes` caller, envelope error logging, envelope JSON marshaling and persistence
- `internal/service/chat_generate.go:862-878` — `captureEnvelopeData`, the only extraction of `<!--ENVELOPE_DATA:-->` markers from tool output
- `internal/service/chat_generate.go:1036` — retry path for envelope correction
- `internal/mcp/self_tools_transport.go:445-456, 509-524, 575-586, 620-627, 655-665, 698-707` — core self-tools that emit envelope types via the marker path
- `internal/mcpserver/handlers.go:27-32` — `convertEnvelopeMarkers` helper for stdio/PTY transport

### 6.3 Plugin host and event system

- `internal/plugin/host.go:1078-1110` — `Host.EmitEvent`
- `internal/plugin/event_stream.go:9-46` — `SubscribeEvents`, `UnsubscribeEvents`, `broadcastEvent`
- `internal/plugin/events.go:110-255` — `EventData` struct and all `Emit*` convenience methods
- `internal/plugin/types.go` — plugin host interface and related types

### 6.4 Plugins referenced

- `plugins/fragments-engine/plugin.go` — sole current caller of `chat.RegisterEnvelopeType`; imports `internal/chat`, `internal/mcp`, `internal/plugin`
- `plugins/fragments-engine/tools.go` — sprint-planning MCP transport; imports `internal/mcp`
- `plugins/fragments-engine/plugin.yaml` — declares 3 envelope types
- `plugins/support-ticket/plugin.go` — imports `internal/plugin`, `internal/mcp`, `internal/store`
- `plugins/support-ticket/plugin.yaml` — declares 4 envelope types
- `plugins/support-ticket/seed.go` — agent-profile seeding via direct `store.Store` access
- `plugins/support-ticket/kb.go` — KB transport with `<!--ENVELOPE_DATA:-->` marker output
- `plugins/support-ticket/tickets.go`, `download.go` — additional plugin source
- `internal/plugin/builtin/giphy/plugin.go:154-161` — dead `event.Data["envelope"]` write
- `internal/plugin/builtin/oembed/plugin.go:120-141` — dead `event.Data["envelope"]` write

### 6.5 Schemas and contracts test

- `internal/envelope/schemas/*.json` — 24 schema files (test-only, see §2.4)
- `internal/envelope/contracts_test.go` — `knownTypes` list (lines 18-49), `examplePayloads` map (lines 52-258), schema-loading and validation tests

### 6.6 Frontend codegen output (not edited in this investigation)

- `ui/src/generated/plugin-envelopes.ts` — generated by `scripts/generate-plugin-imports.mjs`
- `ui/src/generated/envelope-types.generated.ts`
- `ui/src/components/chat/envelopes/` — core envelope React components
- `plugins/*/ui/` and `plugins/fragments-engine/` / `plugins/support-ticket/` component paths referenced in each plugin's `plugin.yaml`
- `ui/src/components/chat/envelopes/ticket-utils.ts` — frontend utility specific to support-ticket envelopes (not traced in this investigation)

## 7. Verification log

Commands and actions run during this investigation on 2026-04-10:

- `grep -rn 'RegisterEnvelopeType' --include='*.go'` — confirmed §2.6 and §3.2
- `grep -rn 'chat\.RegisterEnvelopeType\|\.RegisterEnvelopeType' --include='*.go'` — same result
- `grep -rn 'Data\["envelope"\]' --include='*.go'` — confirmed §2.3 (two writers, zero readers)
- `grep -rn 'envelopes\|RegisterEnvelopeType\|UIComponentTypeEnvelope'` over `internal/plugin/loader.go host.go registry.go types.go manage.go config.go` — confirmed §2.5
- `grep -rn 'schemaFS\|loadSchemaFiles\|jsonschema\.NewCompiler' --include='*.go'` — confirmed §2.4
- `grep -rn 'ParseEnvelopes\|ENVELOPE_DATA' --include='*.go'` — enumerated all emission sites and the captureEnvelopeData extractor
- `sqlite3 /Users/chrispian/Projects-apps/nanite/nanite.db "SELECT envelope FROM messages WHERE session_id = 'bf0a2fbe-9d10-40b3-9191-254a98741595' ..."` — retrieved the live giphy envelope documented in §2.1
- `wc -l config/envelopes.yaml` — confirmed 84 lines, full contents read
- Full read of `internal/chat/envelope.go` (runtime file) — confirmed `registeredTypes` map, advisory validation
- Full read of `internal/plugin/host.go:1078-1110` — confirmed `EmitEvent` flow
- Full read of `internal/plugin/event_stream.go` — confirmed `broadcastEvent` behavior
- Full read of `internal/service/chat_generate.go:595-640, 862-878` — confirmed emission and extraction paths
- Full read of `plugins/fragments-engine/plugin.go` (first 100 lines) — confirmed §3.2 registration calls
- Full read of `internal/plugin/builtin/giphy/plugin.go:154-161` and `internal/plugin/builtin/oembed/plugin.go:120-141` — confirmed dead-code pattern

## 8. What was NOT verified in this investigation

- Whether any frontend SSE subscriber consumes `event.Data["envelope"]` from the `broadcastEvent` stream (see §2.3, "Unverified" note, and Gap 7). A ~15-minute trace of the `/api/events` endpoint and its frontend consumer would resolve this.
- Whether the runtime subprocess plugin mechanism (`internal/plugin/subprocess/`) correctly forwards `chat.RegisterEnvelopeType` calls or envelope-emission events from subprocess plugins. Subprocess plugins do not currently exist in the codebase; this path is unused.
- The full list of envelope type references in frontend TypeScript code (`ui/src/**/*.ts`, `*.tsx`). Only the files that matched the initial `kb-result|ticket-form|...` grep were noted.
- Whether `internal/chat/errors.go:95` emits a specific envelope type, and if so, which one.
- Whether `internal/assets/framework/docs/ref-envelope-component.md` and `ref-conduit-plugin.md` describe an intended architecture that differs from the observed runtime behavior.

## 9. Related documents

- `docs/architecture/plugin-system.md` — existing plugin system architecture document (not updated as part of this investigation)
- `docs/architecture/plugin-it-support.md` — existing support-ticket plugin architecture document
- `docs/architecture/plugin-evolution-plan.md` — existing plugin evolution plan
- `docs/plugin-extraction-plan.md` — existing plugin extraction plan
- `.nanite/agents/plugin-dev.md` — plugin-dev agent context document
- `docs/beta-known-issues.md` — beta known issues tracker (the original entry point that led to this investigation was the `plugins/support-ticket` fresh-clone build failure)
