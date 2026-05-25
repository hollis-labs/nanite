# Boot Prompt: Shared Plugin Registry and Directory Metadata Model

You are working in Nanite at:

`/Users/chrispian/dev/hollis-labs/apps/nanite`

Related plugin repos live under:

`/Users/chrispian/dev/hollis-labs/plugins/`

## Goal

Evolve Nanite's plugin manifest, catalog, scaffold, and admin/catalog UI into a structured shared plugin registry model that can serve all Hollis Labs apps, not just Nanite.

The registry should help both users and agents reason about plugins:

- Which app(s) a plugin supports.
- What kind of plugin it is: adapter, provider, widget, envelope/card, automation, agent tooling, import/sync, content/media, observability, etc.
- What capabilities it contributes.
- Whether it is app-specific or reusable across multiple apps.
- Who authored/maintains it.
- Where to find the plugin page, docs, repo, issue tracker, license, screenshots, icon, and release assets.

This should also be suitable as the data model for a future public/private plugin directory website.

## Current State

The current manifest schema is:

- `internal/plugin/schemas/plugin.schema.v1.json`
- Parsed by `internal/plugin/config.go` as `PluginManifest`.
- Documented in `docs/plugin-yaml-reference.md`.

The current catalog type is:

- `internal/plugin/catalog.go`

Current catalog docs:

- `docs/plugin-catalog-guide.md`

Current scaffold:

- `cmd/nanite/plugin_cmd.go` (`nanite plugin new`)
- `internal/plugin/scaffold/scaffold.go`
- `internal/plugin/scaffold/templates/subprocess/plugin.yaml.tmpl`
- `internal/plugin/scaffold/templates/builtin/plugin.yaml.tmpl`

Current plugin repo list:

- `plugins/repos.yaml`

Current plugin admin/catalog UI:

- `ui/src/components/settings/PluginManager.tsx`
- `ui/src/components/settings/CatalogBrowser.tsx`
- `ui/src/lib/api.ts`

External plugin examples:

- `/Users/chrispian/dev/hollis-labs/plugins/nanite-plugin-giphy/plugin.yaml`
- `/Users/chrispian/dev/hollis-labs/plugins/nanite-plugin-oembed/plugin.yaml`
- `/Users/chrispian/dev/hollis-labs/plugins/nanite-plugin-agent-mux/plugin.yaml`

## Important Current Gaps

The current schema has a few directory-facing fields, but the model is too flat:

- `plugin.yaml` supports `description`, `short_desc`, `author`, `url`, `homepage`, `repository`, `license`.
- The catalog guide mentions optional `tags`, `screenshots`, and `category`.
- `internal/plugin/catalog.go` only has `Name`, `Version`, `Description`, `Author`, `Repo`, archive/signature fields, compat, runtime, and tags.
- `plugins/repos.yaml` has a local `type` field (`core`, `default`) but this is not a real semantic registry model.
- The scaffold does not generate default icon/screenshot paths, website metadata, support URLs, categories, capabilities, or multi-app compatibility.

## Desired Model

Design this as a versioned manifest/catalog schema evolution, preserving backward compatibility where practical.

Prefer additive schema fields first. Do not break existing plugin installs unless a deliberate schema v2 migration is necessary.

### Identity and Directory Metadata

Support WordPress-style / marketplace-style fields:

- `id`: canonical stable ID.
- `name`: display name.
- `slug`: optional directory slug if different from ID.
- `short_desc`: one-line listing description.
- `description`: full markdown-safe description.
- `version`.
- `license`.
- `author`: preserve existing string compatibility, but consider adding structured author metadata.
- `authors[]`: optional structured authors/maintainers.
- `maintainers[]`: optional separate maintainers.
- `homepage`.
- `repository`.
- `plugin_url` or `website`.
- `docs_url`.
- `support_url`.
- `issues_url`.
- `changelog_url`.
- `funding_url`.
- `privacy_url`.
- `terms_url`.

Suggested structured author shape:

```yaml
authors:
  - name: Hollis Labs
    email: plugins@hollislabs.dev
    url: https://hollislabs.dev
    github: hollis-labs
```

Keep the existing top-level `author` string for compatibility and display fallback.

### Apps and Compatibility

Make it clear which apps can use a plugin.

Suggested fields:

```yaml
apps:
  - id: nanite
    compat:
      min: 0.1.0
      max: 1.0.0
  - id: tether
    compat:
      min: 0.1.0
```

Consider retaining `nanite_compat` as a Nanite-specific compatibility alias for schema v1 compatibility, but introduce a generalized `apps[]` model for the shared registry.

