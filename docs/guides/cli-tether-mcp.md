# Giving a CLI agent the Tether MCP bridge

A CLI-runtime agent (Claude, agy) can use Tether's own MCP gateway besides Nanite's. Nanite projects registry servers named in the host's `mcp_servers` into the CLI's MCP file, and only for the tools the host's native authority grants (fail closed). Codex and OpenCode are refused: they have no per-tool allow Nanite can set.

1. **Token file.** A 0600 file holding a Tether token for the agent's stable actor (issued by the Tether side, never by Nanite; never paste it anywhere).
2. **Register the gateway once per host:** `nanite mcp tether-messaging <absolute-token-file> [--db <path>]` writes the registry entry (`internal/mcpconfig/tether.go`: `tether mcp --no-discover --token-file <path>`, scope `message.write`). One gateway per host applies today (single actor token).
3. **Name it in the agent's `host.yaml`:** `mcp_servers: [tether]`.
4. **Grant the tools** you want through the native authority (nothing is granted automatically): `tether_message_send`, `tether_message_reply`, `tether_message_inbox`, `tether_message_list`, `tether_message_get`, `tether_message_mark_read`.
5. `nanite agent load` and start a session; the planted `.mcp.json` (Claude) or `mcp_config.json` (agy) now lists `tether`.

## Replying without a per-agent credential

An enrolled agent can reply with Nanite's own `tether_send` tool (grant it like any tool; it exists only when the Tether bridge is on, that is `NANITE_TETHER_ADDR` is set on the Nanite process, as the local unix socket form). The sender is always the session's enrolled actor, taken from the MCP session context, sent with the bridge service identity and an Idempotency-Key. This is the MVP path; per-actor tokens are the model for later (Tether sender-identity enforcement, CW-20261024-0023, will need a scoped send-as grant for the bridge identity).

## Mail to an enrolled CLI-backed agent

An enrolled agent whose host settings use `runtime: "cli"` (claude, codex or
agy) receives Tether mail the same way an API-runtime one does. Nothing polls:
the bridge starts a session from the host settings when the actor has none,
attaches the actor, and delivers the mail as a turn. A running session takes the
mail as its next turn. The agent replies with `tether_send` (verified sender) or,
for an unverified trust-listed sender, Nanite relays the final answer.

Proved live on agent-os on 2026-10-30, one enrolled host per runtime, each with
a `tether_send` grant on its native authority: a verified mail got a reply
(`in_reply_to` set) from agy, codex and claude with no manual step. The unverified
relay path was not exercised on a CLI runtime. A CLI process keeps whatever MCP
tools it was started with, so "tool-free" for an unverified sender is an
instruction to the model, not an enforced limit as it is on the native loop.

Findings from the proof:

- agy reads each MCP tool's descriptor file under its own
  `antigravity-cli/mcp/` with `read_file`, and headless agy denies that read
  unless allowed, which ended the turn with an empty answer. Nanite now writes a
  `read_file` rule for that one directory whenever it allows MCP tools.
- Token cost is dominated by the CLI's own prompt: one agy turn was 8.8k to 27.6k
  tokens, and one claude (haiku) turn 146.9k counting cache reads (30k of it cache
  writes). Budget a live proof accordingly.
