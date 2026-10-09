# Remote MCP transports

Nanite accepts `stdio` subprocesses and `streamable` remote servers. Remote
registration uses `Manager.AddRemoteServerFromConfig`; direct HTTP registration
uses the same shared `go-mcp/client.Pool`. Its HTTP dialer constructs the official
MCP SDK `StreamableClientTransport`, including initialization, session handling,
JSON responses and SSE responses on the configured Streamable endpoint.

| Configuration | Wire transport |
|---|---|
| `mcp_servers.transport_type: stdio` | subprocess pipes |
| `mcp_servers.transport_type: streamable` | official SDK Streamable HTTP |
| `.mcp.json` URL entry with `type: http`, or no type | official SDK Streamable HTTP |
| legacy `sse` discriminator or remote path ending `/sse` | refused before new persistence or runtime registration |

## Legacy configuration

`mcpconfig.ValidateTransport` applies to server writes and runtime registration.
Imports validate every entry before creating any rows. Unknown or contradictory
`.mcp.json` types are errors, rather than silently becoming HTTP.

Legacy stored rows remain readable and exportable with their original URL and
transport discriminator. They are refused at startup, and may be disabled or
deleted through the existing server operations. Re-enabling them requires an
explicit supported configuration. Export preserves their original protocol so
re-import cannot silently convert them into usable Streamable rows.

To migrate a server, obtain its actual Streamable HTTP endpoint from its operator
and verify its authentication and identity-header contract. Select `streamable`
in the Nanite server configuration, or `http` in a `.mcp.json` URL entry. Update
both discriminator and endpoint explicitly. Nanite does not guess `/mcp` from
`/sse`, rewrite stored rows, or fall back to legacy SSE when initialization fails.
A gateway that forwards identity only over its old endpoint needs an operator
contract change; choosing a new transport alone cannot establish that contract.

Shared go-mcp generic SSE compatibility is separate from Nanite's operational
transport choices. Chat response SSE is also separate: receiving a Streamable
HTTP SSE response does not require the deprecated MCP `/sse` protocol.

## Headers, limits and retry

Static configured headers are parsed by `Manager.ParseHeaderJSON`, then passed
to the shared client's HTTP builder for every request. CR/LF rejection, header
value redaction and unusable-header handling retain their existing semantics.
Trust-tier response limits reach the client through `remoteTransport.SetMaxResponseBytes`;
call deadlines cover connection and execution.

Read-only discovery can reconnect and retry a failed listing. Tool calls use
`RetryIfUnsent`: only a request proved not to have reached the wire may be retried.
A missing or lost reply alone is insufficient to repeat a possible side effect.

## Gateway credentials and tool names

A gateway's own credential and an upstream's credential can be separate.
When the gateway consumes `Authorization`, the upstream credential needs the
specific header that gateway is configured to forward. Nanite supplies configured
static headers; it cannot establish an external gateway's forwarding policy.
Successful discovery alone does not prove a tool call is authorized upstream.

`service.RedactHeaders` masks values at the API boundary while retaining
stored values. `MergeRedactedHeaders` treats a returned placeholder as unchanged,
so editing an unrelated field cannot replace a working token with bullets.
Header values are not included in manager registration logs.

Grant names come from Nanite's actual discovery index, not a constructed server
prefix. Names remain bare until a collision requires disambiguation. Gateway or
endpoint changes can change publisher names; inspect the refreshed catalog and
grant the intended names explicitly. Discovery updates catalog availability
without granting agents or replaying revoked install declarations.

## Source verification boundary

`TestRemoteTransportStreamable_StatefulSessionWithoutLegacyEndpoint` exercises
initialization, session-bound listing and a tool call against an in-process
SDK endpoint that exposes `/mcp` only. The header and response-cap fixtures use
that same transport, and `TestRemoteTransportStreamable_NoLegacyFallback` checks
that a failed endpoint does not cause a legacy or guessed URL request.
Configuration/service/API refusal fixtures check that rejected inputs create no
rows or registrations and preserve retained operator configuration.

These fixtures characterize Nanite's source contract. They do not establish an
external ContextForge deployment's endpoint, installed version, identity
forwarding or credentials. Those must be verified separately before any live
configuration cutover.
