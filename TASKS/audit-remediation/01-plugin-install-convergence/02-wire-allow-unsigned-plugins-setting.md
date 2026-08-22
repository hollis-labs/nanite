# Wire (or retire) the allow_unsigned_plugins setting's promised CLI effect

**Phase:** Audit remediation — Wave 1 (release-blocking trust boundaries)
**Status:** reviewed
**Depends on:** `01-unify-plugin-catalog-install-pipeline.md` (this folder) — **sequencing only**, not a technical/compile dependency. See Context below for why sequencing (not blocking) is the right relationship.
**Touches:** `cmd/nanite/plugin_install_flow.go` (`buildInstaller`), `internal/plugin/install/verify.go` (`SignatureVerifier.AllowUnsigned` doc comment, if the setting is retired rather than wired), `internal/plugin/devmode/devmode_on.go` / `devmode_off.go` (doc comments only, if behavior changes), `internal/store/user_settings.go` (reference only — the storage/API-exposure side is already correct and out of scope), `internal/api/settings.go` (reference only, same reason)

> **Actual touches (post-`01/01`, recorded at implementation time — see Work log for the re-verification):** `cmd/nanite/plugin_install_flow.go` (`buildInstaller` + new `resolveAllowUnsignedPlugins` helper), `internal/api/catalog.go` (`handleCatalogInstall`, the second production caller `01/01` introduced), `internal/plugin/install/verify.go` (doc comment only — wired, not retired). `internal/plugin/devmode/devmode_on.go`/`devmode_off.go` and `internal/plugin/install/build.go` were re-read but needed no edits (already accurate). New test files: `cmd/nanite/plugin_install_flow_devmode_test.go`, `internal/api/catalog_install_devmode_test.go`.
**requires_architect_decision:** true — is this dev-workflow opt-in still wanted at all, given task 01 in this folder is converging every plugin-install entry point onto a fail-closed pipeline? Wiring a bypass and simultaneously hardening the rest of the system in the same wave is a real tension worth an explicit call, not a default "wire it because the doc comment says so."

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 1 — release-blocking trust boundaries · **Dispatch unit:** `W1`
> - **Depends on:** `01/01` — sequencing, not compilation: both edit the same `SignatureVerifier` construction site, and one pass avoids two conflicting edits
> - **Blocks:** none
> - **Parallel-safe with:** none in-wave (follows `01/01`)
> - **Gated on:** AD-25 (wire vs. retire) — decided, see banner below.
> - **requires_security_review:** true · **requires_regression_test:** true

