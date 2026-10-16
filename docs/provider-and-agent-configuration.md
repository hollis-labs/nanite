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

## Provider keys by reference

A native provider's API key is a reference (`internal/providerkeys`, on
`libs/security/secrets`), not a value Nanite stores. For each provider with an
adapter, resolution walks an ordered chain and reports which step answered:

1. the `key_ref` field in the provider row's settings JSON, if set. It must be
   a reference (`keychain://`, `helper://`, `file://`, `op://`); a literal key
   is refused with HTTP 400 and never echoed;
2. the default keychain entry `keychain://nanite/provider/<provider-id>`, which
   also reads the earlier location (service `nanite`, account
   `provider-api-key:<provider-id>`) so an existing install keeps working. The
   earlier location is read, never written, and is not migrated;
3. the conventional environment variable, only when `NANITE_PROVIDER_KEY_ENV=1`.

A step that finds nothing falls through to the next. A step that is denied or
unavailable stops the chain and is reported, so a later empty step cannot hide
it. The source is reported as `keychain`, `keychain:legacy:nanite`,
`helper:<name>`, `file`, `op` or `env:<VARIABLE>`; the status and key endpoints
carry it as `key_source`, with `key_code` (`secret_not_found`, `secret_denied`,
`secret_unavailable`) when an API provider has no usable key.

Registration checks the chain once to decide whether a provider is usable; the
adapter holds no key until a request needs one, then fetches it and keeps it.
An adapter replaced by a key save starts over, and one already in use keeps the
key it fetched. A model selection that names an API provider with no usable key
is refused with that typed reason and the remedy, not "not a registered native
provider".

The key endpoint and `nanite provider set-key` write the configured `key_ref`
if it has a writable scheme, else the default keychain entry. The credential
store never falls back to a file or a database row. If it cannot save or remove
a key, the endpoint returns HTTP 503 with `credential_store_unavailable` and
sanitized guidance. A backend failure does not confirm whether its credential
operation completed; the running adapter is left alone. Clearing a key also
removes the earlier keychain entry, and can leave an enabled environment
variable active, reported separately as its key source.

Resolved key values are registered with a process-wide redactor that the log
handler applies to messages, string attributes and errors. Tests resolve through
`secretstest` (an in-memory store); a test binary that reaches the real keychain
fails instead of prompting.

`nanite provider list|status|set-key|clear-key|set-ref` is the terminal
interface. `set-key` reads the key from standard input only.

Reflex validation checks definition shape and provenance permissions without
executing actions or mutating live state. Its `matched` response is a limited
preview of the legacy `tool_calls_window` equality predicate over supplied
state. Unsupported trigger previews return `evaluation_supported: false`,
not a claimed negative match. `action_executed` is always false. The retained
`fired` field is a compatibility alias for matching, not an execution report.
This preview does not simulate runtime cooldowns, scheduling or action success.
