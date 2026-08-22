# Unify the plugin-install security pipeline across CLI, GUI, and API

**Phase:** Audit remediation — Wave 1 (release-blocking trust boundaries)
**Status:** implemented
**Depends on:** none (this task is self-contained; task 02 in this folder is sequenced after/with it — see that task's Context for why)
**Touches:** `internal/api/catalog.go`, `internal/api/plugins.go` (read-only reference for the already-fixed pattern), `internal/plugin/catalog.go`, `internal/plugin/signature.go`, `internal/plugin/catalog/` (`fetch.go`, `trust.go`, `trustedkeys.go`), `internal/plugin/install/` (`install.go`, `verify.go`, `staging.go`, `extract.go`, `download.go`), `cmd/nanite/plugin_install_flow.go` (reference only — do not change the CLI path's behavior), `internal/plugin/devmode/` (reference only)
**requires_architect_decision:** true — flagged per the remediation guide's §9 decision queue. The direction (converge onto the CLI's pipeline) is not in serious doubt given the audit's evidence, but the blast radius (this is the single most severe finding in the whole audit, touches a user-facing "Plugin Manager" flow, and requires deciding exactly how catalog-sourced archives map onto the CLI's `install.Source`/`Handle`/`Installer` abstractions) means an architect should sign off on the concrete integration shape before a worker starts, not discover it mid-implementation.

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 1 — release-blocking trust boundaries · **Dispatch unit:** `W1`
> - **Depends on:** `00/01`, `00/02`
> - **Blocks:** `01/02`, `08/09`, `11/15`
> - **Parallel-safe with:** `02/01`, `02/02`, `03/01`, `12/02`
> - **Gated on:** AD-04 (concrete integration shape). AD-05 is a follow-up, not a blocker.
> - **requires_security_review:** true · **requires_regression_test:** true

> ## ✅ AD-04 DECIDED (2026-08-22) — all six sub-questions resolved; implement, do not re-open
>
> Converge `handleCatalogInstall` onto the CLI's `install.Installer`. The
> "Proposed direction" section below asks six questions; here are the answers.
> Where they differ from anything below, **this banner wins**.
>
> 1. **Confinement: `validatePluginID` via `install.DirStaging.Commit`** — route
>    the API path through DirStaging and inherit its allowlist plus atomic
>    backup-then-rename. Do *not* add a second `pathsafe.ResolveUnder` call on
>    this path. (This does not relieve `08/09`/`12/01` of widening the
>    `forbidigo` rule to `internal/api/` — that guards the handlers this task
>    does not re-plumb.)
> 2. **Archive formats: keep `.zip` AND `.tar.gz`** via a format-dispatching
>    `install.Extractor` that reuses the API's existing
>    `extractZip`/`extractTarGz`. **This was a gap in this task file.**
>    `internal/api/catalog.go:388-391` dispatches on format; the CLI installer
>    is wired `TarGzExtractor`-only, so a naive convergence would silently drop
>    zip support and break externally-hosted catalog entries you cannot
>    enumerate. Add a regression test covering a `.zip` catalog entry.
> 3. **Loader: thin adapter around `pms.runPluginLoadIntoHost`**
>    (`internal/api/plugins.go:839`), already used by all four API handlers.
>    The CLI's `noopLoader` does not apply — it runs out-of-process.
> 4. **Source: reuse `catalogArchiveSource`**
>    (`cmd/nanite/plugin_install_flow.go:66-89`) — it already carries
>    sha256/signature/signerKey into `install.Handle`. Parameterise the progress
>    emitter (API emits to `pluginHost.EmitPluginInstallProgress`, CLI to
>    stdout) rather than forking the type.
> 5. **Legacy retirement is narrower than this file assumes.** Retire
>    `internal/plugin/signature.go`'s `VerifyChecksum`/`VerifySignature` — each
>    has exactly one caller, both in `handleCatalogInstall` (`catalog.go:358`,
>    `:367`), so both go fully dead. **Keep `internal/plugin/catalog.go`'s
>    `CatalogFetcher`**: `cs.fetcher` is load-bearing for browse/refresh at
>    `catalog.go:97, 138, 148, 205, 242, 281`. The audit's "retire the old
>    CatalogFetcher" recommendation is wrong on this point — do not follow it.
> 6. **Extract a shared constructor** usable from both `cmd/nanite` and
>    `internal/api`. A second hand-rolled `Installer` wiring site is exactly how
>    the original divergence happened. CLI behaviour must not change; its
>    existing tests pass unmodified.
>
> **AD-05** (provision a real signing key for the default seeded catalog
> source) remains open and does **not** gate this task. Do not scope it in or
> out silently — flag it as a follow-up per this file's Dependencies section.

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