Questions to answer in the implementation:

- Should `apps[]` live only in the catalog, or also in plugin.yaml?
- Should plugin.yaml declare supported apps and catalog only index them?
- How does Nanite validate app compatibility when both `nanite_compat` and `apps[]` exist?

Recommendation: `plugin.yaml` should be authoritative for what the plugin says it supports. Catalog can mirror/index those fields and add registry-only moderation metadata.

### Kind, Category, Capabilities, and Tags

Separate human browsing from machine reasoning.

Suggested model:

```yaml
kind: adapter
kinds:
  - adapter
  - provider
categories:
  - agents
  - developer-tools
capabilities:
  - cli-adapter
  - agent-launch
  - mcp-server
  - chat-widget
tags:
  - claude
  - cli
  - harness
```

Notes:

- Some plugins are scoped and should have one kind.
- Some plugins legitimately provide multiple things.
- `kind` can be a primary/default kind for simple UI grouping.
- `kinds[]` can represent multi-role plugins.
- `categories[]` should be user-facing directory buckets.
- `capabilities[]` should be machine-readable and tied to what the plugin contributes.
- `tags[]` stay freeform-ish for search/discovery.

Suggested controlled vocabularies to start with:

- Kinds: `adapter`, `provider`, `widget`, `envelope`, `tool`, `mcp-server`, `agent-profile`, `reflex`, `workflow`, `automation`, `importer`, `theme`, `integration`, `demo`, `developer-tool`.
- Categories: `agents`, `providers`, `adapters`, `widgets`, `media`, `knowledge`, `productivity`, `observability`, `developer-tools`, `admin`, `import-sync`, `content`, `communication`, `data`.
- App scope: `nanite`, `tether`, `torque`, `agridd`, `shared`, or explicit app IDs.

Do not hardcode these only in the frontend. Put the canonical vocabulary somewhere agents, backend, docs, and the website can read.

### Contributions

The existing `registers` block is valuable. Use it to infer capabilities where possible.

Examples:

- `registers.mcp_servers` implies `mcp-server`.
- `registers.envelopes` implies `envelope`.
- `registers.slots` / `registers.components` implies `widget` or `ui-component`.
- `registers.agent_profiles` implies `agent-profile`.
- Future `registers.reflexes` implies `reflex`.
- Provider/adapter builtins should declare provider/adapter kind explicitly.

Do not rely solely on inference. Let plugin authors declare `kind`, `kinds`, and `capabilities`, then validate/infer warnings when declarations contradict `registers`.

### Media Assets

The scaffold should include directory-ready assets:

- Default icon.
- Optional screenshots directory.
- Optional preview image/cover/banner.

Suggested manifest shape:

```yaml
media:
  icon: assets/icon.svg
  banner: assets/banner.png
  screenshots:
    - path: assets/screenshots/example.png
      alt: Example screenshot
      caption: Example plugin UI
```

Scaffold requirements:

- Generate `assets/icon.svg` by default.
- Generate `assets/screenshots/.gitkeep` or README placeholder.
- Add media references to generated `plugin.yaml`.
- Ensure install validation verifies referenced local asset paths exist and cannot escape plugin root.
- Ensure catalog can carry resolved asset URLs for website use.

### Registry Website Readiness

Add enough fields so a registry site can render a high-quality plugin directory without scraping GitHub:

- Listing cards: icon, name, short description, author, categories, app support, kind, tags, install status.
- Detail page: full markdown description, screenshots, links, supported apps, compatibility, permissions/requirements, release info.
- Search/filter: app, kind, category, capability, tag, author, runtime, install state, update availability.
- Trust/security: signing key, publisher identity, verified status, last reviewed date if catalog-owned.

Catalog-only moderation fields can exist outside plugin.yaml:

```yaml
registry:
  verified: true
  featured: false
  visibility: public
  review_status: approved
  reviewed_at: 2026-05-25T00:00:00Z
```

Keep a clear boundary:

- `plugin.yaml`: author-declared metadata and runtime registrations.
- Signed catalog: distribution, trust, moderation, mirrored searchable fields, archive platform data.
- Registry website: renders catalog data and may add analytics/download counts later.

## Implementation Direction

### Backend / Schema

Update:

- `internal/plugin/schemas/plugin.schema.v1.json`
- `internal/plugin/config.go`
- `internal/plugin/config_manifest_v1_test.go`
- `internal/plugin/install/validate.go`
- `internal/plugin/catalog.go`
- `internal/store/catalog.go` if persisted catalog rows need new fields.

