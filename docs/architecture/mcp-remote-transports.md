# Remote MCP transports

Nanite reaches an MCP server over one of three transports, named by
`mcp_servers.transport_type` and defined as `TransportStdio`, `TransportSSE`
and `TransportStreamable` in `internal/store/mcp_servers.go`. Two of them are
HTTP, and the distinction between those two is **which wire protocol the URL
speaks**, not whether anything streams:

| `transport_type` | Client | Protocol |
|---|---|---|
| `stdio` | `StdioTransport` | subprocess over pipes |
| `streamable` | `HTTPTransport` | JSON-RPC over POST |
| `sse` | `SSETransport` | the 2024-11-05 HTTP+SSE transport: a long-lived GET plus POSTs to an announced endpoint |

Both remote transports are registered through one entry point,
`AddRemoteServerFromConfig` on the manager, so a stored row has a single
meaning. Two registration paths each deciding what a row means is how
`AddHTTPServerWithHeaders` came to sit uncalled for months.

## What HTTPTransport is, and what it therefore cannot reach

`HTTPTransport` is a hand-rolled JSON-RPC POST client. It performs no
`initialize` handshake, keeps no session identifier, and has no SSE decoder: it
posts a request and parses a JSON body. That is sufficient for a **stateless**
endpoint and structurally insufficient for anything else.

It sends `Accept: application/json`. Go sends no `Accept` header unless one is
set, and a server that requires an explicit one answers `406 Not Acceptable`
before any MCP concern is reached — the server is registered, authenticated and
reachable, and publishes no tools.

The header is deliberately *not* the streamable-HTTP spec's
`application/json, text/event-stream`. Offering an event stream to a client
with no decoder invites a response it cannot read, which converts a clean 406
into a parse failure further from its cause. A caller-supplied header still
overrides the default, because the static header map is applied afterwards.

## Probe an upstream before choosing a transport

Three questions decide whether a server is reachable over `streamable`, and all
three are answerable with one request each. A stateful server fails the second
and third:

```bash
# 1. Does initialize issue a session id, and what does it answer with?
curl -s -D - -o /dev/null -X POST "$URL" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"probe","version":"0.1"}}}' \
  | grep -iE '^HTTP|mcp-session-id|content-type'

# 2. Does tools/list work with no prior initialize?
curl -s -X POST "$URL" -H 'Content-Type: application/json' \
  -H 'Accept: application/json' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
```

A `mcp-session-id` response header, a `content-type: text/event-stream`, a
`400 Missing session ID`, or a `406` demanding *both* media types each mean the
same thing: `HTTPTransport` cannot talk to it. Such a server needs a gateway in
front — a stateless gateway endpoint absorbs the handshake, the session and the
framing, and answers plain JSON — or it needs the SDK's
`StreamableClientTransport`, which this transport is not yet built on.

Run the probe against a server known to answer before trusting a negative
result. Both failure modes here are quiet: a 406 looks like a configuration
error rather than a missing header, and an empty tool list looks like a server
with no tools.

## Credentials are per-server static headers, and there are usually two

`mcp_servers.headers` is a JSON object sent with every HTTP request to that
server. Parsing lives in `ParseHeaderJSON`; the stdio transport ignores it.

A gateway-fronted server generally needs **two** unrelated credentials in that
one object, and conflating them is the common error:

- the **gateway's** own credential, which the gateway consumes and does not
  forward — typically `Authorization`;
- the **upstream's** credential, which the gateway forwards under a distinct
  header name it has been configured to pass through.

The upstream credential cannot travel on `Authorization` when the gateway
already claims that header, which is why upstreams behind a gateway read their
token from a header of their own. Where a gateway forwards a named header, a
static header set on the Nanite row reaches the upstream unchanged, so each
installation carries its own per-user credential and the gateway stores none.

The failure this produces is asymmetric and worth recognizing: gateway
credentials alone are enough for `initialize` and `tools/list` to succeed, so
**discovery passes and only the tool call fails.** Registration, authentication
and tool listing are not evidence that a call will work.

### Redaction

Header values are redacted at the API boundary, never in the store, which is
the record of truth. `redactHeaders` and `redactOne` in
`internal/api/mcp_servers.go` blank values on the way out of every endpoint
that returns a server record — including create and update, which echo back the
record the caller just wrote. The key survives so a UI can show that a header
is set.

`mergeRedactedHeaders` closes the loop: a value that comes back still redacted
on an update means "unchanged", so a UI round trip cannot overwrite a working
token with bullets.

## Tool names come from the publisher, and grants are matched on them

A gateway typically prefixes every tool it republishes with its own label for
the upstream, so the name Nanite discovers is the gateway's, not the MCP
server's. The same upstream reached directly publishes its bare name. Switching
a server between a gateway and a direct URL therefore **renames its tools**.

Agent grants are matched against `known_tools` by name, and a `roleTools` entry
with no matching row lands in the roster and produces no grant, silently. Two
consequences:

- an MCP server must be connected and discovered **before** an agent is granted
  its tools, never after;
- read the names from `GET /api/tools` rather than constructing them, and
  re-read them after any change to how a server is reached.

Nanite leaves a discovered name bare and only disambiguates on collision, so a
short generic name from a direct registration is stable only until a second
server publishes the same one.
