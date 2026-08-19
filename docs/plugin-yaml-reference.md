# `plugin.yaml` Reference (v1)

Field-by-field reference for the Nanite plugin manifest, schema v1. The
canonical JSON Schema is at
[`internal/plugin/schemas/plugin.schema.v1.json`](../internal/plugin/schemas/plugin.schema.v1.json);
this doc restates it for human consumption.

`plugin.yaml` is **authoritative**. The host reads declarations from the
manifest and applies them on the plugin's behalf — the plugin's Go code
does not call `RegisterCommand` / `RegisterEnvelopeType` / etc. To decline
a declared registration at runtime, the plugin returns a `SkippedRegistration`
from `Load`.

## Top-level fields

### `schema_version` *(integer, required)*

Must be `1`. Unset or `0` is treated as legacy (pre-v1) and rejected by
new installs.

```yaml
schema_version: 1
```

### `id` *(string, required)*

Canonical plugin identifier. Used for registry lookups, config keys, data
directory paths, and log output. Pattern: `^[a-z][a-z0-9-]{1,62}$`.

```yaml
id: giphy
```

### `name` *(string, required)*

Human-readable display name. Shown in the Plugin Manager UI.

```yaml
name: Giphy
```

### `version` *(string, required)*

Plugin version. Must match the strict semver 2.0.0 pattern (including
pre-release and build metadata suffixes).

```yaml
version: 0.1.0
# or
version: 1.2.3-rc.1+build.42
```

### `description` *(string, required)*

Full description. Shown on the plugin details page.

### `short_desc` *(string, optional)*

Short tagline. Shown in catalog listings and plugin cards.

### `author` *(string, required)*

Author or organization name.

### `license` *(string, required)*

SPDX license identifier (e.g. `MIT`, `Apache-2.0`).

### `url`, `homepage`, `repository` *(strings, optional)*

`url` and `homepage` must be valid URIs. `repository` is typically a git
URL; no format validation is applied.

### `protocol` *(integer, required)*

Plugin JSON-RPC protocol version. Current is `1`. Minimum `1`.

```yaml
protocol: 1
```

### `runtime` *(string, required)*

Either `subprocess` (default for all new plugins) or `builtin`. `builtin`
is reserved for plugins compiled into the Nanite binary.

```yaml
runtime: subprocess
```

### `entrypoint` *(string, conditional)*

Executable command for subprocess plugins. **Required when `runtime: subprocess`.**
Relative paths resolve from the plugin install directory.

```yaml
entrypoint: ./giphy
```

### `load_type` *(string, optional)*

Default tool-loading behavior: `auto` (all tools available immediately —
the default), or `opt-in` (tools hidden until explicitly enabled by user /
agent / project / session config). Empty is treated as `auto`.

```yaml
load_type: auto
```

### `nanite_compat` *(object, required)*

Host version range the plugin supports.

| Field | Type | Required | Description |
|---|---|---|---|
| `min` | string | yes | Minimum compatible Nanite version. |
| `max` | string | no | Maximum compatible Nanite version (exclusive). |

```yaml
nanite_compat:
  min: 0.1.0
  max: 1.0.0
```

### `config` *(object, optional)*

Schema for plugin configuration values. Each key is a config name; each
value is:

| Field | Type | Required | Description |
|---|---|---|---|
| `type` | enum | yes | One of `string`, `secret`, `bool`, `int`, `select`, `multiline`. |
| `required` | bool | no | Refuse to load when unset. |
| `env_var` | string | no | Environment variable source. |
| `default` | string | no | Default value. |
| `description` | string | no | Help text for the settings UI. |

```yaml
config:
  giphy_api_key:
    type: secret
    required: false
    env_var: GIPHY_API_KEY
    description: Giphy API key; plugin runs in demo mode when unset.
  giphy_rating:
    type: string
    default: g
    description: Maximum content rating (g, pg, pg-13, r).
```

Resolution order (at `Init` time): env var → per-plugin `config.yaml`
override → default → required-check error.

### `dependencies` *(array of strings, optional)*

Legacy flat list of plugin IDs this plugin depends on. Prefer
`requires.plugins` in v1.

### `requires` *(object, optional)*

Structured dependency declaration.

| Field | Type | Description |
|---|---|---|
| `mcp_servers` | string[] | Required MCP server names. |
| `plugins` | string[] | Required plugin IDs. |
| `features` | string[] | Required host feature flags. |

