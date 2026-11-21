# Publishing and installing tool packages

Nanite has a draft tool-package registry client boundary and a filesystem fake
for development. It does not yet connect to the shared Registry service.
The Registry team owns the final kind and service contract; this document is
Nanite's proposal, not a change to `registry/contracts`.

## Development CLI

The publisher needs its Tether URN in the host environment variable
`TETHER_AGENT_URN` (for example `msg://agent/agent-mux/agt_example`) and a valid
`tools/<name>/tool.yaml` with a nonempty version. The opaque `agt_*` ID from that
URN determines the publisher's own namespace; display names and identity
principal strings are not used. No shared-namespace promotion command is provided.

```sh
nanite tools publish echo --dir ./publisher --registry-dir ./dev-registry
nanite tools install tool-package:agt_example/echo@1.0.0 --dir ./consumer --registry-dir ./dev-registry
```

`--registry-dir` is a **development-only filesystem fake with no authentication**.
It is explicitly configured, has no default location, and is not the shared
service. The two commands do not start a model or require a running agent's
state database. Do not put credentials or environment values in package files.

Version references are immutable records of kind `tool-package/1`.
The install writes `tools.lock.json`, mapping the requested ref to its digest and
metadata. Repeating it verifies the existing package, or restores a missing
package from the same pinned artifact. Changed registry digests, modified
installed content, name collisions, and untracked local packages are refused.
Updates require a deliberate owner-managed removal of the old package and its
lock entries; there is no automatic upgrade or force-overwrite option.

Installation never executes a package or writes an execution approval. On the
next turn the normal digest-bound approval path applies, or AutoApprove if the
host explicitly enabled it. MCP packages retain their startup approval checks.
The lock file records reproducibility, not trust: editing it does not grant
permission to execute different package contents.

## Embedding

Set `agent.Options.ToolRegistry` to a `toolregistry.Client` with record and blob adapters to
offer `tool_install` in the `BuiltinTools` group. Nil leaves it unavailable.
The builtin accepts only `ref`; the host chooses the adapter, and the current
agent directory determines the install destination. Its write operation goes
through normal tool authorization. A per-turn profile installs into that
profile's directory and remains subject to its environment/token restrictions.

```go
client := toolregistry.LocalClient("./dev-registry") // development only
opts := agent.Options{Dir: "./consumer", ToolRegistry: &client,
    Builtins: []agent.BuiltinGroup{agent.BuiltinTools}}
```

Records and bytes have separate interfaces. `Registry` has `Publish(ctx, Record)`
and `Resolve(ctx, ref)`; `BlobStore` has `Put(ctx, tarBytes)` and
`Fetch(ctx, artifactRef)`. `Client` combines them. Nanite uploads the tar before
publishing its record. Upload success followed by record failure can leave an
unreferenced CAS artifact. Adapters must honor cancellation and bound responses.

The agreed later adapter will create draft records through
`registry_record_create` on the `registry-write` upstream, resolve through
Registry, and upload/fetch through `caster_put`/`caster_get`. This PR does not
implement those transports. Own-namespace publication is currently a convention;
the local fake does not establish a security boundary or verify identity.

## Accepted tool-package kind boundary

| Field | Meaning |
| --- | --- |
| `id`, `kind` | `tool-package:<agt_id>/<name>@<version>`, `tool-package/1` |
| `name`, `version` | Manifest package name and immutable version |
| `digest` | `sha256:` plus Nanite's whole-package digest |
| `artifact` | Caster address `cas://sha256/<hex>` of the deterministic tar |
| `entry`, `execution` | Relative executable entry and `command` or `mcp` |
| `needs` | Audience, scopes, environment variable name; never a token |
| `env` | Allowed environment variable names; never their values |
| `provenance` | Publisher Tether URN and optional `source_ref` |

The package's `tool.yaml` is authoritative. Nanite checks record metadata against
it after unpacking. The fake records claimed provenance only; the real registry
must attest the authenticated publisher.

Artifacts are deterministic tar archives. Entries are sorted, mtime is Unix
zero, uid/gid are zero and uname/gname are empty; access/change times are unset.
Regular file bytes and permission bits are preserved; directory modes are fixed
at 0755. Relative paths must be unique and cannot escape the package. The
transport rejects symlinks, hardlinks and special files; copy such local package
content into regular files first. Limits are 4,096 entries, 64 MiB of file
content and 72 MiB of encoded tar data.

Install verifies the Caster SHA-256 over the exact downloaded tar bytes, then
independently verifies Nanite's whole-package digest over the extracted tree.

Digest calculation matches Nanite's existing approval digest: lexically walk the
package (including the root directory), hash directory paths and file paths,
lengths, permission bits and bytes. Directory permissions and timestamps do not
contribute. The executable is never run during verification. A private staging
directory is validated before exposing the complete package under `tools/`.
Install operations wait for a separate file lock, honoring context cancellation.
Once locked, an install removes abandoned `.nanite-install-*` staging directories.
It checks for an unexpectedly appeared package before recording the pin. The pin
is committed before exposing the staged package, so a lock-only interrupted
install can be retried without accepting a new digest.

## Deferred work

The real shared-service adapter and authentication await the live Registry kind
and Caster integration. Promotion to a shared namespace, package eval evidence and
Gatecrasher approval are a separate follow-up. This slice neither promotes
packages nor claims that local fake provenance is verified.
