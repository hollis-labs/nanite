# Unify the plugin-install security pipeline across CLI, GUI, and API

**Phase:** Audit remediation — Wave 1 (release-blocking trust boundaries)
**Status:** not-started
**Depends on:** none (this task is self-contained; task 02 in this folder is sequenced after/with it — see that task's Context for why)
**Touches:** `internal/api/catalog.go`, `internal/api/plugins.go` (read-only reference for the already-fixed pattern), `internal/plugin/catalog.go`, `internal/plugin/signature.go`, `internal/plugin/catalog/` (`fetch.go`, `trust.go`, `trustedkeys.go`), `internal/plugin/install/` (`install.go`, `verify.go`, `staging.go`, `extract.go`, `download.go`), `cmd/nanite/plugin_install_flow.go` (reference only — do not change the CLI path's behavior), `internal/plugin/devmode/` (reference only)
**requires_architect_decision:** true — flagged per the remediation guide's §9 decision queue. The direction (converge onto the CLI's pipeline) is not in serious doubt given the audit's evidence, but the blast radius (this is the single most severe finding in the whole audit, touches a user-facing "Plugin Manager" flow, and requires deciding exactly how catalog-sourced archives map onto the CLI's `install.Source`/`Handle`/`Installer` abstractions) means an architect should sign off on the concrete integration shape before a worker starts, not discover it mid-implementation.

## Context

### Findings addressed

- **GO-PLUGIN-001 (CRITICAL, security, confidence high)** — the GUI/API plugin-catalog-install path (`POST /api/plugins/catalog/install`, the actual "Plugin Manager" marketplace UI users click through) bypasses signature/checksum verification by default.
- **GO-PLUGIN-002 (CRITICAL, security, confidence high)** — the same handler has an unconfined path-traversal write.
- **GO-PLUGIN-003 (high, duplication/architecture, confidence high)** — the direct architectural cause of 001/002: two full parallel "fetch catalog → verify → download → extract → install" implementations exist side by side, and the weaker one feeds the vulnerable handler.

