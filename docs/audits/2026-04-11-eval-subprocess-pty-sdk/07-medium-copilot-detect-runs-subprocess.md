# [Medium] CopilotAdapter.Detect() executes subprocess during provider registration

**Scope:** CLI adapter detection
**Topic:** Security — subprocess execution during init
**Date:** 2026-04-11

## Problem

`CopilotAdapter.Detect()` runs `exec.Command(ghPath, "copilot", "--help").CombinedOutput()` to check if the `gh copilot` extension is installed. This subprocess execution happens during provider registration at server startup (`main.go:L373-383`), outside any sandbox or timeout context.

## Evidence

`pkg/provider/pty_copilot.go:L56-67`:
```go
func (a *CopilotAdapter) Detect() (string, bool) {
    // ...
    // Fall back to gh copilot extension.
    ghPath, err := exec.LookPath("gh")
    if err != nil {
        return "", false
    }
    out, err := exec.Command(ghPath, "copilot", "--help").CombinedOutput()
    if err != nil || len(out) == 0 {
        return "", false
    }
    a.ghMode = true
    return ghPath, true
}
```

`cmd/nanite/main.go:L363-383` — called during init:
```go
cliAdapters := []provider.CLIAdapter{
    // ...
    provider.NewCopilotAdapter(),
    // ...
}
for _, adapter := range cliAdapters {
    if path, ok := adapter.Detect(); ok {
        // register...
    }
}
```

Issues:
1. No timeout — if `gh copilot --help` hangs (e.g., network call, plugin update), server startup blocks indefinitely.
2. No context — the call cannot be cancelled.
3. Runs during init, not on first use — every server start pays this cost even if Copilot is never used.

The other 7 adapters use `lookPathExpanded()` which only does file stat checks. Copilot is the only adapter that runs a subprocess during detection.

## Impact

- Server startup latency increases by the time `gh copilot --help` takes to complete.
- If `gh` is installed but broken or slow, the entire server start blocks.
- No visibility into the hang (no log before the exec, no timeout).
- The subprocess inherits the full host environment (same env inheritance issue as finding 01).

## Recommendation

Replace the `exec.Command` call with a simpler check:

```go
// Check if gh copilot extension directory exists.
ghPath, err := exec.LookPath("gh")
if err != nil {
    return "", false
}
// Check for the extension in gh's extension directory.
home, _ := os.UserHomeDir()
extDir := filepath.Join(home, ".local", "share", "gh", "extensions", "gh-copilot")
if _, err := os.Stat(extDir); err == nil {
    a.ghMode = true
    return ghPath, true
}
```

Or add a timeout wrapper:
```go
ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
defer cancel()
out, err := exec.CommandContext(ctx, ghPath, "copilot", "--help").CombinedOutput()
```

## References

- `pkg/provider/cli_detect.go` — `lookPathExpanded()` reference (file stat only)
- `cmd/nanite/main.go:L362-387` — registration loop
