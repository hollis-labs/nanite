# Tether address and durable actor for native agents

Decision record (accepted). Nanite agents created with `agent_setup` have no stable
Tether address, so Tether mail cannot reach them. This document decides which
system owns the address, who owns the binding between an agent and that
address, and how the binding is created, used and retired. The decision was
design only; enrollment (E1/A2/A3) is now built (CW-20261024-0017), see
"Tether API as shipped" and "Enrollment as built" below. Statements in
"Context" describe the state at decision time and are marked where Tether has
since changed.

## Context

What is true today, from the code:

- **No production code creates an actor** (at decision time; `ActorEnrollment`
  in `internal/store/enrollment.go` is now the one writer, see below). `CreateDurableAgentInstance`
  (`internal/store/durable_agents.go`) and `durableAgentService.Create`
  (`internal/service/durable_agents.go`) return `ErrVerifiedActorRequired`
  ("no adopted issuer"). The only inserts into `actor_instances` and
  `agent_actor_bindings` are in tests. A definition pin, host settings id or
  slug does not enroll an actor (`AGENTS.md`).
- **The schema is ready for a writer.** `agent_actor_bindings(actor_uri,
  host_settings_id, binding_receipt, enabled)` and `actor_instances` (with
  `urn`, `current_session_id`, `status`, `profile_id` referencing the binding)
  exist (migrations 177, 178). Triggers refuse an `actor_instances` row unless
  an enabled binding with a non-empty receipt and an enabled host settings row
  exist. The migration comment says rows come only from "an authenticated host
  binding port".
- **The Tether bridge serves only instances that have a URN and a
  `current_session_id`** (`internal/tetherbridge/loop.go`), so without an
  actor no agent is ever served.
- **Two URN authorities exist.** Tether mints `msg://agent/agent-mux/agt_<10>`
  from `POST /registry/agents` and rejects a caller-supplied URN as
  `invalid_request`. Nanite mints `msg://agent/nanite/agt_<10>` itself
  (`a2a.GenerateAgentURN`) and can build `msg://agent/nanite/<slug>` aliases
  (`a2a.SlugAliasURN`). Tether treats an unknown authority as local unless a
  federation peer is configured for it, so a Nanite-minted URN is not a
  Tether mailbox address.
- **Tether's stated ownership rule:** the owning substrate owns public
  identity; Tether keeps a thin directory projection. Nanite and Tether are
  independently useful and compose through explicit references. Tether also
  has an idempotent registration primitive keyed on `(substrate, external_id)`
  (`registry.Service.RegisterIdempotent`, with a storage-level transaction in
  `RegisterWithExternalKey`) that nothing in the HTTP layer or elsewhere in
  production code called at decision time; `POST /registry/{kind}` went through
  plain `Register`, which mints a new URN on every call. **Since changed:**
  Tether build 49a3a6b2 exposes it over HTTP (see "Tether API as shipped"). `GET /registry/agents`
  can look an entry up by `external_id` and `substrate`.
- **One Tether gateway per install.** Migration 182 allows one enabled
  `mcp_servers` row that runs `tether ... --token-file`; it authenticates as a
  single actor, and outbound mail's `from` is the bound instance URN.
- **Sender attribution is not enforced.** Tether's caller-identity mode
  defaults to `observe`, and its own documentation says the current phase does
  not enforce sender or mailbox ownership at all, even in `enforce` mode
  (which only gates authentication). So a credential can claim a `from` it
  does not own. Sender-ownership enforcement has no scheduled date in
  Tether's documents; the enforcement runbook says every flip needs a
  separate owner approval per enforcement point.
- **Deregistration is a soft delete.** `DELETE /registry/{kind}/{urn}` sets
  status `deprecated` and returns the profile; child rows are untouched. The
  message send path did not consult the registry for the recipient's status at
  decision time, so a send to a deprecated URN was not refused. **Since
  changed:** build 49a3a6b2 refuses such sends with `409 recipient_deprecated`
  (see "Tether API as shipped").

Every Tether statement in this document was checked against Tether's `main`
branch, not a working branch.
- **Credentials.** Tether separates `tether.messages`, `tether.read` and
  `tether.registry`. Nanite's bridge needs the first two; registration needs
  the third, which is a different, more privileged credential.

The decision therefore has four parts: which authority mints the URN, who owns
the binding, the lifecycle, and the credential model.

## Options for the URN authority

