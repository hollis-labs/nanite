# Tether MCP configuration

`tether-mcp.json` registers the Tether messaging gateway so a Nanite agent can
send, reply to, list and settle Tether mail. It is what
`nanite mcp tether-messaging <absolute-token-file>` writes, so the command
replaces hand-importing this file. The inbound half (delivery and wake) is the
Tether bridge in [tether-delivery.md](../architecture/tether-delivery.md).

## What was checked

Against the installed `tether v0.8.0-404`: `tether mcp --help` lists
`--no-discover`, `--proxy`, `--servers`, `--scopes` (`message.write` covers
send, consume and cancel) and the global `--token-file`; the binary reads
`TETHER_MCP_SCOPES`. The entry therefore uses only flags that exist. The tool
names it publishes (`tether_message_send`, `_reply`, `_inbox`, `_list`, `_get`,
`_mark_read`) are the ones the hub gateway shows, but they were not discovered
through an imported Nanite server, because that needs a running daemon.

## Choices

- **Messaging only.** There is no `--proxy`, so no Torque or Tesseract upstream
  is reachable through this server. Add them later as a separate entry.
- **A token file, never a token.** `--token-file` points at a file holding the
  bearer token issued for the agent's stable actor. The config stores a path.
  Use one token and file per bound actor.
- **One address in both directions.** Send tools take `from`. It must be the
  actor URN the Tether bridge delivers to; the planted `tether-identity.md`
  names it. A token issued for that actor makes Tether attribute the send to the
  same address mail comes back to.
- **Short of hardened.** The token is a long-lived file credential under the
  relaxed posture. Rotation and device-bound credentials are follow-up work.

## Attaching it

1. Register: `nanite mcp tether-messaging /abs/path/tether-actor.token`. An
   existing server named `tether` is skipped, not replaced.
2. API agents: list `tether` in the host settings `mcp_servers`. CLI agents get
   the same entry once host `mcp_servers` is projected for CLI runtimes
   (CW-20261023-0023); until then a CLI agent cannot use it.
3. Grant the tools. Nothing is granted automatically: a grant is an operator
   act through the native authority, for example
   `nanite authority grant <authority-id> tether_message_send tether_message_reply tether_message_inbox tether_message_list tether_message_get tether_message_mark_read --reason "tether mail"`.
   Grant only `list`/`get`/`inbox` for a read-only agent. A grant makes a tool
   available; the permission engine still decides whether a call runs.

## Variant: Torque reads

The reviewed draft in Tether PR 173 scoped a proxy to Torque reads:
`tether mcp --proxy --no-discover --servers torque` with
`TETHER_MCP_TOOLS=["torque_task_get","torque_task_list"]`. `--proxy`,
`--no-discover` and `--servers` are real flags in the installed binary; the
tool allowlist variable name is present in it too. Its semantics were not
exercised.
