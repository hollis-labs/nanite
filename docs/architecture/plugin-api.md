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

External plugin declarations are generated with `plugin-sdk/manifest.Encode`.
`plugin.yaml` contains schema-v2 JSON (valid YAML), with host compatibility
against `pluginapi.Version`. General YAML and legacy Nanite declarations are
refused at discovery, installation and reload. Execution uses one executable
inside the bundle and literal arguments; PATH commands and symlink escapes are
refused.

Catalogs use the released `plugins-catalog` decoder. Canonical `id` identifies
an install, while `name` is presentation. The host selects the current OS and
architecture archive; entries without a matching archive can be browsed but
cannot be installed. SHA-256 checks cover both downloaded archive bytes and
extracted manifest bytes, and installation checks the declared archive size and
plugin ID before replacing an existing directory. Checksums establish byte
integrity, not publisher identity.

Browser bundles are served under `/api/plugins/<id>/bundle/` using the declared
bundle-relative paths. Envelope schemas use `/api/plugins/<id>/schema/<type>`;
only the corresponding declared schema file can be read through that route.

Installation uses a staged review before directory commit. CLI operators enter
the canonical ID; API clients first receive `409` with `status: review_required`,
actual declarations, `review_digest`, and the previous receipt when present.
They resubmit `approved_digest`; catalog upgrades also set `upgrade: true`.
Changed bytes require a fresh review. Receipts live in `.approvals/<id>.json`
beside installed bundles, independent of plugin data/cache. Installs from CLI
and API use `.install-locks/<id>.lock` under their common plugins root. A failed
commit restores the previous approval. A failed hot-load leaves accepted files
installed and reports a failure so configuration and reload can be retried.

Subprocess startup resolves only declared configuration/secrets. Secret keys
are `plugin:<id>:<name>` in the OS keychain; named environment variables take
precedence. Required missing secrets refuse loading. Accepted capability names
control additional connection variables and init grants. The shared lifecycle
host's `BeforeSpawn` callback revalidates the receipt before each spawn and
pins supervised restarts to the original review. Transport calls always apply
the host deadline, even when callers supplied a longer one; request and response
frames have independent caps. `SECURITY.md` documents the execution boundary.

The browser endpoint uses `plugin-sdk/registry.Response` at protocol 1:
`plugins` describes bundles/runtime dependencies, and `contributions` maps
host-defined kinds and keys to explicit module exports. Nanite stores grouping,
priority, labels, props and schema URLs in opaque contribution metadata. Slot
keys include the slot name so IDs in separate groups cannot collide. Browser
reconciliation, module caching, stylesheet ownership and subscriptions belong
to `@hollis-labs/plugin-registry`; Nanite supplies core-name refusal and React
render policy. A manifest change reconciles against already loaded modules.
Bundle cache tokens use the accepted bundle digest; stylesheet URLs carry it
too. Slot renderers pass the owner and entry ID rather than guessing from an
export name shared by several plugins.

The optional live browser fixture runs `TestPluginsRegistryLiveBrowserSmoke`
with `NANITE_PLUGIN_BROWSER_SMOKE_READY` naming a new private readiness file.
It serves a real loaded SDK subprocess and bundle from an isolated test host.
Pass the recorded origin as `VITE_NANITE_PLUGIN_SMOKE_URL` to the frontend
`plugin-loader.test.tsx` test; its final request stops the fixture.

Chat drawer tabs use `drawer.primary.tabs` and `drawer.working.tabs`. The host
preserves each declaration's title, icon, priority and props. Both drawers
render resolved exports from the shared browser registry, with owner-qualified
tab IDs separate from built-in and pinned-card IDs. `session_id` always reflects
the active session and overrides a stale value in manifest props. Unload removes
the plugin tab immediately; a selected unloaded tab shows an unavailable message
until another tab is selected. A failed render is contained to that tab.

Manifest `panels` contribute explicit exports under the browser registry's
`panel` kind. The right rail reconciles those declarations and loaded
`right-rail-tab` slots into its panel preferences, then renders the owning
export with the active `session_id`. Unload removes both the view and its
in-memory panel definition. The host reserves core panel IDs before discovery
and refuses conflicting ownership across panels and rail slots; the browser
and panel store enforce core precedence too. Render errors are contained to the
plugin view. Error recovery observes the changed export without remounting a
healthy view when another registry contribution changes.

Read-only queries use a separate `QueryProtocol` and `QueryClient`. A reviewed
`readonly.query` capability carries a strict scope: one or more of `sessions`,
`usage`, `execution_metrics` and `context_slots`, with either explicit session
IDs or `all_sessions` in the current workspace. `include_content` additionally
authorizes captured context text. CLI and GUI approvals show this metadata and
its upgrade diff. Unknown fields and malformed scopes refuse review.

After checking accepted bytes, `BeforeSpawn` binds a random connection token;
init delivers its grant under `identity.nanite_host_query`. The core
`GET /api/plugin-host/query/{resource}` route authenticates this bearer token,
checks the approved scope, and calls fixed service projections. Plugins cannot
supply SQL, tool dispatch or mutation callbacks. Lists are bounded to 100 rows,
responses to 1 MiB and reads to 30 seconds. Session metadata and metric error
text, debug snapshots and resolved configuration are excluded. Slot reads use
only inspector captures; they never assemble context or invoke resolvers.

The host retains token hashes and copied scopes. A supervised restart preserves
the original accepted grant. Failed initialization, unload, permanent supervisor
failure and host shutdown revoke it; revocation cancels active read leases.
Optional query requests are omitted from init grants when the host URL is
unavailable, while required requests refuse startup. This route's authority is
separate from the user's broader loopback tool API and process privileges.
