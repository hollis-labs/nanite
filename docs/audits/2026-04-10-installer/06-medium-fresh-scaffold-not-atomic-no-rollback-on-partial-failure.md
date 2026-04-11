# [Medium] Fresh-scaffold and adopt-existing paths are not atomic; partial failures leave the project in a half-installed state with no recovery

**Scope:** installer / freshScaffold + adoptExisting flows
**Topic:** Correctness — atomicity / recovery / idempotency
**Date:** 2026-04-10

## Problem

`freshScaffold` and `adoptExisting` execute a 5-step pipeline (ScaffoldNaniteDir → ScaffoldNaniteMD → loadProjectConfig → ResolveAdapters → cleanupRemovedAdapters → persistAdapterList → syncAdaptersForProject) and abort on the first error. None of the steps are atomic. None of them has a rollback. If any step after the first one fails, the project is left with a partial `.nanite/` directory, a partial `NANITE.md`, possibly a partial `.nanite/config.yaml`, possibly partial CLI target files (CLAUDE.md, AGENTS.md, GEMINI.md, OPENCODE.md), and no way to recover without either `--rollback` (which only works on migrate flows, not fresh) or manual cleanup.

The migrate-from-agentrc flow DOES have phase-based recovery via the state marker and the `Resume` / `Restart` flows, because it archives `.agentrc/` first and writes a state file. The fresh and adopt-existing flows skip that scaffolding entirely — no archive, no state marker, no resume. If the user's first-ever `nanite install --project ~/new-project` fails on the last step (adapter sync), they are left with a scaffolded `.nanite/`, a scaffolded NANITE.md, a persisted adapter list in config.yaml, and no CLI marker files. Re-running the command hits the `hasNaniteDir` branch and dispatches to `adoptExisting`, which runs the same pipeline with no awareness that the previous run failed. If the failure was transient it might succeed; if it was not transient (e.g., the user's config.yaml is unparseable because of a YAML typo they introduced between runs) the installer will keep failing in the same place with no diagnostic hint.

This is particularly relevant for Phase 4 Task 16 (`nanite install --project ~/Projects-apps/nanite`), which runs `adoptExisting` against a project that already has a partly-custom `.nanite/` with broken symlinks. Any failure mid-pipeline there leaves Nanite's own repo in a worse state than it was before.

## Evidence

```go
// internal/service/install/install.go:L175-224
func (s *Service) freshScaffold(projectDir, globalHome string, opts InstallProjectOptions) (*InstallProjectReport, error) {
    src := ScaffoldSource{...}
    if err := ScaffoldNaniteDir(projectDir, globalHome, src); err != nil {
        return nil, err
    }
    if err := ScaffoldNaniteMD(projectDir, src); err != nil {
        return nil, err
    }

    cfgPath := filepath.Join(projectDir, ".nanite", "config.yaml")
    cfg, err := loadProjectConfig(cfgPath)
    if err != nil {
        return nil, err
    }

    resolved, previous, err := ResolveAdapters(cfg, projectDir, ResolveOpts{...})
    if err != nil {
        return nil, fmt.Errorf("resolve adapters: %w", err)
    }

    removed := setDifference(previous, resolved)
    cleanupReports, err := cleanupRemovedAdapters(projectDir, removed)
    if err != nil {
        return nil, fmt.Errorf("cleanup removed adapters: %w", err)
    }

    if err := persistAdapterList(cfgPath, resolved); err != nil {
        return nil, fmt.Errorf("persist adapter list: %w", err)
    }

    if err := syncAdaptersForProject(projectDir, resolved); err != nil {
        return nil, fmt.Errorf("adapter sync: %w", err)
    }
    ...
}
```

Each `if err := ...; return nil, err` leaves previously-completed side effects in place. Specifically:

- After `ScaffoldNaniteDir` succeeds: `.nanite/` directory exists with symlinks.
- After `ScaffoldNaniteMD` succeeds: `NANITE.md` exists.
- After `cleanupRemovedAdapters` succeeds and `persistAdapterList` fails: one or more CLI target files have had their managed sections stripped, but the config.yaml does not reflect the new state. Running again will hit `adoptExisting`, which reloads the config, and the `removed` set is computed from the old config.yaml (not the stripped files). The user now has a mismatch: the config says "claude, codex" but AGENTS.md is already stripped.
- After `syncAdaptersForProject` fails mid-adapter: some adapters have written their managed sections, others have not. The config.yaml is correct, but the disk state does not match it.

Every one of these states is recoverable by re-running the install, but only if the cause of the first failure was transient. If the cause was persistent, the user is stuck.

### Adopt flow has the same problem

