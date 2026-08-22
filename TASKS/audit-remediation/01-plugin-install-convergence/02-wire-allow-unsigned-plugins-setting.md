# Wire (or retire) the allow_unsigned_plugins setting's promised CLI effect

**Phase:** Audit remediation — Wave 1 (release-blocking trust boundaries)
**Status:** not-started
**Depends on:** `01-unify-plugin-catalog-install-pipeline.md` (this folder) — **sequencing only**, not a technical/compile dependency. See Context below for why sequencing (not blocking) is the right relationship.
**Touches:** `cmd/nanite/plugin_install_flow.go` (`buildInstaller`), `internal/plugin/install/verify.go` (`SignatureVerifier.AllowUnsigned` doc comment, if the setting is retired rather than wired), `internal/plugin/devmode/devmode_on.go` / `devmode_off.go` (doc comments only, if behavior changes), `internal/store/user_settings.go` (reference only — the storage/API-exposure side is already correct and out of scope), `internal/api/settings.go` (reference only, same reason)
**requires_architect_decision:** true — is this dev-workflow opt-in still wanted at all, given task 01 in this folder is converging every plugin-install entry point onto a fail-closed pipeline? Wiring a bypass and simultaneously hardening the rest of the system in the same wave is a real tension worth an explicit call, not a default "wire it because the doc comment says so."

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

## Review notes
