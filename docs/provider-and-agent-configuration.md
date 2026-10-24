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

## Station gateway providers

The Station LLM gateway is reached through two seeded provider rows, one per
wire the gateway serves:

| Row id | Registry name | Base URL |
| --- | --- | --- |
| `station-openai-001` | `station-openai` | `https://llm.nanite.cloud/api/llm-gateway/v1` |
| `station-anthropic-001` | `station-anthropic` | `https://llm.nanite.cloud/api/llm-gateway` (no `/v1`) |

The rows are inert until a key is saved or a `key_ref` is set. The OpenAI wire
takes the base URL with `/v1`; the Anthropic wire takes it without.

Nine aliases are catalogued (`pkg/models/station.go`), each bound to the wire the
gateway serves it on. Using an alias on the wrong wire is refused by the gateway.

| Wire | Provider | Aliases |
| --- | --- | --- |
| chat/completions | `station-openai` | `go-glm`, `openai-mini`, `gemini-flash`, `openrouter-auto`, `hf-llama`, `local-light` |
| Responses | `station-openai` | `go-gpt` |
| Messages | `station-anthropic` | `go-haiku`, `anthropic-haiku` |

The gateway publishes no context window, max output or vision support for any
alias, and Nanite knows no price. Every alias therefore carries `limits_unknown`;
the catalogued figures are conservative floors, not measurements, and cost is
shown as unknown rather than zero. Aliases with no tool-call probe (all except
`go-glm`, `go-gpt` and `go-haiku`) also carry `tools_unknown`. Replace a floor and
clear its flag when Station publishes the figure.

### Settings keys

A station row's settings JSON accepts:

- `base_url` (required): an `http(s)` URL with no embedded credentials;
- `headers`: extra headers sent on every request. They are stored in clear, so
  they carry no credential: `Authorization`, `X-Api-Key`, `Proxy-Authorization`,
  `Cookie`, `anthropic-version`, `Host`, `Content-Length`, `Content-Type` and
  `X-Station-Session` are refused;
- `disable_anthropic_beta`: stop the Anthropic adapter sending `anthropic-beta`;
- `disable_cache_control`: stop it planting `cache_control` markers;
- `auth`: empty for the adapter default (`x-api-key`), or `bearer` for
  `Authorization: Bearer`. It applies to the Anthropic wire;
- `key_ref`: the key reference, as described above.

Invalid settings are refused on save, and a bad row is reported rather than
registered pointing nowhere.

### Key handling

Keep the key in a file readable only by the Nanite service, for example
`/var/lib/fabric/config/llm/station/nanite-gateway.key`: mode `0600`, a single
line, never printed or pasted into chat, mail or logs. Point each row at it:

```
nanite provider set-ref station-openai-001 file:///var/lib/fabric/config/llm/station/nanite-gateway.key
nanite provider set-ref station-anthropic-001 file:///var/lib/fabric/config/llm/station/nanite-gateway.key
```

`set-key` refuses `file://` because that scheme is read-only; use `set-ref`.
`set-ref` merges into the row's settings and keeps `base_url`;
`set-ref <id> --clear` removes `key_ref` and reverts to the default location.

`PUT /api/providers/{id}` replaces the whole settings string. Send the complete
object, including `base_url`, or the omitted keys are lost.

The adapter caches the key after its first fetch, so rotating the file takes
effect after a settings save (which re-registers the provider) or a restart.

`nanite provider status` confirms a key resolves without calling the gateway.
`POST /api/providers/{id}/test` does call the gateway (it checks the key by
listing models).

### Session header

Every gateway request carries `X-Station-Session`, the Nanite session id
normalized to the gateway grammar: 1 to 128 characters of `[A-Za-z0-9._:-]`,
starting alphanumeric (other characters become `-`, leading punctuation is
trimmed, the result is cut to 128). The mapping is deterministic, so the value
is stable across the turns and retries of one session. It is set per request and
cannot come from `headers`.

The gateway's refusals are surfaced as typed errors with the remedy:
`session_required` (the call did not come from a Nanite session),
`invalid_session`, and `model_not_available`, which means the alias is served on
a different wire ("wrong wire for this alias").

### Selecting a station model for a native agent

For a native (`api`) agent, `model_selection` is `{provider, model}`. `provider`
is the registry name (`station-openai` or `station-anthropic`), not the row id,
and `model` is the bare alias (`go-glm`, not `station-go-glm`).

A selection must match the agent's host settings. If it differs, session create
returns 422 `unsupported_selection` with cause `model_selection_mismatch`: the
requested provider/model "differs from the host settings ... omit the request
selection or change the host settings". With no host settings, the selection is
the whole choice. So `POST /api/agent/v1/sessions` with `definition_ref` set to
the `default_definition_ref` from `GET /api/agent/v1/capabilities`, plus a
`model_selection`, runs a tool-free session on any registered provider. Tools
need host settings and a native authority with grants.

### Verified

Verified 2026-10-24 on agent-os (`0.3.0-beta.1-43-g9ff919c1`): one tiny turn each
completed on `local-light`, `go-glm`, `go-gpt` (Responses wire) and `go-haiku`
(Anthropic wire). `anthropic-beta` and `cache_control` were sent by default and
the gateway tolerated them, so no `disable_*` flag was needed.

Tool calling: to be recorded.

### Attribution and usage

The gateway attributes calls by session. The operator reads usage with
`GET /v1/usage` using an operator credential holding `llm-gateway.usage.read`;
the Nanite token deliberately lacks it. Ledger writes are asynchronous and
bounded, and the `conversation_id` is namespaced by principal, so the raw Nanite
session id is not greppable in the ledger. It is an attribution aid, not a
financial ledger.

### Cost

No gateway budgets are configured. Go-subscription aliases (`go-glm`, `go-gpt`,
`go-haiku`) are distinct from paid ones (`openai-mini`, `anthropic-haiku`,
`gemini-flash`, `openrouter-auto`; `hf-llama` is unverified). Do not run paid
aliases without an operator budget.

### Rollback

- `nanite provider set-ref <id> --clear` for each row;
- disable the rows: `PUT /api/providers/{id}` with `{"is_enabled": false}`;
- reload Nanite.

The token's lifecycle (issue, rotation, revocation) belongs to the Station
operator, not Nanite.

The helper scripts used for the verification above live outside this repository
and are not part of the product.

Reflex validation checks definition shape and provenance permissions without
executing actions or mutating live state. Its `matched` response is a limited
preview of the legacy `tool_calls_window` equality predicate over supplied
state. Unsupported trigger previews return `evaluation_supported: false`,
not a claimed negative match. `action_executed` is always false. The retained
`fired` field is a compatibility alias for matching, not an execution report.
This preview does not simulate runtime cooldowns, scheduling or action success.
