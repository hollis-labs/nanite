# Project repo-path validation, artifact/plugin-UI confinement, and catalog SSRF policy

**Phase:** Wave 3 — Remaining security hardening (guide §4; sequenced 2026-08-21 — see the sequencing block below)
**Status:** implemented
**Depends on:** none
**Touches:** `internal/api/projects.go` (`handleCreateProject`/`handleUpdateProject`), `internal/api/artifacts.go` (`handlePlaceArtifact`/download defense in depth), `internal/api/catalog.go` (`handleCatalogInstall`), `internal/api/plugins.go` (plugin-UI static-file route), `internal/plugin/install/download.go`, `internal/mcp/general_tools.go`, `internal/sandbox/proxy.go`, new `internal/ssrf`, and focused tests; `internal/pathsafe.ResolveUnder` is reused, not modified
**Requires architect decision:** resolved by AD-27 and AD-28

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
> for.** Only destination-address policy remains open. Do not re-add a timeout.
>
> **`GO-API-001` is intact** — `resolveRoot` (`autocomplete.go:136-154`) still
> returns `p.RepoPath` unvalidated; only the ctx sweep touched that file.
>
> **✅ AD-27 and AD-28 are now DECIDED (2026-08-22). This task now covers five
> findings.**
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

- **GO-API-001** (low severity, high confidence, security) — report §8.5. Architect decision resolved by AD-27.
- **GO-API-002** (low severity, high confidence, security + testing) — report §8.5. Requires architect decision: **false** (matches `findings.json`).
- **GO-API-003** (low severity, medium confidence, security) — report §8.5. Architect decision resolved by AD-28.
- **GO-API-008** (low severity, high confidence, duplication + security-consistency) — report §8.5. Requires architect decision: **false** (matches `findings.json`).
- **GO-SEC4-007** (medium severity, high confidence, duplication + security) — report §8.12. Requires architect decision: **resolved by AD-28**.

> **Re-baselined disposition:** AD-04 already supplied the bounded download
> timeout and archive-size cap. AD-28 resolves the remaining destination
> policy and assigns GO-SEC4-007 here so the catalog does not become a third
> independent copy of the same CIDR rule.

## Context

**Trust classification per the guide's Wave 3 instruction, applied explicitly per finding:**

