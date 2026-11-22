# Adding an agent

Nanite separates three things: immutable intrinsic definitions, mutable host
execution settings, and verified actor authority. Installing content does not
create an actor or grant tools and skills.

## Author and install intrinsic content

Definitions use agentdef v2 frontmatter, a stable definition ID, an authored
revision and a Markdown body. The SDK validates the complete schema. References
carry exact URI/digest pins; installation checks every referenced resource byte.
The semantic definition digest is distinct from each artifact byte digest.

Nine fresh flat archetypes are embedded under `internal/agentdefs/`. They are
newly authored content, not conversions of historical profiles. The original
`def:nanite-default` revision 1 artifact is preserved unchanged so an explicit
historical General Chat keep receipt remains verifiable.

```sh
nanite agent author --role role.md concrete.md > flat.md
nanite agent validate flat.md
nanite agent install --resources resources.json flat.md
```

`author` also accepts a standalone concrete definition without `--role`.
Flattening keeps concrete identity, requirements, permissions, continuity and
extensions. Behavioral prompt fields and native class may inherit. A nonnil
instruction/SOP list, including an empty list, overrides the whole role field.
Hooks stay concrete; required behavior that cannot be represented refuses.
Host provider/model, enrollment and grants never inherit from a role template.

`validate` checks schema and semantic pin; it does not claim referenced byte
availability. The install resource manifest is an explicit JSON array:

```json
[{"uri":"resource:instructions","path":"./instructions.md"}]
```

Installation writes the immutable artifact and referenced resources in one
transaction. Missing, changed, duplicate or unreferenced resources refuse.
Reusing an ID/revision with different bytes returns a conflict. Author a new
revision when content changes. Old profile sync/import and mutable profile
CRUD are retired, with no converter or boot-time grant replay.

Equivalent authenticated host endpoints are `POST /api/agent-definitions/author`
(`concrete`, optional `role`) and `POST /api/agent-definitions` (`artifact`,
`resources` entries containing `uri` and `content`). GET returns installed pins
and their immutable artifacts. Authoring alone does not install resources.

## Configure host execution inputs

Admin > Agents manages installed definitions and host settings separately.
`POST /api/agent-host-settings` creates a local host document; PUT and DELETE
require its current revision. The host assigns local IDs and revisions. A local
UUID is not an actor address.

Host settings contain provider/model, API or CLI runtime and transport,
harness-profile inputs, numeric recall limits/timeouts, filesystem/MCP execution
inputs and debug settings. Native provider/model selection is checked against
actual host policy. Caller source/plugin ownership fields are not accepted.
Workspace custody, sandbox and plugin credential enforcement remain host-owned.

Host title/enabled/settings edits do not mutate intrinsic content. Changing the
selected pin requires another already installed definition. Revision conflicts
refuse before effects. Retired IDs/slugs cannot be recreated through host setup.

Native `POST /api/agent/v1/sessions` requires `definition_ref` with API spellings
`definition_id`, `revision`, `semantic_digest`. Optional `host_settings` contains
`id` and `revision`; admission checks the matching pin, enabled API runtime,
authorized model and current host revision. The accepted view stores the typed
host snapshot. Later host edits affect new views, not previously accepted
configuration. Unsupported required semantics refuse before session rows.

## Native and reflex extensions

The optional negotiated extensions are:

- `com.hollislabs.nanite/native-policy`, version `1`, area `harness_profile`:
  intrinsic class, completion/wake policy, write-claim guard, and auto-recall
  enabled/confidence behavior. Numeric recall limits and host loop settings
  belong in host settings. A host guard cannot be weakened by intrinsic policy.
- `com.hollislabs.nanite/reflex-policy`, version `1`, area `behavior`: a pinned
  declarative bundle resource. Rules are validated, use the published evaluator,
  and request session-owned reminder/halt effects. Failed collection/evaluation
  stops the turn. A halt wins over softer requests and stops provider dispatch.

Unknown required namespaces, versions, fields and unsupported mandatory actions
refuse. A definition tool preference is not a tool grant. Native views refuse
force-tool preferences until a verified actor grant port is adopted. Mutable DB
reflex rules, plugin seed imports, procedures, knowledge seeds and context
resolver authoring do not have fresh runtime equivalents.

## Authority and historical boundaries

Verified actors are stored separately from host settings, with issuer binding
receipts and actor-keyed operational state/grants. No enrollment writer is
adopted yet. Supplying a URI, local UUID, slug, definition declaration, metadata
claim or an old profile's identity does not create authority. Actor-dependent
launch, durable creation, fabric operations and grant issuance explicitly refuse
when genuine issuer support is unavailable. Native General Chat remains a local
cognitive view with no borrowed profile grants. Its session-local cached-result
navigation is a host capability, not external tool authority.

Historical profiles and their full old relation graph remain intact. Ordinary
readers do not fall back to them. Explicit historical export and audited
retirement use named historical lookup methods and independent receipts.
Protected-profile retirement and suppression are separate source capabilities;
this change performs no old-row deletion, live data operation, backfill or
identity conversion.

Between-turn artifact refresh remains typed unavailable for active bindings.
Nanite retains the old binding and bytes; it does not directly overwrite active
boot artifacts or claim an inactive root based on timing. CLI/provider or fabric
adoption is not implied by installing a definition or saving host settings.
