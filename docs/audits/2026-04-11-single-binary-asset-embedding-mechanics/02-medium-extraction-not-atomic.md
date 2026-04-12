# [Medium] Framework extraction is direct-write, not atomic

**Scope:** single-binary asset embedding mechanics
**Topic:** Extraction atomicity
**Date:** 2026-04-11

## Problem

`assets.ExtractTo` writes files directly to the target directory. A crash, power loss, or `SIGKILL` during extraction leaves a partially populated `~/.nanite/` tree with no rollback or state marker at the asset-extraction layer.

## Evidence

`internal/assets/framework.go:L57-116` — the `ExtractTo` function:

```go
// internal/assets/framework.go:63
err := fs.WalkDir(frameworkAssets, "framework", func(path string, d fs.DirEntry, walkErr error) error {
    // ...
    if errors.Is(statErr, fs.ErrNotExist) {
        if err := os.WriteFile(target, embedded, 0o644); err != nil {
            return fmt.Errorf("write %s: %w", target, err)
        }
        report.Created++
        return nil
    }
    // ...
})
```

Each file is written individually via `os.WriteFile` directly to the final path. There is no temporary directory + atomic rename pattern. `fs.WalkDir` processes files in directory order — if the walk is interrupted after writing 40 of 76 files, the target directory contains a partial framework tree.

The install service (`internal/service/install/install.go:L42-52`) delegates directly to `assets.ExtractTo`:

```go
// internal/service/install/install.go:51
return assets.ExtractTo(target, assets.ExtractOptions{Force: opts.Force})
```

The project-level installer (`InstallProject`) does have a state machine (`state.go`) with phase tracking and resume/rollback, but `InstallHome` (the `~/.nanite` extraction) does not use it — it's a single `ExtractTo` call with no state marker.

## Impact

In practice, the blast radius is limited:

- `ExtractTo` is idempotent — re-running it after a partial extraction creates the missing files and skips already-correct ones. A user can recover by running `nanite install` again.
- The framework content is 76 small text files (492KB total). The extraction completes in milliseconds on modern hardware. The window for a crash is narrow.
- No file depends on another file being present first — the extracted tree is a collection of independent documents.

The risk is that a user hits a partial state and doesn't know to re-run install. The `--refresh` flag exists but is a separate invocation. There is no automatic self-healing on next binary startup.

## Recommendation

**Recommended (low effort):** Add a version marker file (e.g., `.nanite/.framework-version`) written as the last step of `ExtractTo`. On the next `ExtractTo` call, if the marker is missing or stale, treat the directory as incomplete and re-extract all files. This costs one extra `os.Stat` on the happy path and makes partial extraction self-healing.

**Alternative (higher effort):** Extract to a temp directory, then rename. This is the textbook atomic pattern but is more complex for a tree extraction (temp dir must be on the same filesystem as the target for atomic rename, and the rename replaces the entire tree which may break user modifications).

The version-marker approach fits better with the existing skip-user-modified semantics.

## References

- `internal/assets/framework.go:L57-116` — `ExtractTo` implementation
- `internal/service/install/install.go:L42-52` — `InstallHome` caller
- `internal/service/install/state.go` — state machine used by `InstallProject` but not `InstallHome`
- Prior audit: `2026-04-10-installer` — covered the install pipeline but noted `internal/assets/framework/` content was out of scope