```go
// internal/service/install/adopt.go:L25-81
func (s *Service) adoptExisting(projectDir, globalHome string, opts InstallProjectOptions) (*InstallProjectReport, error) {
    src := ScaffoldSource{...}
    if err := ScaffoldNaniteDir(projectDir, globalHome, src); err != nil {
        return nil, err
    }
    if err := ScaffoldNaniteMD(projectDir, src); err != nil {
        return nil, err
    }
    cfgPath := filepath.Join(projectDir, ".nanite", "config.yaml")
    cfg, err := loadProjectConfig(cfgPath)
    if err != nil {
        return nil, err
    }
    resolved, previous, err := ResolveAdapters(cfg, projectDir, ResolveOpts{...})
    ...
}
```

Same shape. Same risk.

### Migrate-from-agentrc has partial mitigation

```go
// internal/service/install/migrate.go:L21-154
// (Excerpt of the phase-marking pattern)
state.MarkPhaseComplete(PhaseArchived)
if err := WriteState(statePath, state); err != nil {
    return nil, fmt.Errorf("write state after archive: %w", err)
}
```

The migrate path writes a `.install-state.json` after every phase and refuses to proceed if a partial install is detected. But:

- The state marker lives under the archive directory, not the project directory. If the fresh or adopt flow had to be abandoned, there is no place to put a state marker because there is no archive. The existing design assumes state-marker flows always have an archive.
- Resume covers only four phases; the adapter sync phase is treated as atomic even though `syncAdaptersForProject` calls `SyncAllProjectRootsFiltered` which iterates all adapters and fails on the first error, leaving some adapters' files in-progress.

## Impact

- **Who:** any user whose fresh or adopt install fails after the first step. Triggers: bad YAML in an existing config, a disk full mid-write, a race with another process editing the project tree, a symlink that is suddenly un-writable, a hand-edited CLAUDE.md with markers the parser chokes on (see finding 03).
- **What:** the project ends up with a half-installed `.nanite/` that looks valid to the next run. The next run either quietly compounds the mess (adopt path reruns through the pipeline, possibly re-stripping sections that were already stripped) or fails in a different place with a different error, making the root cause even harder to find.
- **Release impact:** beta users who hit this will not be able to recover without `rm -rf .nanite/ NANITE.md CLAUDE.md.bak* CLAUDE.md`, which is destructive and requires them to know which files the installer touched.

## Recommendation

Add a state marker and phase tracking to `freshScaffold` and `adoptExisting`, mirroring the migrate flow. Two options:

1. **(Minimal) Write a state marker under the project's `.nanite/.install-state.json`.** The marker records which phases completed. On the next `nanite install --project ...` run, if the state marker exists with an incomplete phase, the installer either refuses to proceed (and tells the user to pass `--resume` or `--restart`) or auto-resumes from the last completed phase. This makes fresh and adopt recoverable in the same way migrate is.

2. **(Heavier) Make each phase atomic.** Write to a tempdir, validate the full pipeline, then rename into place. This is significantly more work (the adapter sync touches multiple top-level files, not just `.nanite/`) and may not be feasible without a schema-driven list of "files this flow writes." Given the scope of the change, option 1 is probably the right move for the first beta, with option 2 deferred to a follow-up scope.

Specific delta for option 1:

```go
// internal/service/install/install.go:freshScaffold (sketch)
state := NewState(...)
statePath := filepath.Join(projectDir, ".nanite", ".install-state.json")
// Scaffold phase
if err := ScaffoldNaniteDir(...); err != nil {
    return nil, err
}
state.MarkPhaseComplete(PhaseScaffoldNaniteDir)
WriteState(statePath, state) // ignore error, best-effort
// ... rest of pipeline with marks
// At completion:
os.Remove(statePath) // happy-path cleanup
```

On re-entry, the existing partial-install detection in `InstallProject` (which currently only checks the archive base) would need to also check `{projectDir}/.nanite/.install-state.json` before dispatching to `adoptExisting`.

### Additional recommendation: make `syncAdaptersForProject` atomic or resumable

`SyncAllProjectRootsFiltered` iterates adapters and returns on the first error. Either:

- Collect all errors, apply all successful writes, and return a combined error (so the user has a best-effort complete state even when one adapter fails); or
- Transactionally prepare all writes in memory first, then flush — if any adapter's write fails, skip the whole batch.

The former is simpler and matches how `cleanupRemovedAdapters` already works (which does propagate on first error, but at least the cleanup is idempotent so re-running is safe).

## References

- `internal/service/install/install.go:L175-224` — `freshScaffold`.
- `internal/service/install/adopt.go:L25-81` — `adoptExisting`.
- `internal/service/install/migrate.go:L21-154` — the migrate flow, for comparison with its state-marker pattern.
- `internal/service/install/state.go:L1-86` — the State type and phase enumeration.
- `internal/service/install/resume.go:L30-101` — the resume flow that would need to be extended if this finding's recommendation is taken.
- Related: `03-high-managed-section-end-marker-confusion.md` (one of the failure modes that can leave partial state).
