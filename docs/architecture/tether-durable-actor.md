# Tether address and durable actor for native agents

Decision record (accepted). Nanite agents created with `agent_setup` have no stable
Tether address, so Tether mail cannot reach them. This document decides which
system owns the address, who owns the binding between an agent and that
address, and how the binding is created, used and retired. It is design only;
no code accompanies it.

## Context

What is true today, from the code:

- **No production code creates an actor.** `CreateDurableAgentInstance`
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
  production code calls; `POST /registry/{kind}` always goes through plain
  `Register`, which mints a new URN on every call. `GET /registry/agents`
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
  message send path does not consult the registry for the recipient's status,
  so a send to a deprecated URN is not refused by the registry and the mail is
  addressed by URN string alone.

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
   Tether must expose its existing idempotent registration over HTTP (a
   Tether-side change; see Tasks; on `main` nothing calls it). Until it does,
   enrollment uses lookup by external id, then register, then attaching the
   external id. Plain register does not store external ids, so the attach step
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