> ## ✅ AD-25 DECIDED (2026-08-22) — wire it, do not retire
>
> Thread `user_settings.allow_unsigned_plugins` into whatever
> `SignatureVerifier` construction site exists once `01/01` lands (its shared
> constructor, if `01/01`'s convergence introduces one) and set
> `AllowUnsigned` accordingly. Production (`!devmode`) builds are unaffected
> either way — `devmode.HostDevSigningBypass` is `false` outside `devmode`
> builds, so this is dead-code-eliminated there regardless.
>
> **This task's own `requires_architect_decision: true` header had no
> matching `AD-NN` entry anywhere in `ARCHITECT-DECISIONS.md`** — a real
> planning-pass gap (not a decision anyone had made), found and closed during
> Wave 1 dispatch prep. Resolved by direct operator confirmation, recorded as
> AD-25. Full reasoning: `ARCHITECT-DECISIONS.md`'s AD-25 section.
>
> Scope is otherwise exactly as this file already specifies: wire the one
> field, do not remove `user_settings.allow_unsigned_plugins`'s storage/API
> surface, correct the three doc-comment locations (`verify.go`,
> `devmode_on.go`, `devmode_off.go`) to match the final wiring.

## Context

### Finding addressed

**GO-PLUGIN-008 (low, dead-code/error-handling, confidence high)** — `user_settings.allow_unsigned_plugins` is stored (`internal/store/user_settings.go:30`, `AllowUnsignedPlugins bool`) and API-exposed (`internal/api/settings.go:188-194`, the `PATCH` settings handler accepts and persists the field) but never actually wired into the CLI `SignatureVerifier`'s `AllowUnsigned` field. Evidenced in `docs/audits/2026-08-21-go-quality/REPORT.md` §8.6 and `findings.json`'s `GO-PLUGIN-008` entry, whose own evidence line states: *"grep confirms zero production `AllowUnsigned: true` assignments anywhere."* This task's own grep (`grep -rn "AllowUnsigned" --include="*.go" . | grep -v _test.go`) independently reproduces that: `AllowUnsigned` appears only as the struct field declaration and its consuming `if` check in `internal/plugin/install/verify.go`, never assigned `true` at any construction site.

### Root cause

`cmd/nanite/plugin_install_flow.go:102-116`, `buildInstaller`, is the **one production construction site** for `install.SignatureVerifier`:

```go
return &install.Installer{
    Verifier:  &install.SignatureVerifier{KeyLookup: ring.LookupFunc()},
    ...
```

`AllowUnsigned` is left at its Go zero-value (`false`) — the struct literal never sets it, and nothing upstream of `buildInstaller` reads `user_settings.allow_unsigned_plugins` and threads it through. Meanwhile `verify.go`'s own doc comment (lines 35-45) explicitly describes the intended wiring:

> `AllowUnsigned`, when true AND when the binary was built with the `devmode` build tag, causes `Verify` to accept archives without a signature / signer key... Callers wire this from `user_settings.allow_unsigned_plugins`, but the production binary folds the read path out via dead-code elimination on the compile-time constant.

The comment describes a wiring that does not exist in `cmd/nanite/plugin_install_flow.go`. This is the opposite-direction case from GO-PLUGIN-001/002 in task 01 of this folder: here the system is **stricter than documented**, not weaker — a real functional gap in a documented dev-workflow feature, plus a stale/aspirational doc comment, but not a live security exposure. (`devmode.HostDevSigningBypass` is `false` in production builds regardless, per `internal/plugin/devmode/devmode_off.go:25`, so this gap has zero production-security consequence either way — it only affects developers building with `-tags devmode` who set the setting expecting it to do something.)

### Current behavior

- `user_settings.allow_unsigned_plugins` round-trips correctly through storage and the settings API: `internal/store/user_settings.go:30` (column definition), `:122` (SELECT column list), `:163` (`AllowUnsignedPlugins: allowUnsigned` on read), `:324`/`:352` (UPDATE statement + bound param on write). `internal/api/settings.go:188-194` accepts and persists a `PATCH` to this field. This half of the feature is confirmed correct and is **out of scope** for this task.
- The CLI's `buildInstaller` (`cmd/nanite/plugin_install_flow.go:102`), called from two sites in the same file (`installFromFlow`-style callers at lines 145 and 155 per this task's own grep — re-verify exact line numbers/function names at implementation time, since this file may shift), never reads `user_settings.allow_unsigned_plugins` and never sets `AllowUnsigned: true` on the `SignatureVerifier` it constructs.
- Net effect: even a developer who (a) builds with `-tags devmode` and (b) sets `allow_unsigned_plugins = true` via the settings API still gets full signature enforcement on `nanite plugin install`, because `AllowUnsigned` stays `false` regardless of the stored setting. The `devmode`-tag half of the bypass condition (`devmode.HostDevSigningBypass && v.AllowUnsigned` in `verify.go:72`) is real and correctly gated; only the `v.AllowUnsigned` half is dead.

### Desired invariant

Exactly one of:

