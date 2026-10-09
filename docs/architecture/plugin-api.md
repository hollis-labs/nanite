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


### Host grant descriptors and lifetime

Protocol 2 grants are issued by the host from the actual accepted review.
Nanite names its descriptors `host.nanite.readonly.query`,
`host.nanite.context.source`, `host.nanite.context.always_ship`,
`host.nanite.reflex.seed`, `host.nanite.durable_agent.wake`,
`host.nanite.ssh_agent` and `host.nanite.docker_socket`, with descriptor schema
version 1 and audience `nanite`. Manifest capability names remain unchanged.
Raw `Grant.Scope` carries the corresponding strict `pluginapi` DTO; it is not
an assertion that these fields implement the shared normalized
`capability.Scope` vocabulary. In particular, `all_sessions` retains its
current-workspace meaning and always-ship retains its aggregate byte ceiling.
The two environment descriptors enumerate only their host-allowlisted keys;
no declaration supplies arbitrary environment access.

The host policy uses a 24-hour grant lifetime by default. A positive Go duration
in `NANITE_PLUGIN_GRANT_LIFETIME` changes that lifetime without writing settings.
The policy revision binds the lifetime and accepted review. Each dispatch
revalidates that review and revision, and expiry or revocation cancels active
permits and fences the child. Unload, disconnect and replacement terminate the
old incarnation; there is no authority inherited from browser metadata or
plugin-requested grants.

Host-initiated renewal requires the same grant IDs, names, scopes, audience,
policy revision and runtime identity while the old lease is live. The host
commits only after the negotiated child renewal method acknowledges the exact
update. Missing negotiated support refuses renewal before dispatch; uncertain, failed or late acknowledgment
fences the owner rather than retrying or reviving expired authority. Already
running calls keep their old deadline and can be canceled conservatively by
replacement.

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

The browser endpoint uses `plugin-sdk/registry.Response` at registry version 2.
A process-owned host instance, monotonic live-owner generation and registry
revision identify each snapshot. Only running, accepted owners contribute.
`plugins` describes bundle bytes/runtime dependencies; `contributions` maps
owner-qualified kinds and keys to explicit module exports. Nanite stores grouping,
priority, labels, props and schema URLs in opaque contribution metadata. Slot
keys include the slot name so IDs in separate groups cannot collide. Browser
reconciliation, module caching, stylesheet ownership and subscriptions belong
to `@hollis-labs/plugin-registry`; Nanite supplies core-name refusal and React
render policy. A manifest change reconciles against already loaded modules.
The JavaScript bundle version is the SHA-256 of its actual approved artifact
bytes, rather than the digest of the full directory. Stylesheet URLs carry the
same bundle version. Exact React range declarations remain app metadata and
are checked against the running React version before activation; malformed or
unsupported ranges refuse activation. Slot renderers pass the owner and entry ID rather than guessing from an
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

After checking accepted bytes, the incarnation Init factory binds a fresh random connection token;
init delivers its grant under `identity.nanite_host_query`. The core
`GET /api/plugin-host/query/{resource}` route authenticates this bearer token,
checks the approved scope, and calls fixed service projections. Plugins cannot
supply SQL, tool dispatch or mutation callbacks. Lists are bounded to 100 rows,
responses to 1 MiB and reads to 30 seconds. Session metadata and metric error
text, debug snapshots and resolved configuration are excluded. Slot reads use
only inspector captures; they never assemble context or invoke resolvers.

The host retains token hashes and copied scopes. A supervised restart rechecks
the accepted bytes and policy, then issues fresh grant IDs and credentials for
its new incarnation; the accepted authority is unchanged. Failed initialization, unload, permanent supervisor
failure and host shutdown revoke it; revocation cancels active read leases.
Optional query requests are omitted from init grants when the host URL is
unavailable, while required requests refuse startup. This route's authority is
separate from the user's broader loopback tool API and process privileges.

Agent tools are declared in the common manifest's `tools` array. The host
validates the public `read`, `write` and `destructive` effect vocabulary before
review; unknown effects refuse the bundle. Its MCP adapter discovers accepted
names, schemas and behavior hints without consulting subprocess discovery.
Canonical SDK calls carry the original declared tool name and the current
host session, independently of arguments. Validated envelopes use the same
session's stream sink. Unload removes the namespace and marks durable catalog
rows unavailable, preserving IDs and existing agent grants across upgrades.

