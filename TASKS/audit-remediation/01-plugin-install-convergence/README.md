# 01 — Plugin install convergence

This grouping covers the single most severe finding in the whole Go quality
audit: the GUI/API "Plugin Manager" catalog-install path
(`POST /api/plugins/catalog/install`, `internal/api/catalog.go`) bypasses
signature/checksum verification by default and has an unconfined
path-traversal write, while a genuinely solid, fail-closed install pipeline
already exists — built later, for the CLI (`nanite plugin install`) — and was
never back-ported to the GUI/API handler. Both critical findings (GO-PLUGIN-001,
GO-PLUGIN-002) and their direct architectural cause (GO-PLUGIN-003, the two
parallel install pipelines) live here, evidenced in
`docs/audits/2026-08-21-go-quality/REPORT.md` §8.6.

Per the remediation guide's proposed wave structure (§4), this is **Wave 1 —
release-blocking trust boundaries**: a critical, externally-reachable
security gap in a user-facing feature, with a clear (if architecturally
non-trivial) fix already proven out elsewhere in the same codebase. It is
grouped ahead of correctness/lifecycle work, architecture cleanup, and
mechanical hygiene because it is the guide's own top-priority category —
"critical trust-boundary/security failures" — and because the fix pattern
(converge every entry point onto one already-fail-closed pipeline) is exactly
the guide's named "fix every sibling path" principle in its most literal,
highest-stakes form.

- **`01-unify-plugin-catalog-install-pipeline.md`** — converges
  `handleCatalogInstall` onto the CLI's fail-closed signature/staging
  pipeline, closing GO-PLUGIN-001, GO-PLUGIN-002, and GO-PLUGIN-003.
- **`02-wire-allow-unsigned-plugins-setting.md`** — resolves the smaller,
  low-severity GO-PLUGIN-008 gap (a documented dev-workflow setting that's
  never actually wired into the CLI signature verifier), sequenced
  after/with task 01 since both touch the same `SignatureVerifier`
  construction site.