Implemented per the `✅ AD-04 DECIDED` banner's six numbered answers, executed
exactly, not re-derived. Worked from the worktree at
`.claude/worktrees/agent-a484015a1264996dd/` (all paths below are relative to
repo root within that worktree).

### Re-confirmed "All production callers" table (at implementation time)

Re-ran the task's own sweep before editing, and again after, to confirm no
drift and no line-number staleness beyond what's expected from the edits
themselves:

```
grep -rn "HandleFunc" internal/api/catalog.go internal/api/plugins.go
grep -rn "buildInstaller(" cmd/nanite/*.go
```

| Entry point | Route/command | Handler | Pipeline used (after this task) | Status |
|---|---|---|---|---|
| GUI "Plugin Manager" catalog install | `POST /api/plugins/catalog/install` (registered `internal/api/catalog.go:50`) | `handleCatalogInstall` (`internal/api/catalog.go:256`) | Converged: `install.Installer` via the shared `install.NewInstaller` constructor — `install.SignatureVerifier` (fail-closed in production), `install.DirStaging.Commit` (confinement + atomic commit), a format-dispatching `catalogExtractor` (zip/tar.gz), a `hostLoader` adapter around `pms.runPluginLoadIntoHost` | **Fixed — this task's primary target** |
| GUI "Plugin Manager" install-by-name (git clone) | `POST /api/plugins/install` (registered `internal/api/plugins.go:99`) | `handleInstall` (`internal/api/plugins.go:317`) | Unchanged: no signature verification (git-clone trust model); path confined via `pathsafe.ResolveUnder` | Untouched (Non-goals: out of scope unless AD-04 said otherwise — it didn't) |
| GUI "Plugin Manager" local install | `POST /api/plugins/install-local` (registered `internal/api/plugins.go:100`) | `handleInstallLocal` (`internal/api/plugins.go:386`) | Unchanged: no signature verification (local trust model); path confined via `pathsafe.ResolveUnder` | Untouched |
| GUI "Plugin Manager" archive upload | `POST /api/plugins/install-archive` (registered `internal/api/plugins.go:101`) | `handleInstallArchive` (`internal/api/plugins.go:456`) | Unchanged: no signature verification (local trust model); path confined via `pathsafe.ResolveUnder` | Untouched |
| CLI `nanite plugin install <name\|path>` | `cmd/nanite` subcommand | `plugin_cmd.go` → `plugin_install_flow.go` → `buildInstaller` (`cmd/nanite/plugin_install_flow.go:79`) → `install.Installer.Install` | Same strong pipeline as before (`install.SignatureVerifier` + `install.DirStaging` + `catalog.SignedFetcher`/`KeyRing`), now built via the shared `install.NewInstaller` constructor instead of a hand-rolled struct literal | Unchanged behavior — refactored construction site only (AD-04 item 6); CLI's own test suite (`internal/plugin/install/*_test.go`, `cmd/nanite/plugin_install_flow_test.go`) passes unmodified |

### What was implemented, mapped to the banner's six items

1. **Confinement.** `handleCatalogInstall` calls the newly-exported
   `install.ValidatePluginID` (renamed from the package-private
   `validatePluginID` in `internal/plugin/install/staging.go`) on
   `entry.Name` immediately after the catalog lookup, before any network
   I/O — the exact same allowlist `install.DirStaging.Begin`/`Commit` apply
   internally, not a second/different confinement mechanism. No
   `pathsafe.ResolveUnder` call was added to this path. The install itself
   routes through `install.DirStaging.Commit` (via `install.NewInstaller`),
   inheriting its atomic backup-then-rename commit.
2. **Archive formats.** Added `catalogExtractor`
   (`internal/api/catalog_install.go`) implementing `install.Extractor`,
   dispatching on `entry.ArchiveURL`'s `.zip` suffix and delegating to the
   existing, already-audited `extractZip`/`extractTarGz` helpers in
   `internal/api/plugins.go` — unchanged. Added a dedicated regression test,
   `TestHandleCatalogInstall_Success_Zip`, exercising a signed `.zip`
   catalog entry end-to-end (the gap the banner itself flagged).
3. **Loader.** Added `hostLoader` (`internal/api/catalog_install.go`), a
   thin adapter around `pms.runPluginLoadIntoHost` — the same helper all
   four API install handlers already use. Deliberately preserves the
   existing handlers' convention of not surfacing a hot-load failure as an
   HTTP error (files are already safely committed to disk by that point;
   `runPluginLoadIntoHost`'s bool result is discarded, matching
   `handleInstall`/`handleInstallLocal`/`handleInstallArchive`'s own
   behavior) rather than introducing a new failure mode AD-04 didn't ask
   for.
4. **Source.** Moved `catalogArchiveSource` out of
   `cmd/nanite/plugin_install_flow.go` into the shared
   `internal/plugin/install` package as exported
   `install.CatalogArchiveSource` (`internal/plugin/install/source.go`).
   Both `cmd/nanite`'s `installFromCatalog` and
   `internal/api`'s `handleCatalogInstall` construct it directly; the
   progress emitter is not part of the type (it was never coupled to it —
   `Installer.Emit` is what `Source.Download`'s `emit` parameter forwards),
   so no forking was needed, only relocation + field export.
5. **Legacy retirement.** Removed `internal/plugin/signature.go`'s
   `VerifySignature` (its one caller, `handleCatalogInstall`, is gone) and
   `internal/plugin/catalog.go`'s `VerifyChecksum` (same). Kept
   `internal/plugin/catalog.go`'s `CatalogFetcher` in full — `cs.fetcher` is
   still load-bearing for `handleBrowseCatalog`/`handleRefreshCatalog`/
   `handleCatalogInstall`'s own catalog lookup (`catalog.go:97, 191, 205,
   242, 276`, current line numbers). `GenerateKeyPair`/`SignFile` in
   `signature.go` were left untouched — the banner doesn't name them, they
   have no production caller either way (utility for catalog maintainers
   per their own doc comment), and removing them wasn't asked for. Updated
   `internal/plugin/signature.go`'s package-level doc comment to point at
   `install.SignatureVerifier` as the real verification path.
6. **Shared constructor.** Added `install.NewInstaller`/`BuildOptions`
   (`internal/plugin/install/build.go`) as the one canonical `Installer`
   wiring site. `cmd/nanite/plugin_install_flow.go`'s `buildInstaller` and
   `internal/api/catalog.go`'s `handleCatalogInstall` both call it now.
   Also extracted the CLI's private `cliValidator`/`pluginYAMLPresent` into
   an exported `install.ManifestValidator` (same file) so both callers share
   the one manifest-validation step, not two copies.

### Deviations from the task file's own draft (all logged, none silent)

- **Path confinement pre-check vs. "no second mechanism."** The task file's
  Context worried about picking between `pathsafe.ResolveUnder` and
  `validatePluginID`-style confinement as *the* canonical mechanism. AD-04
  settled that (DirStaging/`validatePluginID`), but the exported
  `install.ValidatePluginID` early-exit call added here (before any network
  I/O, so a malicious catalog entry name is rejected fast instead of only
  after a full download+staging cycle) is new surface not explicitly spelled
  out in the banner. Treated as "inheriting its allowlist" (the banner's own
  phrase) rather than a second mechanism, since it calls the exact same
  function `DirStaging.Commit` calls internally — not a reimplementation.
- **Dropped the old handler's "plugin.yaml at root OR single top-level
  subdir" tolerance.** `handleCatalogInstall`'s pre-convergence code
  detected a single wrapper directory in the extracted archive and treated
  it as the plugin root. `install.Installer`'s state machine calls
  `Validator.Validate(ctx, stagingDir)` directly against the extraction
  root with no such fallback (matching the CLI's own contract, which the
  Non-goals section explicitly says not to rewrite). Preserving the
  tolerance would have meant adding a step the state machine has no hook
  for, which is out of scope per Non-goals ("do not rewrite
  `internal/plugin/install/install.go`'s state machine ... used as-is").
  Catalog-sourced archives converged through this pipeline are now expected
  to ship `plugin.yaml` at the archive root, same as the CLI's own signed
  catalog already requires. Flagging this as a real, if minor, behavioral
  difference for anyone dogfeeding the GUI's catalog-install flow against a
  catalog whose archives use a wrapper directory.
- **Dropped the "signed-but-unsigned-plugin" warn-and-proceed branch.** The
  old code's `else if sourcePublicKey != "" && entry.Signature == ""`
  branch logged a warning and let the install proceed anyway. Under the
  converged pipeline this case is indistinguishable from "no signature at
  all" and is rejected outright (fail-closed), which is the entire point of
  GO-PLUGIN-001's fix — not treated as a deviation needing separate
  sign-off, since it's the direct, intended consequence of AD-04's decision
  and the Risk/rollback section's own framing (a previously-permissive path
  now rejects) already anticipated exactly this class of change.
- **Progress-event granularity on the failure path changed slightly.** The
  pre-convergence code emitted `plugin.install_progress` with `state` set
  to whichever step failed (e.g. `"downloading"`). `install.Installer.fail`
  always transitions to and emits `StateFailed` ("failed") on any failure,
  with the failing phase preserved only in the message text (`"failed in
  downloading"`) and the wrapped error. Not corrected — this is inherent to
  reusing the shared, already-tested state machine unmodified (Non-goal:
  don't rewrite `install.go`), not something this task's own construction
  code controls. Happy-path progress state strings are unchanged
  (`install.State`'s own constants already matched the old ad hoc strings
  byte-for-byte: `"downloading"`, `"verifying"`, `"extracting"`,
  `"validating"`, `"loading"`, `"ready"`).
- **`checksumFile` in `internal/api/catalog.go` is pre-existing dead code,
  left untouched.** Confirmed via grep it had zero callers even before this
  task (not introduced or orphaned by this change) — out of scope per the
  task's own file-list ("`internal/api/catalog.go` — `handleCatalogInstall`
  and its supporting helpers"; `checksumFile` isn't one of
  `handleCatalogInstall`'s helpers, it's an unrelated pre-existing orphan).
  Not removed, to avoid scope creep beyond AD-04's six items.
- **`docs/audits/2026-08-21-go-quality/REPORT.md`'s "retire the old
  CatalogFetcher" recommendation is confirmed wrong**, exactly as AD-04's
  item 5 already stated — `CatalogFetcher.Fetch` is load-bearing for browse
  and now also for the install path's own catalog lookup. No action beyond
  what AD-04 already directed; noted here per the task file's own
  instruction to record when a decision-log/audit recommendation doesn't
  hold up against current code (it doesn't reopen the decision, AD-04
  already overrode it).
- **AD-05** (provisioning a real signing key for the default/official
  seeded catalog source) was not scoped in or out silently. It remains open
  and untouched by this task, as the banner directs — the default seeded
  source still has no `public_key` configured, so
  `catalogKeyLookup`/`SignatureVerifier.Verify` reject any install attempt
  against it with "unknown signer key id" until an operator either sets a
  key via `PUT /api/plugins/catalog/sources/{id}/key` or an AD-05 follow-up
  ships. This is the intended, documented (Risk/rollback section)
  consequence of the fix, not a regression to chase down in this task.

### Files touched

- `internal/plugin/install/staging.go` — exported `validatePluginID` →
  `ValidatePluginID` (2 internal call sites updated, doc comment expanded).
- `internal/plugin/install/build.go` (new) — `BuildOptions`,
  `install.NewInstaller`, `ManifestValidator` (the shared construction
  site).
- `internal/plugin/install/source.go` (new) — `CatalogArchiveSource`
  (moved/exported from `cmd/nanite`).
- `cmd/nanite/plugin_install_flow.go` — removed the private
  `catalogArchiveSource`/`cliValidator`/`pluginYAMLPresent`; `buildInstaller`
  now calls `install.NewInstaller`; `installFromCatalog` constructs
  `install.CatalogArchiveSource`. No behavioral change; CLI's own tests
  (`internal/plugin/install/*_test.go`, `cmd/nanite/plugin_install_flow_test.go`)
  pass unmodified.
- `internal/api/catalog.go` — `handleCatalogInstall` rewritten to converge
  onto `install.Installer` via `install.NewInstaller`; `findSourcePublicKey`
  kept and repurposed (now called from `catalog_install.go`); removed the
  `naniteplugin.VerifyChecksum`/`VerifySignature` calls and the manual
  download/verify/extract implementation; `log/slog` import dropped (its
  only use was the removed warn-and-proceed branch).
- `internal/api/catalog_install.go` (new) — `catalogKeyLookup`,
  `catalogExtractor`, `hostLoader`, `catalogState.catalogInstallEmit`,
  `stripChecksumPrefix`, `decodeCatalogSignature`,
  `catalogInstallErrorStatus`.
- `internal/api/catalog_install_test.go` (new) — regression tests (see
  below).
- `internal/plugin/catalog.go` — removed `VerifyChecksum` (dead after
  migration per AD-04 item 5).
- `internal/plugin/catalog_test.go` — removed `TestVerifyChecksum`/
  `TestVerifyChecksum_BadFormat`; dropped now-unused `os` import.
- `internal/plugin/signature.go` — removed `VerifySignature`; updated the
  package doc comment to point at `install.SignatureVerifier`.
- `internal/plugin/signature_test.go` — removed the four `VerifySignature`-
  dependent tests; replaced `TestSignAndVerify` with
  `TestSignFile_ProducesValidSignature`, which asserts `SignFile`'s output
  directly against `crypto/ed25519.Verify` instead of the retired helper
  (kept coverage of correct-key/wrong-key/tampered-content cases).

### Tests added (`internal/api/catalog_install_test.go`)

- `TestHandleCatalogInstall_RejectsUnsignedEntry` — production-mode
  (no `devmode` tag), unsigned entry + source with no configured public key
  (today's default-seeded-source state) → non-2xx, nothing written under
  `pluginsDir`.
- `TestHandleCatalogInstall_PathTraversal` — catalog entry named
  `../../etc/passwd` → 400, error contains "invalid plugin name", nothing
  written outside `pluginsDir` (asserted by listing `pluginsDir`'s own
  contents, not just checking the specific escape target).
- `TestHandleCatalogInstall_Success_TarGz` — validly-signed, checksummed
  `.tar.gz` entry installs end-to-end (200, `plugin.yaml` present under
  `pluginsDir`).
- `TestHandleCatalogInstall_Success_Zip` — same, but `.zip` (AD-04 item 2's
  explicitly-requested regression test).
- `TestHandleCatalogInstall_WrongSignature` — checksum-valid but
  wrong-key-signed entry → rejected, nothing written.
- `TestHandleCatalogInstall_AlreadyInstalled` — 409 when the plugin dir
  already exists (parity with the sibling handlers' own coverage).
- `TestHandleCatalogInstall_NotFound` — 404 when the name isn't in any
  configured catalog source.

Devmode-bypass survival: no new `devmode`-tagged code was added by this
task (the API path reuses `install.SignatureVerifier` unmodified). Verified
existing `internal/plugin/install/verify_dev_bypass_test.go` and
`verify_prod_enforcement_test.go` still hold under both `go test
./internal/plugin/install/...` and `go test -tags devmode
./internal/plugin/install/...` — both pass.

### Baseline check (run repeatedly through the session, not just at the end)

```
go build ./...                                          # clean
go vet ./...                                             # clean except 2 pre-existing findings in
                                                           # internal/service/container.go, unrelated to
                                                           # this diff (not touched by this task)
go build -tags devmode ./...                              # clean
go test ./...                                             # all packages pass
go test -tags devmode ./internal/plugin/... ./internal/plugin/install/...   # all pass
go test ./internal/api/... -run 'Catalog|Install' -v      # all pass, including the new tests and the
                                                           # existing TestHandleInstall_PathTraversal /
                                                           # TestHandleInstallLocal_ManifestTraversal etc.
go test ./internal/plugin/... -v                          # all pass
go test -race ./internal/api/... -run 'Catalog|Install' -v  # all pass, 0 DATA RACE reports, ~146s
go test -race ./internal/plugin/...                        # ok, ~93s
gosec ./internal/api/... ./internal/plugin/...             # 98 pre-existing findings, none in the new
                                                           # files (build.go, source.go, catalog_install.go)
                                                           # or in the touched portion of catalog.go
staticcheck ./internal/api/... ./internal/plugin/...       # 27 pre-existing findings (U1000/SA1019 in
                                                           # files this task never touched, e.g.
                                                           # adapter-nanite-native, host.go, panels.go,
                                                           # subprocess/plugin_test.go), plus one
                                                           # confirming checksumFile (internal/api/
                                                           # catalog.go) is genuinely unused — the
                                                           # pre-existing dead helper already identified
                                                           # and deliberately left alone above. Zero
                                                           # findings in any new/changed file this task
                                                           # added (build.go, source.go,
                                                           # catalog_install.go, or the edited portions
                                                           # of catalog.go/signature.go/plugin's own
                                                           # catalog.go).
gofmt -l <every touched/new file>                          # clean except 2 pre-existing, unrelated
                                                           # struct-tag misalignments (CatalogEntry in
                                                           # internal/plugin/catalog.go, catalogEntryLite
                                                           # in cmd/nanite/plugin_install_flow.go) —
                                                           # confirmed via git diff neither struct was
                                                           # touched by this task; left as-is
```

**One deliberate exception:** `go test -race ./internal/api/...` (the
*whole* package, unscoped) hits a pre-existing 600s default-timeout
artifact unrelated to this diff — already independently tracked as its own
Wave 2 investigation task,
`TASKS/audit-remediation/04-container-reaper-lifecycle/03-investigate-internal-service-race-timeout.md`
(`GO-SVCCORE-006`), which documents the same package-wide `-race` timeout
and explicitly rules out the container-shutdown-leak explanation as the
sole cause. Confirmed this is not a race in the code this task touched by
running `go test -race ./internal/api/... -run 'Catalog|Install'` (146s,
0 `DATA RACE` reports, all pass) — the scoped run isolates exactly the code
this task modified from that separately-tracked systemic issue. Not
re-investigated here; out of this task's scope per its own Touches list.

### Legacy `VerifyChecksum`/`VerifySignature` status (Done means)

Both fully removed (zero remaining callers of any kind, production or
test) rather than left in place with a justification — matches AD-04 item
5's "go fully dead on migration" and the project's standing
aggressive-removal-on-genuinely-settled-dead-code default. `CatalogFetcher`
was explicitly kept per the same item.

### Follow-up (2026-08-22): restored the wrapper-directory extraction tolerance

A fresh reviewer with no shared context re-reviewed the implementation above
and gave an overall PASS, but flagged one real gap: the pre-convergence
`handleCatalogInstall` (confirmed via `git show
main:internal/api/catalog.go:399-410`) tolerated an extracted archive whose
`plugin.yaml` isn't at the extraction root but inside exactly one
subdirectory — the shape a plain GitHub "Download ZIP" produces
(`reponame-branch/`). The convergence pass above (see "Deviations from the
task file's own draft" → "Dropped the old handler's 'plugin.yaml at root OR
single top-level subdir' tolerance") deliberately dropped this rather than
add a hook to `install.Installer`'s state machine. The reviewer's assessment
— that `install.Extractor` is the right seam for this, since `catalogExtractor`
(already a new type in a new file, `internal/api/catalog_install.go`) can
extract to a scratch location and flatten a detected single-wrapper-directory
shape into `targetDir` before returning, without touching `install.go`,
`staging.go`, or `extract.go`'s `TarGzExtractor` — was correct. Operator
decision: restore the tolerance. Preserve the prior GUI/API behavior
(installing from a plain "Download ZIP"-shaped archive should still work),
consistent with how AD-04 item 2's zip-format gap was handled — fixed
in-scope, not silently dropped.

**What changed:**

- `internal/api/catalog_install.go` — `catalogExtractor.Extract` now
  extracts into a scratch subdirectory of `targetDir` (via `os.MkdirTemp`,
  same filesystem as `targetDir` since `targetDir` is itself the staging
  dir `DirStaging.Begin` created under `StagingRoot`), resolves the plugin
  root against that scratch dir via a new `resolveCatalogPluginRoot`
  helper — matching the old handler's exact detection rule byte-for-byte
  (plugin.yaml at the extraction root wins outright; otherwise, if there's
  exactly one subdirectory, its contents become the plugin root; zero or
  more than one candidate falls through unresolved, deferring rejection to
  the existing `Validator.Validate` "plugin.yaml missing" error rather than
  guessing among ambiguous subdirectories) — and flattens the resolved
  plugin root's immediate children up into `targetDir` via a new
  `flattenCatalogPluginRoot` helper (per-entry `os.Rename`, cheaper than a
  second full-tree copy since both dirs are already on the same
  filesystem). The scratch dir (and anything left in it, including stray
  root-level content alongside a wrapper directory) is removed via
  `defer os.RemoveAll` afterward — matching the old code's own behavior of
  only ever copying `pluginRoot`'s tree into the target, nothing else.
- No changes to `internal/plugin/install/install.go`'s state machine,
  `staging.go`, or `extract.go`'s `TarGzExtractor` — the fix is entirely
  contained within `catalogExtractor`, as the reviewer's own analysis
  confirmed was possible and as this follow-up's instructions required.
- `internal/api/catalog_install_test.go` — added
  `TestHandleCatalogInstall_Success_WrapperDirectory`: builds an in-memory
  `.zip` with `plugin.yaml` and a sibling `README.md` inside a single
  `wrapplug-main/` wrapper directory (not at the archive root), signs and
  checksums it, POSTs it through the real `handleCatalogInstall` HTTP
  handler, and asserts a 200, `plugin.yaml`/`README.md` present directly
  under `pluginsDir/wrapplug` (not nested under the wrapper-dir path), and
  no leaked `catalog-extract-scratch-*` directory in the committed plugin
  dir. All pre-existing flat-root tests
  (`TestHandleCatalogInstall_Success_TarGz`,
  `TestHandleCatalogInstall_Success_Zip`, and the rest of the suite) pass
  unmodified — this change is purely additive for the flat-root case, since
  `resolveCatalogPluginRoot` returns the extraction root unchanged whenever
  `plugin.yaml` is already there.

**Baseline check (this follow-up):**

```
go build ./...                                                     # clean
go build -tags devmode ./...                                       # clean
go vet ./internal/api/... ./internal/plugin/...                    # clean
go test ./internal/api/...                                         # ok, 66.257s
go test ./internal/plugin/...                                      # ok (all subpackages)
go test ./internal/api/... -run 'TestHandleCatalogInstall' -v      # all 8 pass (7 pre-existing + new)
go test -race ./internal/api/... -run 'TestHandleCatalogInstall' -v  # all 8 pass, 0 DATA RACE reports, 68.6s
go test ./internal/api/... -run 'Catalog|Install' -v                # all pass
gofmt -l internal/api/catalog_install.go internal/api/catalog_install_test.go  # clean
```

No further deviations. `install.CatalogArchiveSource`, `install.NewInstaller`,
`install.DirStaging`, and `install.TarGzExtractor` (the CLI's own extractor)
are untouched by this follow-up — the CLI path continues to use
`TarGzExtractor` directly and does not go through `catalogExtractor` at all,
so this change has zero effect on CLI behavior.

## Review notes

**2026-08-22, fresh reviewer round 1 (no shared context with the worker). Verdict: PASS, with one item requiring operator attention and one minor documentation nit.**

Reviewed the diff (`internal/api/catalog.go`, `internal/plugin/install/staging.go`, `internal/plugin/catalog.go`/`catalog_test.go`, `internal/plugin/signature.go`/`signature_test.go`, `cmd/nanite/plugin_install_flow.go`, plus new `internal/api/catalog_install.go`/`catalog_install_test.go`, `internal/plugin/install/build.go`/`source.go`) against AD-04's six sub-answers, all independently confirmed correctly implemented:

1. **Confinement** — `install.ValidatePluginID` early exit + `DirStaging.Commit`; no second `pathsafe.ResolveUnder` added.
2. **Archive formats** — new `catalogExtractor` dispatches `.zip`/`.tar.gz`; `TestHandleCatalogInstall_Success_Zip` genuinely exercises the real handler over `httptest.NewServer`, ran under `-race`, passes.
3. **Loader** — `hostLoader` confirmed a thin wrapper around `pms.runPluginLoadIntoHost`, not a reimplementation.
4. **Source** — `catalogArchiveSource` moved to exported `install.CatalogArchiveSource`, used by both `cmd/nanite` and `internal/api`, not forked.
5. **Legacy retirement** — `grep` confirms zero production callers of `VerifyChecksum`/`VerifySignature`; `CatalogFetcher` confirmed still load-bearing at 6 real call sites.
6. **Shared constructor** — `install.NewInstaller`/`BuildOptions` confirmed called from both `cmd/nanite/plugin_install_flow.go` and `internal/api/catalog.go`, one canonical wiring site.

Re-ran the task's own production-caller grep sweep independently — every line number in the Work Log's table matches current source. `internal/api/plugins.go` shows zero diff (scope fence held — `handleInstall`/`handleInstallLocal`/`handleInstallArchive` untouched). `internal/plugin/catalog/`, `internal/plugin/devmode/`, `internal/plugin/install/verify.go`/`install.go` all show zero diff (trust model reused, not redesigned). Fail-closed behavior verified both by reading `SignatureVerifier.Verify` and by independently running `TestHandleCatalogInstall_RejectsUnsignedEntry`/`WrongSignature`/`PathTraversal` — all pass under `-race`. Full baseline independently re-run clean (`go build`, `go build -tags devmode`, `go vet`, `go test -race`, `go test -tags devmode ./internal/plugin/install/...`, `go test ./cmd/nanite/...`).

**Minor nit (non-blocking):** the Work Log's `CatalogFetcher` call-site citation has one wrong line number (191 instead of 138) — the underlying claim (still load-bearing, not dead) is correct and independently verified; just a citation-precision slip.

**Real finding requiring operator attention:** the converged pipeline dropped the pre-convergence handler's tolerance for an archive whose `plugin.yaml` sits in a single wrapper subdirectory (the shape a plain GitHub "Download ZIP" produces). The CLI's own `TarGzExtractor` never had this tolerance either, so it's not a capability the "good" pipeline lost — but the reviewer determined a clean, in-scope fix was available (extend the new `catalogExtractor`, without touching `install.go`'s state machine) and wasn't taken; the worker's own cited Non-goals fence didn't actually forbid it. Assessed as "should have been escalated, not resolved unilaterally," though disclosed (not silent) and not a security regression.

**Resolution:** operator decision, 2026-08-22 — restore the tolerance. Fix dispatched into the same worktree: `catalogExtractor` now detects a single-wrapper-subdirectory shape (matching the old handler's exact detection rule) and flattens it into `targetDir`, entirely within its own new file — `install.go`, `staging.go`, and `extract.go` remain untouched. New regression test `TestHandleCatalogInstall_Success_WrapperDirectory` added; all existing flat-root tests confirmed unmodified and still passing.

**2026-08-22, fresh reviewer round 2 (re-review of the wrapper-subdir fix, no shared context with either prior worker/reviewer). Verdict: PASS on the fix itself.**

Independently confirmed: detection logic (`resolveCatalogPluginRoot`) matches the old handler's exact rule (root `plugin.yaml` wins; else exactly one subdir promotes; else falls through to the existing "missing" rejection — verified with two extra ad hoc tests for the zero-subdir and two-subdir ambiguous cases, both correctly reject); `internal/plugin/install/install.go`/`extract.go` confirmed genuinely untouched; the new wrapper-directory test builds a real signed zip and POSTs through the actual HTTP handler, confirmed passing; all 8 `TestHandleCatalogInstall_*` subtests pass including under `-race`; full baseline re-run clean.

**New finding surfaced during this second review round, outside this follow-up's own scope:** the downloaded archive file is never cleaned up after extraction in either the API or CLI install path — every catalog-installed (and CLI-installed) plugin directory ends up permanently containing the archive file alongside the extracted plugin. A real regression versus the pre-convergence handler (which downloaded to a fully separate OS temp file). Not a security issue — hygiene/correctness only. **Not fixed as part of Wave 1** — logged as a new mid-batch discovery in `TASKS/ESCALATIONS.md` (2026-08-22 entry) per the batch's own rule for new findings, recommended as a fast-follow for whoever next touches `internal/plugin/install/`. Does not affect the correctness or completeness of this task's own GO-PLUGIN-001/002/003 security fixes.
