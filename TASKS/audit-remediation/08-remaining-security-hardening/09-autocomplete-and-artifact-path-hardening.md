# Autocomplete repo_path exposure, artifact write-path confinement, catalog fetch timeout, plugin-UI symlink gap

**Phase:** Wave 3 — Remaining security hardening (guide §4; sequenced 2026-08-21 — see the sequencing block below)
**Status:** not-started
**Depends on:** none
**Touches:** `internal/api/autocomplete.go` (`resolveRoot`), `internal/api/projects.go` (`handleCreateProject`/`handleUpdateProject`), `internal/api/artifacts.go` (`handlePlaceArtifact`), `internal/api/catalog.go` (`handleCatalogInstall`), `internal/api/plugins.go` (plugin-UI static-file route, lines 107-120), `internal/pathsafe` (`ResolveUnder` — reused, not modified)
**Requires architect decision:** **mixed — see per-finding note below**

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 3 — remaining security hardening · **Dispatch unit:** `W3`
> - **Depends on:** `01/01` — that task rewrites `handleCatalogInstall`, which this task also hardens
> - **Blocks:** `11/15`
> - **Parallel-safe with:** `08/01`–`08/06`
> - **Gated on:** AD-04 — specifically the canonical-confinement sub-question. If `01/01` and this task pick different mechanisms for the same file, the batch has reintroduced the inconsistency it exists to remove.
> - **requires_security_review:** true · **requires_regression_test:** true

> ## ⚠ RE-BASELINED 2026-08-22 — two decisions added, one finding half-resolved
>
> **`GO-API-003`'s scope shrank.** It was *"`http.DefaultClient`, no timeout, no
> host/scheme restriction."* `01/01`'s AD-04 convergence landed after Wave 0
> measured this and removed `http.DefaultClient` from
> `internal/api/catalog.go` entirely — the download now runs through
> `install.HTTPDownloader` (`catalog.go:339`), which applies
> `DefaultDownloadTimeout` (2 min) and `DefaultMaxArchiveBytes` on zero values.
> **The timeout half is already fixed, plus a size cap the finding never asked
> for.** Only the host/scheme allowlist remains open. Do not re-add a timeout.
>
> **`GO-API-001` is intact** — `resolveRoot` (`autocomplete.go:136-154`) still
> returns `p.RepoPath` unvalidated; only the ctx sweep touched that file.
>
> **✅ AD-27 and AD-28 are now DECIDED (2026-08-22). This task gained a third
> finding.**
>
> **AD-27 (`GO-API-001`) — validate `repo_path` at write time.** Constrain it
> in `handleCreateProject`/`handleUpdateProject` (which this task already
> touches), **not** by confining the autocomplete walk. Suggested policy, to
> confirm rather than assume: reject `/`, the home directory *itself*
> (subdirectories must stay allowed), and system dirs (`/etc`, `/usr`, `/var`,
> `/System`); require an existing directory. Decide and record whether existing
> `projects` rows get swept or are validated on next update only.
>
> **AD-28 (`GO-API-003`) — block private/loopback/link-local destinations, and
> DO NOT ADD A THIRD CIDR COPY.** Reject archive URLs resolving into RFC1918,
> loopback, or link-local ranges (including cloud IMDS `169.254.169.254`). No
> host allowlist — it would break catalog-on-one-host/releases-on-another,
> which is the common real pattern.
>
> **⚠ `GO-SEC4-007` is now this task's finding too.** The denylist already
> exists twice — `internal/mcp/general_tools.go:61-68` and
> `internal/sandbox/proxy.go:37+` — identical, with the sandbox copy's comment
> promising parity and nothing enforcing it. Adding a third copy here would
> worsen that finding while closing another. **Extract the CIDR set to one
> shared location and have all three consumers import it**: the sandbox proxy,
> `callWebFetch`, and this task's new catalog-download guard.
>
> That finding was an orphan until now — its `task_file` pointed at `11/07`,
> which disclaims being an implementation task and defers to this folder, where
> no task for it had ever been written. Reassigned here on 2026-08-22.
>
> Citations in this file predate both `01/01` and the ctx sweep — re-locate
> before editing.

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
