# Service errors and transport parity

Service error categories use `github.com/hollis-labs/go-svcerr`. Services
choose an explicit code and safe message; a wrapped store error remains a
server-side cause. Missing rows are identified through `errors.Is` against
`sql.ErrNoRows` or domain absence sentinels, never by matching error text. An
infrastructure failure must not become a missing-resource response. The HTTP
and MCP mappers log the code and safe message even when no cause is wrapped.
HTTP logs include the route pattern, method and path identifiers; mapped MCP
self tools include the tool name and resource identifier. Wrapped causes are
logged separately from the safe message;
`Error()` intentionally omits that cause. Internal and unavailable failures log
at ERROR; client-correctable categories log at WARN.

HTTP owns its wire format. `API.serviceError` in `internal/api/service_errors.go`
uses `StatusFor` and the carrier's safe message to write the flat
`{"error":"message"}` body. Nanite deliberately does not use the library's
optional nested JSON envelope. Plain errors map to a generic 500. Domain
responses with extra information keep their explicit mappings; for example,
`writeNotManaged` reports a permission refusal as HTTP 409 with
`agent_not_managed`, management metadata and copy guidance.

MCP owns a different mapping. `ServiceErrorResult` in `internal/mcp` writes
`IsError=true` with JSON text containing `code`, `message`, and an optional
`field`. The stdio server preserves that text and failure flag through the
MCP SDK. Its status need not equal HTTP's status. Callers should use the
category rather than inspect human-readable text for these failures.

Todo updates share `TodoService.UpdateTodo` through a cycle-safe
`TodoUpdater` collaborator. Both doors accept title, description, status,
priority, labels and metadata, with pointer semantics: omission leaves a
field alone, while an explicit empty string clears it where valid. MCP's
`id` addresses the same resource as the HTTP path parameter. Both doors
return the updated todo; MCP carries its JSON in text content.

The shared todo-update contract and agent management refusals are the paired
HTTP/MCP seams. Agent configuration, todo scope and schedule HTTP errors also
use the typed mappings. This contract applies where a service returns a typed
error and the transport uses its mapper; other legacy operations retain their
own contracts.

Transport parity assertions belong at the real HTTP and MCP doors, including
request decoding, discovery and error serialization. The test-only
`github.com/hollis-labs/go-transportparity` library supplies those assertions.
A paired operation must preserve validation, resource absence, permission and
field semantics across doors; generated timestamps need not match.
