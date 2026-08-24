# Plugin Packaging Guide

How to build, sign, and ship a subprocess plugin. For the authoring
walkthrough see [plugin-authoring-guide.md](./plugin-authoring-guide.md);
for catalog submission see [plugin-catalog-guide.md](./plugin-catalog-guide.md).

## 1. Release artifact layout

Each release publishes one tarball **per platform**:

```
my-plugin-0.1.0-darwin-arm64.tar.gz
my-plugin-0.1.0-darwin-arm64.tar.gz.sha256
my-plugin-0.1.0-darwin-arm64.tar.gz.sig        # optional in dev; required in prod
```

Inside the tarball:

```
./
├── plugin.yaml
├── my-plugin                   # the subprocess binary (or my-plugin.exe on windows)
├── dev-signing-key.pub         # the plugin's author-ed25519 public key
├── envelopes/                  # envelope JSON Schemas referenced in plugin.yaml
├── ui/
│   ├── index.js                # ESM bundle (omit if plugin has no UI)
│   └── style.css               # optional stylesheet
├── README.md
├── LICENSE
└── CHANGELOG.md
```

The host expects the binary to match `entrypoint` in `plugin.yaml`
(`./my-plugin` by convention). On Windows, the entrypoint resolves to
`my-plugin.exe` automatically.

## 2. Cross-platform builds

Ship all six target platforms for broad coverage. The scaffold Makefile
builds them in a single `make release`:

| GOOS | GOARCH | Archive suffix |
|---|---|---|
| `darwin` | `arm64` | `darwin-arm64` |
| `darwin` | `amd64` | `darwin-amd64` |
| `linux` | `amd64` | `linux-amd64` |
| `linux` | `arm64` | `linux-arm64` |
| `windows` | `amd64` | `windows-amd64` |
| `windows` | `arm64` | `windows-arm64` *(optional, add when your deps support it)* |

Key flags in the giphy Makefile:

```makefile
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(STAGING_DIR)/$$plat/$$bin .
```

- `CGO_ENABLED=0` keeps the binary statically linked and portable.
- `-trimpath` strips local filesystem paths from the binary.
- `-ldflags="-s -w"` strips debug symbols to shrink the archive.

Omit `CGO_ENABLED=0` only if your plugin genuinely needs cgo — in that
case you must also cross-compile toolchains for every target.

## 3. UI build

Before packaging, build the UI bundle:

```bash
cd ui && npm install --no-audit --no-fund --silent && npm run build
```

The output is `ui/dist/index.js` and (optionally) `ui/dist/style.css`. The
scaffold's Vite config externalizes React and the eleven `@nanite/ui/*`
primitives — your bundle must not include them. If `npm run build`
produces a bundle containing React or any `@nanite/ui/*` symbol, check
the Vite `rollupOptions.external` list.

## 4. SHA-256 checksums

Every archive ships a matching `.sha256`:

```bash
sha256sum my-plugin-0.1.0-darwin-arm64.tar.gz | awk '{print $1}' \
  > my-plugin-0.1.0-darwin-arm64.tar.gz.sha256
# or, on macOS:
shasum -a 256 my-plugin-0.1.0-darwin-arm64.tar.gz | awk '{print $1}' \
  > my-plugin-0.1.0-darwin-arm64.tar.gz.sha256
```

The scaffold Makefile does this automatically.

## 5. Signing with ed25519

The trust model uses two independent ed25519 signatures:

- **Per-plugin signature**, made by the plugin author. Included as
  `<archive>.sig` next to the tarball.
- **Catalog signature**, made by the catalog maintainers over the catalog
  entry. Handled separately at catalog-submission time (see
  [plugin-catalog-guide.md](./plugin-catalog-guide.md)).

Production installs require both. Developer mode downgrades both to
warnings but still surfaces the trust tier in the UI:

| Tier | Icon | Meaning |
|---|---|---|
| Signed | `✓` | Both catalog sig and per-plugin sig verified. |
| Unsigned | `⚠️` | Either signature missing or invalid; only installable in developer mode or with "allow unsigned plugins" enabled. |
| Untrusted | `🚫` | Signature present but does not match the declared public key or catalog entry. Never installable. |

