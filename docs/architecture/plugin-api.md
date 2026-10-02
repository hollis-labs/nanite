# Public plugin contract

`pkg/pluginapi` is a nested Go module that subprocess plugins can import without
Nanite internals. Its version describes the host registration contract, not the
application version; nested-module release tags use `pkg/pluginapi/vX.Y.Z`.

The host-neutral declaration belongs to `plugin-sdk/manifest`. It carries
identity, subprocess execution metadata, host compatibility, ordinary config,
secret declarations, capability requests and agent tool schemas/effects.
`pluginapi.Block` supplies the `nanite` extension: UI bundle metadata and
manifest-authoritative UI, command, event, CRUD and HTTP route declarations.
Drawer slots have public names, and panels/envelopes identify component exports
from the same bundle. HTTP route paths are relative to the plugin namespace.

A host adopting this contract must validate the common manifest and its own
extension before registration, enforce the declared host range, resolve and
confine real bundle paths (including symlinks), and keep registrations owned by
the plugin so unload can remove them. Type validation never grants authority.
Process launch guards, install confirmation, declared-secret resolution, bundle
digest verification and destructive-operation acknowledgments belong to the
host. Upgrades must preserve data and cache directories outside the bundle.

The public `Client` uses the existing loopback `/api/tools/call` wire shape.
It takes an explicit host URL and session scope, disables environment proxies,
refuses redirects, preserves content blocks and distinguishes tool errors from
transport failures. It never retries because an uncertain response can follow
a successful destructive action. This is the desktop user's execution surface;
it is not a read-only query capability or a credentialed plugin sandbox.

Subprocess lifecycle runs through the released `plugin-host` module. Nanite
supplies the allowlisted child environment, resolved init parameters and secret
redaction values; the library owns framing, handshake, health, supervised
restart, process groups and bounded shutdown. RPC handlers resolve the current
child on each call so a restart cannot leave them attached to the dead pipe.
The init identity must match the manifest before calls reach the child. Data
and cache roots stay outside the installed bundle. Load acknowledgments retain
skipped declarations without sending a second load call.

The nested module has its own CI gate because the application's root Go test
pattern excludes nested modules. Changes to the application loader and its
security boundary must be verified separately when it adopts the contract.