All three are evidenced in `docs/audits/2026-08-21-go-quality/REPORT.md` §8.6 (the "GO-PLUGIN-001/002/003" paragraphs) and cross-referenced in §1 ("Deep-dive phase findings"), §2 (top-10 architect-review candidate #1), and `findings.json`.

### Root cause

A genuinely solid, fail-closed, `devmode`-gated signature-verification pipeline (Ed25519, `KeyRing`, expiry/revocation) was built later (per the audit's git-history trace, 2026-04-13) for the **CLI** plugin-install path (`nanite plugin install`, wired through `internal/plugin/install/` + `internal/plugin/catalog/`). The original **API** handler for catalog-driven installs (`handleCatalogInstall` in `internal/api/catalog.go`, backing the GUI's "Plugin Manager" catalog browser) was never migrated onto it — it still calls the older, weaker `internal/plugin/catalog.go` + `internal/plugin/signature.go` implementation that predates the CLI rewrite. This is exactly the remediation guide's named "GUI/API/CLI code implementing business rules independently" pattern (§4 Wave 6 seed list), here with critical-severity consequences rather than just style drift.

### Current behavior

**`handleCatalogInstall`** (`internal/api/catalog.go:248-452`), registered at `internal/api/catalog.go:50` (`mux.HandleFunc("POST /api/plugins/catalog/install", cs.handleCatalogInstall)`):

- Line 301: `target := filepath.Join(cs.pluginsDir, entry.Name)` — a plain `filepath.Join`, not `pathsafe.ResolveUnder`, where `entry.Name` comes straight from an untrusted, unvalidated `catalog.yaml` fetched over HTTP from a configured catalog source URL.
- Line 358: `naniteplugin.VerifyChecksum(tmpPath, entry.Checksum)` — `VerifyChecksum` (`internal/plugin/catalog.go:246`) returns `nil` if `entry.Checksum == ""` (a "user-uploaded trust model" per its own comment); the catalog entry's checksum is attacker/catalog-source-controlled, so a malicious or compromised catalog can simply omit it.
- Lines 364-375: the signature branch — `if sourcePublicKey != "" && entry.Signature != ""` — only runs `VerifySignature` when **both** conditions hold. `findSourcePublicKey` (`internal/api/catalog.go:454`) reads `PublicKey` off the `store.CatalogSource` row. The default seeded "official" catalog source's `INSERT` statement never sets a `public_key` column value, so `sourcePublicKey == ""` for the out-of-the-box configuration — neither the `if` branch nor the `else if` warning branch fires; verification is silently skipped with no log line at all.
- This code path never references `devmode.HostDevSigningBypass` and carries no `//go:build devmode` gating — behavior is identical between production and dev builds. There is a real, working `devmode` gate elsewhere in this codebase (`internal/plugin/devmode/devmode_on.go` / `devmode_off.go`); this handler simply doesn't use it.
- Line 427: `copyDir(pluginRoot, target)` — writes the extracted plugin into the unconfined `target` computed at line 301. The archive-extraction step itself (`extractZip`/`extractTarGz` in `internal/api/plugins.go`, shared with the other handlers) is soundly defended; the vulnerable point is specifically this final copy from the safely-extracted temp dir to the unguarded `target`.
- Zero test coverage exists for `handleCatalogInstall` at all — confirmed by the audit and by this task's own read of `internal/api/plugins_install_test.go` (which covers `handleInstall`/`handleInstallLocal`/`handleInstallArchive` but has no `TestHandleCatalogInstall*` counterpart).

**The already-fixed sibling handlers**, all in `internal/api/plugins.go`, show the pattern this task needs to bring `handleCatalogInstall` up to (at minimum, for path confinement — see Non-goals for why signature convergence is the deeper fix):
- `handleInstall` (line 317) — line 327: `target, err := pathsafe.ResolveUnder(pms.pluginsDir, req.Name)`, with an inline comment: `// Confine the plugin target path under pluginsDir. ... (audit finding: Critical — path traversal in install).`
- `handleInstallLocal` (line 386) — line 416: same `pathsafe.ResolveUnder` call on `manifest.Name`, comment citing the same audit-finding pattern.
- `handleInstallArchive` (line 456) — line 537: same `pathsafe.ResolveUnder` call on `manifest.Name`.
- Regression tests exist for all three: `TestHandleInstall_PathTraversal` (`internal/api/plugins_install_test.go:201`), `TestHandleInstallLocal_ManifestTraversal` (`internal/api/plugins_install_test.go:225`), and traversal coverage for the archive path via `TestExtractZip_RejectsTraversal` (`internal/api/plugins_install_test.go:436`).

**The stronger CLI pipeline** (`internal/plugin/install/` + `internal/plugin/catalog/`), wired together in `cmd/nanite/plugin_install_flow.go`:
- `install.Installer` (`internal/plugin/install/install.go:116`) is a small state machine (`Install` method at line 153) driven by pluggable `Source`/`Verifier`/`Extractor`/`Validator`/`Loader`/`Staging` interfaces (lines 42-115).
- `install.SignatureVerifier` (`internal/plugin/install/verify.go:27`) checks sha256 unconditionally and Ed25519 signature unless `signatureBypassed := devmode.HostDevSigningBypass && v.AllowUnsigned` (verify.go:72) — meaning in a production (`!devmode`) build, signature verification is **always** enforced regardless of `AllowUnsigned`'s value, because `devmode.HostDevSigningBypass` compiles to `false` (`internal/plugin/devmode/devmode_off.go:25`) and Go dead-code-eliminates the bypass branch.
- `install.DirStaging.Commit` (`internal/plugin/install/staging.go:76`) confines the install target via `validatePluginID` (staging.go:139) — a regex-style check (`^[a-z][a-z0-9-]{1,62}$`) applied to `pluginID` before `finalDir := filepath.Join(s.PluginsRoot, pluginID)` (staging.go:90), plus an atomic backup-then-rename commit. This is a **different confinement mechanism** than the `pathsafe.ResolveUnder` used in the three already-fixed API handlers above — both are valid but this task's converged pipeline needs to pick one canonical mechanism (see Proposed direction).
- `catalog.SignedFetcher` (`internal/plugin/catalog/fetch.go:39`) fetches and Ed25519-verifies the catalog file itself (not just per-plugin archives) against a `catalog.yaml.sig` companion file.
- `catalog.KeyRing` (`internal/plugin/catalog/trust.go:24`) is a clean, correct trust store (`Add`/`Revoke`/`Lookup`), confirmed healthy by the audit (§8.6 "Reviewed and found healthy").
- Wired in `cmd/nanite/plugin_install_flow.go:102` (`buildInstaller`), which constructs `&install.Installer{Verifier: &install.SignatureVerifier{KeyLookup: ring.LookupFunc()}, Extractor: &install.TarGzExtractor{}, Validator: &cliValidator{}, Loader: noopLoader{}, Staging: staging, Emit: emit}`. `noopLoader` (line 94-96) is CLI-specific — it doesn't hot-load into a running host because the CLI runs out-of-process from the service; this is a real behavioral difference the converged pipeline must account for (the GUI/API path needs an in-process loader, e.g. the existing `pms.runPluginLoadIntoHost` helper already used by the other three API handlers).
- Both build-tag configurations (`devmode`/`!devmode`) have dedicated regression tests (`internal/plugin/install/verify_dev_bypass_test.go`, `verify_prod_enforcement_test.go`) confirmed by the audit as genuinely fail-closed and correctly gated.

### Desired invariant

**Every plugin-install entry point (CLI, GUI, API) converges on one authoritative pipeline that fails closed on missing/invalid signature in production builds and confines the install target path.** Concretely:

1. There is exactly one code path that turns "an untrusted catalog entry or uploaded archive" into "files written under the plugins directory" — reachable from `nanite plugin install`, `POST /api/plugins/catalog/install`, `POST /api/plugins/install`, `POST /api/plugins/install-local`, and `POST /api/plugins/install-archive` alike (each with entry-point-appropriate `Source`/`Loader` adapters, not a parallel verify/extract/write implementation).
2. In a production (non-`devmode`) build, a missing or invalid signature always rejects the install — no code path can silently proceed. This is already true for the CLI; this task's job is to make it true for the GUI/API too.
3. Every catalog- or manifest-derived name that becomes part of a filesystem path is validated/confined before use — either via `pathsafe.ResolveUnder` (the pattern already used by 3 of 4 API handlers) or `validatePluginID`-style allowlist validation (the pattern already used by the CLI's `DirStaging`), consistently applied, not a plain `filepath.Join`.
4. The older, weaker pipeline (`internal/plugin/catalog.go`'s `VerifyChecksum`, `internal/plugin/signature.go`'s `VerifySignature`, `CatalogFetcher`) either has no remaining callers or is explicitly and narrowly retained with a documented justification — it must not remain the pipeline actually reachable from any production entry point.

### Scope

- `internal/api/catalog.go` — `handleCatalogInstall` and its supporting helpers (`findSourcePublicKey`), and `RegisterCatalogRoutes`'s wiring of `catalogState`.
- `internal/plugin/catalog.go`, `internal/plugin/signature.go` — the legacy pipeline being retired/superseded; `CatalogFetcher` may still be needed for the *browse* path (`handleBrowseCatalog`, `GET /api/plugins/catalog`) which only lists entries and does not write files — confirm before removing wholesale.
- `internal/plugin/catalog/` (`fetch.go`, `trust.go`, `trustedkeys.go`) and `internal/plugin/install/` (`install.go`, `verify.go`, `staging.go`, `extract.go`, `download.go`) — the target pipeline; likely needs new adapter types (an API-side `install.Source` implementation analogous to `catalogArchiveSource` in `plugin_install_flow.go`, and an API-side `install.Loader` that calls into `pms.runPluginLoadIntoHost` or equivalent) but should not need behavioral changes to the pipeline's own verification/staging logic.
- `cmd/nanite/plugin_install_flow.go` — read as the reference implementation for how to construct an `Installer`; do not change CLI behavior as a side effect of this task.
- Test files: new coverage in `internal/api/` (see Tests required); existing `internal/plugin/install/*_test.go` should continue passing unmodified unless the pipeline's public interfaces need to grow.

### All production callers

Enumerated via `grep -rn "HandleFunc" internal/api/catalog.go internal/api/plugins.go` and `grep -rn "buildInstaller("  cmd/nanite/*.go`:

| Entry point | Route/command | Handler | Pipeline used today | Status |
|---|---|---|---|---|
| GUI "Plugin Manager" catalog install | `POST /api/plugins/catalog/install` | `handleCatalogInstall` (`internal/api/catalog.go:248`) | Legacy: `internal/plugin/catalog.go` (`CatalogFetcher`) + `internal/plugin/signature.go` (`VerifyChecksum`/`VerifySignature`) | **Vulnerable — this task's primary target** |
| GUI "Plugin Manager" install-by-name (git clone) | `POST /api/plugins/install` | `handleInstall` (`internal/api/plugins.go:317`) | No signature verification (git-clone trust model, not catalog-driven); path confined via `pathsafe.ResolveUnder` (line 327) | Already fixed for path traversal; no signature story because it's not a signed-artifact flow — confirm this stays true after convergence, don't accidentally route it through the signed pipeline incorrectly |
| GUI "Plugin Manager" local install | `POST /api/plugins/install-local` | `handleInstallLocal` (`internal/api/plugins.go:386`) | No signature verification ("local trust model" per its own doc comment, line 385); path confined via `pathsafe.ResolveUnder` (line 416) | Already fixed for path traversal; intentionally unsigned (local filesystem trust) — out of scope for signature convergence, in scope only if its path-confinement pattern needs to match the new canonical one |
| GUI "Plugin Manager" archive upload | `POST /api/plugins/install-archive` | `handleInstallArchive` (`internal/api/plugins.go:456`) | No signature verification ("local trust model" per doc comment, line 453); path confined via `pathsafe.ResolveUnder` (line 537) | Same as install-local |
| CLI `nanite plugin install <name\|path>` | `cmd/nanite` subcommand | `plugin_cmd.go` → `plugin_install_flow.go` → `buildInstaller` (line 102), `install.Installer.Install` | Strong: `install.SignatureVerifier` + `install.DirStaging` (`validatePluginID` confinement) + `catalog.SignedFetcher`/`KeyRing` when catalog-sourced | Healthy — this is the pipeline to converge onto |

**Note on scope of "all callers":** `handleInstall`/`handleInstallLocal`/`handleInstallArchive` are not currently catalog/signature flows — they're git-clone, local-directory, and raw-archive-upload flows respectively, each with its own (already-fixed) trust model. This task's mandatory convergence target is specifically `handleCatalogInstall`, the one catalog-driven, signature-bearing GUI/API path. Whether the other three should *also* be re-plumbed through `install.Installer` for consistency (rather than just matching its path-confinement pattern) is part of what the architect decision below should settle — don't silently expand scope to "rewrite all four handlers" without that sign-off, and don't silently narrow to "only fix `entry.Name`'s path-traversal" without addressing the signature bypass, which is the more severe of the two findings.

### Proposed direction

1. **Get architect sign-off first** (this task's `requires_architect_decision: true`) on the concrete integration shape. Open questions to bring to that decision, informed by the tracing above:
   - Does `handleCatalogInstall` construct an `install.Installer` directly (mirroring `buildInstaller`), or does the API layer get a shared constructor (e.g. `catalog.NewAPIInstaller(...)`) usable from both `cmd/nanite` and `internal/api`, to avoid a second hand-rolled wiring site?
   - What `install.Source` implementation represents "a catalog entry fetched over HTTP with a checksum/signature attached" for the API path — likely near-identical to `plugin_install_flow.go`'s `catalogArchiveSource` (lines 67-89); can it be shared, or does it need an API-specific variant (e.g. because the API path streams progress events to `pluginHost.EmitPluginInstallProgress` where the CLI streams to stdout)?
   - What `install.Loader` implementation hot-loads into the running `Host` for the API path (the CLI's `noopLoader` doesn't apply) — likely a thin adapter around the existing `pms.runPluginLoadIntoHost` helper (already used by the other three API handlers) or `cs.pluginHost`'s existing hot-load path (`internal/api/catalog.go:436-441`).
   - Which path-confinement mechanism becomes canonical for this converged pipeline — `pathsafe.ResolveUnder` (used by 3 of 4 existing API handlers) or `validatePluginID`-style allowlist (used by `DirStaging.Commit`)? The CLI's `DirStaging` already does its own internal confinement via `validatePluginID`; if the API path routes through `DirStaging.Commit`, it inherits that confinement for free and doesn't need a second `pathsafe.ResolveUnder` call — but this needs an explicit decision, not silent inconsistency.
   - Does the seeded default catalog source's `INSERT` (currently omitting `public_key`) need a follow-up — should the default/official catalog source ship with a real trusted public key so verification is meaningfully enforced (not just "no key configured → fail closed with an error" but "the intended common case actually verifies against something")? This may be a separate task if it requires provisioning/distributing an actual signing key — flag it as a dependency/follow-up rather than silently scoping it in or out here.
   - Is `internal/plugin/catalog.go` + `internal/plugin/signature.go` fully retired, or does `CatalogFetcher` (the *browsing*, non-install-writing half) stay for `handleBrowseCatalog`/`handleRefreshCatalog`? The audit's recommendation ("retire the old CatalogFetcher/VerifyChecksum/VerifySignature once the caller is migrated (no other callers today)") suggests full retirement is intended, but confirm `CatalogFetcher.Fetch` (used for browsing, not just installing) doesn't need to stay — if browsing still needs it, only `VerifyChecksum`/`VerifySignature` from `signature.go` are dead-after-migration, not `catalog.go` itself.

2. Once the shape is decided, implement `handleCatalogInstall` against the converged pipeline: fail closed on missing/invalid signature in production builds (matching the CLI's already-correct `devmode`-gated behavior exactly — don't reimplement the gate, reuse `install.SignatureVerifier`), and confine the target path via whichever mechanism the architect decision selected.
3. Migrate or explicitly retire the legacy `VerifyChecksum`/`VerifySignature`/`CatalogFetcher.Fetch`-for-install-purposes call sites once `handleCatalogInstall` no longer depends on them.
4. Add regression tests (see below) that exercise the real HTTP handler path, not just the underlying library functions in isolation — the audit specifically flagged "zero test coverage for this handler at all" as part of the finding.

### Non-goals

- **Do not redesign the signature/trust model itself.** `KeyRing`, the Ed25519 scheme, the `devmode` build-tag gate, and `SignedFetcher`'s catalog-signing approach are already solid per the audit (§8.6 "Reviewed and found healthy") — this task needs the GUI/API path to *use* that existing model, not invent a new one.
- Do not rewrite `internal/plugin/install/install.go`'s state machine, `staging.go`'s atomic-rename commit logic, or `extract.go`'s `TarGzExtractor` — all confirmed healthy by the audit and used as-is.
- Do not change CLI (`nanite plugin install`) behavior as a side effect of sharing code with the API path — if refactoring `plugin_install_flow.go` to extract a shared constructor, the CLI's own test suite (`internal/plugin/install/*_test.go`, `cmd/nanite/plugin_install_flow_test.go` if it exists) must still pass unchanged.
- Do not silently decide the "does the default catalog source get a real public key provisioned" question inside this task if it turns out to require external key-distribution work — flag it as a follow-up per this task's Dependencies section instead.
- Do not expand scope to rewrite `handleInstall`/`handleInstallLocal`/`handleInstallArchive`'s already-fixed path-confinement pattern unless the architect decision explicitly calls for consistency across all four handlers.

### Dependencies

- None hard-blocking. Soft/sequencing note: task `02-wire-allow-unsigned-plugins-setting.md` in this same folder touches the same `SignatureVerifier` construction site this task will likely need to reuse or extend for the API path — see that task's Context for the sequencing rationale (not a technical blocker either direction, but doing them in the same pass avoids two separate edits to the same construction code).
- Possible follow-up (not this task's scope): provisioning a real public key for the default/official seeded catalog source, if the architect decision above determines the current "no key configured" default state should change.

### Tests required

- A regression test that POSTs to `/api/plugins/catalog/install` with a catalog entry carrying no signature (and/or a source with no configured public key, matching today's default-seeded-source state) against a **production-mode** build/test configuration, and asserts the install is **rejected** — not silently allowed. Follow the existing test harness pattern in `internal/api/plugins_install_test.go` (`setupPluginTestState`, `httptest.NewRequest`/`httptest.NewRecorder`).
- A path-traversal regression test for `handleCatalogInstall`'s `entry.Name`, mirroring `TestHandleInstall_PathTraversal` (`internal/api/plugins_install_test.go:201`) — a catalog entry named e.g. `../../etc/passwd` must be rejected with a 400 and an error mentioning the escape/invalid-name condition, not proceed to write outside `pluginsDir`.
- A positive-path regression test confirming a validly-signed, checksummed catalog entry still installs successfully end-to-end through the new pipeline (protects against the fail-closed fix becoming fail-always).
- If the architect decision routes the API path through `install.Installer`/`DirStaging` directly, confirm `internal/plugin/install/`'s existing devmode-bypass tests (`verify_dev_bypass_test.go`, `verify_prod_enforcement_test.go`) still hold for this new caller — no new devmode-tagged code path should exist outside what's already covered.

### Prevention

- The regression tests above become the durable prevention mechanism for this specific defect recurring.
- Per the remediation guide's "Security/Correctness Migration Completeness" standard (§4 Wave 7): *"When a security or correctness fix replaces a primitive or pipeline, enumerate and verify every production caller of the superseded implementation."* This task's "All production callers" table above is that enumeration; a future architect/reviewer pass should re-run the same `grep -rn "HandleFunc"` / `buildInstaller(` sweep before considering this closed, to confirm no new plugin-install entry point was added elsewhere without going through the converged pipeline.
- Consider (as part of the architect decision, not unilaterally) whether a lint rule or code-review checklist item should flag any future direct `filepath.Join(pluginsDir, ...)` call in `internal/api` outside the shared confinement helper, to prevent a fifth handler from reintroducing the same bug class.

### Verification

```bash
go build ./...
go vet ./...
go test ./internal/api/... -run 'Catalog|Install' -v
go test ./internal/plugin/... -v
go test -race ./internal/api/... ./internal/plugin/...
staticcheck ./internal/api/... ./internal/plugin/...
gosec ./internal/api/... ./internal/plugin/...
```

Observable behavior required for PASS: a manual (or automated, if the test harness supports it) POST to `/api/plugins/catalog/install` with an unsigned catalog entry, against a binary built without the `devmode` tag, returns a non-2xx response and does not write any files under the plugins directory.

### Risk / rollback

- **Regression surface:** the GUI "Plugin Manager" catalog-browse-and-install flow is the primary user-facing feature at risk of breaking if the convergence introduces a behavioral regression (e.g. a previously-working unsigned/no-checksum install from a source an operator trusts manually now gets rejected with no override). This is a deliberate consequence of fixing GO-PLUGIN-001, not a bug — but it should be called out in the PR/task write-up so it isn't mistaken for one, and the architect decision should consider whether an explicit, visible "trust this source anyway" opt-in belongs in this task's scope or is a separate follow-up.
- **Rollback approach:** since this task converges an entry point onto an existing, already-tested pipeline rather than introducing new untested logic, rollback is a straightforward revert of the `handleCatalogInstall` changes back to the pre-task implementation; the underlying `internal/plugin/install/` pipeline is unmodified by this task except possibly to add an API-specific `Source`/`Loader` adapter type, which can be reverted independently without touching CLI behavior.

### Done means

- `handleCatalogInstall` fails closed on missing/invalid signature in a production (non-`devmode`) build — verified by the regression test above, not just by code inspection.
- `handleCatalogInstall`'s target path is confined via the architect-selected canonical mechanism — verified by the path-traversal regression test above.
- The "All production callers" table in this task is re-confirmed accurate against current source at implementation time (line numbers may have shifted since this task was authored) before the worker starts editing.
- All new and existing tests listed under "Tests required" pass, including `-race`.
- The legacy `VerifyChecksum`/`VerifySignature` functions in `internal/plugin/signature.go` either have zero remaining production callers (confirmed via grep) or their continued existence is explicitly justified in the Work Log with a reason.
- `go build ./...`, `go vet ./...`, and the verification commands above all pass clean.

## Work log

## Review notes
