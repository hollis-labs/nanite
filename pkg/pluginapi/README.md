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