`nanite.load_type` supplies a manifest default. Persisted user preferences take
precedence and publish to the live manager with serialized settings writes.
Opt-in tools remain in the permission catalog and preferences UI but are hidden
from selection and server discovery, and both execution paths refuse them until
explicitly enabled. Enabling visibility does not grant agent roster membership
or bypass the execution permission engine. Conflicting declared names refuse
registration before any namespace mutation.

Context sources are declared in `nanite.registers.context_sources` and require a
matching, non-optional `context.source` capability. Its reviewed metadata permits
specific sessions or all workspace sessions; `include_query` separately permits
user text and extracted keywords. The host does not forward project paths.
Retrieval uses the public typed context wire over SDK `http/handle` at a private
child path, without registering a browser HTTP route.

The service container adds one permanent context-broker adapter at construction.
It snapshots plugin-owned sources for each fetch, bounds the combined retrieval
to three seconds and each child call to two, divides the allocated token budget,
and assigns source names and conservative estimates from observed content. It
rejects invalid or oversized output before assembly. Contributions enter the
existing dynamic Context slot; fixed ordering, the Universal slot and cache
markers retain their slot invariants. Unload, shutdown, failed launch and terminal
restart failure remove source ownership and cancel active calls. Publication
checks the source lifetime so a response cannot survive unload and replacement.

### Persistent and dynamic plugin context

Ordinary `context.source` contributions enter compactable `SlotContext` and
remain subject to intent skipping for `review_session`, `recall_decision` and
`resume_task`, with whole-item omission when their dynamic share is exhausted.
The extracted pins and documents plugins still lack core context parity until
they adopt the always-ship class. Plugins that need persistent placement must declare `always_ship_sources` and
obtain the separate reviewed `context.always_ship` grant. The host-owned review
notice describes system-prompt authority, up to 1500 uncached tokens per owner
per turn, and competition with the user's own context. It is part of the accepted
approval digest and is shown by both CLI and GUI installers. Rewording it
requires fresh approval for each always-ship plugin. The notice shows the validated
plugin ID and each source title/list tool, not a plugin-controlled name in the
host-authored warning sentence.

`PluginAlwaysShipSources` composes plugin-rendered bodies after the core
UserContext stash decision in `ContextService.AssembleSlots`. The class survives
compaction and bypasses dynamic intent skipping. It does not grant tool access.
Titles still owned by core, including Session Documents, refuse installation and
fetch until their core renderer is adopted. The host admits at most four owners
and four sources; install review reserves capacity against accepted installed
bundles (including disabled ones), with runtime enforcement as a defense. Updates
replace their own reservation. Concurrent installs of different owners use
per-plugin-ID locks, so both can pass the install-time check and exceed the cap;
runtime registration is the backstop and refuses the excess plugin's load.
An unreadable approval in the installed inventory blocks a new always-ship
install rather than assuming the existing owner has no reservation.
Titles are unique across owners after lowercasing
and collapsing runs of spaces, dots, underscores and hyphens; the first owner to
register keeps the title and later registration fails with an operator diagnostic.
The host orders Pinned Context ahead of other plugin titles, then sorts
by owner/source. Documents adoption gives Session Documents first rank when its
core reservation is removed.

The 2,000-token slot ceiling is shared. With an owner active, core's oversize
threshold reserves 256 tokens for late injections plus the measured complete
fallback line. This reserve tax applies even to empty successful responses:
about 290 tokens for a typical single owner and about 460 for four owners
with long names. At all three name limits (63-byte owner, 64-byte source and
64-byte tool), the default conservative estimator reserves 482 tokens, leaving
a core threshold of 1518. A typical pins/pins -> pins_list fallback reserves
289, leaving 1711. After stash failure, the inline-core plus fallback fit band
therefore starts at 1712 core tokens for that example, already below 2000;
1745 is inside the band, not its starting boundary. Names and the estimator
change these measured thresholds. Owner body bytes share its reviewed `max_bytes` allowance; each
owner's rendered sections also fit 1500 estimated tokens. Headings, separators
and pending fallback entries count toward the final slot budget. Fetches run
sequentially through private owned stdio with two-second child and three-second
turn collection deadlines. Bodies collected before the deadline remain eligible
for publication; approval is rechecked with the parent context. Approved active
owners that fail, time out or do not fit retain one deterministic merged fallback
line naming owner/source and list tool. Revoked or unapproved loaded leases
contribute nothing: no fetch or fallback, with one operator diagnostic per approval
transition. Final
publication rechecks approval and the original lease, preventing stale replies
from an unloaded/replaced owner. A successful source currently performs four
full-bundle approval checks per turn: before core budgeting, at composition
entry, before its owned fetch, and before publication. The pre-collection and
publication checks use the parent context outside the two-second child and
three-second collection deadlines. This is a known hashing-cost limitation and
follow-up: about 50 ms per warm 64 MiB bundle can add roughly 0.8 seconds per
turn for four owners of that size.