**A. Tether mints, Nanite binds (recommended).** Nanite asks Tether to
register the agent once and stores the returned
`msg://agent/agent-mux/agt_*` URN as `agent_actor_bindings.actor_uri` and
`actor_instances.urn`.

- For: the address is one Tether already routes, delivers to, traces and
  lists; no federation or alias layer; works with the existing bridge,
  gateway `from` and sender policy unchanged; Tether's directory is the
  discovery surface other agents already search.
- Against: Nanite needs a `tether.registry` credential and a reachable Tether
  at enrollment; the agent's address is born outside Nanite, so enrollment is
  a two-system operation that needs crash-safe retry (see Enrollment).

**B. Nanite mints, Tether federates to Nanite.** Keep
`msg://agent/nanite/...`, register `nanite` as a federation peer authority
and have Tether route to a Nanite endpoint.

- For: Nanite stays the sole authority for its agents; no registry
  credential.
- Against: needs Nanite to serve a Tether peer-routing endpoint (new
  inbound surface, new auth); mail then lives in Nanite rather than Tether, so
  the ledger, trace, list/watch and claim/ack work already done in Tether and
  the bridge would not apply; contradicts the bridge design, which treats
  Tether as the durable store ("a stopped session leaves mail in Tether").

**C. Tether accepts a caller-supplied URN.** Tether relaxes the rejection so
Nanite can register its own `msg://agent/nanite/...` URN as a directory entry.

- For: keeps the existing Nanite-format URNs.
- Against: reverses a deliberate Tether contract (server mints, never from
  input); the `nanite` authority would still have to be treated as local;
  weakens the registry's uniqueness and trust story for little gain.

**D. Derive and never persist.** Compute the address from the host settings
id.

- Rejected: Tether cannot route to an address it never registered, and
  nothing would own deregistration.

Recommendation: **A**. Tether is the authority for the address; Nanite is
the authority for what the address means (which agent, which host settings,
which grants).

## Decision

1. **Tether mints the URN; Nanite owns the binding.** The address is
   `msg://agent/agent-mux/agt_*`. Nanite persists it and is the only writer
   of the binding and actor rows.
2. **Registration is idempotent on an external key.** Nanite registers with
   substrate `nanite` and an external id that is stable across retries and
   unique across installs (install identity plus host settings id).
   Tether had to expose its existing idempotent registration over HTTP (T0;
   now shipped, so the interim lookup-register-attach scheme described next
   is not used). The interim plan was lookup by external id, then register,
   then attaching the external id. Plain register does not store external ids, so the attach step
   has no HTTP route either: the practical interim is to carry the external
   key in the display name or a registry field and look it up by that, which
   leaves a window for duplicate directory entries that deregistration cleans
   up. This is why T0 gates a safe E1, not just a tidy one.
3. **Enrollment is an explicit, operator-authenticated act, not a side effect
   of `agent_setup`.** A new single writer (modelled on
   `store.LocalAuthority`, with a test pinning it to one consumer) creates the
   binding, the `actor_instances` row and an audit record in one transaction.
   The receipt records the authenticated credential id, never a request
   field. `agent_setup` stays free of Tether so Nanite remains independent of
   it.
4. **One actor instance per agent (host settings), not per session.** The
   address is stable; sessions attach and detach.
5. **Existing `msg://agent/nanite/...` URNs are not Tether addresses and are
   not migrated.** They stay where they already exist as local identifiers.
   Nothing new mints one for a durable agent, and nothing registers them in
   Tether. If any row exists in an install that uses one as an actor key, it is
   reported by the migration check and enrolled afresh rather than rewritten.
6. **The per-actor token is a follow-on, not a prerequisite.** Enrollment and
   delivery work with the install's one gateway token. Per-actor tokens
   replace the one-gateway rule later, and attribution is only trustworthy
   once Tether enforces sender identity.

### Enrollment API shape

Operator scope, same family as `agent_setup` and native authority:
`admin` for the first version.

- HTTP: `POST /api/native-agents/{host_settings_id}/enroll`
  body `{ "reason": "...", "revision": <host settings revision> }`
  returns `{ "instance_id", "urn", "binding": {"actor_uri", "enabled"},
  "status" }`. Repeating the call for an already enrolled agent returns the
  existing binding unchanged.
- MCP admin tool `agent_enroll` and CLI `nanite agent enroll <id> --reason R`
  with the same body. Retire is the existing `host_settings_retire` extended
  (below); there is no separate unenroll for v1.
