# Boot Prompt: Plugin Standalone Cleanup, Updates, Reflexes, and Agent Import

You are working in Nanite at:

`/Users/chrispian/dev/hollis-labs/apps/nanite`

Related plugin repos live under:

`/Users/chrispian/dev/hollis-labs/plugins/`

## Goal

Resolve plugin ownership and lifecycle tension instead of patching around it.

Nanite should provide the plugin host, plugin registry, UI surfaces, streaming/envelope plumbing, and generic lifecycle management. Feature plugins like Giphy and oEmbed should own their feature logic, UI components, envelopes, tools, hooks, and plugin-specific behavior.

Also add a real plugin update path for the GUI/API, clean up stale grayed plugin entries in admin, and evaluate the remaining agentrc functionality as an import/sync path rather than a runtime source of truth.

## Current Findings

### Plugin Update Is CLI-Only Today

There is an update code path in the CLI:

- `cmd/nanite/plugin_cmd.go`
- `cmd/nanite/plugin_install_flow.go`
- `pluginUpdate(id)` reinstalls from the catalog, preserves the old install if staging fails, reports the version change, and triggers restart.

The GUI/API does not expose update:

- `internal/api/plugins.go` registers list, install, install-local, install-archive, uninstall, disable, enable, and reload. No update route.
- `internal/api/catalog.go` supports catalog browse, refresh, and install. No update route.
- `catalogInstall` rejects already-installed plugins, so update cannot be implemented by calling install.
- `ui/src/lib/api.ts` has install/uninstall/enable/disable helpers but no update helper.
- `ui/src/components/settings/PluginManager.tsx` computes `updateMap` and shows an "Update available" badge, but clicking only navigates to the Catalog tab.
- `ui/src/components/settings/CatalogBrowser.tsx` shows installed/update status but does not offer an update action.

Implement update as a first-class backend/API/UI path, ideally factoring shared install/update logic with the CLI rather than duplicating it.

### Grayed Plugin Rows In Admin

The plugin manager dims rows based on status:

- `active`: normal
- `disabled`: opacity 0.60
- `available`: opacity 0.45

See:

- `ui/src/components/settings/PluginManager.tsx`

Current running API showed:

- Active builtin/core includes `adapter-nanite-native`, `bookmarks-widget`, provider adapters, widgets, etc.
- `agentrc` and `bookmarks` appear as `available` core rows.
- `giphy` and `oembed` appear as `available` default rows.

The likely cause is stale repo/catalog aliasing:

- `plugins/repos.yaml` still lists `agentrc` with repo `nanite-plugin-agentrc`, type `core`.
- `plugins/repos.yaml` still lists `bookmarks`, type `core`.
- Actual active builtin IDs are `adapter-nanite-native` and `bookmarks-widget`.
- Catalog has `agentrc-sync`, not `agentrc`.

Clean this up so admin does not show stale unavailable core aliases as grayed rows. If core plugins cannot be installed from the GUI, do not represent unavailable core aliases as installable cards. Align names/IDs with real plugin IDs.

Also check version comparison. The catalog currently reports some builtin `1.0.0` plugins as having updates because catalog version is `0.1.0`. Use semver-aware comparison and do not treat lower catalog versions as updates.

## Giphy: Make It Truly Standalone

External plugin repo:

`/Users/chrispian/dev/hollis-labs/plugins/nanite-plugin-giphy`

The plugin already owns most behavior:

- `plugin.yaml` registers the `giphy-modal` envelope, `/giphy` command, MCP server/tool, config, and UI bundle.
- `main.go` handles slash command and MCP tool calls.
- `search.go` owns Giphy/demo search logic.
- `ui/src/components/GiphyModalCard.tsx` owns rendering.

Nanite core still has duplicated Giphy logic:

- `internal/mcp/self_tools.go` registers core `giphy_search`.
- `internal/mcp/self_tools.go` allowlists `giphy-modal` in `card_show`.
- `internal/mcp/self_tools_giphy.go` contains a core Giphy client, demo fallback, and structured error handling.
- Shared schema exists in `libs/go-envelopes/manifest/schemas/giphy-modal.schema.json`.

Required direction:

- Remove core Giphy search/tool/client functionality from Nanite.
- Giphy search must come from the plugin when installed.
- If the plugin is not installed, the Giphy tool should not exist.
- Decide whether `giphy-modal` remains a generic shared envelope schema or becomes fully plugin-owned. Prefer plugin ownership if the plugin system supports dynamic/plugin envelopes cleanly.
- If generic `card_show` remains, it must not fetch or know Giphy behavior. It can only render supplied envelope data.
- Add tests for install/load/unload behavior: installed plugin registers tool/envelope; unloaded plugin removes them; absent plugin means no Giphy tool.

## oEmbed: Keep Logic Out Of Core

External plugin repo:

`/Users/chrispian/dev/hollis-labs/plugins/nanite-plugin-oembed`

The plugin appears mostly self-contained:

- `plugin.yaml` registers `oembed-card`, UI bundle, config, and `message.sent` event hook.
- `main.go` scans messages for URLs, fetches metadata, and emits `oembed-card`.
- `fetch.go` owns oEmbed fetching.
- `providers.go` owns provider patterns/endpoints.
- `ui/src/components/OEmbedCard.tsx` owns rendering.