Stash failure preserves the existing inline-core behavior and logs an operator
diagnostic if plugin fallback/headroom cannot fit. In that exceptional case the
final Window clamp can consume the entire slot; plugin context and fallback are
not guaranteed. No turn error or new core truncation policy is introduced.
Window's existing truncation suffix and byte heuristic remain approximate token
accounting. Plugin bodies remain verbatim, including Markdown headings and even
text resembling the host fallback prefix; user pins can legitimately contain
these. The host owns the outer heading, not the meaning or sanitization of trusted
plugin text. A plugin returning a partial inventory must explain the omitted items
in its own body; the host cannot detect completeness from opaque text. List tools
must accept an initial call without required arguments, but remain subject to
per-agent grants and transport availability, including CLI MCP reach. Without active owners, late subagent delivery and unconditional batch Ack remain
byte-identical to the previous path. With an owner, inject messages individually
and Ack exact surviving appends. Free headroom is the slot's byte capacity
(`MaxTokens * 4`) minus already composed bytes and the append separator.
A message exceeding that currently free headroom is Acked after one truncated
delivery, preserving the historic truncated-result behavior even for 2–8 KiB
messages that could fit an empty slot. An append that fits that headroom but
fails exact assembly remains unread; zero-owner batches retain their old bytes
and unconditional Ack.

Extracted features retain core session/message IDs as references. The reviewed
`message_refs` query verifies identity within a permitted session and returns
role/creation metadata without message content. A missing core reference leaves
plugin-owned data intact; callers decide how to show the missing source.

The extraction helper `dataexport.ExportAndDrop` runs inside the caller's SQLite
write transaction. It preserves every column and SQLite storage class in a
bounded JSON Lines snapshot under the plugin DataDir. A source ID separates
workspace databases sharing that directory. Content-addressed files are published
without replacing existing files, synced, re-read and checked against source row
counts and checksums before a receipt and table drop enter the same transaction.
A savepoint removes provisional receipts on failure. Transaction rollback can
leave a durable file, which does not authorize import.

The `data_exports` read requires explicit workspace scope and filters committed
`plugin_core_exports` receipts by the authenticated plugin owner. Importers verify
that receipt against the retained file before an idempotent transaction in their
own database. The host never accepts table names, export owners or SQL through
this API. Feature-specific extraction invokes the helper only after release,
adoption and the reader cutover; adding the helper retires no core table itself.

## Durable wake integration

`durable_agent.wake` requires a reviewed explicit `agent_slugs` allowlist.
The slugs resolve durable instances already provisioned in the database; the
capability grants no profile edits, provisioning or scheduling. Init receives
`identity.nanite_durable_wake` with its own connection credential, independent
of any read grant. The fixed POST `/api/plugin-host/durable-wake` route checks
the live connection, strict bounded input and target scope before calling the
existing durable-agent wake service. Unload cancels its lease and in-flight
requests. Both scoped routes enforce their own credentials when Basic Auth is
enabled; plugin bundles never receive the user's broader password.

The plugin supplies translation into a bounded nonempty prompt and optional
identity facts. Core owns instance resolution, session creation and prompt
delivery. A successful projection reports `queued`, not completed execution;
skipped or failed wakes return a conflict. Clients never retry an uncertain wake.

