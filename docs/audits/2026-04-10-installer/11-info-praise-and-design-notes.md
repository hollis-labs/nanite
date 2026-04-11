# [Info] Praise, design notes, and positive observations about the installer

**Scope:** installer
**Topic:** Design — things the installer does well
**Date:** 2026-04-10

This is a read-only audit, not a hit list. The installer has several design choices that are genuinely good and worth naming so they survive future refactors.

## P1: The five-phase state marker pattern in migrate

**Files:** `internal/service/install/state.go`, `migrate.go`, `resume.go`, `rollback.go`

The migrate-from-agentrc flow writes a `.install-state.json` marker after every completed phase, so an interrupted install can be resumed from the last good point or restarted cleanly. This is exactly the right pattern for a multi-step filesystem mutation, and the test coverage in `resume_test.go` and `migrate_test.go` exercises it reasonably well.

The main limitation is that the pattern doesn't extend to fresh or adopt flows (see finding `06-medium-fresh-scaffold-not-atomic-no-rollback-on-partial-failure.md`), but the migrate flow itself is a template to copy, not rewrite.

## P2: Pre-edit snapshots for rollback

**File:** `internal/service/install/adapters.go:L113-129`, `rollback.go:L71-86`

`snapshotAdapterTargets` copies existing project-root CLI files to `{archive}/{name}.pre-edit` *before* any managed-section writes happen. Rollback then uses the presence or absence of the pre-edit file as the authoritative signal for "was this file created by the installer or pre-existing?" — missing snapshot means installer created it, so rollback deletes it; present snapshot means pre-existing, so rollback restores it.

The integration test (`integration_test.go:L21-172`) actually verifies bit-for-bit round-trip equivalence by snapshotting the project tree before migrate, rolling back, and diffing. That's a strong test and it covers the rollback invariant properly.

## P3: Tri-state `Adapters` pointer in config

**File:** `internal/service/install/adapters.go:L53-64`

```go
type projectConfig struct {
    Adapters *[]string               `yaml:"adapters,omitempty"`
    ...
}
```

Using `*[]string` distinguishes three states:

1. `nil` — key absent (new install, no preference recorded)
2. `&[]` — key present, empty list (user explicitly opted out)
3. `&[...]` — key present, non-empty list (active adapter selection)

This is the right data model for a config value that has to survive round-trips without losing the "explicit none" vs "unset" distinction. The `persistAdapterList` implementation at `adapter_persist.go:L65-68` also writes the empty list as `adapters: []` flow-style to preserve this distinction on disk.

Textbook correct.

## P4: `ResolveAdapters` priority order is clear and documented

**File:** `internal/service/install/adapter_select.go:L28-89`

The precedence — NoAdapters flag > Flag > cfg+not-reconfigure > detection+prompt > detection — is documented in the function comment and matched by the implementation. The test suite at `adapter_select_test.go` hits each branch. This is the kind of well-factored decision logic that's a pleasure to review.

## P5: Integration test uses real embedded assets

**File:** `internal/service/install/migrate_test.go:L11-22`

```go
func setupFakeHome(t *testing.T) string {
    t.Helper()
    home := t.TempDir()
    t.Setenv("HOME", home)
    svc := New()
    if _, err := svc.InstallHome(InstallHomeOptions{Target: filepath.Join(home, ".nanite")}); err != nil {
        t.Fatalf("setup InstallHome: %v", err)
    }
    return home
}
```

The test setup actually extracts the real embedded framework assets into a tempdir before running the project install. That means the tests exercise the true `assets.ExtractTo` path rather than mocking it out. This catches regressions in the embedded asset tree that unit-test-level mocks would miss, and the hermetic tempdir means no pollution of the user's real `~/.nanite/`.

Note that the test also calls `t.Setenv("HOME", home)` to redirect `os.UserHomeDir`. That covers the most common path, but any code that reads `~` via `os.Getenv("HOME")` directly (rather than `os.UserHomeDir`) would need a different fake. I did not find any such code in the installer, so the mechanism is complete for now.

## P6: The archive dir collision-avoidance loop

**File:** `internal/service/install/archive.go:L43-71`

`ResolveArchiveDir` tries date-only → date+HHMMSS → date+HHMMSS-N. This handles the edge case where a user runs migration twice in the same day (first try succeeds, second try would collide) gracefully, and caps at 1000 attempts with a clear error. Small detail, handled correctly.

## P7: Read-only by default for `.nanite/config.yaml` writes

**File:** `internal/service/install/scaffold.go:L38-53`, `adapter_persist.go:L44-106`

`ScaffoldNaniteDir` only writes `config.yaml` if it's missing. `persistAdapterList` only rewrites the `adapters:` key. Neither ever clobbers user edits to `nanite_version:`, `agents:`, or any other top-level key. That's the right contract for a config file the user is supposed to own. The YAML round-trip has its own issues (see finding `05`), but the *intent* here is correct.

## P8: The `InstallProject` decision tree is easy to read

**File:** `internal/service/install/install.go:L101-173`

The dispatch logic — ArchiveOnly → Migrate → Adopt → PartialDetect → Fresh — is a flat set of if-branches with clear refusal errors for impossible combinations. "Both `.agentrc/` and `.nanite/` present — please resolve manually" is the kind of message that saves a beta user 30 minutes of debugging.

## P9: Test quality: table-driven + round-trip + hermetic

The install package has 21 test files covering state, adapter selection, persist, cleanup, archive, migrate, resume, rollback, scaffold, and one top-level integration test. The tests avoid `time.Sleep` as a synchronization mechanism (I grep-checked), use tempdirs for isolation, and have table-driven cases for the pure-function helpers. The install package is one of the better-tested corners of Nanite.

The main gap is error-path coverage for the scaffold pipeline — most tests exercise the happy path or a single mutated invariant. Adding a failure-injection layer (e.g., a `writeFileFn` that can be stubbed to fail) would strengthen the atomicity story flagged in finding 06, but the existing coverage is well above bar.

---

None of the above require any action. They're noted here so the user can see what the audit thinks is worth preserving, and future reviewers have a baseline to compare against.