- Configuration: the registry credential is a token file path
  (`NANITE_TETHER_REGISTRY_TOKEN_FILE`, 0600), kept separate from the bridge's
  messages/read token so a compromised bridge cannot register actors.
  Never accepted in a request body or logged.

Enrollment sequence, crash-safe:

1. Write an `enrolling` record (host settings id, external key, reason,
   principal) in its own table, committed first.
2. Register in Tether with the external key (idempotent, or lookup then
   register then attach).
3. In one transaction: insert `agent_actor_bindings`, insert
   `actor_instances` (status `sleeping`, empty `current_session_id`),
   write the audit row, mark the record done.
4. A restart finds an `enrolling` record and resumes at step 2; step 2 is safe
   to repeat.

### Lifecycle

- **Start:** setting `current_session_id` and a status of `active` attaches the
  actor to the session; the bridge picks it up on its next stream refresh.
  Stop clears it. A stopped session leaves mail in Tether; it is delivered
  when the next session attaches (catch-up reads from Tether, not Nanite).
- **Retire:** `host_settings_retire` archives the instance, disables the
  binding (`enabled=0`), and deregisters the URN in Tether. Deregistration in
  Tether is a soft delete to `deprecated`; the URN is never reused. Retire
  must succeed locally even if Tether is unreachable; the deregistration is
  retried from a queue and the instance shows `deregister_pending`.
- **Disable then re-enable** keeps the same URN; only retire ends it.
- **Authority and grants** are unchanged: a native authority is bound to a
  reviewed pair of host settings and definition pin and is not carried over
  by enrollment. Enrolling grants no tools.

## Consequences

- Positive: agents get a stable routable address; the existing bridge, ledger,
  sender policy and gateway `from` work unchanged; Nanite still starts, runs
  and tests without Tether (enrollment is optional and off the setup path);
  retire has one clear owner.
- Cost: a second credential (registry) and a two-system enrollment saga;
  cleanup of directory entries on failure; the Tether HTTP surface for
  idempotent registration must ship before enrollment is fully safe.
- Risk accepted: until sender-identity enforcement is on in Tether, a
  holder of the install's gateway token can send as any actor. With a single
  agent per install this is no worse than today; with several, treat
  attribution as advisory.
- Two-authority confusion is removed for durable agents: only Tether-minted
  URNs are addresses. `a2a` code that still builds nanite-authority URNs for
  other purposes is untouched.
- The A2A push notifier's `ErrVerifiedActorRequired` log noise remains until
  it is silenced separately; enrollment does not fix it.

## Tether API as shipped