Add typed structs for:

- Structured author/maintainer info.
- App compatibility.
- Media assets.
- Directory metadata.
- Registry/catalog moderation metadata, if catalog-owned.

Use JSON/YAML tags consistently.

Keep unknown additional properties behavior deliberate. The schema currently allows additional top-level properties. Decide if this remains for forward compatibility or if new structured fields should be strict internally.

### Catalog

Update catalog entry model so it can represent:

- `id` as canonical identity, not only `name`.
- Display `name`.
- `short_desc`, full `description`.
- Structured authors/maintainers.
- Links.
- `kind`, `kinds`, `categories`, `capabilities`, `tags`.
- `apps[]` compatibility.
- `media`.
- Platform archive/signature metadata.
- Registry moderation metadata.

Make sure `docs/plugin-catalog-guide.md` reflects the real typed shape. Today the docs and Go catalog type do not fully match.

### UI

Update plugin manager/catalog surfaces to expose and use the new structure:

- Group/filter by app support, kind, category, capability, and tag.
- Show icons on cards.
- Show richer detail page fields and links.
- Show screenshots when present.
- Avoid hardcoding only Nanite-specific assumptions.

Relevant files:

- `ui/src/components/settings/PluginManager.tsx`
- `ui/src/components/settings/CatalogBrowser.tsx`
- `ui/src/lib/api.ts`

### Scaffold

Update:

- `cmd/nanite/plugin_cmd.go`
- `internal/plugin/scaffold/scaffold.go`
- `internal/plugin/scaffold/templates/subprocess/plugin.yaml.tmpl`
- `internal/plugin/scaffold/templates/builtin/plugin.yaml.tmpl`
- scaffold tests

Generated subprocess plugins should include:

- `short_desc`.
- `kind` / `kinds`.
- `categories`.
- `capabilities`.
- `tags`.
- `apps`.
- `media.icon`.
- `assets/icon.svg`.
- `assets/screenshots/README.md` or `.gitkeep`.
- Useful URL placeholders based on module path when possible.

Add CLI flags only if useful, but do not block on perfect CLI UX. Good defaults are more important:

- `--kind`
- `--category`
- `--tag`
- `--homepage`
- `--repository`

### Plugin Repos

Update sample/real plugin manifests as part of the migration if in scope:

- Giphy: media/content plugin, Nanite app support, envelope/tool/MCP capability, icon/screenshot placeholders.
- oEmbed: media/content/plugin-envelope/event-hook capabilities.
- Agent Mux: agent/developer-tool/provider/adapter capabilities depending on actual behavior.

External paths:

- `/Users/chrispian/dev/hollis-labs/plugins/nanite-plugin-giphy`
- `/Users/chrispian/dev/hollis-labs/plugins/nanite-plugin-oembed`
- `/Users/chrispian/dev/hollis-labs/plugins/nanite-plugin-agent-mux`

Only edit these if the task explicitly includes cross-repo migration. If not, produce a migration guide and example patches.

## Design Constraints

- Do not create a second competing plugin registry format.
- Do not make `plugins/repos.yaml` the semantic source of truth. It can remain a local/bootstrap list, but catalog/manifest should carry semantic directory metadata.
- Do not put all categorization only in frontend code.
- Preserve existing plugin installs when possible.
- Keep plugin.yaml author-declared and catalog maintainer-verified.
- Use versioned migration notes if schema changes are non-trivial.

## Suggested Acceptance Criteria

- Plugin manifest parser round-trips new metadata fields.
- JSON schema validates new fields.
- Catalog fetch/merge/API includes the new metadata.
- Plugin Manager/Catalog Browser can group/filter by at least app, kind, category, and tag.
- Scaffold generates a plugin with default icon and directory-ready metadata.
- Docs show the new canonical plugin.yaml and catalog examples.
- Existing plugins continue to load.
- Tests cover schema roundtrip, scaffold output, catalog merge, and frontend rendering of representative metadata.

## Questions To Resolve In The Design

- Is this still `schema_version: 1` with additive fields, or should this become `schema_version: 2`?
- Should `author` remain a string forever with `authors[]` as the richer form, or should v2 make author structured?
- Should `kind` be singular plus `kinds[]`, or only `kinds[]` with a `primary_kind`?
- Should app compatibility live under `apps[]` only, or should app-specific legacy fields like `nanite_compat` remain accepted indefinitely?
- Which metadata is author-declared in plugin.yaml versus catalog-maintainer-owned in signed catalog entries?
