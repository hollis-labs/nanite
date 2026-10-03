# Nanite plugin API

An independently versioned public Go module for subprocess plugin authors:

```sh
go get github.com/hollis-labs/nanite/pkg/pluginapi
```

`Block` is the `nanite` object in a shared `plugin-sdk/manifest.Manifest`.
`EncodeBlock` validates it and produces the raw JSON value assigned to
`Manifest.Nanite`. Declare `pluginapi.Version` as the minimum `hosts.nanite`
contract version. `DecodeBlock` uses the SDK's strict decoder; the host then
checks runtime compatibility, available slots, registration conflicts and
filesystem confinement before loading anything.

UI registrations name exports of `UI.Bundle`. Drawer slots use
`SlotPrimaryDrawer` and `SlotWorkingDrawer`; panels and envelopes also name
component exports. Command, event, CRUD and plugin-relative HTTP route
registrations are declarative. The host owns registration conflicts and unload
cleanup. Common config, secrets, capabilities and agent tools live in the
shared manifest rather than this extension.

`Client` forwards a `ToolCall` to an explicitly configured literal loopback
HTTP(S) address at `/api/tools/call`, preserving session scope and MCP content
blocks. It refuses redirects, disables environment proxies, bounds responses,
respects context cancellation and never retries an uncertain action. A tool
error is returned as `ToolResult.IsError`; transport errors are Go errors.

This tool-call API executes as the desktop user. Session IDs are scope, not
credentials. The helper does not grant permissions or provide the read-only
host query surface. A plugin must still follow its host's approval rules.

For scoped read-only data, declare `readonly.query` in the common manifest's
capabilities. Its metadata is a `QueryScope`: choose the permitted resources and
either up to 64 session IDs or `all_sessions: true` for the host's workspace.
`include_content: true` additionally requests raw context-slot text; it requires
`context_slots`. Scope JSON rejects duplicate, unknown and case-variant fields.
The host must review the scope, enforce it on every read and revoke its
connection credential on unload.

Read the granted credential from `plugin/init`'s opaque `Identity` using
`QueryGrantFromIdentity`, then pass it to `NewQueryClient`. An absent grant
returns `ErrQueryNotGranted`; an optional plugin capability must degrade in
that case. Keep the credential in the subprocess: never send it to a browser,
write it to a file or log it. Grant objects carry a literal loopback origin and
query protocol 1. The UI registration contract remains `pluginapi.Version`;
query protocol negotiation is separate.

```go
grant, err := pluginapi.QueryGrantFromIdentity(params.Identity)
if err != nil {
    return err
}
client, err := pluginapi.NewQueryClient(grant, nil)
if err != nil {
    return err
}
result, err := client.Query(ctx, pluginapi.QueryRequest{
    Resource: pluginapi.QueryUsage,
    SessionID: sessionID,
})
```

| Resource | `QueryResponse.Data` | Session scope |
| --- | --- | --- |
| `sessions` | `QuerySessionsData` | One session or a bounded list of permitted sessions |
| `usage` | `QueryUsageData` | One permitted session |
| `execution_metrics` | `QueryMetricsData` | One permitted session |
| `context_slots` | `QuerySlotsData` | Most recent captured slots for one permitted session |

Queries use GET `/api/plugin-host/query/<resource>`, a bearer credential, and
`session_id`/`limit` query parameters. Limits are 1–100; zero selects the host
default. List payloads expose `more` when the limit omits rows. Context-slot
reads consume a captured snapshot and must never execute a context resolver;
`available: false` reports a missing capture. Metric payloads exclude raw errors,
debug snapshots, prompts and configuration. Session payloads exclude messages
and arbitrary metadata. The client bounds complete responses to 1 MiB, uses a
30-second transport timeout in addition to caller cancellation, refuses
redirects, disables environment proxies and makes no application retries.
Credentials grant no arbitrary SQL or tool execution.

Run `GOWORK=off go vet ./...` and `GOWORK=off go test -race -count=20 ./...`
from this directory. Tags for this nested module use `pkg/pluginapi/vX.Y.Z`.
The application root's `go test ./...` does not traverse a nested Go module.

