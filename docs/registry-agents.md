# Materialize Registry agents

`registryagent.Materialize` resolves a Registry agent and its authored references
into one ordinary Nanite folder. Open the result with `agent.Open`, or validate
it with `nanite validate`. Runtime turns never query Registry.

The initial adapter is a development filesystem fake, without authentication:

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
The fake chooses the last matching revision. The live Registry transport is a
separate adapter.

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
the existing development tool registry in the same directory.

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
detection, not a local edit repair check. `--force` regenerates from sources.
Changed inputs atomically replace the folder and retain the entire previous
folder at the returned `Backup` path, printed by the CLI. Backups are never
automatically deleted. Existing tool pins prevent a package version from
silently changing digest.

A folder containing runtime state requires explicit `--force`; stop its runtime
first, or choose a fresh destination. Replacement generates fresh runtime state
and leaves prior state in the backup. Force also permits replacing an unmanaged
folder, retaining it as a backup. Symlink destinations are always refused.
