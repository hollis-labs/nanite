# Tether MCP configuration

`tether-mcp.json` illustrates a scoped stdio server configuration for Nanite's
MCP importer. Its flag contract is drawn from the reviewed draft in
[Tether PR173](https://github.com/hollis-labs/tether/pull/173), commit
`ced6982c4e9ceb0b94ac6cecb3184e86903fe05c`. This example does not establish
availability in an installed Tether release. Verify the binary's supported
flags before using the configuration. The example supplies no credentials.

The ordered arguments select Torque without discovery. `TETHER_MCP_TOOLS`
is a JSON string restricting the upstream wire tools to task get/list;
Tether's gateway status diagnostic is separate from that allowlist. Neither
upstream exposure nor discovery grants an agent authority to call a tool.

Nanite discovers uniform names such as `torque_task_get` and
`torque_task_list`. A collision can change a name to a server-qualified form;
use the discovered catalog name and ID when granting access. To permit only
reads, grant the actual task-get entry to the agent and leave task-list and
status ungranted. Nanite's separately configured always-included tools retain
their existing host policy.

The importer skips an existing server name and preserves its stored
configuration. Importing this example therefore does not replace an existing
`tether-mux` server. Choose the intended configuration explicitly; importing a
configuration is an operator action, separate from these development fixtures.
