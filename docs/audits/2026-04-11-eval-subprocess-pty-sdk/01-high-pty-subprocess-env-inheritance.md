# [High] PTY and Subprocess bridges inherit full parent environment including secrets

**Scope:** PTY/Subprocess spawning
**Topic:** Security — env var inheritance
**Date:** 2026-04-11

## Problem

Both `PTYBridge.streamCLI` and `SubprocessBridge.streamCLI` spawn CLI processes without setting `cmd.Env`, causing the child process to inherit the full environment of the Nanite host process. This includes `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `NANITE_AUTH_USER`, `NANITE_AUTH_PASSWORD`, and any other secrets present in the host environment.

## Evidence

`pkg/provider/pty.go:L105-109`:
```go
cmd := exec.CommandContext(ctx, p.cliPath, args...)

// Run in the sandbox directory if one was provided.
if dir, ok := SandboxDirFromContext(ctx); ok {
    cmd.Dir = dir
}
// No cmd.Env assignment — inherits full os.Environ()
```

`pkg/provider/subprocess.go:L88-92`:
```go
cmd := exec.CommandContext(ctx, s.cliPath, args...)

if dir, ok := SandboxDirFromContext(ctx); ok {
    cmd.Dir = dir
}
// No cmd.Env assignment — inherits full os.Environ()
```

Compare with `internal/sandbox/exec.go:L82-130` (`AgentExec`), which explicitly constructs a minimal environment using `buildAgentEnv()` with only `HOME`, `USER`, `LANG`, `TERM` and a restricted `PATH`. The sandbox also filters secrets via `secretKeyPatterns`.

The `internal/mcp/stdio_transport.go:L46-49` has the same pattern — no explicit `cmd.Env`:
```go
t.cmd = exec.Command(t.command, t.args...)
if len(t.env) > 0 {
    t.cmd.Env = append(t.cmd.Environ(), t.env...)
}
```

When `t.env` is empty, no `cmd.Env` is set at all, inheriting everything.

## Impact

A spawned CLI agent (Claude, Codex, Gemini, Copilot, Aider, Junie, Kiro, Qwen) has access to:
- All provider API keys (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`, etc.)
- Auth credentials (`NANITE_AUTH_USER`, `NANITE_AUTH_PASSWORD`)
- Database paths (`SUPPORT_DATABASE_URL`)
- Any other secrets the host process carries

Some of these CLIs (Claude, Gemini, Qwen) are themselves LLM agents that can be prompt-injected. A prompt injection attack through the LLM response could instruct the spawned CLI to exfiltrate environment variables.

This is the most significant gap vs. the sandbox's `AgentExec` which was designed specifically to prevent this.

## Recommendation

Apply `sandbox.filterSecrets()` (or equivalent) to the environment before spawning CLI processes. Add a shared helper:

```go
func sanitizedEnv() []string {
    return filterSecrets(os.Environ())
}
```

Set `cmd.Env = sanitizedEnv()` in both `PTYBridge.streamCLI` and `SubprocessBridge.streamCLI`. This preserves `PATH` and general env vars that CLIs need to function while stripping secret-bearing variables.

For defense in depth, consider the stricter `buildAgentEnv()` approach with an allowlist, but this may break CLIs that depend on environment state (e.g., `PATH` entries, locale, shell config).

## References

- `internal/sandbox/exec.go:L209-231` — `buildAgentEnv()` reference implementation
- `internal/sandbox/exec.go:L234-247` — `filterSecrets()` implementation
- Reviewer context: trust boundary #9 (Environment variables)
