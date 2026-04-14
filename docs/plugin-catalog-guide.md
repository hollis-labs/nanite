# Plugin Catalog Guide

How to submit a plugin to the official Nanite catalog at
`plugins.nanite.hollis-labs.dev`. For building and signing artifacts, see
[plugin-packaging-guide.md](./plugin-packaging-guide.md).

## 1. What the catalog is

The Nanite plugin catalog is a single signed file —
`catalog.yaml` + `catalog.yaml.sig` — served from
`plugins.nanite.hollis-labs.dev`. The Plugin Manager fetches it to list
installable plugins and to verify install metadata (archive URL, checksum,
per-plugin signing key).

**Today the catalog is hand-authored.** Entries are added by the catalog
maintainers via PRs against the catalog repo. This is a transitional
state flagged in BLG-20260414-006: the long-term plan is an automated
submission pipeline with author-side schema validation and maintainer-side
signature approval. Until that lands, expect manual review turnaround and
lean on the maintainers process described below.

## 2. Entry shape

Each plugin occupies one entry in `catalog.yaml`. The shape:

```yaml
plugins:
  - id: my-plugin
    name: My Plugin
    version: 0.2.1
    short_desc: One-line description for listings.
    description: |
      Full description, rendered as markdown on the plugin details page.
    author: Your Name or Org
    license: Apache-2.0
    homepage: https://github.com/you/nanite-plugin-my-plugin
    repository: https://github.com/you/nanite-plugin-my-plugin
    public_key: <base64 ed25519 public key>
    platforms:
      - os_arch: darwin-arm64
        archive_url: https://archives.nanite.hollis-labs.dev/plugins/my-plugin/0.2.1/my-plugin-0.2.1-darwin-arm64.tar.gz
        sha256: <hex>
        signature_url: https://archives.nanite.hollis-labs.dev/plugins/my-plugin/0.2.1/my-plugin-0.2.1-darwin-arm64.tar.gz.sig
      - os_arch: darwin-amd64
        archive_url: ...
        sha256: ...
        signature_url: ...
      - os_arch: linux-amd64
        ...
      - os_arch: linux-arm64
        ...
      - os_arch: windows-amd64
        ...
    nanite_compat:
      min: 0.1.0
      max: 1.0.0
```

Required fields:

| Field | Notes |
|---|---|
| `id` | Must match `plugin.yaml id`. |
| `name` | Display name. |
| `version` | Must match `plugin.yaml version` and the tarball basename. |
| `description` | Markdown-safe. |
| `author`, `license`, `homepage`, `repository` | Strings. |
| `public_key` | Base64-encoded ed25519 public key. Must equal `dev-signing-key.pub` inside every platform tarball. |
| `platforms[]` | One entry per OS/arch combo. Each carries `archive_url`, `sha256`, `signature_url`. |
| `nanite_compat` | Same shape as `plugin.yaml nanite_compat`. |

Optional fields: `short_desc`, `tags`, `screenshots`, `category`.

## 3. Submission process

The catalog repo is private-by-default and maintained by Hollis Labs.
Submission is through the catalog maintainers:

1. **Open a GitHub issue** on your plugin's own repo with the title
   `catalog submission: <plugin-id>@<version>` and the proposed entry
   pasted into the description. The maintainers monitor this channel.
2. **Attach the GitHub release URL.** The release must carry the signed
   archives, `.sha256` files, `.sig` files, and `dev-signing-key.pub`.
3. **Maintainer review.** A maintainer validates the entry, checks
   signatures, verifies checksums match the archives, and downloads each
   archive to confirm `dev-signing-key.pub` inside equals
   `public_key` in the entry.
4. **Catalog sign + publish.** On approval, the maintainer opens a PR
   against the catalog repo with your entry, signs the updated
   `catalog.yaml` with the catalog maintainer key, and deploys to
   `plugins.nanite.hollis-labs.dev`. The Plugin Manager picks up the new
   entry on its next refresh.
5. **Install verification.** You (and anyone) should install the plugin
   from the catalog on a clean machine to confirm end-to-end operation
   before announcing the release.

If a submission pipeline or author-side upload flow is live by the time
you read this, it supersedes step 1 — check the catalog repo README for
the current submission URL.

## 4. Catalog signature vs plugin signature

These are two independent trust anchors and the install flow verifies
both:

| Signature | Signer | Covers | Verified when |
|---|---|---|---|
| Catalog signature | Catalog maintainer key | `catalog.yaml` (whole file) | Plugin Manager fetches the catalog. |
| Per-plugin signature | Plugin author key | Each platform tarball | `nanite plugin install` verifies the archive. |

The per-plugin signature proves the archive matches what the author
published. The catalog signature proves the maintainers reviewed and
approved the entry. In production mode, both are required.

## 5. Publishing a new version

The minimum flow:

1. Tag + push a new version in your plugin repo; CI produces signed
   archives (see [plugin-packaging-guide.md](./plugin-packaging-guide.md)).
2. Upload the new archives to the archives CDN (or verify GitHub Actions
   has done so).
3. Update your catalog entry:
   - Bump `version`.
   - Update every platform's `archive_url`, `sha256`, `signature_url`.
   - Leave `public_key` unchanged unless you're rotating keys.
4. Submit as per §3 — either through the issue channel or whatever the
   current submission UI dictates.

The catalog keeps a single entry per plugin id, pointing at the latest
version. Older versions remain installable via their direct archive URL
but are not discoverable through the Plugin Manager listing.

## 6. Key rotation

To rotate your per-plugin signing key:

1. Generate a new keypair.
2. Ship a transition version signed by **both** keys (author-managed
   workflow — the maintainers may ask for evidence of continuity, e.g.
   a signature from the old key over the new public key).
3. Update the catalog entry's `public_key` to the new value in the same
   PR as the transition version.
4. Subsequent versions sign with the new key only.

## 7. Deprecation and removal

To mark a plugin deprecated (e.g. superseded, archived):

- Update the entry with `deprecated: true` and optionally
  `deprecation_notice: "..."`. The Plugin Manager grays out the listing
  and surfaces the notice but leaves existing installs untouched.
- To replace with another plugin, add `deprecated_by: <other-plugin-id>`
  so the UI can link users to the successor.

To remove a plugin entirely (e.g. abandoned, license change, security
issue):

- File an issue explaining the reason.
- Maintainers remove the entry from `catalog.yaml` and re-sign the
  catalog.
- Existing installs remain functional (the host doesn't phone home for
  catalog verification after install), but the plugin no longer appears
  in the Manager and can't be updated through it.
- For security issues, maintainers can additionally blocklist the public
  key so re-publishing under the same identity is rejected.

## 8. Tips

- Keep your plugin repo public and the release history public so
  maintainers can audit without NDAs.
- Tag the release with a plain SemVer tag (`v0.2.1`, not `2026-04-14.1`).
- Don't re-upload a tarball under the same name with different bytes —
  the catalog's `sha256` is binding. If you need to re-release, bump the
  patch version.
- If your plugin ships more than one platform, list all of them in the
  catalog entry. Platforms not listed are not installable from the UI.
