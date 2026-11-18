# Folio and Substrate gaps

- Folio planned and rendered 21 scaffold files for `substrate-cli` with MCP disabled. It also wrote `.folio.yaml` metadata.
- The scaffold required manual manifest edits and regeneration to add the secrets, database, and policy modules.
- Folio placed the descriptor package under `internal/`, where its Substrate import conflicted with Nanite's engine boundary. Nanite moved it under `cmd/nanite/`.
- Substrate's policy module requires the app to register a PDP before startup.
- Folio's default XDG filename collided with legacy Nanite's incompatible `config.yaml`. Substrate's config module reads a file only from `SUBSTRATE_CONFIG` and provides neither XDG discovery nor an app-specific filename.
- The `v0.1.0` Substrate tag predates `boot.StartWith`. The scaffold uses the pinned pseudo-version for its post-config hook.

- The public scanner has no commit range or since option, so inherited history with unresolved findings prevents using its full history check as an app CI gate.

## libs

- The plugin-mcp client ties the legacy SSE stream to the discovery context. Nanite detaches the stream after the endpoint handshake so discovery cancellation leaves the registration open.
