# [Medium] `dev_write` / `dev_edit` are non-atomic and silently reset file permissions

**Scope:** `internal/mcp/dev_tools.go` — `callWrite`, `callEdit`
**Topic:** Error handling, data integrity, least-surprise
**Date:** 2026-04-10

## Problem

Two related data-integrity issues in the write paths:

1. **Non-atomic writes.** Both tools use `os.WriteFile(path, ..., 0o644)` which is `open(O_CREATE|O_TRUNC) → write → close`. If the process dies between `O_TRUNC` and the final `write` — SIGKILL, panic elsewhere in the process, power loss — the file is left truncated or half-written. For `dev_edit` specifically, this is worse: the old content is gone before the new content is durably on disk, so a mid-write failure destroys user data.

2. **Permissions silently reset to 0o644.** `os.WriteFile` only honors the mode argument when creating a new file. For an existing file, the mode argument is ignored and the existing permissions are preserved — *except* that `dev_edit` goes via `os.WriteFile(path, data, 0o644)` which on some filesystems (and with some umask interactions) results in surprising behavior. More importantly, the existing file's mode is never *read* before the write, so if a user invoked `dev_edit` on a shell script at `0o755`, the file stays executable (OK) — but an attacker could prepend or append shell content and the file remains executable. For files that were previously `0o600` (a secret), the mode is also preserved on overwrite, which is neutral. The real hazard is the **new-file path**: `dev_write` creates files at a hardcoded `0o644`, which is world-readable on typical systems. A tool that writes secrets to a file — even inadvertently — leaves them world-readable to every local user.

Neither issue is a sandbox escape, but both affect data durability and least-privilege posture, and both are addressable with small changes that do not alter the tool contract.

## Evidence

`callWrite`:

```go
// internal/mcp/dev_tools.go:308-328
func (d *DevToolsTransport) callWrite(args map[string]any) (*ToolResult, error) {
    ...
    dir := filepath.Dir(path)
    if err := os.MkdirAll(dir, 0o755); err != nil {
        return errorResult(fmt.Sprintf("mkdir: %v", err)), nil
    }

    if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
        return errorResult(fmt.Sprintf("write: %v", err)), nil
    }
    ...
}
```

`callEdit`:

```go
// internal/mcp/dev_tools.go:330-386
func (d *DevToolsTransport) callEdit(args map[string]any) (*ToolResult, error) {
    ...
    data, err := os.ReadFile(path)
    if err != nil {
        return errorResult(fmt.Sprintf("read: %v", err)), nil
    }
    content := string(data)
    ...
    if err := os.WriteFile(path, []byte(newContent), 0o644); err != nil {
        return errorResult(fmt.Sprintf("write: %v", err)), nil
    }
    ...
}
```

No `os.Stat` to capture the prior mode. No `Rename`-based atomic swap. No temp-file-and-rename pattern. No checksum of the prior content to confirm nothing raced the read-write window (the sequence is: read → compute new → write; any concurrent edit by any other process is silently overwritten).

## Impact

- **Data loss:** a SIGKILL-sized event during `callEdit` loses the prior file content. For config files, source code, or notes, this is surprising and hard to recover.
- **Permission regression:** new files land at `0o644`. If a developer uses `dev_write` to create a file that later holds secrets, or if a downstream tool reads the file on the assumption it is protected, the default is wrong.
- **Who:** any caller. Triggered by any write operation.
- **Reproducibility:** deterministic for the mode issue; probabilistic (depends on process lifecycle) for the atomicity issue.

## Recommendation

1. **Atomic writes via temp-file-and-rename.** Write to `<path>.tmp.<random>`, `fsync` (optional but recommended), then `os.Rename(tmp, path)`. `Rename` is atomic on POSIX within a single filesystem and is the standard idiom:

   ```go
   func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
       dir := filepath.Dir(path)
       tmp, err := os.CreateTemp(dir, ".dev-write-*.tmp")
       if err != nil {
           return err
       }
       tmpPath := tmp.Name()
       defer os.Remove(tmpPath) // noop if Rename succeeds
       if _, err := tmp.Write(data); err != nil {
           tmp.Close()
           return err
       }
       if err := tmp.Chmod(mode); err != nil {
           tmp.Close()
           return err
       }
       if err := tmp.Sync(); err != nil {
           tmp.Close()
           return err
       }
       if err := tmp.Close(); err != nil {
           return err
       }
       return os.Rename(tmpPath, path)
   }
   ```

2. **Preserve existing file mode on edit.** Before calling the atomic writer, `os.Stat(path)` the existing file and pass its mode through. If the file doesn't exist, fall back to a sensible default — `0o600` for `dev_write` since anything the agent creates is plausibly sensitive.

   ```go
   mode := os.FileMode(0o600) // new-file default
   if info, err := os.Stat(path); err == nil {
       mode = info.Mode().Perm()
   }
   return writeFileAtomic(path, data, mode)
   ```

3. **Document the race window.** Even with atomic writes, `dev_edit` has a read-modify-write race: if another process edits the file between the read and the write, the other process's changes are lost. This is the same semantics as most text editors (open-edit-save) and is probably acceptable, but it should be documented in the tool description so the LLM doesn't expect finer guarantees.

4. **(Optional) compare-and-set for multi-agent scenarios.** If nanite ever runs multiple concurrent agents on the same working tree, a checksum-based CAS — "file's SHA256 must still equal `<pre-read hash>` when writing" — is the right primitive. Out of scope for the immediate fix.

Recommended severity: Medium. No security bypass, but data loss and permission regressions are real and the fix is a ~30-line helper used from both handlers.

## References

- `internal/mcp/dev_tools.go:L308-L328` — `callWrite`
- `internal/mcp/dev_tools.go:L330-L386` — `callEdit`
- Go stdlib: `os.CreateTemp`, `os.Rename`, `os.File.Sync`
- POSIX `rename(2)` — atomic within one filesystem
- CWE-367 — TOCTOU race (the read-modify-write window is mitigated, not eliminated, by atomic writes)
