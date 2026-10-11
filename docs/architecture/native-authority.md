# Native-view authority

A native view is a session Nanite itself hosts and runs: General Chat, a Code
Mode fork, an embedded-client session. It is not a fabric actor, so its tool
authority is local to Nanite and kept apart from fabric authority.
`agent_actor_bindings` and `actor_granted_tools` belong to fabric actors and are
never written for a native view.

## Records

`native_authorities` names one reviewed pair: a host settings revision and the
definition pin it carries. `native_authority_tools` holds the tools granted to
it, and `native_authority_audit` is an append-only record of every change. At
most one live authority exists per reviewed pair. Changing the host settings or
the pin does not carry grants forward; the operator reviews again and the old
authority stops applying.

Receipts are provenance: they say who granted what against which reviewed state.
Triggers check their shape only. Nothing on the dispatch path reads a receipt to
authenticate anyone.

## Writer

`store.LocalAuthority` is the only writer. It is constructed in the container and
used by the handlers in `internal/api/native_authority.go`, behind the `admin`
scope; `TestLocalAuthorityWriterHasOneConsumer` pins that. The credential id in a
receipt and audit row is the authenticated principal's, never a request field.
A grant change and its audit row commit in one transaction.

```
nanite authority list <host-settings-id>
nanite authority show <authority-id>
nanite authority create <host-settings-id> --reason R
nanite authority revoke <authority-id> --reason R
nanite authority grant <authority-id> <tool>... --reason R
nanite authority ungrant <authority-id> <tool>... --reason R
```

## Decisions per call

`NativeGrants` decides on every tool offer and dispatch; nothing is cached for a
turn. A call is allowed only if all of these hold now:

- the execution capability is live;
- the submitting credential still exists, is not revoked or expired, and holds
  the `tools` scope (a credential with only `operate` gets tool-free turns);
- the tool is granted by the live authority whose reviewed revision and pin still
  match the host settings in force;
- the turn's narrowing allows it. Narrowing only removes tools.

Revoking a grant, an authority or the credential therefore refuses the next call
of a turn already running. A call that has started cannot be recalled.

## Execution capabilities

An execution capability states that this turn of this view, submitted by this
credential, is executing. It is not a string, header, session id, slug, pin,
receipt or credential id; the type and its constructor are unexported, and a
value is valid only while the registry holds that exact pointer.

A Code Mode child gets its own capability: narrowed, with its own lifetime (it
outlives the parent turn), unable to spawn another child. It records the parent
capability identity (which execution spawned it) and the parent view id (whose
history it may read) as separate fields; history authorization compares the view
relationship. `nativeForkVerifier` implements `store.CodeModeForkVerifier` by
authenticating the capability in the context and treating the id argument only
as a consistency check.

## Attaching to the mesh

A native view that joins the fabric is a separate, deliberate step. Attaching
stops native admission (no new capability is issued), drains the active turn and
its children, and only then completes the handover; cancellation may shorten the
drain. Mesh control and native execution never overlap. This document describes
the boundary; the attach and detach flow is not part of native authority.
