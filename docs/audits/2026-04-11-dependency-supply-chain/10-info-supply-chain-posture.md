# [Info] Supply chain posture summary — no vendor dir, no lockfile signing, no SBOM

**Scope:** Whole project — supply chain hygiene
**Topic:** Supply chain — architecture observations
**Date:** 2026-04-11

## Problem

Observations about the overall supply chain posture. No action required but worth documenting for future hardening.

## Evidence

### Vendoring

**Go:** No `vendor/` directory exists. Dependencies are fetched from the module proxy (`proxy.golang.org`) at build time. The `go.sum` file (165 lines) provides cryptographic verification of downloaded modules. Module checksums verify cleanly:

```
$ go mod verify
all modules verified
```

**Frontend:** `ui/package-lock.json` exists and pins exact versions. No `node_modules` vendoring or `npm ci --prefer-offline` configuration was observed in build scripts.

### Replace directives — complete inventory

Seven replace directives point to sibling local directories:

| Module | Replace target | Needed? |
|--------|---------------|---------|
| `github.com/hollis-labs/otel` | `../framework/libs/go-otel` | Yes — direct dep |
| `github.com/hollis-labs/tool-broker` | `../framework/libs/go-toolbroker` | Yes — direct dep |
| `github.com/hollis-labs/go-plugin` | `../framework/libs/go-plugin` | Yes — direct dep |
| `github.com/hollis-labs/go-providers` | `../framework/libs/go-providers` | Yes — direct dep |
| `github.com/hollis-labs/vanta-conduit` | `../vanta-conduit` | Yes — direct dep |
| `github.com/hollis-labs/go-queue` | `../framework/libs/go-queue` | Yes — transitive via vanta-conduit |
| `github.com/hollis-labs/mcp-helpers` | `../framework/libs/go-mcp` | **No** — phantom (see finding 04) |

All sibling library directories exist and contain valid `go.mod` files. The development model is monorepo-adjacent: separate repos with local path overrides. This is a valid pattern but means builds are not reproducible without access to all sibling directories at the expected relative paths.

### Known-bad packages

No dependencies matched known-bad package lists:
- No typosquatting indicators (all package names are well-established)
- No packages flagged as compromised or abandoned in the Go or npm advisory databases
- `lib/pq` is in maintenance mode but not deprecated or flagged as unsafe
- `badger/v4` by Dgraph is actively maintained

### Build reproducibility

Without the sibling libraries present at their expected relative paths, `go build` will fail. This is documented in `backend.md` and `reviewer-backend.md`. Docker builds resolve this via multi-stage builds that include all sibling libs.

## Impact

The supply chain posture is reasonable for a pre-release project with private sibling libraries. The `go.sum` verification provides integrity guarantees for public modules. The main gaps are:

1. No SBOM generation (Software Bill of Materials)
2. No Sigstore/cosign signing of releases
3. No `govulncheck` in CI (see finding 03)
4. Phantom replace directive that breaks tooling (see finding 04)

## Recommendation

For future hardening, consider:
1. Adding `govulncheck` to CI
2. Generating SBOMs as part of the release process (`cyclonedx-gomod` or `syft`)
3. Removing the phantom `mcp-helpers` replace

These are improvements, not correctness issues.

## References

- `go.mod:20-38` — replace directives
- `go.sum` — 165-line checksum file
- `ui/package-lock.json` — frontend lockfile
