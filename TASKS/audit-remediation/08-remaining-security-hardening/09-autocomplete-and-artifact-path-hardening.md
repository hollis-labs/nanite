# Autocomplete repo_path exposure, artifact write-path confinement, catalog fetch timeout, plugin-UI symlink gap

**Phase:** Wave 3 — Remaining security hardening (guide §4; unsequenced — see folder README)
**Status:** not-started
**Depends on:** none
**Touches:** `internal/api/autocomplete.go` (`resolveRoot`), `internal/api/projects.go` (`handleCreateProject`/`handleUpdateProject`), `internal/api/artifacts.go` (`handlePlaceArtifact`), `internal/api/catalog.go` (`handleCatalogInstall`), `internal/api/plugins.go` (plugin-UI static-file route, lines 107-120), `internal/pathsafe` (`ResolveUnder` — reused, not modified)
**Requires architect decision:** **mixed — see per-finding note below**

## Findings addressed

- **GO-API-001** (low severity, high confidence, security) — report §8.5. Requires architect decision: **true** (matches `findings.json`).
- **GO-API-002** (low severity, high confidence, security + testing) — report §8.5. Requires architect decision: **false** (matches `findings.json`).
- **GO-API-003** (low severity, medium confidence, security) — report §8.5. Requires architect decision: **false** (task-authoring call — see divergence note).
- **GO-API-008** (low severity, high confidence, duplication + security-consistency) — report §8.5. Requires architect decision: **false** (matches `findings.json`).

> **Divergence from `findings.json` on GO-API-003:** the catalog flags this finding's `requires_architect_decision` as `true`. This task sets it to `false` because the concrete part of the fix — an explicit client timeout — is unambiguous and needs no architect input. The report's own recommendation additionally raises a genuine open policy question ("whether catalog sources should be treated as fully trusted or need an outbound-URL allowlist"); this task flags that separately below as a narrower, optional follow-on decision, not a blocker for the timeout fix.

## Context

**Trust classification per the guide's Wave 3 instruction, applied explicitly per finding:**

- **GO-API-001 / GO-API-002** classify as **external authenticated** — reachable by any caller with valid API access (report §8.5's own body-size/auth review confirms uniform Basic Auth covers these routes, no bypass found). The audit's own severity-mitigating note applies directly: in a single-operator local deployment, "authenticated caller," "the person who set `repo_path`," and "someone who already has a shell on this machine" are plausibly the same person — exactly why these are rated low despite being externally reachable in principle.
- **GO-API-003 / GO-API-008** classify as **plugin/catalog-controlled** — both require an already-privileged operator action first (adding/trusting a catalog source; installing a plugin, itself subject to signature verification elsewhere per GO-PLUGIN-001/002 in folder 01) before the finding's own gap becomes reachable at all. This is a materially different, lower-urgency trust boundary than a directly externally-reachable finding, and severity should (and does, per the audit) reflect that.

**GO-API-001:** autocomplete file listing (`resolveRoot`, `autocomplete.go:135-164`) walks `project.RepoPath` with **no validation anywhere in the chain** — `handleCreateProject`/`handleUpdateProject` (`projects.go`) store `RepoPath` verbatim with no constraint. Any authenticated caller can point a project at `/` or `$HOME` and enumerate filenames/sizes/mtimes (metadata only, no content) up to depth 8.

**GO-API-002:** `handlePlaceArtifact` (`artifacts.go`) accepts an unrestricted `StoragePath` at write time with zero sanitization, unlike its sibling `handleUploadArtifact` which correctly uses `pathsafe.ResolveUnder`. Not currently exploitable for content disclosure — the *download* path independently confines via `pathsafe.ResolveUnder`, confirmed by the existing regression test `TestDownloadDoesNotLeakAbsoluteEscapingPath` — but it's an inconsistency inviting future drift, and this specific write path has **zero test coverage**.

**GO-API-003:** `handleCatalogInstall` (`catalog.go`) fetches `entry.ArchiveURL` via `http.DefaultClient` with **no explicit timeout** (bounded only by the inbound request's own context, if any) and no host/scheme restriction beyond http/https — the standard package-manager-registry SSRF shape. Response size is separately confirmed correctly capped at 100 MiB.

**GO-API-008:** the plugin-UI static-file route (`plugins.go:107-120`) hand-rolls its own `Clean`+`HasPrefix` path-confinement check instead of `pathsafe.ResolveUnder` — sound against `..`/absolute-path traversal, but doesn't call `EvalSymlinks`, so a symlink inside an installed plugin's `ui/` dir pointing outside its root would not be caught.

## What to do

Four distinct, independently-landable fixes:

**1. GO-API-001 (architect decision required first):** decide whether `repo_path` should be constrained to an operator-configured allowlist of directories. This is a real policy question, not an implementation detail — an allowlist changes what a legitimate operator can point a project at, which is a product-behavior decision. Do not implement an allowlist speculatively; wait for the architect's direction. If the architect declines (judging the single-operator trust model sufficient, matching the audit's own mitigating framing), formally disposition this finding as **accepted-risk** rather than leaving it silently unresolved.

**2. GO-API-002:** apply the same `pathsafe.ResolveUnder`-based confinement at write time in `handlePlaceArtifact` that `handleUploadArtifact` already uses correctly — bring the two sibling handlers into parity. Add a regression test mirroring the download-side's existing `TestDownloadDoesNotLeakAbsoluteEscapingPath` pattern, since this write path currently has zero test coverage.

**3. GO-API-003:** give `handleCatalogInstall`'s download `http.Client` an explicit timeout (the fetch currently relies only on the inbound request's own context, if any, which may not exist or may be unbounded). This is the concrete, unambiguous part of the fix. Separately — flag, do not implement speculatively — whether catalog sources should be treated as fully trusted or need an outbound-URL allowlist is a genuine, narrower open policy question the audit's own recommendation raises; note it for a future architect pass rather than blocking the timeout fix on it.

