# [Medium] Plan's install-time trust model has gaps — dev-mode bypass, unsigned fallbacks, no supply-chain verification

**Scope:** plan completeness / security
**Topic:** plan-completeness / security
**Date:** 2026-04-10

## Problem

The plan proposes plugin installation from an Ed25519-signed catalog hosted on Cloudflare. Good start. But the trust boundary design has holes: developer mode bypasses signing entirely, the signing key lives on a CI secret (single point of compromise), there's no pinning, no transparency log, no revocation after install, no first-run confirmation for unsigned plugins, no warning when a previously-signed plugin is replaced with an unsigned one, and no verification of the actual plugin code (only the archive hash). The reviewer-context file explicitly frames plugins as "runs in-process with no isolation. A malicious plugin has full host access." That framing demands stricter install-time controls than the plan delivers.

## Evidence

Plan §J.2:

> - Catalog signatures always required in production mode (dev mode bypasses)
> - Per-plugin signatures required in production mode unless user enables "allow unsigned plugins" in settings
> - Plugin Manager UI displays trust tier (signed ✓ / unsigned ⚠️ / untrusted 🚫)

Plan §13.22 (sharp edge):

> **Dev mode affordances cannot be enabled in production builds** — if the developer mode flag is runtime-only, a malicious user could flip it to bypass signing. Make sure dev mode requires both the runtime flag AND a build-time tag or environment check that production binaries can't satisfy.

This is the right instinct but it's listed as a sharp edge instead of a design requirement. It also doesn't address:

1. **Dev-mode plugins can trojan production plugins.** A plugin installed in dev mode sits in the same `~/.nanite/plugins/` directory. If the user later restarts in production mode, what happens? Does the unsigned plugin load? Get flagged and skipped? Get deleted? Plan doesn't say.

2. **CI-secret key compromise is game-over.** Plan §F.2:
   > Generate an Ed25519 keypair for the catalog root
   > Store private key in a password manager / YubiKey (offline backup)
   > Store a copy in GitHub Actions secrets for CI signing
   
   GitHub Actions secrets are accessible to any maintainer with write access to the catalog repo and any workflow file they can land. A malicious PR with a tweaked workflow that exfiltrates the secret is a realistic attack. No rotation, no revocation — once compromised, every existing signature is suspect and there's no way to tell users to distrust them.

3. **No transparency log.** Compromised catalog → malicious plugin signed with legit key → user installs → user has no way to verify this plugin was actually published by the maintainer. A transparency log (Sigstore, Binary Transparency, even a GitHub-hosted append-only log) would let users detect unauthorized releases. Listed in §15 "Deferred to v2" as "Cosign / sigstore keyless signing" — but the plan commits to shipping before that's in place.

4. **No pinning.** Users can't pin a specific version or a specific hash. `nanite plugin install giphy` always resolves to the latest. If the maintainer publishes v1.1 that's compromised and v1.0 was clean, users upgrading automatically get compromised. Plan's update flow at §G.3 atomically swaps; there's no "stay on 1.0" policy.

5. **No warning on trust downgrade.** User has giphy installed from catalog v1.0 (signed). Upgrade resolves to v1.1 which is unsigned (maintainer forgot to sign, key expired, whatever). Plan says "unsigned ⚠️" but doesn't specify whether upgrade auto-accepts or requires confirmation. Downgrading trust silently is a supply-chain failure mode.

6. **No verification of actual plugin contents.** The signature covers the archive hash, but the archive contains Go source or a prebuilt binary. If prebuilt, what prevents the publisher from shipping a different binary than the source claims? No reproducible builds, no SBOM, no `go build` verification step at install time. Plan just checks `sha256(archive)`.

7. **Permissions model is absent.** Plugins run in-process with full host access per reviewer-context. The plan doesn't propose any capability or permission system for plugins. `permissions:` in plugin.yaml is mentioned for commands (plan §B.1 schema) but there's no plugin-wide permission model — no "plugin can read files / plugin can make network requests / plugin cannot touch the store."

8. **First-run confirmation not specified.** Plan §G.5 says Plugin Manager UI has "Uninstall confirm modal." Does it also have an "install confirm modal" that warns about trust tier and scope of access? If yes, where's the spec? If no, users click install and inherit host-level privileges silently.

## Impact

For the beta release:

- Dev-mode bypass is a magic flag that anyone who can run nanite can flip. Maybe fine for developer friends; not fine long-term.
- CI key compromise has no recovery mechanism. If/when it happens, every existing user's installed plugins become suspect.
- Trust downgrades on update can slip through without warning.
- No permission model means any plugin you trust is a plugin you give full host access to — the reviewer-context explicitly says this is the design, but the plan should call it out as an accepted risk and the install flow should make it visible to users.

The reviewer-context already frames plugin install as "install-time review, not runtime sandboxing." That framing depends on install-time review being thorough. The plan's install-time review is a signature check on the archive hash — not a review.

## Recommendation

Minimum for beta:

1. **Dev mode requires both a build tag and a runtime flag.** Production binaries built without the `devplugins` build tag hard-fail any attempt to install an unsigned plugin, regardless of settings. Runtime flag is for developer builds only.

2. **Trust-downgrade warning.** On `plugin update`, if the new version has a weaker trust tier than the installed version, refuse unless `--allow-downgrade-trust` is explicit.

3. **Per-version install confirmation.** First install of a plugin from the catalog prompts the user with: plugin name, publisher, trust tier, version, archive hash. User confirms. Cached per-plugin-per-version in `~/.nanite/plugin-trust/`.

4. **Separate dev-mode plugins from production-mode plugins.** `~/.nanite/plugins-dev/` vs `~/.nanite/plugins/`. Nanite in production mode only loads from the latter. Prevents dev-mode cross-contamination.

5. **CI key procedure.** Document: "if this key is suspected compromised, rotate the catalog root key and publish a revocation list at `https://plugins.nanite.hollis-labs.dev/revoked.yaml` signed with the new key." Embed a check for this revocation list in nanite's catalog fetcher.

6. **Capture permission model as a follow-up scope.** Track J doc work should include `plugin-security-model.md` that enumerates: plugins have full host access by design, install-time is the control point, users must review plugin source before installing, dev-mode bypass is for development only. Make it explicit and discoverable.

Deferred to post-beta but document in plan's §15:

- Sigstore-style keyless signing with transparency log
- Reproducible builds
- SBOMs
- Plugin capability manifests (declare required capabilities; host grants or denies at install)
- Per-version pinning and lockfile

## References

- Plan §J.2 — signing enforcement (incomplete)
- Plan §F.2 — key storage strategy (single point of compromise)
- Plan §13.22 — sharp edge about dev mode (should be a requirement)
- Plan §15 — deferred items (Cosign listed as deferred)
- Reviewer-context `reviewer-backend.md` trust boundaries 5, 6 — plugins as full-host-access surface
- `internal/plugin/signature.go` — existing Ed25519 verification code not referenced in plan
- Related: sibling audit `docs/audits/2026-04-10-sandbox-hardening/` — sandbox findings also touch "what's in the trust boundary"
