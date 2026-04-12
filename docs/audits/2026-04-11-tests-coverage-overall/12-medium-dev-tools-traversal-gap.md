# [Medium] Dev tools tests missing symlink escape and path traversal via ../ cases

**Scope:** internal/mcp/
**Topic:** Test Quality — security-relevant edge cases
**Date:** 2026-04-11

## Problem

`internal/mcp/dev_tools_test.go` (325 lines) tests `dev_bash`, `dev_glob`, `dev_edit`, `dev_read`, `dev_grep`, and `dev_write`. Path scoping tests exist (verify disallowed paths like `/etc` are rejected), but no tests exercise symlink escape or `../` traversal within an allowed directory.

## Evidence

Path-related tests in `dev_tools_test.go`:
- `TestDevBash_RejectsBadWorkingDir` — `/etc` rejected
- `TestDevGlob_RespectsAllowedPaths` — `/etc` rejected
- `TestDevEdit_PathScoping` — `/etc/passwd` rejected

Missing test cases:
1. **Symlink escape:** Create a symlink inside the allowed directory that points outside it (`ln -s /etc/passwd allowed_dir/escape`), then read/write via the symlink path. The `dev-tools-input-validation` audit identified symlink escape in `isAllowed` as a finding. No test verifies the fix.
2. **Path traversal via `../`:** `dev_read` with path `allowed_dir/../../../etc/passwd` — does `isAllowed` catch this? The test uses absolute paths (`/etc`) but not relative traversal from within the allowed tree.
3. **Null byte in path:** `dev_read` with path containing `\x00` — some path handling libraries truncate at null bytes.

`internal/mcp/dev_tools.go` uses `filepath.EvalSymlinks` at line 2+ references, but the test file only references `EvalSymlinks` at line 16 (in the test helper, not as a test subject).

## Impact

The `dev-tools-input-validation` audit identified symlink escape as a real finding. Without a regression test, the fix (if applied) has no automated verification. Path traversal via `../` is a classic attack on path-scoped tools.

## Recommendation

Add test cases:
1. `TestDevRead_SymlinkEscape` — create symlink to `/etc/passwd` inside allowed dir, attempt read via symlink, verify rejection
2. `TestDevRead_TraversalViaDotDot` — attempt read of `allowed_dir/../../../etc/passwd`, verify rejection
3. `TestDevWrite_TraversalViaDotDot` — same for write
4. `TestDevBash_WorkingDirTraversal` — working_dir set to `allowed_dir/../../etc`

## References

- `2026-04-10-dev-tools-input-validation` — symlink escape in `isAllowed`
- `internal/mcp/dev_tools_test.go:L16` — `EvalSymlinks` in test helper only