```yaml
requires:
  mcp_servers: [cortex]
  plugins: [auth]
  features: [http-routes]
```

### `registers` *(object, optional)*

All declarative registrations. Every nested array is optional.

#### `registers.envelopes[]`

| Field | Type | Required | Description |
|---|---|---|---|
| `type` | string | yes | Envelope type identifier. Pattern: `^[a-z][a-z0-9-]*$`. |
| `component` | string | yes | React component export name. |
| `version` | integer | no | Envelope schema version (>= 1). |
| `schema` | string | no | Relative path to the envelope JSON Schema. |

```yaml
registers:
  envelopes:
    - type: giphy-modal
      component: GiphyModalCard
      version: 1
      schema: envelopes/giphy-modal.schema.json
```

#### `registers.commands[]`

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | Slash command name. Pattern: `^[a-z][a-z0-9-]*$`. |
| `description` | string | no | Help text. |
| `hidden` | bool | no | Exclude from `/help` listing. |
| `handler` | string | no | Named handler (for multi-command plugins). |
| `aliases` | string[] | no | Alternate names. |
| `args[]` | object | no | Argument list. |
| `metadata` | object | no | Free-form metadata. |

Each `args[]` entry:

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | Argument name. |
| `type` | string | no | Argument type (`string`, `int`, `bool`, ...). |
| `required` | bool | no | Refuse to dispatch when missing. |
| `description` | string | no | Help text. |
| `default` | string | no | Default value. |

```yaml
registers:
  commands:
    - name: giphy
      description: Search Giphy for a GIF and render it inline.
      args:
        - name: query
          type: string
          required: true
          description: Search query.
```

#### `registers.slots[]`

| Field | Type | Required | Description |
|---|---|---|---|
| `slot` | string | yes | Host slot identifier. |
| `id` | string | yes | Unique id for this contribution. |
| `component` | string | yes | React component export name. |
| `priority` | integer | no | Sort order (higher first). |
| `props` | object | no | Static props passed to the component. |

#### `registers.components[]`

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | Component name. Pattern: `^[a-z0-9][a-z0-9-]{0,62}[a-z0-9]$`. |
| `type` | enum | no | `widget`, `envelope`, `action`, `workflow`, `view`. |
| `description` | string | no | Help text. |
| `export` | string | no | ESM export name (defaults to `name`). |

#### `registers.keybindings[]`

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | string | yes | Keybinding id. |
| `keys` | string | yes | Key combination (e.g. `mod+shift+g`). |
| `description` | string | no | Help text. |
| `command` | string | no | Command id to dispatch. |
| `when` | string | no | Context expression. |

#### `registers.events[]`

| Field | Type | Required | Description |
|---|---|---|---|
| `types` | string[] | yes | Event types to subscribe to. |
| `handler` | string | no | Named handler (for multi-event plugins). |
| `priority` | integer | no | Dispatch order. |

#### `registers.crud[]`

| Field | Type | Required | Description |
|---|---|---|---|
| `resource` | string | yes | Resource type name. |
| `methods` | enum[] | no | Any of `create`, `read`, `update`, `delete`, `list`. |

#### `registers.http_routes[]`

| Field | Type | Required | Description |
|---|---|---|---|
| `pattern` | string | yes | URL pattern. |
| `method` | enum | yes | `GET`, `POST`, `PUT`, `PATCH`, `DELETE`, `OPTIONS`, `HEAD`. |
| `handler` | string | no | Named handler. |

#### `registers.mcp_servers[]`

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | MCP server name. |
| `description` | string | no | Help text. |
| `tools` | string[] | no | Tool names this server exposes. |

```yaml
registers:
  mcp_servers:
    - name: giphy
      description: Giphy GIF search tools.
      tools: [search]
```

#### `registers.agent_profiles[]`

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | string | yes | Registration key (unique within the plugin; used for duplicate detection and log/error messages). |
| `file` | string | yes | Relative path to the profile YAML. |

`file` must be a YAML document in the role/agent composition shape (Phase 5 item 03 —
`TASKS/phase-5/03-wire-registers-agent-profiles.md`; see
`docs/engineering/architecture/01-agent-construction.md` for the underlying model). The old flat
agent-profile shape is rejected with a clear error at plugin load time, not silently coerced.

