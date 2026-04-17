# [Medium] `WriteManagedSection` uses `os.WriteFile` with no temp-file-then-rename pattern; crash mid-write leaves a torn file

**Scope:** managed-section parser / writer
**Topic:** Memory & resources — filesystem atomicity
**Date:** 2026-04-11

## Problem

`WriteManagedSection` writes the final assembled string directly to the target path via `os.WriteFile`, which on POSIX truncates then writes. If the process crashes, loses power, or receives SIGKILL mid-write, the file is left in a partially-written state: the truncate has happened, but some or all of the new bytes have not. There is no temp-file-then-`os.Rename` atomic-replace pattern, no file lock, and no backup of the prior content.

The same pattern exists in `RemoveManagedSection`, which performs the same read-modify-truncate-write sequence.

This is distinct from the "file-level data loss via marker injection" issues (finding 01 and installer-03): those are *silent* corruption on successful writes. This is *catastrophic* corruption on unsuccessful writes. A Ctrl-C at the wrong millisecond during `nanite install` will produce an empty or truncated CLAUDE.md / AGENTS.md / GEMINI.md / OPENCODE.md.

## Evidence

```go
// internal/agent/managed_section.go:L28-79 (abbreviated)
func WriteManagedSection(path string, content string) error {
    block := buildManagedBlock(content)

    data, err := os.ReadFile(path)
    // ... branching for non-existent / no-markers / malformed ...

    var b strings.Builder
    b.WriteString(before)
    b.WriteString(block)
    if after != "" {
        b.WriteString("\n")
        b.WriteString(after)
    }

    return os.WriteFile(path, []byte(b.String()), 0o644)
}
```

```go
// internal/agent/managed_section.go:L128-184 (abbreviated)
func RemoveManagedSection(path string) (removedAny bool, becameEmpty bool, err error) {
    data, err := os.ReadFile(path)
    // ...
    combined := before + after
    // ...
    if err := os.WriteFile(path, []byte(combined), 0o644); err != nil {
        return false, false, fmt.Errorf("write %s: %w", path, err)
    }
    return true, false, nil
}
```

`os.WriteFile` delegates to `os.OpenFile(path, O_WRONLY|O_CREATE|O_TRUNC, 0o644)` followed by `Write` followed by `Close`. The `O_TRUNC` happens first: if the subsequent `Write` or `Close` fails (or the process dies), the file is already truncated to zero bytes, and the new content is not yet persisted to disk.

## Impact

Reproduction is straightforward:

1. Make CLAUDE.md large enough (or system slow enough) that write takes >1ms.
2. Run `nanite install` and immediately hit Ctrl-C during the adapter sync phase.
3. Observe CLAUDE.md is now either empty, or contains only the first N bytes of the new content.

Who is affected:
- Any user who interrupts `nanite install` mid-run — not uncommon if the run is longer than expected.
- Any user whose system loses power, OOM-kills the process, or has a filesystem that crashes during write.
- Any test suite that uses `WriteManagedSection` inside a timeout-harnessed goroutine — if the timeout fires mid-write, the temp dir is left with a torn file.

The blast radius is bounded:
- Adapter target files are generally small (a few KB at most). Probability of catching a crash mid-write is low in practice.
- The installer runs `snapshotAdapterTargets` before editing, so a manual rollback to the archive is possible — if the user notices the corruption before running installer again.
- The pre-edit snapshot lives in `.nanite/archive/...`, accessible for recovery.

But "bounded" is not "zero", and the fix is cheap. Users have reasonable expectations that a tool updating *their* markdown files will not truncate them on failure.

## Recommendation

Use the temp-file-then-rename atomic-replace idiom:

```go
func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
    dir := filepath.Dir(path)
    tmp, err := os.CreateTemp(dir, ".nanite-managed-*.tmp")
    if err != nil {
        return fmt.Errorf("create temp file: %w", err)
    }
    tmpPath := tmp.Name()
    defer os.Remove(tmpPath) // best-effort cleanup on error

    if _, err := tmp.Write(data); err != nil {
        tmp.Close()
        return fmt.Errorf("write temp: %w", err)
    }
    if err := tmp.Sync(); err != nil {
        tmp.Close()
        return fmt.Errorf("sync temp: %w", err)
    }
    if err := tmp.Close(); err != nil {
        return fmt.Errorf("close temp: %w", err)
    }
    if err := os.Chmod(tmpPath, mode); err != nil {
        return fmt.Errorf("chmod temp: %w", err)
    }
    if err := os.Rename(tmpPath, path); err != nil {
        return fmt.Errorf("rename temp: %w", err)
    }
    return nil
}
```

This is atomic on POSIX (rename(2) within the same filesystem) and approximately atomic on modern Windows. The target file is either the old content or the new content — never partial.

Apply to both `WriteManagedSection` and `RemoveManagedSection`.

### Alternative: use the snapshot + fsync the parent dir

A minimal change is to `fsync` the parent directory after the rename, to guarantee the rename is visible on crash. This is pedantic but required for true durability.

### Pair with installer's snapshot layer

Installer already writes a pre-edit snapshot to the archive dir. If the atomic-write layer is added, the snapshot becomes the rollback-of-last-resort rather than the primary defense. Both should stay. Do not rely solely on the snapshot — it lives under the same filesystem and does not protect against filesystem-level corruption.

## References

- `internal/agent/managed_section.go:L78` — `os.WriteFile` call in `WriteManagedSection`.
- `internal/agent/managed_section.go:L180` — same pattern in `RemoveManagedSection`.
- `internal/agent/managed_section.go:L174` — the empty-file truncate path in `RemoveManagedSection` has the same issue.
- `internal/service/install/claudemd.go:L128` — `UpdateCLAUDEmd` also uses direct `os.WriteFile` for the cleaned-content write before delegating to `WriteManagedSection`; same fix applies.
- Go stdlib docs on `os.Rename`: https://pkg.go.dev/os#Rename
- Related: `docs/audits/2026-04-10-installer/` — installer's snapshot/rollback layer is the current mitigation.
