# Giving a CLI agent the Tether MCP bridge

A CLI-runtime agent (Claude, agy) can use Tether's own MCP gateway besides Nanite's. Nanite projects registry servers named in the host's `mcp_servers` into the CLI's MCP file, and only for the tools the host's native authority grants (fail closed). Codex and OpenCode are refused: they have no per-tool allow Nanite can set.

1. **Token file.** A 0600 file holding a Tether token for the agent's stable actor (issued by the Tether side, never by Nanite; never paste it anywhere).
2. **Register the gateway once per host:** `nanite mcp tether-messaging <absolute-token-file> [--db <path>]` writes the registry entry (`internal/mcpconfig/tether.go`: `tether mcp --no-discover --token-file <path>`, scope `message.write`). One gateway per host applies today (single actor token).
3. **Name it in the agent's `host.yaml`:** `mcp_servers: [tether]`.
4. **Grant the tools** you want through the native authority (nothing is granted automatically): `tether_message_send`, `tether_message_reply`, `tether_message_inbox`, `tether_message_list`, `tether_message_get`, `tether_message_mark_read`.
5. `nanite agent load` and start a session; the planted `.mcp.json` (Claude) or `mcp_config.json` (agy) now lists `tether`.

## Replying without a per-agent credential

An enrolled agent can reply with Nanite's own `tether_send` tool (grant it like any tool; it exists only when the Tether bridge is on, that is `NANITE_TETHER_ADDR` is set on the Nanite process, as the local unix socket form). The sender is always the session's enrolled actor, taken from the MCP session context, sent with the bridge service identity and an Idempotency-Key. This is the MVP path; per-actor tokens are the model for later (Tether sender-identity enforcement, CW-20261024-0023, will need a scoped send-as grant for the bridge identity).
