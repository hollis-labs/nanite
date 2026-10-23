# Native agent API

`/api/agent/v1` controls local cognitive views and turns. A view is stored chat
content. Creating one does not enroll an actor, boot a process, or borrow a fabric
identity. Host subject, parent and fork bindings are nullable. Retained operator
profiles and durable administration are separate resources.

Retained retry, agent-message and session-wide chat cancellation refuse defined
views. Native cancellation also remains scoped to a turn when a retained profile
view has native work in flight. Mailbox and subagent wakeups for defined views
use the same pinned, tracked native path, with background caller identity and
idle-only admission; their retained inbox/result records remain available.

## Core ownership

Nanite embeds `github.com/hollis-labs/libs/agent/core`. The library owns native
iteration ordering, provider event observation, bounded tool scheduling,
once-bound approvals, child lifecycle, canonical reduction and per-run HTTP
encoding. `internal/service/cognitive_turns.go` projects Nanite producer events
and committed `store.Message` output into the neutral service ports;
`internal/subagent/adapter.go` supplies database-backed settings/profiles, the
existing trust resolver, liveness configuration and panic observation.

Nanite owns definition resolution and model authorization, permission posture,
tool grants and execution, transcript/output commits, admission and shutdown.
HTTP handlers retain authenticated view/run lookup before passing a subscription
to the library writer. Transport extraction does not provision subprocess
credentials or widen the off-box host boundary.

## Definition and model binding

`POST /sessions` requires `definition_ref` with `definition_id`, `revision` and
`semantic_digest` (`sha256:` followed by lowercase hexadecimal SHA-256). The wire
spelling maps to shared `mesh.DefinitionRef`'s `id`, `revision`, `digest`. The
host-supplied `service.DefinitionResolver` owns pin/content verification; core
mapping checks the returned identity and supported semantics without a second
fabric verifier. A semantic digest mismatch returns typed 409 before creating a
view. An unknown revision returns 404.

Definitions are the installed, immutable artifacts in the database; a view keeps
its verified launch content and pin, and an edit never silently repins it.
Discovery supplies the embedded default pin for clients that need a default.
Nothing reads definitions from a directory yet. A proposal for doing so is in
`agent-alignment.md`; treat it as a design, not as behavior.

The chat mapping applies Markdown body, purpose/completion instructions and
permission posture. `default` binds to the native default gate; `read-only`
binds to plan mode. These can narrow the host engine's decisions, never grant
access or mutate its mode. Pinned dependencies, hooks, context policies,
capability/tool/skill/resource requirements and durable continuity policies are
refused because this consumer does not apply them. Unknown mandatory extension
semantics are refused. Unknown optional extensions remain part of the verified
digest and confer no authority. `com.hollislabs.nanite/native-policy` version 1
contains intrinsic class, completion/wake behavior, write-claim posture and
recall behavior. Model/provider and numeric execution settings belong only to
revisioned host settings. `com.hollislabs.nanite/reflex-policy` version 1
references a digest-pinned declarative bundle. Supported native effects operate
on the owning session; definitions cannot create mutable profile reflex rows,
actors, grants, or dispatch authority. Unsupported required effects are refused
before admission.

`model_selection`, when supplied, is exactly `{provider, model}`. It is a request
to `ModelAuthorizer`, independent of definition extensions. There is no
user-level or app-level default provider or model: the selection comes from the
agent's host settings or this request, and a view with neither is refused. The
standalone host requires a registered native provider. An embedding host may
supply a different authorizer. Unsupported selections return 422 before rows. Definition claims and caller `metadata`
cannot authorize a model, change harness settings, or grant tools. Verified pin
and chat configuration have dedicated storage, separate from mutable metadata.
`agent_id`, legacy model/provider fields and runtime selectors are refused on
native creation. Retained profile views report a null definition pin rather than
pretending a conversion occurred.

## Turns and observations

Submit text parts with `delivery: at_idle`; `effort` and `delta_mode` are optional.
The response allocates `session_view_id`, `turn_id`, `run_id`,
`output_message_id`, effective effort/delta mode and links for status/snapshot,
events and cancellation. The local run/output/turn identifiers share one opaque
identifier. They make no fabric URN claim.

An `api` session has the same durable session workspace a CLI session has:
`<WorkspacesRoot>/<session_view_id>/{prompts,state,logs}`, created on the first
turn (idempotent) and kept after the session ends. A turn does not run if it
cannot be created. Today the directory is created and carried on the turn;
tools are not yet rooted in it, scratch state is not yet written to `state/`,
and `directories` is still ignored for `api` hosts (CW-20261022-0007 is
delivering these in steps).

### Embedded view observations

An optional `client_context` on this turn request carries an untrusted view
observation, separate from the question and from all authorization:

```json
{
  "version": 1,
  "view": {
    "route": "/docs",
    "active_filters": [{"name": "status", "values": ["open"]}],
    "search": "release",
    "selected_ids": ["doc-1"],
    "visible_rows": [{"id": "doc-1", "summary": "Release notes"}],
    "available_commands": [{
      "name": "open_doc",
      "scope": "ephemeral",
      "input_schema": {"type": "object"}
    }]
  }
}
```

`version` and `view.route` are required. Other fields may be omitted or empty;
null is refused outside descriptive `input_schema` data. Field names are exact
snake case. Unknown or duplicate keys, invalid Unicode, wrong types, duplicate
named entries, unsupported versions and overflow return 400 before admission.
The host refuses oversized input rather than truncating it.