- **GO-API-001 / GO-API-002** classify as **external authenticated** — reachable by any caller with valid API access (report §8.5's own body-size/auth review confirms uniform Basic Auth covers these routes, no bypass found). The audit's own severity-mitigating note applies directly: in a single-operator local deployment, "authenticated caller," "the person who set `repo_path`," and "someone who already has a shell on this machine" are plausibly the same person — exactly why these are rated low despite being externally reachable in principle.
- **GO-API-003 / GO-API-008** classify as **plugin/catalog-controlled** — both require an already-privileged operator action first (adding/trusting a catalog source; installing a plugin, itself subject to signature verification elsewhere per GO-PLUGIN-001/002 in folder 01) before the finding's own gap becomes reachable at all. This is a materially different, lower-urgency trust boundary than a directly externally-reachable finding, and severity should (and does, per the audit) reflect that.

**GO-API-001:** autocomplete file listing (`resolveRoot`, `autocomplete.go:135-164`) walks `project.RepoPath` with **no validation anywhere in the chain** — `handleCreateProject`/`handleUpdateProject` (`projects.go`) store `RepoPath` verbatim with no constraint. Any authenticated caller can point a project at `/` or `$HOME` and enumerate filenames/sizes/mtimes (metadata only, no content) up to depth 8.

**GO-API-002:** `handlePlaceArtifact` (`artifacts.go`) accepts an unrestricted `StoragePath` at write time with zero sanitization, unlike its sibling `handleUploadArtifact` which correctly uses `pathsafe.ResolveUnder`. Not currently exploitable for content disclosure — the *download* path independently confines via `pathsafe.ResolveUnder`, confirmed by the existing regression test `TestDownloadDoesNotLeakAbsoluteEscapingPath` — but it's an inconsistency inviting future drift, and this specific write path has **zero test coverage**.

**GO-API-003:** after AD-04, `handleCatalogInstall` fetches `entry.ArchiveURL` through `install.HTTPDownloader`, which already supplies a two-minute timeout and 100 MiB cap. The remaining standard package-manager-registry SSRF shape is destination policy: a catalog-controlled archive URL can still resolve to private, loopback, link-local, or IMDS addresses unless the actual dial is validated and pinned.

**GO-API-008:** the plugin-UI static-file route (`plugins.go:107-120`) hand-rolls its own `Clean`+`HasPrefix` path-confinement check instead of `pathsafe.ResolveUnder` — sound against `..`/absolute-path traversal, but doesn't call `EvalSymlinks`, so a symlink inside an installed plugin's `ui/` dir pointing outside its root would not be caught.

**GO-SEC4-007:** MCP web fetch and the sandbox proxy independently carry the same SSRF CIDR list. AD-28 adds catalog download as a third consumer, so the semantic rule must be extracted rather than copied again.

## What to do

Five fixes under the re-baselined decisions:

1. **GO-API-001 / AD-27:** validate `repo_path` when create/update writes it. Empty remains allowed; non-empty paths must resolve to an existing directory. Reject filesystem root, home itself while allowing home subdirectories, and the named system trees `/etc`, `/usr`, `/var`, and `/System`. Leave autocomplete unchanged. Choose and record existing-row handling.
2. **GO-API-002:** confine `handlePlaceArtifact` storage paths under the configured artifacts root through `pathsafe.ResolveUnder`, reject escaping paths before persistence, and keep the download-side defense in depth.
3. **GO-API-003 / AD-28:** keep `HTTPDownloader`'s existing two-minute timeout and 100 MiB cap unchanged. Block private/RFC1918, loopback, link-local, IMDS, and the existing sibling-denylist ranges at the dial seam; validate every DNS answer and dial the checked literal so DNS rebinding and redirect targets cannot bypass the check.
4. **GO-API-008:** replace the plugin-UI route's `Clean`+`HasPrefix` check with `pathsafe.ResolveUnder` and reject a symlink escape.
5. **GO-SEC4-007:** before adding the catalog consumer, extract the duplicate CIDR policy from `internal/mcp/general_tools.go` and `internal/sandbox/proxy.go` into one narrowly-layered shared package; make all three egress paths consume it. Preserve `08/06`'s `ReadHeaderTimeout`.

Enumerate sibling `internal/api` hand-rolled path checks relevant to GO-API-002/008 and widen only with concrete evidence.

## Non-goals

These five findings share a dispatch unit, not one root cause. Do not build a general URL-policy framework or change autocomplete confinement. Do not re-add or replace `HTTPDownloader`'s already-landed timeout/size cap. The only new shared abstraction is the narrow SSRF destination-address policy AD-28 explicitly requires.

## Tests required

- **GO-API-002:** new regression test for `handlePlaceArtifact` mirroring `TestDownloadDoesNotLeakAbsoluteEscapingPath`'s pattern, asserting a traversal/absolute-path `StoragePath` is rejected at write time.
- **GO-API-003:** private/loopback/link-local/IMDS rejection plus a pinned-literal regression showing the checked DNS answer is the address dialed; retain the existing timeout regression unchanged.
- **GO-API-008:** a regression test planting a symlink inside an installed plugin's `ui/` directory pointing outside its root, asserting the static-file route now rejects it (mirrors the symlink-escape test shape report §8.12 confirms already exists elsewhere for `pathsafe.ResolveUnder`).
- **GO-API-001:** create/update policy coverage for root, home, home subdirectories, system trees, nonexistent paths, files, and existing-row handling.
- **GO-SEC4-007:** shared-policy tests plus unchanged consumer regressions for web fetch and sandbox proxy.

## Prevention

GO-API-002 and GO-API-008 are both instances of the guide's own "Trust-Boundary Paths" standard (§4 Wave 7: "values influenced by external callers... must not become filesystem paths without canonical validation/confinement") — landing both closes two more sibling gaps in that recurring class. Worth a repo-wide grep for any remaining `Clean`+`HasPrefix`-style hand-rolled confinement checks as a follow-up (not required by this task, but a natural next sweep).

## Verification

Focused adversarial tests for `internal/ssrf`, plugin download, MCP web fetch, sandbox proxy, and the affected API handlers; relevant `gosec`; then `go build ./cmd/nanite/`, `go vet ./...`, and `go test ./...` without a prolonged full-repo race run.

## Risk / rollback

The behavior changes reject unsafe project roots, artifact paths outside the configured root, plugin-UI symlink escapes, and catalog archives resolving to non-public destinations. Existing project rows are not swept. Local catalog-download tests and explicit local development callers must opt into localhost; production defaults remain fail-closed.

## Done means

- [x] GO-API-001: AD-27 write-time validation implemented in create/update, with policy and existing-row behavior covered
- [x] GO-API-002: artifact placement is confined with `pathsafe.ResolveUnder`; escaping-path and positive download regressions pass
- [x] GO-API-003: shared destination blocking and DNS-answer pinning protect catalog downloads; existing timeout/size caps remain intact
- [x] GO-API-008: plugin-UI static files use `pathsafe.ResolveUnder`; symlink escape is rejected
- [x] GO-SEC4-007: one shared CIDR policy backs MCP web fetch, sandbox proxy, and catalog download

## Work log

- Implemented all five re-baselined findings. `internal/ssrf` is deliberately
  narrow: `Resolver`, `DefaultResolver`, `ResolveAndPin`, `IsLocalhostName`,
  and classifiable `ErrBlocked`. Its CIDR slices stay private; callers cannot
  mutate or fork the policy. This is the package/API shape `08/01` can import.
- `HTTPDownloader` keeps `DefaultDownloadTimeout` (2 minutes) and
  `DefaultMaxArchiveBytes` (100 MiB) unchanged. Its transport now validates
  every DNS answer through `ssrf.ResolveAndPin`, rejects the whole answer if
  any IP is denied, disables ambient proxy routing, and dials the validated IP
  literal. Redirects remain HTTP(S)-only and run through the same dial check.
- AD-27 policy choice: canonicalize non-empty repo paths (including symlink
  resolution), require an existing directory, reject filesystem root, home
  itself, and the named system trees `/etc`, `/usr`, `/var`, `/System`; allow
  home subdirectories explicitly. Empty repo paths remain valid. **Existing
  rows use next-repo-path-update-only handling**: no sweep, and unrelated
  updates do not fail because of a legacy value.
- Artifact placement converts absolute paths to a root-relative candidate
  before calling `pathsafe.ResolveUnder`, rejects escape/nonexistent/non-file
  targets before persistence, and stores the canonical confined path. The
  download handler uses the same helper for defense in depth and compatibility
  with existing canonical absolute rows.
- The plugin-UI route now uses `pathsafe.ResolveUnder`; its symlink-escape
  regression returns 403 without exposing the target.
- Sibling enumeration: the only `internal/api` `Clean`+`HasPrefix` path
  confinement was the named plugin-UI route. Artifact upload already uses
  `ResolveUnder` for both directory and file placement; archive extraction and
  four plugin install/uninstall paths already use it too. The other relevant
  `filepath.Rel` use is autocomplete response formatting, not confinement, so
  scope was not widened.
- Focused adversarial tests passed with `-count=1` for `internal/ssrf`,
  `internal/plugin/install`, `internal/mcp`, `internal/sandbox`, and
  `internal/api`. The relevant gosec sweep completed; it reported the repo's
  existing known-noise set (including taint warnings at `pathsafe`-confined
  sinks) and no new catalog-download SSRF finding. `go build ./cmd/nanite/`
  and `go vet ./...` passed. The first `go test ./...` run hit the known
  intermittent `internal/service` `driveBootSession` send-on-closed-channel
  panic; `go test ./internal/service` then passed, and a clean full
  `go test ./...` rerun passed. No full-repo race campaign was launched.

## Review notes

<!-- Reviewer fills this in. -->
