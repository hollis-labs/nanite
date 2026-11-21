# Materialize Registry agents

`registryagent.Materialize` resolves a Registry agent and its authored references
into one ordinary Nanite folder. Open the result with `agent.Open`, or validate
it with `nanite validate`. Runtime turns never query Registry.

For live reads, configure a Registry HTTP API base URL and a reader credential
file. Endpoints and credential values are never embedded in agent folders:

```sh
nanite agents materialize agent:assistant ./assistant \
  --registry-url "$NANITE_REGISTRY_URL" \
  --registry-token-file "$NANITE_REGISTRY_TOKEN_FILE" \
  --publication draft \
  --caster-executable "$NANITE_CASTER_EXECUTABLE"
```

The flags also accept defaults from `NANITE_REGISTRY_URL`,
`NANITE_REGISTRY_TOKEN_FILE`, `NANITE_REGISTRY_PUBLICATION`, and
`NANITE_CASTER_EXECUTABLE`. Flags override environment values. Published is the
default; draft reads require explicit selection and never happen as a fallback.
Each live source records `publication` in `.nanite/materialized.json`.
The credential file contains only a bearer token and is read lazily. Protect it
with host filesystem permissions. HTTP redirects are refused, requests have a
30-second deadline, and record envelopes are bounded to 2 MiB.

The adapter uses Registry's `GET /v1/record` and `GET /v1/revision` APIs. Native
IDs remain opaque query values. References accept Registry canonical IDs
(`skill:name`, `launch_profile:name`) and the development fake's unprefixed IDs;
payload IDs remain unprefixed. Pinned revisions are checked exactly. Registry
revision history contains authored drafts; in published mode a pinned revision
must also match the current published record. Older published revisions cannot
be proven by this API and are refused rather than treated as published.

Tool-package bytes come from an explicitly configured absolute path to an
already paired Caster CLI, invoked only as `get <cas://sha256/...>`. Caster owns
its own credential resolution. Output is bounded to 72 MiB and 30 seconds,
checked against its content address, and passed through Nanite's existing
package digest, archive and pin verification. Package needs with multiple env
bindings expand to Nanite's individual bindings; needs without an env binding
are refused. Nanite's existing package ID/version restrictions still apply.
Neither adapter publishes records or artifacts, and installation grants no tool
execution approval.

For development, use the filesystem fake without authentication:

```sh
nanite agents materialize agent:assistant ./assistant --registry-dir ./registry
nanite validate ./assistant
```

The destination's parent must already exist. `--authority` defaults to
`registry`. The fake's `records.json` is an array of envelopes:

```json
[
  {
    "reference": {"authority": "registry", "kind": "agent", "id": "assistant"},
    "revision": "r1",
    "payload": {
      "schema": "agent/1", "id": "assistant", "name": "assistant",
      "summary": "Help with local work.", "charter": "Answer clearly.",
      "roles": ["assistant"],
      "launch_profile": {
        "reference": {"authority": "registry", "kind": "launch_profile", "id": "local"},
        "revision": "r1"
      }
    }
  },
  {
    "reference": {"authority": "registry", "kind": "launch_profile", "id": "local"},
    "revision": "r1",
    "payload": {
      "schema": "launch_profile/1", "id": "local", "name": "local",
      "source": {
        "reference": {"authority": "tether", "kind": "launch_profile", "id": "local"},
        "revision": "r1"
      },
      "launcher": "nanite", "host": "nanite-cli",
      "runtime": {"provider": "openai", "model": "your-model"}
    }
  }
]
```

For an embedding host, implement `registryagent.Resolver.Get` and call
`Materialize(ctx, resolver, reference, dir, options)`. Resolvers return the exact
requested identity and revision; an empty revision requests the current record.
The fake chooses the last matching revision. Hosts can use
`registryagent.NewLive(LiveConfig{URL: endpoint, Authority: "registry", ...})`,
provide a credential callback and optional HTTP client, and use
`live.ToolClient(blobStore)` with `registryagent.CasterCommand` or their own
Caster `toolregistry.BlobStore`. The authority names the configured Registry
resolver; a record's separate source-of-truth authority is not substituted for
it. Use one publication channel for a materialization. Local and live CLI
transport options are mutually exclusive.

## Generated files

| Input | Output |
| --- | --- |
| Agent name, summary, revision and charter | Schema 2 `agent.md`, durable continuity, default permissions |
| Launch profile host and runtime | `host.yaml` with runtime settings flattened |
| Skill name, summary and instructions | `skills/<name>/SKILL.md` |
| Context document body | `context/<name>.md` |
| Variant document body, including its authored frontmatter | `variants/<name>.md` |
| Tool package references | Packages installed by `toolregistry.Install`, with `tools.lock.json` pins |
| Source identities, revisions, digests, roles and ordinary tool references | `.nanite/materialized.json` |

A launch profile is required. `surface-module:*` and `nanite-service` hosts map
to embedded/application hosting; `nanite-cli` maps to stable/on-demand hosting.
The application or seat identity is the agent name. Credential references and
host permissions remain host configuration; Registry cannot supply credential
values or grant tool execution. Ordinary tool references are recorded, not
installed or executed. Supply `Options.Tools` for tool packages; the CLI uses
the selected Registry adapter and Caster reader (or the development tool
registry in the same local directory).

Records must satisfy the vendored Registry schemas. Each payload is limited to
1 MiB, JSON nesting to 32 levels, and total authored bodies to 8 MiB. File names
must be lowercase slugs of at most 64 characters. Missing records, conflicting
names, duplicate JSON keys, invalid authored variants and recognizable secret
literals fail before publication.

## Replacement and provenance

Materialization stages a sibling folder and validates it before publication.
Linux and macOS support atomic publication and directory exchange. Other
platforms return an unsupported-publication error. Concurrent materializers
for the same destination serialize using a sibling lock file.

An existing folder must have a matching materialization manifest. Unchanged
source revisions and digests return `Changed: false`; this is source staleness
detection, not a local edit repair check. Unchanged inputs return unchanged even
with `--force` and even while the runtime is open.
Changed inputs atomically replace the folder and retain the entire previous
folder at the returned `Backup` path, printed by the CLI. Backups are never
automatically deleted. Existing tool pins prevent a package version from
silently changing digest.

Updates acquire a stable sibling publication lock shared with `agent.Open` and
the existing `.nanite/agent.lock`. An open runtime blocks replacement, including
with `--force`. Close it and retry. The complete `.nanite` tree is copied into
the staged folder while locked, except for the regenerated materialization
manifest. Conversations, transcripts, host state and other runtime files survive
definition updates. Links and special files in runtime state are refused.
Explicit external `DataDir` state stays in place; its open runtime still blocks
publication through the shared directory lock.

`--force` only permits replacing a folder without a matching materialization
manifest; it never bypasses locks or the unchanged check. Symlink destinations
are always refused. Backups use `.nanite-backup-*`; abandoned stages for the same
destination use `.nanite-stage-<name>-*` and are cleaned on the next attempt.
Materializers need write access to the agent folder's parent. Runtime opening
skips shared publication locking if a read-only parent prevents creating or
opening that lock, as it cannot be replaced there by an ordinary materializer.
