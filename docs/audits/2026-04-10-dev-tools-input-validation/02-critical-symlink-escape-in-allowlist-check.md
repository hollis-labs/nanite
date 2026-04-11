# [Critical] `isAllowed` symlink check is bypassable for non-existent paths — write/edit escape

**Scope:** `internal/mcp/dev_tools.go` — `isAllowed` helper, used by every file tool
**Topic:** Security — path traversal via symlinks, TOCTOU
**Date:** 2026-04-10

## Problem

`DevToolsTransport.isAllowed` attempts to resolve symlinks via `filepath.EvalSymlinks` before checking the path against the allowlist, but falls back to the raw absolute path if `EvalSymlinks` returns an error. For any file that does not exist yet — the entire `dev_write` path, and the parent-directory-creation path of `dev_write` — `EvalSymlinks` returns an error and the check is performed against the unresolved path. The kernel, however, does resolve symlinks when the subsequent `os.MkdirAll` / `os.WriteFile` runs. This is a classic TOCTOU symlink-escape primitive. Any attacker who can plant a symlink inside `~/Projects-apps` or `~/Projects` — which is inside the allowlist — can redirect writes to any user-writable path on the system.

The same fallback affects `dev_read` / `dev_grep` / `dev_glob` when reading through a broken-target symlink, but the write path is the one that escalates directly to arbitrary host modification.

## Evidence

The allowlist check:

```go
// internal/mcp/dev_tools.go:36-53
func (d *DevToolsTransport) isAllowed(path string) error {
    abs, err := filepath.Abs(path)
    if err != nil {
        return fmt.Errorf("invalid path: %w", err)
    }
    // Resolve symlinks.
    real, err := filepath.EvalSymlinks(abs)
    if err != nil {
        // File may not exist yet (write). Check parent dir.
        real = abs
    }
    for _, allowed := range d.AllowedPaths {
        if strings.HasPrefix(real, allowed+"/") || real == allowed {
            return nil
        }
    }
    return fmt.Errorf("path %q is outside allowed directories", path)
}
```

The comment `// File may not exist yet (write). Check parent dir.` describes the intent correctly but the code does **not** check the parent dir. It assigns `real = abs` (the unresolved path including any symlink components) and falls through to the prefix check against the unresolved value.

The write path then issues `os.MkdirAll(filepath.Dir(path), 0o755)` and `os.WriteFile(path, ...)`:

