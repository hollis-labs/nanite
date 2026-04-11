# [Medium] `NANITE_ARCHIVE_BASE` env var lets an external process steer where the installer moves `.agentrc/`

**Scope:** installer / archive path resolution
**Topic:** Security — attacker-controlled filesystem destination
**Date:** 2026-04-10

## Problem

The installer reads `NANITE_ARCHIVE_BASE` from the environment (via `archiveBaseOverride`) and uses its value as the destination directory for all migrated `.agentrc/` content. The env var is not validated: it is expanded via `ExpandArchiveBase` (which only handles `~` expansion) and then used as the base for `filepath.Join(base, "{basename}-{date}")`. There is no check that the resolved path is inside a safe location, no check that it is user-owned, no check that it does not contain `..` segments, and no refusal to operate on a base that points to a system directory.

The impact depends on whether the attacker controls the environment. In most cases, the user is the one setting the env var and the `.archived/` base is somewhere in their home directory, so the behavior is as intended. But environment variables are routinely inherited across subprocess boundaries, and Nanite's own subagent spawning (per the backend context, PTY bridges and `AgentExec`) pass the full env by default. A malicious plugin or a compromised provider could set `NANITE_ARCHIVE_BASE=/etc` before invoking the install service, and the installer would obediently move `.agentrc/` content into `/etc/`.

This is a variant of the "trust environment variables" trap. The code comment at `archiveBaseOverride` says "used in tests" but the env var has the same name and effect in production.

## Evidence

```go
// internal/service/install/migrate.go:L174-181
func archiveBaseOverride() string {
    if v := os.Getenv("NANITE_ARCHIVE_BASE"); v != "" {
        return v
    }
    return ArchiveBase
}
```

```go
// internal/service/install/archive.go:L13
const ArchiveBase = "~/Projects-apps/.archived"
```

```go
// internal/service/install/archive.go:L19-36
func ExpandArchiveBase(path string) (string, error) {
    if path == "" || path[0] != '~' {
        return path, nil
    }
    // "~user" (no slash) is not supported — return as-is.
    if path != "~" && path[1] != '/' {
        return path, nil
    }
    home, err := os.UserHomeDir()
    if err != nil {
        return "", fmt.Errorf("resolve home dir: %w", err)
    }
    if path == "~" {
        return home, nil
    }
    // path starts with "~/" — strip the two leading chars before joining.
    return filepath.Join(home, path[2:]), nil
}
```

Note `ExpandArchiveBase` accepts and returns absolute paths like `/etc` unchanged — it only expands `~` prefixes. A hostile env var value `NANITE_ARCHIVE_BASE=/etc` passes through untouched.

Caller:

```go
// internal/service/install/archive.go:L76-99
func ArchiveProjectAgentrc(projectDir, archiveBase, basename string, ts time.Time) (string, error) {
    archiveDir, err := ResolveArchiveDir(archiveBase, basename, ts)
    if err != nil {
        return "", fmt.Errorf("resolve archive dir: %w", err)
    }
    if err := os.MkdirAll(archiveDir, 0o755); err != nil {
        return "", fmt.Errorf("mkdir archive %s: %w", archiveDir, err)
    }

    if err := moveIfExists(
        filepath.Join(projectDir, ".agentrc"),
        filepath.Join(archiveDir, ".agentrc"),
    ); err != nil {
        return "", fmt.Errorf("move .agentrc: %w", err)
    }
    ...
}
```

`os.MkdirAll` with mode 0o755 will create `/etc/project-2026-04-10/` if the user is root, or fail with a permission error if not. On Linux, most developer accounts can't write to `/etc`, so the vector is blunted — but `~/.ssh/archive-2026-04-10/` or `~/bin/archive-2026-04-10/` or any user-owned path outside `~/Projects-apps/.archived` is wide open. A particularly unpleasant target is `~/Desktop/archive-2026-04-10/` where the moved `.agentrc` would reappear as a surprise folder on the user's desktop.

More seriously: if `NANITE_ARCHIVE_BASE` points at an existing directory with pre-existing files, the installer's `snapshotAdapterTargets` and state-marker writes will land inside it. A hostile process that sets `NANITE_ARCHIVE_BASE=~/Documents/important-project/` before the user runs `nanite install --migrate-from-agentrc` would cause `.agentrc/` to move on top of `~/Documents/important-project/.agentrc/`, silently replacing any existing content there. `os.Rename` will overwrite regular files in the destination under Unix semantics.

## Impact

- **Who:** users in environments where another process can set environment variables before `nanite install` runs. Examples: a compromised shell init file, a malicious plugin that spawns the install service with a custom env, a CI runner with injected env vars, a sandbox escape that reaches the host shell.
- **What:** `.agentrc/` content is moved to an unexpected location. If the destination already has content, that content is silently overwritten. There is no confirmation prompt, no notice, no validation.
- **Blast radius:** limited to files the running user can write. But within that scope, the attacker chooses where data lands.

Severity Medium because the precondition (attacker-controlled env) is not trivial on a well-configured developer machine, but the attack is noiseless and the impact is non-obvious recovery.

## Recommendation

1. **Normalize and validate the archive base early.** In `archiveBaseOverride` (or immediately after `ExpandArchiveBase` in callers), require that the resolved path is an absolute path inside the user's home directory. Reject anything outside `~/`. If the user wants to override this for testing, require an explicit flag like `--archive-base=` rather than reading an env var.

   ```go
   func archiveBaseOverride() string {
       v := os.Getenv("NANITE_ARCHIVE_BASE")
       if v == "" {
           return ArchiveBase
       }
       // Only honor in test environments. Production users use the default.
       if os.Getenv("NANITE_TEST") != "1" {
           return ArchiveBase
       }
       return v
   }
   ```

   Or, more surgical: accept the env var but require it to resolve to a path inside `~/`, with a clear error otherwise.

2. **Add a CLI flag for tests.** Tests currently `t.Setenv("NANITE_ARCHIVE_BASE", archBase)` — that pattern would move to a `WithArchiveBase(path)` service option, which the CLI does not expose. This keeps the env-var path off the production surface entirely.

3. **Warn loudly when the archive base is non-default.** Even with the flag approach, print a line to stderr: `nanite install: using non-default archive base /tmp/test-...`. Makes runtime behavior observable.

4. **Refuse to overwrite existing files** during archive population. `moveIfExists` calls `os.Rename` which will overwrite. Change to check-then-refuse or check-then-suffix, so an attacker cannot use the install as an arbitrary file replacer.

The first change is the smallest one and the most important. The rest are defensive.

## References

- `internal/service/install/migrate.go:L174-181` — `archiveBaseOverride`.
- `internal/service/install/archive.go:L13-121` — `ArchiveBase` constant, `ExpandArchiveBase`, `ArchiveProjectAgentrc`, `moveIfExists`.
- `cmd/nanite/install_cmd.go:L272-282` — `latestArchiveFor` also reads `NANITE_ARCHIVE_BASE` directly; same issue.
- Test usage: `integration_test.go:L25` etc. — `t.Setenv("NANITE_ARCHIVE_BASE", archBase)`. Legitimate use, but ties the test hook to the production behavior.