**4. GO-API-008:** route the plugin-UI static-file handler through `pathsafe.ResolveUnder(baseDir, file)` instead of its hand-rolled `Clean`+`HasPrefix` check, for consistency with the rest of the codebase's confinement pattern and to close the symlink gap (`pathsafe.ResolveUnder` calls `EvalSymlinks`; the hand-rolled check doesn't).

**All production callers:** for GO-API-002/008 (shared-primitive-adjacent fixes bringing a handler into line with an existing pattern), confirm there are no other handlers in `internal/api` that duplicate either the missing-`pathsafe`-adoption pattern (GO-API-002) or the hand-rolled `Clean`+`HasPrefix` pattern (GO-API-008) beyond the ones named — the guide's "fix every sibling path" principle applies even to a low-severity finding if a third sibling turns out to exist that the audit's own package review didn't happen to sample.

## Non-goals

These four findings are bundled in one file because they're all `internal/api` findings that didn't fit elsewhere in this folder's grouping — say this explicitly rather than forcing a shared root cause that doesn't exist. GO-API-001 is a policy/allowlist question; GO-API-002/008 are "apply the existing `pathsafe.ResolveUnder` pattern consistently" fixes; GO-API-003 is a missing-timeout fix. Do not attempt to unify these into a new shared abstraction — `pathsafe.ResolveUnder` itself is the only real shared primitive among them, and it already exists; this task is about consistent adoption, not building something new.

## Tests required

- **GO-API-002:** new regression test for `handlePlaceArtifact` mirroring `TestDownloadDoesNotLeakAbsoluteEscapingPath`'s pattern, asserting a traversal/absolute-path `StoragePath` is rejected at write time.
- **GO-API-003:** a test asserting the catalog-install download respects a bounded timeout (e.g., against a deliberately slow/hanging test server).
- **GO-API-008:** a regression test planting a symlink inside an installed plugin's `ui/` directory pointing outside its root, asserting the static-file route now rejects it (mirrors the symlink-escape test shape report §8.12 confirms already exists elsewhere for `pathsafe.ResolveUnder`).
- **GO-API-001:** none required until/unless the architect decision results in an allowlist implementation — if so, a test asserting a project pointed outside the allowlist is rejected.

## Prevention

GO-API-002 and GO-API-008 are both instances of the guide's own "Trust-Boundary Paths" standard (§4 Wave 7: "values influenced by external callers... must not become filesystem paths without canonical validation/confinement") — landing both closes two more sibling gaps in that recurring class. Worth a repo-wide grep for any remaining `Clean`+`HasPrefix`-style hand-rolled confinement checks as a follow-up (not required by this task, but a natural next sweep).

## Verification

`go build ./...`; `go test ./internal/api/...` (new/updated tests for artifact placement, catalog install, plugin-UI static serving); `gosec ./internal/api/...` re-run confirming no regression.

## Risk / rollback

GO-API-002/003/008 are all low-risk, additive-confinement or additive-timeout changes — should not break any legitimate current caller, since they only reject inputs that were already invalid/unsafe. Rollback is a straightforward revert for any of the three. GO-API-001 carries the only real behavior-change risk (an allowlist, if adopted, would reject some currently-accepted `repo_path` values) — flagged above as architect-gated for exactly this reason.

## Done means

- [ ] GO-API-001: architect decision recorded (allowlist adopted, or finding dispositioned accepted-risk)
- [ ] GO-API-002: `pathsafe.ResolveUnder` applied to `handlePlaceArtifact`; regression test added and passing
- [ ] GO-API-003: explicit client timeout added to `handleCatalogInstall`'s download; test added and passing
- [ ] GO-API-008: plugin-UI static-file route routed through `pathsafe.ResolveUnder`; symlink-escape regression test added and passing

## Work log

<!-- Worker fills this in. -->

## Review notes

<!-- Reviewer fills this in. -->
