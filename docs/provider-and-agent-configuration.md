# Provider and agent configuration boundaries

Provider listings merge the live registered-provider catalog with database
configuration rows, then apply `NANITE_VISIBLE_PROVIDERS` by provider type.
`NANITE_VISIBLE_MODELS` filters model IDs. Comma-separated values ignore case
and surrounding whitespace; unset or empty lists leave listings unrestricted.
These controls affect presentation. They do not grant tools, modify stored
profiles or provider keys, or establish dispatch authorization.

The compiled model registry is a curated routing/fallback catalog, supplemented
by the models.dev metadata overlay. A configured gateway can serve a different
subset or expose custom IDs; neither the registry nor metadata discovery proves
that a gateway supports a model. Narrow the picker with the model/provider
visibility environment settings to match the deployment. This distinction is
intentional: listing metadata is separate from availability and host model
policy. It does not make incorrect context-window values intentional or repair
those values; metadata maintenance remains its own work.

`NANITE_AGENT_SLUGS` applies the same server listing allowlist to chat and admin
consumers. Chat start, switch and add selectors share `isSelectableAgent` to
exclude disabled profiles. Management deliberately retains disabled profiles
for inspection and enabling, and its `manageable` listing excludes embedded
internal profiles by source ownership. Existing session rosters retain their
members. These different purposes should not be flattened into dispatch or
mutation authority: readonly plugin/internal provenance and existing grants
remain authoritative at the host boundary.

The provider-key admin endpoint uses the OS credential store. It does not
silently fall back to plaintext files or database rows in a container. If the
store cannot save or remove a key, the endpoint returns HTTP 503 with
`credential_store_unavailable` and sanitized recovery guidance. Configure the
named provider environment variable in the service deployment and restart,
or provide a working OS credential store. A backend failure does not confirm
whether its credential operation completed; the running adapter is left alone.
Successful saves still replace the adapter for subsequent turns; existing
turns retain their adapter. Clearing a key can leave an environment credential
active, reported separately as its key source.

Reflex validation checks definition shape and provenance permissions without
executing actions or mutating live state. Its `matched` response is a limited
preview of the legacy `tool_calls_window` equality predicate over supplied
state. Unsupported trigger previews return `evaluation_supported: false`,
not a claimed negative match. `action_executed` is always false. The retained
`fired` field is a compatibility alias for matching, not an execution report.
This preview does not simulate runtime cooldowns, scheduling or action success.