The `nanite.loom` integration owns Fragments Engine callback translation at
`/api/plugins/nanite.loom/curator-wake` and three explicit Curator/Weaver reminder
declarations. The previous core callback route has no compatibility alias.
Its host service defers pilot ownership transfer until final activation, after
all accepted registrations succeed. A single transaction binds each uniquely
identified system pilot reminder to its original definition ID and changes only
its ownership/provenance markers. Edits, status, timestamps, firing history and
opt-outs remain attached. Ambiguous or non-system sources refuse the handoff.
Missing source definitions receive binding tombstones and are never recreated
by reload; desired new reminders can be defined through the host editor.
The normal reflex editor cannot change provenance; this host-controlled handoff
is the narrow exception for existing feature definitions. No evaluator,
resolver, executor or scheduling implementation moves into the plugin.

## Core feature data adoption

`PluginCoreData`, in `internal/service/plugin_core_data.go`, owns a fixed
feature-to-table allowlist. A plugin cannot request an arbitrary core table.
Bookmarked messages remain core references; bookmark rows belong to the external
`nanite.bookmarks` plugin. There are no core bookmark API routes, service, store
methods, widget, or keyboard binding.

After a reviewed subprocess and all its manifest registrations load, the host
transfers any retired bookmarks table through `dataexport.ExportAndDrop` in one
write transaction. The export lives under the same `brand.PluginDataDir` used by
subprocess initialization. The helper synchronizes and verifies every typed row
before committing the export receipt and table removal together. Failed export
leaves the table and rows intact. Disabled, rejected or absent plugins do not
transfer data.

The plugin imports only the current workspace's committed receipt, verifying its
owner, source, checksum and row count. Rows and its replay checkpoint commit
atomically in plugin storage. Subsequent loads keep operator edits and deleted
rows; a moved database keeps the original receipt source. Missing core references
remain plugin data. Historical schema migrations remain immutable, so a fresh
workspace initially creates the legacy table and transfers its empty snapshot
when the reviewed bookmarks plugin first loads.

Deployment must restart every older reader of the retired table before the new
binary loads the plugin. A running old process retains compiled queries against
`bookmarks`, even though the new binary no longer exposes that feature in core.

## Reminder extraction

`nanite.reminders` owns time and turn-count reminders, its working-drawer tab,
MCP tools, HTTP routes and `reminders` context source. `PluginCoreData` also
allows this owner to transfer the `reminders` table through the same committed,
verified export transaction. No core reminder tool, API, store method or
in-process trigger engine remains. Historical schema migrations still create
an empty legacy table until a reviewed plugin first activates.

Plugin context reads leave reminders pending. An agent calls `reminders_ack`,
or a user acknowledges in the plugin drawer, to stop injection. Budget omission
and failed turns therefore do not consume reminder records. New turn-count
reminders persist the current session message count as their creation baseline;
legacy rows retain the former engine's zero baseline because that information
was never stored. Project scope resolves from core session metadata, and
session/project identities cannot be supplied to the plugin's agent tools.

All older readers of the retired table must restart before deploying the
adoption binary and enabling the plugin. Disabled, rejected or absent plugins
leave core rows retained for a later committed transfer.

## Pins extraction

The released `nanite.pins` plugin owns durable pinned content, its working-drawer
UI, tools, HTTP routes and bounded E3 context source. Core pin readers/writers,
`context_pin`/`context_unpin` and pin REST routes are retired. Generic pinned
envelope cards remain a separate host feature.

After every required registration succeeds, the host's fixed allowlist maps
owner `nanite.pins`, feature `pins`, to table `pinned_content`. E1 exports all
columns and typed cells into the host-supplied DataDir, fsyncs the export, then
commits its receipt and table drop together. Failed activation/export leaves
core rows intact. Operators must refresh older compiled table readers before
cutover. Reconnects use the existing receipt, including its original source ID
when the database moves. Orphan exports grant no write authority.

Session/project pins persist until explicit deletion; project identity is
resolved from the authoritative session. Edits and deletion survive import
replay. Null-origin legacy project pins cannot be demoted into an invented
session. New turn scope is rejected because the old core tool neither persisted
nor injected it; legacy turn rows remain stored but do not enter context unless
promoted. Legacy agent attribution remains intact; new pins leave it empty
because SDK calls carry session identity but no agent identity.

The read-only query grant includes session metadata and export receipts only.
The context source excludes query text and uses the existing broker budget and
slot assembly. Slot positions, cache boundaries and compaction invariants remain
unchanged. Context reads never consume or delete pins.
