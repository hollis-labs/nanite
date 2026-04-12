# [Medium] Subprocess plugin entrypoint parsing allows argument injection

**Scope:** Plugin capability model
**Topic:** Security — command injection
**Date:** 2026-04-11

## Problem

`parseEntrypoint` in `loader.go` splits the entrypoint string by whitespace using `strings.Fields`. The entrypoint comes from `plugin.yaml`, which is a user-installed file. A crafted entrypoint like `python3 -c "import os; os.system('...')"` or entries with shell metacharacters could inject arguments.

## Evidence

`internal/plugin/loader.go:L177-183`:

```go
func parseEntrypoint(entrypoint, pluginDir string) (string, []string) {
    parts := strings.Fields(entrypoint)
    if len(parts) == 0 {
        return entrypoint, nil
    }
    return parts[0], parts[1:]
}
```

The result is passed to `subprocess.DefaultManagerConfig(command, dp.Dir)` and eventually to `exec.Command(command, args...)`. While `exec.Command` does not invoke a shell (so shell metacharacters are safe), the argument splitting is naive — a manifest author can pass arbitrary flags to any command.

Additionally, `loader.go:L146-149` falls back to resolving the command as a relative path from the plugin dir:

```go
absCmd := filepath.Join(dp.Dir, command)
if _, err := exec.LookPath(absCmd); err != nil {
    return nil, fmt.Errorf("entrypoint %q not found: %w", m.Entrypoint, err)
}
command = absCmd
```

There is no validation that the entrypoint binary is within the plugin directory or on an allowed list.

## Impact

- A malicious plugin manifest can specify any binary on the system as its entrypoint (e.g., `/bin/sh`, `/usr/bin/env`, `python3`).
- The subprocess runs with the same privileges as the nanite process.
- Mitigated by the fact that installing a plugin already requires filesystem access and trust — if an attacker can write `plugin.yaml`, they can also write a malicious binary. However, this matters for plugin-installation-from-URL scenarios (if ever implemented).

## Recommendation

1. Validate that the entrypoint binary resolves to a path within the plugin directory or on an explicit allowlist.
2. Consider requiring subprocess plugins to have an executable file with a specific name (e.g., `plugin` or `main`) rather than accepting arbitrary commands.
3. Log the resolved entrypoint path at INFO level during load for auditability.

## References

- `internal/plugin/loader.go:L138-172` — `newSubprocessPluginFromManifest`
- `internal/plugin/loader.go:L177-183` — `parseEntrypoint`
- `internal/plugin/subprocess/manager.go` — process lifecycle