Tether build `49a3a6b2` (Tether PRs #516 and #517), verified live by the Tether
agent. Nanite's tests run against a fake of this surface
(`internal/tetherregistry/tetherregistrytest`); nothing here was exercised
against a live Tether by the Nanite enrollment change.

1. **Idempotent register over HTTP** (#516): `POST /registry/agents` with JSON
   `substrate` and `external_id`, or one `external_key` =
   `<substrate>/<external_id>` split at the FIRST slash. The first call returns
   `201` and creates the entry (minting `msg://agent/agent-mux/agt_<10>`); a
   repeat returns `200` with the SAME URN and changes nothing (use `PATCH` to
   update); concurrent calls agree on one URN; half a key, or a conflicting
   `substrate`/`external_id`/`external_key` set, returns `400`.
   `GET /registry/agents?substrate=nanite&external_id=...` finds the entry. The
   MCP tool `tether_registry_register` takes the same fields and returns
   `created` true or false. For Nanite: substrate `nanite`, external id
   `<install_id>/<host_settings_id>`, so the key is
   `nanite/<install_id>/<host_settings_id>`.
2. **Sends to a deprecated agent URN are refused** (#517): `409
   recipient_deprecated`, nothing stored. A registry lookup failure fails open
   (the send proceeds). Mail queued before the deprecation stays readable. Not
   covered: routed replies, federation sends, group posts.
3. **Credential**: the registry write scope is `tether.registry`
   (`registry.write`), held by tokens scoped for it (and by `tether.maintain`
   and `tether.admin`). It is a separate credential from the bridge's
   `tether.messages`/`tether.read` token.

**Which listener accepts the registry token.** Found in the first live test: Tether's
REMOTE listener (TCP, for example `127.0.0.1:8948`, and the public edge)
authenticates paired device credentials only, so a registry service token sent
there gets `401` by design. The service token (a principal such as
`msg://service/nanite-registry/dev`) works only on Tether's LOCAL unix socket
(for example `/var/lib/fabric/tether/run/tetherd.sock`, mode `srwxrwxr-x`, owned
by the user Nanite runs as). On the socket scopes are attributed, not enforced
(observe mode), so the token can touch ANY actor: keep its use bounded.

Call shapes, HTTP over the unix socket, `Authorization: Bearer <token file
contents>`, any Host (Nanite sends `localhost`):

- lookup: `GET http://localhost/registry/agents?substrate=nanite&external_id=<install_id>%2F<host_settings_id>`
  (`404 not_found` when absent; `200 {"agent": {"urn", "status"}}`)
- register: `POST http://localhost/registry/agents`
  `{"display_name", "substrate": "nanite", "external_id": "<install_id>/<host_settings_id>"}`
  (`201` then `200` with the same URN)
- deregister: `DELETE http://localhost/registry/agents/<url-escaped URN>` (`200`
  with the profile at status `deprecated`; MCP tool `tether_registry_deregister`)

`display_name` on register, the lookup shape and the `DELETE` are **confirmed by
the Tether agent against build 49a3a6b2** and were read from Tether's source on
`main` (`internal/api/registry.go`), but **have not yet been observed by Nanite
against a live Tether**. The deregister call is isolated in
`tetherregistry.Client.Deregister`; a `404` is treated as already done.

**Live enrollment needs a scoped `tether.registry` credential (`registry.write`
only) that the operator issues.** Nanite does not issue, rotate or hold one for
itself; until the operator puts it in the token file below, `agent_enroll`
fails with `registry_token_missing` and writes nothing.

## Enrollment as built (CW-20261024-0017)

**Surface** (admin; `agents:manage` alone is refused, as for tool grants, which
is the ADR's "admin for the first version"):

- HTTP `POST /api/native-agents/{host_settings_id}/enroll`, body
  `{"reason": "...", "revision": "<optional host settings revision>"}`. Returns
  `201` on the call that enrolls and `200` with `"already_enrolled": true` and
  the same URN on a repeat: `{instance_id, urn, binding: {actor_uri, enabled},
  status, already_enrolled, external_key}`.
- MCP admin tool `agent_enroll` (`id`, `reason`, `revision`) on
  `/api/admin/mcp`; it needs the `admin` scope inside the tool, like
  `tool_grant`, so an `agents:manage` credential reaches the endpoint but is
  refused here.
- CLI `nanite agent enroll <host-settings-id> --reason R [--revision REV]`
  (offline, writes the database like `agent setup`; the principal recorded is
  `cli:agent-enroll`). The credential is never a flag.
- Retire is the existing `host_settings_retire` (MCP) and
  `POST /api/agent-host-settings/{id}/retire`, extended: it retires the actor
  first, then disables the host. There is no separate `unenroll`, per the ADR.

**Lifecycle rules (Architect decisions, CW-20261024-0017 follow-up).**

1. *Retiring an enrolled host needs admin.* It changes Tether's registry, so
   `agents:manage` and `maintain` credentials are refused for a host that has
   an enrollment record in any state (HTTP `403` code
   `enrolled_retire_requires_admin`; the MCP tool returns the same code).
   Retiring a host that was never enrolled is unchanged. Nothing is written or
   sent to Tether on a refusal.
2. *Hard DELETE of an enrolled host is refused* (`409`, code
   `enrolled_host_cannot_be_deleted`: retire first). This holds after the retire
   too, so the enrollment record and its URN outlive the host row.
3. *`PUT enabled=false` leaves the actor untouched.* It is the reversible
   disable: the binding, instance and Tether entry stay as they are, and
   `enabled=true` restores the host with the same URN. Only retire ends an actor.
4. *Re-enrolling a retired agent stays `409` (`retired`).* A retired URN is never
   reused. To give the same agent a new address, create a new host settings row
   (a new id): its key `nanite/<install_id>/<new_host_settings_id>` is new, so
   Tether mints a new URN.

**Writer.** `store.ActorEnrollment` (`internal/store/enrollment.go`) is the
only code that creates `agent_actor_bindings` and `actor_instances` rows. In
one transaction it inserts the binding (the receipt records the authenticated
credential id and the external key, never a request field), the instance
(`sleeping`, empty `current_session_id`, `profile_id` = the URN), an
`actor_instance_events` `created` row, an `event_log` audit row
(`actor_enrolled`, category `audit`) and marks the enrollment record done; any
failure rolls all of it back. `CreateDurableAgentInstance` and every other path
still return `ErrVerifiedActorRequired`. Only `internal/service/enrollment.go`
constructs the writer; `TestActorEnrollmentWriterHasOneConsumer` pins that.

**Crash-safe record.** Migration 185 adds `agent_enrollments` (one row per host
settings id: `external_key`, `status`, `urn`, `reason`, `principal`, `attempts`,
`last_error`, timestamps; no credential). Sequence: refuse early, writing
nothing, if Tether is not configured or the token file is unusable; commit the
`enrolling` record; `POST /registry/agents` with the record's own key; then the
one-transaction writer. If the process dies, or the writer fails, after Tether
answered, the record stays `enrolling`; a repeat registers again, gets `200` and
the same URN, and completes. The record keeps the key it was first written
with, so regenerating the install id between attempts cannot fork an agent.
Concurrent enrolls of one agent converge: one record, one binding, one instance,
one Tether entry. An enrolled agent is answered from the local record without
calling Tether. A retired actor is never re-enrolled (`409`); its URN is not
reused.

**Registry token.** `NANITE_TETHER_REGISTRY_TOKEN_FILE` names a 0600 file holding
the `tether.registry` token. The registry client's address is
`NANITE_TETHER_REGISTRY_ADDR`: `unix:///absolute/path/tetherd.sock` (a bare
absolute path also works) or an `http(s)` URL; if unset it falls back to
`NANITE_TETHER_ADDR`, which the delivery bridge keeps using on its own. A unix
address must be absolute and clean; it is checked at call time, a missing path
is `tether_unreachable` (retryable) and a path that is not a socket is
`tether_address_invalid`, both before anything is written. Registry calls are
configured when an address (either variable) and the token file are present.
Use the socket: a service token sent to TCP/https on the remote listener gets
401 by design. The file is read on
every call, refused if any group or world permission bit is set, and the value is
never logged, returned, stored in the database or accepted in a request. It is
never the bridge's `NANITE_TETHER_TOKEN_FILE`; there is no fallback to it.

**Typed errors** (`tetherregistry`; HTTP `code`): Tether not configured
(`tether_not_configured`, 503); token missing (`registry_token_missing`, 503);
token file insecure (`registry_token_insecure`, 503); Tether unreachable,
timed out or 5xx, retryable (`tether_unreachable`, 502); Tether refused with a
4xx (`tether_refused`, 502, carrying Tether's status and error code only, never
its message or any credential); already enrolled is success. Also `reason_required`
(400), `not_found` (404), `stale_revision`, `host_disabled`, `retired` (409).

**Retire.** In one transaction: instance `archived` and `current_session_id`
cleared, binding `enabled=0` (nothing routes to it), record
`deregister_pending`. Then `DELETE /registry/agents/{urn}`; on success the
record becomes `retired`. If Tether is down or not configured the retire still
succeeds locally, the record stays `deregister_pending` (the response carries
`actor: {deregister_pending: true, deregister_error}`), and every later retire
call, for this or any agent, retries all pending deregistrations; repeating the
retire for the same host is idempotent. A small background retry also runs
inside `nanite serve` (below). A record that never reached `enrolled` has no URN, so its key
is looked up in Tether and deregistered if found (the crash window). Local
retirement runs before the host is disabled because the schema refuses actor
updates once the host is disabled.

**Background retry of pending deregistrations.** `nanite serve` runs one pass
about 5 seconds after startup (non-blocking, after the HTTP server is coming up)
and then one every 15 minutes, until shutdown (the lifecycle context). A pass
attempts at most 20 `deregister_pending` records, oldest first. Each record has
its own capped exponential backoff, kept in memory: after the nth consecutive
failure it waits 15m, 30m, 1h, then 2h at most (a restart starts again at
once). Success marks it `retired`. It is not started when neither `NANITE_TETHER_REGISTRY_ADDR`
nor `NANITE_TETHER_ADDR` is set, and a pass does nothing (logged once at info) while the registry token
file is missing or insecure. Logs carry the host settings id and an error class,
never a token, URL or response body. The retry-on-next-retire path stays.

**Session start and stop.** Starting a session from an enrolled agent's host
settings (`POST /api/agent/v1/sessions` with `host_settings`, which is also what
`nanite agent run` uses) sets that actor's `current_session_id` and `status`
`active`, so the bridge serves it. Ending the session (the session service's
archive hook: `POST .../stop`, `DELETE /api/sessions/{id}`) clears it and returns
the actor to `sleeping`; mail waits in Tether. Agents without an actor are
unaffected. A session whose host settings were disabled by a path other than
retire cannot clear the actor (the schema refuses the update); that failure is
logged.

**Observed live (2026-10-25, agent-os, Tether build 49a3a6b2, one throwaway host, since retired):** over the local unix socket (`NANITE_TETHER_REGISTRY_ADDR=unix:///var/lib/fabric/tether/run/tetherd.sock`) with the registry token file: enroll created the actor (`already_enrolled: false`, URN minted, key `nanite/<install_id>/<host_settings_id>`); the repeat returned the same URN (`already_enrolled: true`); `GET /registry/agents?substrate=nanite&external_id=<install_id>%2F<host_settings_id>` returned 200 with `{"agent": {"urn", "kind": "agent", "owner": "nanite", "tether_instance_id", "display_name", "status", "field_metadata", ...}}` and the `display_name` Nanite sent (`display_name` is accepted on register); retire (admin) archived the instance and `DELETE /registry/agents/<escaped URN>` succeeded: the registry entry is NOT removed but set to `status: "deprecated"` (a tombstone; the URN is never reused), and a send to it was refused with `409 recipient_deprecated` (mail sent before the retire stayed stored). The remote listener and the public edge returned 401 "verified caller identity required" for the same token, as documented above. At startup the retry loop looked up a record that never registered (an earlier attempt that got 401), found nothing (404) and marked it `retired`.

**Still not observed live:** the 400 conflict cases of the register route, behaviour with a rotated token, and the retry loop against a Tether that is actually down (tested with the fake only). Verified by tests: register 201 then 200 with the same
URN, lookup, deregister with an escaped path, 400/401/403/5xx/dropped
connection/timeout mapping, the token file checks, the one-transaction rollback,
crash recovery, concurrency, retire with Tether down then retried, route and MCP
authorization, session attach and detach.

## Install identity

Nanite has no stable install id today. Searched: the config layout, the brand
package, `user_settings` (a single settings row with no id column of this
kind), host settings, scheduler identity tables, the server and whoami
surfaces. Only per-row UUIDs exist: `agent_host_settings.id` is globally
unique, but it identifies an agent, not an install, and a restored or cloned
database carries it along.

Proposal: add a single-row `install_identity` table holding a random UUID
minted on first boot after the migration, never derived from hostname or
address. The external key is `nanite/<install_id>/<host_settings_id>`. An
operator command regenerates the id on a cloned database, before any agent is
enrolled from it; enrolled agents keep their stored URN, so regenerating
does not break them. The id is shown on a read-only admin surface so an
operator can match a Tether directory entry to the install that owns it.

### Regenerating the install id on a cloned database

The id is minted on first boot after the `install_identity` migration and
read back on every later boot, so it is stable across restarts. A database
that is cloned or restored onto a second machine carries the source's id
along, and two installs would then build the same external keys. Before any
agent is enrolled from the clone:

    nanite install-identity --db <clone.db> show     # compare with the source's id
    nanite install-identity --db <clone.db> regen    # mint a fresh id

`regen` refuses when any `agent_actor_bindings` or `actor_instances` row exists
(enabled or not, archived or not), because an external key built from the old
id may already be registered in Tether. `--force` overrides that. Agents that
are already enrolled keep their stored URN: regeneration changes only the key
used by later enrollments and never rewrites a binding, an instance or a
registered Tether entry. Every regeneration is recorded in `event_log`
(`install_identity_regenerated`, category `audit`) with the old and new id, and
the row keeps the previous id and the time of the last change.

## Migration plan

1. Schema: add `install_identity` and the `enrolling` record table and a `deregister_pending`
   marker; no change to `agent_actor_bindings` or `actor_instances` columns.
   Relaxing the one-gateway index (migration 182) is deferred to the per-actor
   token step.
2. Existing installs: no actor rows exist today, so there is nothing to
   convert. The migration check counts any `actor_instances` or binding row
   found and refuses to proceed silently if one exists with a non-Tether
   authority; it never rewrites an actor key.
3. Rollout: enroll one test agent on a non-production Tether, verify mail
   reaches it, then enable the bridge (`NANITE_TETHER_ADDR`) on the hub only
   after the non-destructive catch-up work lands, so the lossy sweep is not
   exposed.
4. Rollback: disable the binding and retire the instance; deregister in
   Tether. No data migration to undo.

## Operator decisions

Accepted. The operator answered the questions this record raised:

1. Tether is the URN authority. `nanite`-authority URNs stop being addresses.
2. Tether exposes idempotent registration over HTTP (step T0); it is a Tether
   change.
3. Nanite gets a scoped `tether.registry` credential in its own 0600 file,
   issued at run time with the operator's explicit word. It is never issued
   as part of this design.
4. Enrollment is explicit and only for agents that take part in messaging.
5. Install identity is the `install_identity` table described above: a random
   UUID on first boot, a regeneration command for cloned databases, and the
   external key `nanite/<install_id>/<host_settings_id>`.
6. Tether refuses sends to a deprecated URN, so retire leaves no mail to
   drain on the Nanite side. This is a Tether change.
7. More than one enrolled agent per install is allowed with advisory
   attribution. Sender-identity enforce mode must be on before any production
   messaging that depends on trusted senders.

## Task breakdown (dependency order)

Letters refer to the scoping list for this workstream.

| Step | Owner | Work | Depends on |
|---|---|---|---|
| T0 | Tether | Expose idempotent registration over HTTP (new, not in the original list) | none |
| T0b | Tether | Refuse sends to a deprecated URN | none |
| A4 | libs/Tether | Typed `ListMessages` and `WatchMessages` in the client, release | none |
| A6 | Nanite | `Idempotency-Key` on bridge sends, correct stale delivery docs | none |
| A7 | Nanite | Silence the A2A push notifier log noise | none |
| A9 | Tether | Sender-identity enforce mode and per-agent credentials | none |
| E1 | Nanite | Enrollment writer, `enrolling` table, `agent_enroll` (HTTP, MCP, CLI), registry token path | this decision; T0 for full safety |
| A2 | Nanite | Enroll agents end to end, retire deregisters (E1 realises it) | E1 |
| A3 | Nanite | Attach and detach `current_session_id` on session start and stop | A2 |
| A5 | Nanite | Replace destructive sweep with list catch-up plus watch, persisted `after_seq` | A4 |
| A8 | Nanite | Per-actor gateway token, relax one-gateway rule | A2, A9 |
| A10 | Nanite + Tether | Live end-to-end: mail reaches an enrolled agent, wakes it, reply threads | A2, A3, A5 (A8 for several actors) |

Items that do not follow from this decision: A4, A5, A6, A7, A9. Items that
do: E1/A2, A3, A8, A10. A1 is this document.

## Wake policy and sender trust (MVP push, 2026-10-30)

**Mail wakes an enrolled actor by design.** A definition-bound session takes
`message_wake_policy` from its pinned definition, and the embedded archetypes
(`def:nanite/chat`, reviewer, researcher, backend, worker) pin `render_and_wait`
explicitly, which the message-wake reactor reads as "do not wake". The bridge
therefore wakes an enrolled actor itself whenever the reactor would not
(`tetherInjector.wake`): an actor is enrolled precisely to receive mail. Decision
(Architect, MVP): keep this; a mail-capable archetype that respects an explicit
policy is later work. Unset policies resolve to `auto_summarize` and the reactor
wakes them as before. A failed wake is returned, never swallowed: busy defers, an
ended session is detached and re-woken, an unsupported view is dead-lettered with
its reason.

**Verified senders get the agent's tools; unverified senders do not.** The wake turn
carries the enrolling credential (so the agent holds exactly the tools its host's
native authority grants) only when Tether verified the sender. A sender on
`NANITE_TETHER_TRUST_UNVERIFIED_FROM` (exact URNs, default empty) is accepted but its
turn runs tool-free, because an unverified `from` is caller-asserted. The bridge tells
the model it has no tools and that its plain-text answer will be sent for it; when the
turn completes the bridge relays that answer back to the sender over Tether as the
actor (`kind=response`, `in_reply_to` set, `Idempotency-Key` `nanite-relay-<delivery id>`).
The relay is in memory: a restart mid-turn loses it with the turn.

Note for testing: Tether verifies the sending *token*, so mail sent with `tether-remote`
under any `--from` is "verified"; to produce an unverified message send with a service
token (for example the bridge token over the local socket).