```yaml
role:
  slug: my-plugin-curator       # required
  name: My Plugin Curator       # required
  system_prompt: |               # required — the role's persona/system prompt
    You are ...
  class: process                 # optional: advisor | process | template | harness

agent:
  slug: my-plugin-curator-agent # required — the agent's own slug
  name: My Plugin Curator        # required
  description: ...                # optional
  class: process                  # optional, defaults from role/'advisor'
  activation_mode: fresh-per-wake # optional: singleton | fresh-per-wake | concurrent
  runtime_kind: api                # optional: cli | api
  default_model: ...               # optional
  default_provider: ...            # optional
  system_prompt_override: ...      # optional — rarely set; empty defers to role.system_prompt
  tools: [tool_list]               # optional — known_tools.name values to grant (agent_tools)
  skills: [my-skill-slug]          # optional — skills.slug values to assign (agent_skills)
  consumer_slug: my-plugin         # optional — defaults to the plugin's own id
```

On plugin load, the `role:` block constructs/upserts a `roles` row and the `agent:` block
constructs/upserts the corresponding `agent_profiles` composition row (plus its `agent_tools`/
`agent_skills` grants), tagged with the plugin's canonical id for ownership tracking. Uninstalling
or disabling the plugin removes everything it registered here.

### `ui` *(object, optional)*

Frontend bundle metadata.

| Field | Type | Required | Description |
|---|---|---|---|
| `bundle_dir` | string | no | Directory containing the ESM bundle. |
| `entry` | string | no | ESM entry file (e.g. `index.js`). |
| `stylesheet` | string | no | CSS file (e.g. `style.css`). |
| `assets_dir` | string | no | Extra static assets. |
| `react_version` | string | no | Semver range for required React. |
| `shadcn_version` | string | no | Semver range for host shadcn primitive API. |

`shadcn_version` is validated against the host's `HostShadcnVersion`
(starts at `1.0.0`) at install time. Accepts exact, caret (`^1.0.0`), or
tilde (`~1.2.0`) ranges. Empty means the plugin doesn't use shared primitives
and the check is skipped.

```yaml
ui:
  bundle_dir: ui/dist
  entry: index.js
  stylesheet: style.css
  react_version: ^18.0.0
  shadcn_version: ^1.0.0
```

### `release` *(object, optional)*

Published-artifact metadata. Typically populated by the release pipeline,
not hand-edited.

| Field | Type | Required | Description |
|---|---|---|---|
| `archive_url` | URI | no | Download URL for the tarball. |
| `checksum_url` | URI | no | Download URL for the `.sha256`. |
| `signature_url` | URI | no | Download URL for the `.sig`. |
| `platforms` | string[] | no | Target platforms (e.g. `darwin-arm64`). |

### `tool_overrides` *(object, optional)*

Per-tool overrides for `load_type`. Keys are bare tool names (not prefixed).

```yaml
tool_overrides:
  search:
    load_type: opt-in
```

## Full valid example

```yaml
schema_version: 1
id: giphy
name: Giphy
version: 0.1.0
description: Giphy GIF search — /giphy <query> for inline GIFs and a giphy.search MCP tool.
short_desc: Giphy GIF search.
author: Hollis Labs
license: Apache-2.0
homepage: https://github.com/hollis-labs/nanite-plugin-giphy
repository: https://github.com/hollis-labs/nanite-plugin-giphy
protocol: 1
runtime: subprocess
entrypoint: ./giphy
load_type: auto

nanite_compat:
  min: 0.1.0

config:
  giphy_api_key:
    type: secret
    required: false
    env_var: GIPHY_API_KEY
    description: Giphy API key.
  giphy_rating:
    type: string
    required: false
    default: g
    description: Maximum content rating.

registers:
  envelopes:
    - type: giphy-modal
      component: GiphyModalCard
      version: 1
      schema: envelopes/giphy-modal.schema.json
  commands:
    - name: giphy
      description: Search Giphy for a GIF and render it inline.
      args:
        - name: query
          type: string
          required: true
          description: Search query.
  mcp_servers:
    - name: giphy
      description: Giphy GIF search tools.
      tools: [search]

ui:
  bundle_dir: ui/dist
  entry: index.js
  stylesheet: style.css
  react_version: ^18.0.0
  shadcn_version: ^1.0.0

release:
  platforms:
    - darwin-arm64
    - darwin-amd64
    - linux-amd64
    - linux-arm64
    - windows-amd64
```