### 5.1 Key generation

Generate an ed25519 keypair once per plugin (or once per author). The
scaffold's `cmd/sign` tool generates and manages keys; alternatively use
`openssl genpkey -algorithm ed25519` and convert, or `age-keygen`.

Store the private key in a password manager and in GitHub Actions secrets
as `GIPHY_SIGNING_KEY` (or your plugin's equivalent). The giphy Makefile
expects base64-encoded ed25519 seed+pub format (64 bytes, matching the Go
`ed25519.PrivateKey` concatenated form).

### 5.2 Publishing the public key

Ship the public key inside every archive as `dev-signing-key.pub`. The
catalog entry also carries the public key so the host can cross-verify:
the archive's `dev-signing-key.pub` must equal the catalog entry's
`public_key` field. Mismatch is treated as an untrusted install.

Rotate keys by issuing a new plugin version that ships the new public
key, with the final version signed by the old key and the first new
version co-signed or explicitly approved through the catalog maintainers'
rotation process.

### 5.3 Signing archives

The scaffold's `make release` target signs when `GIPHY_SIGNING_KEY` (or
your plugin's equivalent env var) is set:

```bash
export MY_PLUGIN_SIGNING_KEY="$(cat ~/.secrets/my-plugin.key.b64)"
make release
# → dist/archives/my-plugin-0.1.0-*.tar.gz.sig written next to each archive
```

The generic signing tool invocation (from `cmd/sign`):

```bash
go run ./cmd/sign dist/archives/my-plugin-0.1.0-darwin-arm64.tar.gz \
  > dist/archives/my-plugin-0.1.0-darwin-arm64.tar.gz.sig
```

If the signing env var is unset, `make release` skips signing and prints a
notice — useful for local dev builds that will be installed in developer
mode.

## 6. Verifying a signature locally

Before publishing, sanity-check the signature chain:

```bash
nanite plugin install ./dist/archives/my-plugin-0.1.0-darwin-arm64.tar.gz
```

In production mode, the install refuses if either signature is missing or
fails verification. In developer mode, the install succeeds with a
warning and the Plugin Manager displays the `⚠️` tier so the state is
obvious.

## 7. Release automation

The scaffold ships `.github/workflows/release.yml` that fires on tag
push (`v*.*.*`). The workflow:

1. Checks out the repo.
2. Installs Go and Node.
3. Runs `make release` with `MY_PLUGIN_SIGNING_KEY` injected from GitHub
   Actions secrets.
4. Uploads the resulting `dist/archives/*.tar.gz`, `.sha256`, and `.sig`
   files as release assets on the GitHub release for that tag.
5. Optionally opens a PR against the catalog repo with the new entry (see
   the catalog guide).

Release a new version:

```bash
# bump version in plugin.yaml + CHANGELOG.md + commit
git tag v0.2.0
git push origin v0.2.0
# CI runs make release, uploads signed archives to the GitHub release
```

## 8. Reproducibility checklist

Before you sign and publish:

- `CGO_ENABLED=0`, `-trimpath`, `-ldflags="-s -w"` on every `go build`.
- UI `npm ci` (not `npm install`) in CI so `package-lock.json` is honored.
- Bundle verified: no React, no `@nanite/ui/*` symbols inside `ui/dist/index.js`.
- `plugin.yaml` validates against the v1 JSON Schema.
- `plugin.yaml version` equals the git tag and the `CHANGELOG.md` top entry.
- `ui.shadcn_version` matches the host primitive API you built against.
- `nanite plugin install` against a clean `~/.nanite/` succeeds locally
  before the release is published.

## 9. What gets uploaded where

- **GitHub release**: per-platform tarballs, `.sha256`, `.sig`,
  `dev-signing-key.pub`, `CHANGELOG.md` excerpt.
- **Archives CDN** (`archives.nanite.hollis-labs.dev`): mirrored tarballs,
  checksums, signatures. The catalog points here for installs.
- **Catalog repo**: catalog entry referencing the archive URLs, checksums,
  signatures, and public key. Submitted as a PR — see the catalog guide.