Agent tools belong to the shared manifest's `tools` array. Nanite accepts
`read`, `write` and `destructive` effects; unknown effects refuse the bundle.
`ToolEffectHints` supplies behavior metadata to the host permission engine.
These declarations do not grant execution authority. The host must still apply
agent roster permissions and the plugin's `nanite.load_type` (`auto` or
`opt-in`), with explicit user tool preferences taking precedence. Tool names
use at most 64 ASCII letters, digits, underscores or hyphens, with at most 128
tools per bundle. `ValidateAgentTools` supplements SDK common validation.

The host discovers tools from accepted manifest declarations, without querying
the child for extra names or schemas. Calls use SDK `MCPCallRequest` with the
original declared `tool_name`, `arguments` and host-provided current
`session_id`; a child cannot choose its caller's session. Results use SDK
`MCPCallResult`, preserving `is_error` and delivering validated envelopes to
that session. Unload removes the plugin's tool namespace and availability.

Plugins contribute retrieval sources through `nanite.registers.context_sources`
with an `id` per source and a required `context.source` capability. Its metadata
lists the same `source_ids` and either explicit `session_ids` or `all_sessions`.
`include_query` separately permits user text and extracted keywords. A change to
that scope requires approval against the new bundle. Retrieval runs in the
existing dynamic context slot; it cannot replace instructions or cache markers.

Implement the SDK `HTTPHandler` for the private `ContextFetchPath` POST operation
and decode it with `DecodeContextRequest`. This operation travels over canonical
SDK `http/handle`, requires no public route declaration, and has no browser URL.
Return HTTP 200 with a JSON `ContextResponse` using `ContextProtocol`; an empty
`items` array means no contribution. Keys must be unique, content must be valid
UTF-8, and relevance is finite in [0,1]. Hosts assign source ownership and token
estimates. The wire bounds each response to 1 MiB and 128 items. Hosts additionally
apply retrieval deadlines, token budgets, scope checks and unload cancellation.

Extracted features preserve core session/message IDs as `CoreReference` values.
Request `message_refs` in the reviewed read-only query scope and call
`QueryClient.ResolveReference` to verify that a message belongs to its session.
The projection contains identity, role and creation time, without message text.
A missing reference does not authorize deleting the plugin's own record.

Core-table exports use `EncodeDataExport`/`DecodeDataExport`: JSON Lines with a
strict bounded header and one strict row per line, preserving SQLite NULL,
integer precision, real values, blobs and text bytes. `SourceID` separates
workspaces sharing a plugin DataDir. `DataExportReceipt.Verify` checks ownership,
workspace, row count and checksum. Import only a receipt committed by the host
with its schema change; an orphan file left by a rolled-back transaction is not
an import request. Keep the export after an idempotent transactional import.

Request `data_exports` with explicit `all_sessions` workspace scope to use
`QueryClient.ExportReceipts`. It lists committed receipts owned by the current
plugin in the host database; a credential cannot choose another plugin owner.

Feature plugins declare `nanite.registers.reflex_seeds` with stable plugin-local
IDs, explicit agent slugs, bounded predicates and reminder text. A required
`reflex.seed` capability must list the exact `seed_ids` and `agent_slugs`.
Contributions are opt-out-able, stay at plugin provenance and give no halt,
dispatch, schedule, tool enforcement or class-wide authority. Predicates run
inside the host; this declaration sends no raw steering state to the plugin.
The host owns canonical validation, durable edits/history and inactive-source
execution gating. Default seeds initialize a definition rather than replacing
operator edits on reload.

HTTP root and trailing-slash subtree declarations are distinct. A root resource
and its item routes can therefore use `GET bookmarks`, `GET bookmarks/` and
`DELETE bookmarks/` within the host-owned plugin namespace.

Implement SDK `HTTPHandle` with `HandleHTTP(ctx, pluginID, mux, request)` to use
ordinary `net/http` routes within that namespace. The adapter strips only the
owned prefix, preserves escaped separators and repeated/empty query values,
and bounds buffered bodies to `MaxHTTPBody`. Declare a subtree for item routes
and register patterns such as `GET /bookmarks/{id}` on the child mux. Dispatch
private context retrieval separately. Streaming remains on core SSE paths.

Integration plugins may request `durable_agent.wake` with an explicit
`agent_slugs` allowlist of database-backed durable instance slugs. The host
delivers a revocable `DurableWakeGrant` under `identity.nanite_durable_wake`.
Use `DurableWakeClient.Wake` to submit a bounded reason, nonempty prompt and
optional identity facts. Prompt delivery starts a real agent turn through the
core wake service. This capability grants no provisioning, profile edits,
arbitrary tool calls or scheduling. The client refuses redirects and environment
proxies, limits request/response sizes, and never retries an uncertain wake.