| Projection | UTF-8 byte/count limit |
|---|---|
| Complete descriptor, received and normalized JSON | 32,768 bytes |
| JSON depth / value nodes | 16 / 2,048; root depth 0; containers count, keys do not |
| Route | 256 bytes; origin-relative pathname without query, fragment or controls |
| Active filters | 16 unique names, 64 bytes/name; 1–16 string values, 256 bytes/value |
| Search | 1,024 bytes |
| Selected IDs | 64 unique IDs, 128 bytes/ID |
| Visible rows | 32 unique IDs, 128 bytes/ID, 512 bytes/display summary |
| Commands | 16 unique names, 128 bytes/name; scope `ephemeral` or `url-backed` |
| Descriptive input schema | JSON object, 2,048 normalized bytes, within global depth/nodes |

Capture a fresh opt-in, minimal display projection for each turn. Exclude
secrets; validation cannot recognize every secret. Command declarations are
descriptive data, never installed tools, grants or an executable schema.
Participant/browser binding and command acknowledgements belong to a separate
effectful command contract.

The accepted snapshot belongs only to that queued turn and survives HTTP
detachment. Native working history uses its exact admitted user-message ID;
future queued questions are excluded, and completed predecessor answers retain
their host-observed user/output pairing. A cleared, truncated or inconsistent
working-history boundary refuses enrichment and provider dispatch. Broker intent
and conversation use the same validated rows; descriptor text never becomes a
broker query. Cancellation drops that
turn's observation; a later turn without the field receives none.

Only provider-bound input receives a clearly labeled untrusted user-role message
immediately before the accepted question, with its tokens reserved in the input
budget. It stays outside pinned slots, compaction/stash/handoff, inspector input,
transcript, metadata, status and canonical events. If reductions or filters lose
the established question anchor, the host refuses rather than rebinding by text.
Restart does not reconstruct the descriptor or rerun a turn. A retry requires a
fresh descriptor. The provider receives this data and an assistant may quote it
in stored output; ephemeral host retention does not promise provider nonretention
or output secrecy. This carrier conveys no identity, participant, caller grant,
command execution, streaming proxy or durable approval-resume authority.

Admission serializes one executing turn plus `CognitiveQueuedTurnLimit` waiting
turns. Overflow returns 429 before writing the user message. The admission
transaction writes the user row and initial turn snapshot together.

After that commit, tracking and launch use the model resolved before admission
and a detached execution context. HTTP cancellation cannot strand a committed
turn. Host shutdown closes admission before draining the execution scheduler.

Status states are `submitted`, `working`, `input_required`, `completed`, `failed`,
`canceled`. Cancellation marks intent and signals only the selected generation;
a committed completed outcome wins a concurrent cancellation.

`GET /sessions/{id}/turns/{turnId}` is authoritative even without retained events.
It returns reduction, effective content, committed output when present, revision,
checkpoint, pending approval and cancellation intent. Outcomes and checkpoints
persist together. Event history is bounded and process-local; reopen preserves
committed outcomes and fails unfinished native turns with `process_lost`.

Per-turn SSE uses only `chatstream/v1` with a complete canonical event per frame.
The numeric `Last-Event-ID` names the last applied event; malformed cursors fail
before SSE headers. Explicit gap events cover retention and ahead-of-log cursors.
After 15 seconds with no event the stream writes an SSE comment line
(`: keepalive`); it carries no id and is not an event, so clients ignore it.
A turn queued behind a running one stays silent apart from these comments.
Status/snapshot is the recovery source. A subscriber disconnect or transport EOF
does not prove completion and cannot cancel the execution. There is no
session-level event route, mesh envelope or negotiated legacy dialect.

Bound approvals identify the run and call, carry the registry's expiry, and allow
only once scope. Both native and retained response facades use the shared native
registry for those prompts. Identical answers repeat the recorded outcome;
conflicting, expired or unanswered closed prompts return 409. They never grant a
subsequent call. Genuinely unbound retained admin approvals keep their existing
semantics.

List/history cursors are signed, process-local and scoped to their query/view.
Pages use immutable creation order and accept limits between 1 and 200, with a
default of 50. Request JSON rejects removed/unknown fields and trailing values.
Errors use a typed private envelope; credentials and internal errors are not
returned as raw diagnostic details.

## Host authentication

The host binds loopback only and authenticates every `/api/` route, including
`/api/agent/v1/*`, with a scoped bearer credential (see
[api-auth.md](api-auth.md)); `initialize` is the one public document. An
embedded client such as the Tangent backend uses a `client` credential with the
`read` and `operate` scopes (`nanite auth create --kind client --label tangent
--scope read,operate`). CLI JSON and event requests disable redirects and use
the operator token file or `NANITE_AUTH_TOKEN`. The planted `nanite mcp`
subprocess receives a short-lived `agent` credential bound to its session (tools
scope only) in its environment and forwards it only to the loopback host
endpoint, without redirects.

Snapshot storage failures produce a live `persistence_failed` terminal and the
host logs the typed library mutation error once per turn. This includes initial
snapshot creation. An initial creation failure closes the accepted producer before
provider/tool dispatch and keeps the failed turn ID discoverable. If both the
normal save and failure save fail, that live
terminal cannot be claimed durable: after recovery a fresh process sees the last
committed snapshot (`process_lost` if unfinished), or no snapshot if creation
never committed. Canonical replay remains process-local.
