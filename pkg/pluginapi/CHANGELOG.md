# Changelog

## v0.3.0

- Move to the grouped libs path: github.com/hollis-labs/libs/mcp/plugin-mcp v0.3.0 (import path swap only; package names and exported pluginapi signatures unchanged apart from the SDK type paths). Clean break from libs/plugin-mcp, no shim.

## v0.2.0

- Use the published libs/plugin-mcp v0.1.1 SDK in public manifest, capability and HTTP signatures.
- Update the shared-manifest fixture to protocol 2 bundled server and artifact inventory declarations.
- Preserve Nanite extension validation, scope narrowing, HTTP confinement and credential behavior.