## Persistent user context

Declare `nanite.registers.always_ship_sources` and a separate required
`context.always_ship` capability to request persistent user-context placement.
This is stronger than `context.source`: the host places its bounded text in
`SlotUserContext`, which survives compaction and is exempt from dynamic-context
intent skipping on review, recall and resume turns. It competes with the user's
own context inside the existing 2,000-token slot ceiling. API agents receive
this text in the system prompt; CLI agents receive it before their user message.
The slot carries no cache marker. The host must explicitly review this power,
enforce the accepted scope on every fetch and revoke owned sources on unload.

`AlwaysShipScope.MaxBytes` caps **body bytes across all sources of one owner**,
not tokens or bytes per source. It must be positive and at most 6,000 bytes.
Host accounting additionally charges headings, separators and fallback text to
the shared token budget, and may assign less space. Like `ContextScope`, choose
either `session_ids` or `all_sessions`; no query text or keywords are permitted.
One to four declared source IDs must exactly match one nonoptional capability.
The same ID cannot occur in both the ordinary and always-ship source tables.

Each `AlwaysShipSource` has an ID, a bounded ASCII `title`, and `list_tool`.
The title becomes `## Title\n`; the plugin supplies the remaining body and
its deterministic item ordering. `Session Context` is reserved. The host also
reserves titles it still renders itself, such as `Session Documents`, and
refuses duplicate ownership before adopting core data.

`list_tool` is the public name of a read-effect tool in the **same shared
manifest**. It must support an initial inventory call in the calling session
and describe any pagination. Call `AlwaysShipScopeFor(block, capabilities,
tools)` after validating the common manifest to check that declaration. This
capability neither grants that tool to an agent nor makes it always included.
Normal tool grants, selection and execution checks still apply. Hosts must
document transport limitations rather than claiming a tool is available merely
because a fallback names it.

For example, this context-only manifest requests persistent pins:

```json
{
  "schema_version": 2,
  "id": "nanite.pins",
  "name": "Pins",
  "version": "0.2.0",
  "protocol": 1,
  "runtime": "subprocess",
  "entrypoint": {"command": "bin/pins"},
  "hosts": {"nanite": {"min": "0.1.8"}},
  "tools": [{
    "name": "pins_list",
    "description": "List visible pin inventory in the calling session",
    "effect": "read",
    "input_schema": {"type": "object", "additionalProperties": false}
  }],
  "capabilities": [{
    "name": "context.always_ship",
    "reason": "Retain user-selected pins on review, recall and resume turns",
    "metadata": {"source_ids": ["pins"], "all_sessions": true, "max_bytes": 6000}
  }],
  "nanite": {"registers": {"always_ship_sources": [{
    "id": "pins", "title": "Pinned Context", "list_tool": "pins_list"
  }]}}
}
```

Handle private SDK `http/handle` POST requests to `AlwaysShipFetchPath` with
`DecodeAlwaysShipRequest`. `AlwaysShipRequest` carries protocol 1, source and
session IDs, an optional host routing agent ID, intent, and `max_bytes`.
The decoder checks the outer SDK session matches; it is not an authorization
check. Return HTTP 200 with `AlwaysShipResponse{Protocol: AlwaysShipProtocol,
Body: body}`. Body is required; an empty string explicitly means no contribution.
`DecodeAlwaysShipResponse(raw, maxBytes)` refuses bodies beyond the assigned
allowance, invalid UTF-8, NUL, missing/null bodies and ambiguous fields. The
JSON wire ceiling is 64 KiB, independently of the 6,000-byte decoded body cap.

The host must use a deterministic section fallback naming the owner/source and
its `list_tool` when a fetch fails or the section cannot fit. If a plugin returns
only part of its own inventory, its body must say content remains and how to
list/read it; an opaque body gives the host no item-completeness signal.
Document and pin bodies can preserve historical core formatting byte for byte;
oldest-pin demotion is a separately defined consumer budget policy. Headings and
fallbacks are host-owned, while item rendering and inventory recovery remain
plugin-owned. This public declaration does not implement host composition,
review notices or agent tool transport.