1. **Wired:** the documented dev-workflow opt-in actually works — `buildInstaller` (or whatever construction site exists after task 01's convergence work lands) reads the current value of `user_settings.allow_unsigned_plugins` and sets `SignatureVerifier.AllowUnsigned` accordingly, so that in a `devmode`-tagged build, toggling the setting has the documented, observable effect on `nanite plugin install`. Production (`!devmode`) builds remain wholly unaffected, per `verify.go`'s existing, already-correct dead-code-elimination guarantee — this task must not weaken that guarantee.
2. **Retired:** if the architect decision (see below) determines this dev-workflow escape hatch is no longer wanted — especially plausible given task 01 in this folder is simultaneously converging *every* plugin-install entry point onto a fail-closed pipeline, and adding a working bypass in the same wave is in tension with that goal — then the doc comments in `verify.go:35-45` and the `devmode_on.go`/`devmode_off.go` package docs are corrected to stop claiming the setting is "the intended wiring," and `user_settings.allow_unsigned_plugins` is either removed (bigger, likely out of this task's scope — would touch stored data/migrations) or explicitly marked in its own doc comment as currently inert/reserved, with a reason.

Either way, **the code and its doc comments must agree** — that mismatch, not the specific direction chosen, is the actual defect.

### Scope

- `cmd/nanite/plugin_install_flow.go` — `buildInstaller` and its call sites, if wiring.
- `internal/plugin/install/verify.go` — the `AllowUnsigned` field's doc comment (lines 35-45), corrected either way.
- `internal/plugin/devmode/devmode_on.go` (lines 11-14) and `devmode_off.go` (lines 14-16, 22-24) — both already describe `user_settings.allow_unsigned_plugins`'s intended role; update if the wiring direction changes what's true, or leave as-is if wiring makes them already-accurate.
- Do **not** touch `internal/store/user_settings.go` or `internal/api/settings.go` unless the architect decision is "retire," in which case removing the setting's storage/API surface would be a larger, separate task — flag it as a follow-up rather than silently expanding this task's scope.

### All production callers

There is exactly one production construction site for `install.SignatureVerifier` (confirmed via `grep -rn "SignatureVerifier{" --include="*.go" . | grep -v _test.go`): `cmd/nanite/plugin_install_flow.go:108-110`, inside `buildInstaller`. No other package constructs a `SignatureVerifier`. This is a small, single-caller wiring gap, not a multi-caller migration — the "All production callers" enumeration the remediation guide's §6 template calls out as mandatory for shared-primitive/security changes is satisfied trivially here: there is exactly one caller, and it is the one this task fixes (or the one whose doc comment this task corrects).

**Interaction with task 01:** task 01's convergence work will likely introduce a *second* `SignatureVerifier` construction site (or refactor `buildInstaller` into something shared between CLI and API), for the GUI/API's converged install path. If task 01 lands first, this task's "one production construction site" premise changes to "N sites, all needing the same wiring (or all needing the same corrected doc comment)" — re-verify the current call-site count via the same grep before starting this task's implementation, and update this section's count if task 01 already landed.

### Proposed direction

1. **Get the architect decision first** (this task's `requires_architect_decision: true`): is the dev-workflow "install unsigned plugins if you opt in via settings, on a devmode build" feature still wanted, given the rest of this wave is hardening the exact same trust boundary elsewhere? Bring both options (wire vs. retire) with their respective effort/risk to that decision — this task should not resolve it unilaterally.
2. **If wiring:** thread a `store.UserSettings` (or equivalent) lookup into `buildInstaller`'s call sites, read `AllowUnsignedPlugins`, and set it on the constructed `SignatureVerifier`. Keep the change minimal — this is a one-field plumbing fix, not a reason to restructure `buildInstaller`'s signature beyond what's needed to pass the setting through. If task 01 has already introduced a shared installer-construction helper by the time this task executes, wire through that helper instead of re-diverging.
3. **If retiring:** correct the three doc-comment locations named in Scope to state plainly that the setting is currently inert (or removed), and note why, so a future engineer doesn't rediscover this same gap and "fix" it without the context of why it was deliberately left unwired.
4. Either way, add a short doc-comment cross-reference between `verify.go`'s `AllowUnsigned` field and this task's resolution, so the next reader understands the current state is intentional, not an oversight (the current gap is *already* undocumented-as-intentional, which is part of why the audit flagged it).

### Non-goals

- Do not remove `user_settings.allow_unsigned_plugins`'s storage column or its settings-API exposure as part of this task, even under the "retire" branch — that's a schema/API-surface change with its own migration and callers-check burden, better scoped as its own follow-up task if the architect decision calls for full removal rather than just correcting the doc comments.
- Do not change `devmode.HostDevSigningBypass`'s production-build behavior (`false`, always) — already correct, confirmed by the audit as fail-closed regardless of this setting.
- Do not use this task as an opportunity to add a *new* opt-in mechanism (e.g. a CLI flag) that wasn't already documented as intended — if the architect decision wants a different UX than "settings-driven toggle," that's a design change beyond this task's low-severity dead-code/doc-comment scope.

### Dependencies

- **Sequencing with `01-unify-plugin-catalog-install-pipeline.md` (this folder), not a hard dependency.** Both tasks touch the `SignatureVerifier` construction site(s) in and around `cmd/nanite/plugin_install_flow.go`'s `buildInstaller`. Doing task 01 first (or in the same pass) means this task's wiring — if the architect decision is "wire it" — lands once, against whatever the post-convergence construction site(s) look like, rather than being wired against the pre-convergence CLI-only site and then needing to be re-threaded through task 01's new API-side construction site(s) shortly after. Neither task requires the other's code to exist to compile or to test in isolation; this is purely a rework-avoidance sequencing note, per this project's task-template convention for soft dependencies.

### Tests required

- If wiring: a test that constructs a `SignatureVerifier` via the updated `buildInstaller` (or its post-task-01 equivalent) with `user_settings.allow_unsigned_plugins = true`, under a `devmode`-tagged build, and asserts `AllowUnsigned == true` reaches the verifier — extending the existing `internal/plugin/install/verify_dev_bypass_test.go` pattern or adding a `cmd/nanite`-level test if one doesn't already exist for `buildInstaller`. Also assert the production-build path remains unaffected (no new test needed if `verify_prod_enforcement_test.go`'s existing coverage already proves `devmode.HostDevSigningBypass == false` makes `AllowUnsigned` irrelevant — confirm rather than assume).
- If retiring: no new test is strictly required (there's no behavior change to verify), but confirm no existing test asserts the old (incorrect) doc-comment-implied behavior — grep test files for `AllowUnsigned` to check.

### Prevention

- The corrected doc comment (either direction) is itself the prevention mechanism — the defect here is specifically "comment describes behavior that doesn't exist," so making the comment accurate closes the gap between documentation and reality that let this drift unnoticed.
- If wiring, the new test above becomes a regression check against this specific gap reopening.

### Verification

```bash
go build -tags devmode ./...
go build ./...
go vet ./...
go test ./internal/plugin/install/... -v
go test -tags devmode ./cmd/nanite/... -run Install -v
```

Observable behavior required for PASS: whichever direction is chosen, `verify.go`'s `AllowUnsigned` doc comment accurately describes what the code does when read side-by-side with `buildInstaller` (or its successor).

### Risk / rollback

- Low risk either direction — this is a single-field wiring fix or a doc-comment correction, not a behavioral change to production (`!devmode`) builds either way (production behavior is unaffected by `AllowUnsigned`'s value regardless of what this task does, per the dead-code-elimination guarantee traced in Context).
- Rollback is a trivial revert of the small diff in either direction.

### Done means

- The architect decision (wire vs. retire) is recorded in the Work Log with its rationale, not just its outcome.
- `verify.go`'s `AllowUnsigned` doc comment, and the `devmode_on.go`/`devmode_off.go` package docs, accurately describe current behavior — re-read all three side-by-side with the final `buildInstaller` (or equivalent) implementation to confirm agreement, don't just trust the diff.
- If wiring: the new test passes and demonstrates the setting has its documented effect on a `devmode` build, with zero effect on a production build.
- If retiring: grep confirms no remaining code comment anywhere claims this setting is functionally wired.
- `go build ./...` (with and without `-tags devmode`), `go vet ./...`, and the relevant package tests all pass clean.

## Work log

**Decision (recorded here per Done-means item 1, not re-derived — already settled by AD-25):** wire it, do not retire. `TASKS/audit-remediation/ARCHITECT-DECISIONS.md`'s AD-25 section (decided 2026-08-22, operator confirmation) resolved this task's own stale `requires_architect_decision: true` header against `findings.json`'s `GO-PLUGIN-008` disposition (`requires_architect_decision: false`), in favor of wiring. Rationale: the rest of this wave (`01/01`, `02/01`, `03/01`) closes paths where an *untrusted, external* input reaches a security-relevant sink; this setting is the opposite shape — a developer explicitly opts in, on a `devmode`-tagged build that never ships to production, to skip signature checks on their own machine. Production (`!devmode`) behavior is provably unaffected either way per `devmode.HostDevSigningBypass`'s compile-time-`false` guarantee.

**Post-`01/01` construction-site re-verification (per this task's own instruction to re-check before implementing).** `01/01` landed on `main` before this task started (confirmed via `git log`: `02c7f0e9 TASKS/audit-remediation/01/01: unify plugin-install pipeline across CLI, GUI, API`, already present in this worktree). Re-ran the task's own greps against the current tree:

- `grep -rn "SignatureVerifier{" --include="*.go" .` → exactly **one** hit outside test files: `internal/plugin/install/build.go:60`, inside `install.NewInstaller`. This is the shared constructor `01/01` introduced — `BuildOptions` already had an `AllowUnsigned bool` field that gets forwarded straight to `SignatureVerifier{..., AllowUnsigned: opts.AllowUnsigned}` (build.go was already correct; nothing to change there).
- `grep -rn "install.NewInstaller(" --include="*.go" .` → exactly **two** production callers, confirming the task file's own prediction ("N sites, all needing the same wiring"): `cmd/nanite/plugin_install_flow.go:80` (`buildInstaller`, called by both `installLocalFromStateMachine` and `installFromCatalog`) and `internal/api/catalog.go:342` (`handleCatalogInstall`). Neither caller populated `BuildOptions.AllowUnsigned` before this task — both left it at the Go zero value (`false`), reproducing the original GO-PLUGIN-008 gap at both post-convergence sites, not just the old CLI-only one.

So the pre-`01/01` "one production construction site" premise in this file's own Context/`### All production callers` section is now stale (as task 01's own file predicted it would become); the corrected count is **one shared constructor (`install.NewInstaller`/`BuildOptions`, already correctly shaped) with two production callers that both needed the same one-line wiring fix.**

**What was implemented:**

1. `cmd/nanite/plugin_install_flow.go` — added `resolveAllowUnsignedPlugins(dbPath string) bool`, a small helper that opens the store at `dbPath` via `store.New`, reads `user_settings.allow_unsigned_plugins` via `GetUserSettings()`, and fails safe to `false` on *any* error (store won't open, DB doesn't exist yet, row missing, etc.) — "couldn't read the setting" is never silently treated as "allow unsigned." `buildInstaller` now passes `AllowUnsigned: resolveAllowUnsignedPlugins(resolveDBPath())` into `install.BuildOptions`. `buildInstaller`'s own signature is unchanged (per the task's "not a reason to restructure buildInstaller's signature" instruction) — both its callers (`installLocalFromStateMachine`, `installFromCatalog`) needed no changes.
2. `internal/api/catalog.go` — `handleCatalogInstall` now reads `cs.store.GetUserSettings()` (the same `*store.Store` it already holds — no new dependency) immediately before constructing `install.BuildOptions`, and sets `AllowUnsigned` from `AllowUnsignedPlugins`, failing safe to `false` on a read error (same discipline as the CLI side).
3. `internal/plugin/install/verify.go` — corrected `SignatureVerifier.AllowUnsigned`'s doc comment: it previously described the wiring in aspirational/uncertain language ("Callers wire this from user_settings.allow_unsigned_plugins, but..."); it now names both concrete production construction sites and states plainly that in production builds the field is read into the struct but never consulted by `Verify`.
4. `internal/plugin/devmode/devmode_on.go` and `devmode_off.go` — re-read side-by-side with the final wiring per the Done-means checklist. **No changes needed** — both already described the intended *final* behavior in present-tense, unconditional language ("Per-plugin signatures are bypassed ONLY when the operator also opts in via user_settings.allow_unsigned_plugins = true"; "user_settings.allow_unsigned_plugins is intentionally inert... in a production binary"). That language was aspirationally *inaccurate* before this task (the setting wasn't actually wired anywhere) but is now literally true, so no edit was required — confirmed by direct comparison, not assumed.
5. `internal/plugin/install/build.go` (`BuildOptions.AllowUnsigned` doc comment) — left unchanged; already accurately described as "threads user_settings.allow_unsigned_plugins through to the SignatureVerifier," which was already true of the field's mechanics (it was the *callers'* job to populate it that was missing, not this comment).

**Tests added** (both under `//go:build devmode`, matching `verify_dev_bypass_test.go`'s existing pattern, added at the level where the setting is actually read since a shared-constructor-level test would be redundant with `build.go`'s already-correct, already-untested-but-trivial field-forwarding):

- `cmd/nanite/plugin_install_flow_devmode_test.go` — three tests against `resolveAllowUnsignedPlugins`, each using an explicit `t.TempDir()`-rooted scratch DB path (never `resolveDBPath()`'s real XDG default — confirmed via `go-apppaths/paths.Resolve`'s source that a non-`WithoutMaterialize()` call `MkdirAll`s real `~/.local/share`/`~/.local/state` directories even when only the DB path itself is overridden by `NANITE_DB_PATH`, so calling `buildInstaller()`/`resolveDBPath()` directly from a test was deliberately avoided per `EXECUTION-PROCESS.md`'s "explicit scratch path, never a relative/ambient default" guidance):
  - `TestBuildInstaller_ResolveAllowUnsignedPlugins_ThreadsSetting` — seeds `user_settings` with `allow_unsigned_plugins=1`, asserts `resolveAllowUnsignedPlugins` returns `true`, then mirrors `buildInstaller`'s own `install.NewInstaller` call and asserts the constructed `*install.SignatureVerifier.AllowUnsigned == true`.
  - `TestBuildInstaller_ResolveAllowUnsignedPlugins_DefaultsFalse` — a freshly-migrated DB with **no** `user_settings` row at all (the real state before `nanite serve` has ever run `Store.Seed()` — `cmd/nanite`'s CLI plugin-install path never calls `Seed()` itself) resolves to `false` via the same fail-safe path as a read error.
  - `TestBuildInstaller_ResolveAllowUnsignedPlugins_FailsSafeWhenStoreCannotOpen` — a `dbPath` that is itself a directory (so `store.New` cannot open it as SQLite) fails safe to `false`.
- `internal/api/catalog_install_devmode_test.go` — two tests against `handleCatalogInstall`, reusing `catalog_install_test.go`'s existing `setupCatalogTestState`/`buildTarGzArchive`/`addCatalogSource` fixtures:
  - `TestHandleCatalogInstall_DevmodeAllowsUnsignedWhenSettingEnabled` — the positive counterpart to the existing production-mode `TestHandleCatalogInstall_RejectsUnsignedEntry`: the *same* unsigned catalog entry that a production build rejects now installs successfully (200, `plugin.yaml` written) once `allow_unsigned_plugins=true` under this file's `devmode` tag.
  - `TestHandleCatalogInstall_DevmodeStillRejectsUnsignedWhenSettingDisabled` — the devmode build tag alone is not sufficient; with the setting left at its default `false`, the same unsigned entry is still rejected even under `-tags devmode`, proving the two-factor gate (`devmode.HostDevSigningBypass && v.AllowUnsigned`) is intact, not just the build tag half.
  - Both tests seed only the `user_settings` singleton row directly (`INSERT OR IGNORE INTO user_settings (id) VALUES (1)`, via a small `seedUserSettingsRow` helper) rather than calling `cs.store.Seed()` — `Seed()` also inserts a real external `catalog_sources` row (`https://raw.githubusercontent.com/hollis-labs/plugin-catalog/main/catalog.yaml`) that `handleCatalogInstall`'s `ListCatalogSources`/`cs.fetcher.Fetch` step would then try to fetch over the network, which would make these tests flaky/network-dependent for no reason connected to what they're testing.

**Deviation from the task file's literal test guidance:** the task's own "Tests required" section anticipated extending `verify_dev_bypass_test.go` itself or adding "a `cmd/nanite`-level test." Since `01/01` converged the construction site into two production callers (CLI + API) of one shared `install.NewInstaller`, and the setting is read independently at each caller (not inside the shared constructor, which only forwards a bool it's given), I added coverage at **both** the CLI level and the API level rather than just one — matching the dispatch instruction's explicit verification command (`go test -tags devmode ./cmd/nanite/... ./internal/api/... -run Install -v`, which names both packages) over the task file's own narrower, pre-`01/01`-landing text.

**Verification run (this worktree, HEAD includes `01/01`):**
```
go build ./cmd/nanite/                                                  # PASS
go build ./...                                                          # PASS
go build -tags devmode ./...                                            # PASS
go vet ./...                                                            # pre-existing, unrelated findings only
                                                                          # (internal/service/container.go:1213/1233/1293,
                                                                          #  stopReaper/stopRuntimeReaper possible context
                                                                          #  leak — confirmed via `git diff --stat` that this
                                                                          #  task's diff never touches container.go)
go test ./internal/plugin/install/... -v                                # PASS, all cases including
                                                                          # TestVerify_ProductionRefusesUnsigned
go test -tags devmode ./cmd/nanite/... ./internal/api/... -run Install -v  # PASS — all 3 new CLI-level +
                                                                          # 2 new API-level tests, plus every
                                                                          # pre-existing Install-matching test
go test ./...                                                           # PASS, zero FAIL across every package
                                                                          # (production/no-tag build — includes
                                                                          # TestHandleCatalogInstall_RejectsUnsignedEntry,
                                                                          # confirming production posture unchanged)
```
`git status --short` after every verification run showed only the intended new/modified files — no stray writes to real tracked files or real XDG data/state directories (checked directly, per the two prior-incident recommendations in `docs/engineering/EXECUTION-PROCESS.md`'s "Promote recommendations, don't just log them" section, since this task involved a store-backed live read path).

**Incidental finding, not fixed (out of scope):** `gofmt -l cmd/nanite/plugin_install_flow.go` flags a pre-existing struct-tag alignment drift in `catalogEntryLite` (untouched by this diff — confirmed via `git show HEAD:...` piped through `gofmt -l`, which flags the same file at the pre-`01/02` revision). Left as-is; not part of this task's scope and not introduced by this change.

## Review notes

**2026-08-22, fresh reviewer (no shared context with the worker). Verdict: PASS. Last task in Wave 1.**

Independently re-verified all seven review criteria against the actual code:

1. **Both construction sites wired** — `grep -rn "SignatureVerifier{"` confirms exactly one construction site (`internal/plugin/install/build.go:60`, the `01/01` shared constructor); `grep -rn "install.NewInstaller("` confirms exactly two production callers, `cmd/nanite/plugin_install_flow.go`'s `buildInstaller` and `internal/api/catalog.go`'s `handleCatalogInstall`, both now populating `BuildOptions.AllowUnsigned`.
2. **Fail-safe on error, confirmed in code** — both new read paths default to `false` (enforcement stays on) on any settings-read error, including the `sql.ErrNoRows` case; not just asserted, independently traced.
3. **Production posture unaffected, verified not just asserted** — `verify.go`'s `signatureBypassed := devmode.HostDevSigningBypass && v.AllowUnsigned` gate is byte-for-byte unchanged; `devmode_off.go` still hard-codes `false`; `TestVerify_ProductionRefusesUnsigned`/`TestHandleCatalogInstall_RejectsUnsignedEntry` both still pass on a non-devmode build.
4. **Doc comment corrected accurately** — `verify.go`'s `AllowUnsigned` field comment now names both real construction sites and correctly describes production behavior; `devmode_on.go`/`devmode_off.go` confirmed already accurate, correctly left untouched.
5. **New tests are real** — all 5 new tests construct real `SignatureVerifier`/`BuildOptions` via the actual production code paths; independently ran all of them plus the full pre-existing suite under `-tags devmode`, all pass.
6. **Scope discipline held** — `internal/store/user_settings.go`/`internal/api/settings.go` confirmed untouched; no new opt-in mechanism added beyond the documented settings toggle.
7. **Test DB sandboxing reasoning sound** — independently confirmed in `go-apppaths/paths/options.go` that `Resolve` without `WithoutMaterialize()` does `MkdirAll` real XDG directories; using `t.TempDir()`-rooted scratch paths for the CLI-level tests was the correct call.

Full verification run independently reproduced clean: `go build ./...`, `go build -tags devmode ./...`, `go vet ./...` (only the same pre-existing, untouched `container.go` findings), `go test ./internal/plugin/install/... -v`, `go test -tags devmode ./cmd/nanite/... ./internal/api/... -run Install -v`, full `go test ./...` — zero failures repo-wide.

**Non-blocking observations, informational only, none introduced or worsened by this task:** `installLocalFromStateMachine` (introduced by `01/01`) is grep-confirmed unreachable from any production call site — pre-existing from `01/01`, adjacent to but not caused by this task's changes. `resolveAllowUnsignedPlugins` opens a full `store.New` connection on every `nanite plugin install` invocation, matching the existing idiom used elsewhere in `cmd/nanite`. The fail-safe-to-`false` paths are silent (no log line on a settings-read error) — satisfies "fails closed, not open" but a developer debugging the devmode opt-in would get no signal the settings read itself failed; worth a note for a future polish pass, not a blocker.

With this PASS, all six Wave 1 tasks are reviewed clean.
