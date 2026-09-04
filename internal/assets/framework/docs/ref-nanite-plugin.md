# Build a Nanite plugin

This is the embedded quick reference for Nanite's plugin host. The repository's `docs/plugin-authoring-guide.md`, `docs/plugin-yaml-reference.md`, `docs/plugin-sdk-reference.md`, and `docs/plugin-catalog-guide.md` are authoritative.

## Choose a plugin kind

- Use `nanite plugin new --subprocess <name>` for the public subprocess MCP contract. This is the normal choice for independently shipped plugins.
- Use `nanite plugin new --builtin <name>` only for a plugin compiled into this Nanite repository.
- Run `nanite plugin new --help` for the current scaffold flags.

Do not copy an old in-process plugin template into a subprocess plugin. Their lifecycle and dependency boundaries differ.

## Subprocess contract

A distributable plugin uses manifest v1 and speaks MCP over stdio. Keep the YAML manifest authoritative, use the published `plugin-sdk` wire types, and send protocol output only on stdout. Diagnostics belong on stderr.

Install and lifecycle operations flow through Nanite's plugin state machine. Production builds verify catalog and per-plugin Ed25519 signatures; only a binary built with the `devmode` tag can bypass verification. Never distribute a `devmode` build.

Useful commands:

```text
nanite plugin new --subprocess example
nanite plugin install <catalog-id>
nanite plugin list
nanite plugin enable <id>
nanite plugin disable <id>
```

Follow generated scaffold names and schemas rather than inventing an older manifest shape. Use the catalog guide for artifact checksums, signatures, versions, and availability metadata.

## Built-in contract

Built-ins implement the host interfaces under `internal/plugin/`, register under `internal/plugin/builtin/<name>/`, and are included by `internal/plugin/allplugins/`. Scaffold with:

```text
nanite plugin new --builtin example
```

Current built-in imports use `github.com/hollis-labs/nanite/internal/plugin`; persistence integrations use `github.com/hollis-labs/nanite/internal/store`. Host events identify their source as `nanite`.

## Tools and Tesseract

The current memory/knowledge MCP server is `tesseract`; its Claude-style prefix is `mcp__tesseract__`. If an agent profile needs Tesseract, scope its allowlist to the smallest current verbs it needs. Do not use a broad retired-server wildcard.

Typical current calls include:

```text
mcp__tesseract__tesseract_recall
mcp__tesseract__tesseract_get
mcp__tesseract__tesseract_history
mcp__tesseract__tesseract_get_revision
mcp__tesseract__tesseract_touch
mcp__tesseract__memory_write
mcp__tesseract__knowledge_write
```

See `docs/tesseract-v0.9-contract.md` for required arguments, JSON-encoded array fields, projection, pagination, and XDG migration details.

## Envelopes

Agents emit an envelope as a fenced JSON block named `nanite-envelope`:

````markdown
```nanite-envelope
{"kind":"content","version":1,"type":"info-card","data":{"title":"Hello","body":"A released core envelope."}}
```
````

`kind` and `version` are required; the current wire version is `1`. Core types and schemas are owned by the pinned [`github.com/hollis-labs/go-envelopes`](https://github.com/hollis-labs/go-envelopes) module in `manifest/envelopes.yaml`, `manifest/envelopes.schema.json`, and `manifest/schemas/`. Plugin types remain declared in each plugin's `plugin.yaml`; Nanite loads both sources into its shared registry.

For a new core type, update and release go-envelopes first, then bump Nanite's module pin. Check out that same released tag at `../../libs/go-envelopes`, because the frontend generators read the sibling source tree rather than the Go module cache. Run `make generate-envelopes` so `scripts/generate-envelope-types.mjs` consumes its module-owned schemas. Add the matching renderer under `ui/src/components/chat/envelopes/`, recording the normal `component`, `export`, and `props` mapping in the upstream manifest. Use `CORE_OVERRIDES` in `scripts/generate-plugin-imports.mjs` only for an intentional Nanite-only deviation. Run `npm run generate:plugins` from `ui/`; that generator consumes the sibling `manifest/envelopes.yaml` and owns `ui/src/generated/plugin-envelopes.ts`. Never hand-edit generated registries. Verify both the streaming SSE path and persisted reload path.

## Verification

For a built-in change run the focused plugin tests followed by `./scripts/check.sh`. For a subprocess plugin, test the manifest, stdio MCP handshake, clean shutdown, install/upgrade/rollback state machine, and signed-catalog path. Validate the production build without `devmode`.