```go
// internal/mcp/dev_tools.go:308-328
func (d *DevToolsTransport) callWrite(args map[string]any) (*ToolResult, error) {
    path, _ := args["path"].(string)
    content, _ := args["content"].(string)
    if path == "" {
        return errorResult("path is required"), nil
    }
    if err := d.isAllowed(path); err != nil {
        return errorResult(err.Error()), nil
    }

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

Both `MkdirAll` and `WriteFile` follow symlinks in intermediate path components. Go's stdlib has no `O_NOFOLLOW`-on-parent-dirs primitive here; symlink-safe writes require `openat`-style code that nanite does not currently have.

### Attack walkthrough

Preconditions: attacker can influence tool arguments (prompt injection). Attacker has, at some earlier point, caused a symlink to be created inside an allowed directory. This is trivially accomplished via `dev_bash` (finding 01), via `nanite_code_execute`, or via an external process the user runs — the latter is a supply-chain concern, the former two are one-shot.

Step 1 — plant the symlink. Via `dev_bash` (before this finding is fixed), or via `nanite_code_execute`:

```text
ln -s /Users/victim/.ssh /Users/victim/Projects-apps/.pwn
```

Step 2 — write through the symlink:

```text
dev_write(path="/Users/victim/Projects-apps/.pwn/authorized_keys", content="ssh-rsa AAAA... attacker@evil")
```

Walkthrough inside `isAllowed`:
- `abs = /Users/victim/Projects-apps/.pwn/authorized_keys`
- `EvalSymlinks(abs)`: `.pwn/authorized_keys` does not exist → error → `real = abs`
- Prefix check: `/Users/victim/Projects-apps/.pwn/authorized_keys` starts with `/Users/victim/Projects-apps/` → **allowed**

Then in `callWrite`:
- `dir = /Users/victim/Projects-apps/.pwn`
- `MkdirAll(dir)`: the path exists (it's a symlink to `~/.ssh`) → no-op, returns nil
- `WriteFile(/Users/victim/Projects-apps/.pwn/authorized_keys, ...)`: kernel resolves `.pwn` to `~/.ssh`, writes `~/.ssh/authorized_keys`

The attacker now has persistent SSH access as the victim.

### `dev_edit` is worse on existence-match

`dev_edit` reads via `os.ReadFile` and writes via `os.WriteFile`, and both follow symlinks. If `/Users/victim/Projects-apps/.pwn/config.yaml` already exists (via the symlink, the *real* file is `~/.ssh/config`), then:
- `EvalSymlinks` succeeds, returns `~/.ssh/config`
- Prefix check fails — **blocked**

So `dev_edit` is safe against the existence-match case. But it is still exposed through the non-existence case if the final component does not exist: editing a file "that should exist" fails the read but may still be writable depending on flow. In practice the attack surface here is `dev_write` (covered above) and `dev_read`/`dev_grep` with an attacker-planted dead symlink (exposes filesystem-existence oracle but not content).

### `HasPrefix` edge case

Even setting aside the EvalSymlinks fallback, the prefix check uses `strings.HasPrefix(real, allowed+"/") || real == allowed`. This is correct for the suffix-collision case (`/allowed-dir-evil` is not accepted as `/allowed-dir/`). It is however only as strong as the `real` value it receives. If `real = abs` (unresolved), the prefix check is inherently unreliable. Any fix needs to make `real` trustworthy.

## Impact

- **Who:** any caller who can both (a) get one symlink planted inside `~/Projects-apps` or `~/Projects` and (b) issue one `dev_write`. Bar (a) is trivially cleared by any prior prompt-injection step or by any file the user has cloned from an untrusted git repo that ships a symlink.
- **What:** write-anywhere-the-user-can-write. On a dev workstation this is host compromise: `~/.ssh/authorized_keys`, `~/.bashrc`, `~/.zshrc`, `~/.claude/settings.json`, `~/.config/*/tokens.json`, nanite's own store SQLite, the nanite binary in `~/go/bin/`, cron/launchd plists.
- **Reproducibility:** deterministic. No race window — the attack works on a single call once the symlink is in place.
- **Blast radius:** compounds with finding 01 (`dev_bash` bypasses sandbox), which provides a trivial symlink-planting primitive, and with finding 03 (`web_fetch` SSRF) which provides the exfiltration half.

## Recommendation

Two layers; apply both.

**Layer 1 — make `isAllowed` symlink-safe even for non-existent targets.** Resolve the nearest existing ancestor, check that, then check that the remainder is a lexical suffix without any further components that could be symlinks planted in a race. A simple correct shape:

```go
func (d *DevToolsTransport) isAllowed(path string) error {
    abs, err := filepath.Abs(path)
    if err != nil {
        return fmt.Errorf("invalid path: %w", err)
    }
    // Walk up until we find an existing ancestor, then EvalSymlinks that.
    // Anything above must be inside the allowlist after full resolution.
    resolved := abs
    for {
        real, err := filepath.EvalSymlinks(resolved)
        if err == nil {
            resolved = real
            break
        }
        parent := filepath.Dir(resolved)
        if parent == resolved {
            return fmt.Errorf("cannot resolve ancestors of %q", path)
        }
        resolved = parent
    }
    // Now re-join any non-existent tail, but only if it contains no "..".
    rel, err := filepath.Rel(resolved, abs)
    if err != nil || strings.Contains(rel, "..") {
        return fmt.Errorf("path %q escapes resolved root", path)
    }
    final := filepath.Join(resolved, rel)
    for _, allowed := range d.AllowedPaths {
        if final == allowed || strings.HasPrefix(final, allowed+string(os.PathSeparator)) {
            return nil
        }
    }
    return fmt.Errorf("path %q is outside allowed directories", path)
}
```

The key invariant: the *nearest existing ancestor* of the target is always resolved through symlinks, and any remaining non-existent tail is appended *lexically*, never followed by the kernel at check time. The subsequent `MkdirAll` / `WriteFile` still has the kernel-follow-symlink behavior, so this only raises the bar — it does not close every TOCTOU window. Which brings us to:

**Layer 2 — use `O_NOFOLLOW` on the final component when writing.** For `dev_write` and `dev_edit`, replace `os.WriteFile(path, data, 0o644)` with:

```go
f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscall.O_NOFOLLOW, 0o644)
```

`O_NOFOLLOW` refuses to open the final component if it's a symlink. For parent components, the correct primitive is `openat` + `O_NOFOLLOW` in a loop from the allowed root — nontrivial but necessary for TOCTOU safety. An acceptable interim: document that file tools are scoped to directories under the user's trust, and refuse to traverse *any* symlinked directory inside an allowed root by extending layer 1 to reject `rel` that crosses a symlinked component. (This breaks legitimate uses of symlinked directories in `~/Projects-apps`; trade-off worth discussing.)

**Layer 3 — shared helper.** The sandbox audit flagged the same class of issue in `internal/sandbox/` (seatbelt profile escaping in finding 01; the installer audit flagged it in symlink-creating paths). A shared `internal/pathsafe` helper — `ResolveUnder(root, userPath) (safe string, err error)` — would consolidate this into one tested primitive and let both sandbox code, installer code, and MCP dev tools share the same implementation.

Recommended severity: Critical. The primitive is trivial to reach once any other prompt-injection vector gives the attacker one symlink-creation step, and the impact is host-wide modification.

## References

- `internal/mcp/dev_tools.go:L36-L53` — `isAllowed`, the broken helper
- `internal/mcp/dev_tools.go:L308-L328` — `callWrite`, the primary escalation path
- `internal/mcp/dev_tools.go:L330-L386` — `callEdit`, partial exposure on non-existent targets
- `internal/mcp/dev_tools.go:L160-L206` — `callRead` (oracle via dead symlinks)
- `docs/audits/2026-04-10-sandbox-hardening/01-critical-session-id-path-traversal-and-seatbelt-injection.md` — related trust-boundary class
- `docs/audits/2026-04-10-installer/` — Symlink handling theme; `02-high-*` and `04-high-*` family
- OWASP CWE-59 — Link Following
- Go stdlib: `os.OpenFile` with `syscall.O_NOFOLLOW`; see also `openat(2)` for full TOCTOU mitigation
- Related: finding `01-critical-dev-bash-bypasses-sandbox.md` (provides the symlink-planting primitive)
