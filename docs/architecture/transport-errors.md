# Service errors and transport parity

Service error categories use `github.com/hollis-labs/go-svcerr`. Services
choose an explicit code and safe message; a wrapped store error remains a
server-side cause. Missing rows are identified through `errors.Is` against
the store sentinel or `sql.ErrNoRows`, never by matching error text. An
infrastructure failure must not become a missing-resource response. The HTTP
and MCP mappers log the carrier's wrapped cause separately from its safe message;
`Error()` intentionally omits that cause.

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

`internal/mcpserver/transport_parity_test.go` drives registered HTTP routes
and MCP SDK `tools/call` requests, then uses the test-only
`github.com/hollis-labs/go-transportparity` assertions. The comparisons
cover todo update values, validation, absence, infrastructure failures,
agent editability refusals, agent create/update write failures, decoded request
fields and MCP discovery schema. SQLite abort triggers exercise write failures
after successful reads; closed-store cases exercise infrastructure failures.
Generated update timestamps are omitted from successful-value comparison.
This suite is a set of named operations, not a claim that every HTTP and
MCP operation has a matching door.

Agent configuration create/update/copy failures use the HTTP mapper, including
conflicts. Their composition/protocol assignment step keeps invalid references
and protocol values separate from infrastructure failures; both use the mapper.
Todo scope changes distinguish invalid input, absent todos and store
failures. Schedule creation maps a missing agent to 404 and an unreadable store
to 500; schedule validation remains 400, while database write failures are
internal errors. These categories describe the named service seams, rather than
an exhaustive conversion of legacy API error paths.