Nanite core references to `oembed-card` appear mostly limited to plugin/event/envelope tests. Audit this carefully.

Required direction:

- No provider-specific oEmbed logic should live in Nanite core.
- Core tests may verify generic plugin envelope delivery, but should not imply core ownership of oEmbed behavior.
- Installing oEmbed should make URL-rich-preview behavior work through the plugin event hook.
- Unloading/disabling oEmbed should stop that behavior.

## Plugin Reflex Dogfood

Use Giphy and possibly oEmbed to dogfood plugin-defined reflexes.

Current Nanite reflex pointers:

- `internal/agent/driftguard/types.go`
- `internal/agent/driftguard/seeds.go`
- `internal/agent/driftguard/engine_plugin_test.go`
- `internal/plugin/filter.go` contains `FilterReflexState` and `FilterReflexAction`

There is plugin hook/filter integration, but no obvious plugin manifest surface for declaring reflexes.

Required direction:

- Add a plugin-owned reflex declaration surface, likely under `registers.reflexes` in `plugin.yaml`, unless another existing schema is more appropriate.
- Reflex rows/config created from a plugin must be owned by that plugin ID.
- Disabling/uninstalling the plugin must remove or disable its reflexes.
- Giphy reflex examples could recognize intent like "gif", "reaction gif", "send a gif", or "celebration gif" and nudge/use `giphy.search` plus `giphy-modal`.
- Be conservative with oEmbed. Its event hook may be enough; avoid over-triggering reflexes for arbitrary URLs unless the UX is clearly better.

## Plugin Update API/UI Requirements

Implement an update path that works from the admin GUI and API.

Suggested backend shape:

- Add `POST /api/plugins/update` or `POST /api/plugins/catalog/update`.
- Request body should include plugin ID/name.
- Validate installed plugin exists and catalog entry exists.
- Refuse or clearly handle builtin/core update semantics.
- Use catalog download/signature/extract/staging flow.
- Preserve old install on failure.
- Hot reload if possible; otherwise emit lifecycle state requiring restart.
- Emit lifecycle/status events so GUI can update.

Suggested frontend shape:

- Add `updatePlugin` API helper.
- Installed tab should show an Update button when `update_available` is true.
- Catalog tab should show Update for installed plugins with newer catalog version, not only "Installed".
- Show clear disabled state for core/builtin plugins if updates are not supported there.
- Refresh managed and catalog state after update.

Do not rely on a badge-only flow.

## Agentrc: Salvage As Import/Sync, Not Runtime Truth

Nanite has moved away from agentrc-based agents toward launch tools and current Nanite agent profiles/config.

Relevant current code:

- `internal/plugin/builtin/adapter-nanite-native/plugin.go` reads `.nanite/config.yaml` and hydrates current agent profiles.
- `internal/agent/discovery.go` directly discovers `.nanite/agents`, `~/.nanite/agents`, and `plugins/*/agents`.
- `internal/service/install/install.go` explicitly rejects legacy `.agentrc/`.
- `.nanite/config.yaml` in this repo still has legacy naming drift: comments and `agentrc_version`.

Do not reintroduce `.agentrc` as a live runtime source of truth.

Worth salvaging:

- Import `.agentrc/agents/*.md` into current Nanite-managed agent config.
- Import useful role/skill/context metadata if present in old `.agentrc/config.yaml`.
- Provide dry-run, diff, conflict handling, and source annotations.
- Optionally support future import formats such as `.claude/agents/*.md`, `.nanite/agents/*.md`, and AGENTS.md-derived profiles.

Suggested product shape:

- Create or plan `plugin-agent-sync` / `plugin-agent-import`.
- It should discover legacy file-based agents and convert them into the current Nanite agent schema and file-backed managed profiles.
- It should write through the same validated management path as the GUI/API.
- It should record source path/type/import timestamp and avoid silent overwrites.
- It may offer one-shot import first; watch/sync can come later if still useful.

If keeping the install-time `.agentrc` rejection, update the error to point users to the import/sync path.

## Verification Checklist

Run targeted checks appropriate to the files changed:

- Go tests for plugin management/catalog/install/update paths.
- Go tests for plugin load/unload registration of tools, envelopes, hooks, and reflexes.
- Frontend typecheck/build/tests for Plugin Manager and Catalog Browser changes.
- Plugin repo tests/builds for Giphy and oEmbed if touched.
- Manual smoke:
  - Giphy unavailable before install.
  - Install Giphy, use `/giphy`, render `giphy-modal`.
  - Disable/uninstall Giphy, verify tool/envelope/reflex disappear.
  - Install oEmbed, send supported URL, render `oembed-card`.
  - Disable/uninstall oEmbed, verify behavior stops.
  - Admin shows no stale `agentrc`/`bookmarks` aliases.
  - Update button appears only when catalog semver is newer.

## Non-Goals

- Do not make `.agentrc` a new competing source of truth.
- Do not leave Nanite core with provider-specific Giphy/oEmbed behavior.
- Do not hide update functionality behind catalog badges only.
- Do not silently overwrite agent configs during import.
