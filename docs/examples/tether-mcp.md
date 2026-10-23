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
  The file must be mode 0600 (registration checks it) and sit outside every
  path a CLI agent can read or write: an agent that can read it holds the
  credential. A CLI agent with a shell that runs as the same user can read
  any file that user can, so this cannot be prevented from Nanite: treat the
  token as exposed to such an agent, and scope it to message.write.
- **One actor per host, for now.** The `tether` entry is host-wide, so every
  agent listing it launches the gateway with the same token. Registering a
  second gateway is refused where the server is registered (store level), so
  `nanite mcp import`, the API and the CLI all meet it under any server name.
  List `tether` only in the host settings of the one actor whose token it holds.
  Per-agent planting (an entry in each agent's own boot dir) is a follow-up.
- **One address in both directions.** Send tools take `from`. It must be the
  actor URN the Tether bridge delivers to; the planted `tether-identity.md`
  names it. A token issued for that actor makes Tether attribute the send to the
  same address mail comes back to. **That attribution holds only when Tether
  enforces sender identity.** The send tools take `from` from the caller, and
  the daemon refuses a mismatched `from` only in enforce mode. In observe mode,
  which is how Tether runs today, an agent granted `tether_message_send` can
  send as any URN, including another agent's. Until Tether enforces it
  (tracked on the Tether side, CW-20261020-0047), the planted
  `tether-identity.md` is advice, not a control.
- **Short of hardened.** The token is a long-lived file credential under the
  relaxed posture. Rotation and device-bound credentials are follow-up work.

## Attaching it

1. Register: `nanite mcp tether-messaging /abs/path/tether-actor.token`. The
   file must exist with mode 0600. Re-running with the same file is a no-op; a
   different file is refused (see one actor per host).
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
