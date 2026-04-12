# [High] PTY and subprocess bridges inherit full host environment including API keys

**Scope:** Provider — trust boundary #7 (PTY bridge input/output), #9 (Environment variables)
**Topic:** Security
**Date:** 2026-04-11

## Problem

Both `PTYBridge.streamCLI` and `SubprocessBridge.streamCLI` spawn CLI agent subprocesses without setting `cmd.Env`, which causes the child process to inherit the full host environment via Go's `os/exec` default behavior. This includes all API keys (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GOOGLE_API_KEY`, etc.) and auth credentials (`NANITE_AUTH_USER`, `NANITE_AUTH_PASSWORD`).

## Evidence

`pkg/provider/pty.go:L105-L109`:

```go
cmd := exec.CommandContext(ctx, p.cliPath, args...)

// Run in the sandbox directory if one was provided.
if dir, ok := SandboxDirFromContext(ctx); ok {
    cmd.Dir = dir
}
```

No `cmd.Env` is set. Same pattern in `pkg/provider/subprocess.go:L88-L92`:

```go
cmd := exec.CommandContext(ctx, s.cliPath, args...)

if dir, ok := SandboxDirFromContext(ctx); ok {
    cmd.Dir = dir
}
```

Again, no `cmd.Env` is set.

Contrast with the sandbox `AgentExec` which correctly builds a minimal environment (`internal/sandbox/exec.go:L100-L101,L210-L231`):

```go
env := buildAgentEnv(opts.Env)
// ...
cmd.Env = env
```

And `UserExec` which at least filters secrets (`internal/sandbox/exec.go:L155`):

```go
env := filterSecrets(os.Environ())
```

The PTY/subprocess bridges do neither. The spawned CLI agent (Claude, Codex, Gemini, Copilot, Aider, Junie, Kiro, Qwen) receives the full `os.Environ()` including:
- `ANTHROPIC_API_KEY`
- `OPENAI_API_KEY`
- `GOOGLE_API_KEY`
- `MISTRAL_API_KEY`
- `AZURE_OPENAI_API_KEY`
- `OPENROUTER_API_KEY`
- `OPENZEN_API_KEY`
- `NANITE_AUTH_USER` / `NANITE_AUTH_PASSWORD`
- `SUPPORT_DATABASE_URL`
- Any other secrets in the host process environment

The same issue exists in MCP stdio transport (`internal/mcp/stdio_transport.go:L46-L49`) and plugin subprocess manager (`internal/plugin/subprocess/manager.go:L128-L131`), which both use `cmd.Environ()` (the parent's full environment) plus additional env vars. These are already covered by `mcp-client-transport` and `plugin-system-plan-eval` respectively. The PTY/subprocess bridge instances are net-new.

## Impact

Any CLI agent spawned via PTY or subprocess bridge has access to all provider API keys and auth credentials. If a CLI agent is compromised via prompt injection (trust boundary #2), it can:
1. Use any API key for its own purposes (billing abuse)
2. Exfiltrate keys via its own tool calls (e.g., `web_fetch` or `bash` to POST keys to an external server)
3. Read `NANITE_AUTH_USER`/`NANITE_AUTH_PASSWORD` to authenticate back to the nanite API

This is distinct from (and worse than) the intentional "CLI needs its own API key" design — the spawned CLIs receive ALL keys, not just the one they need.

## Recommendation

Apply the same `filterSecrets` treatment used by `UserExec`:

```go
cmd := exec.CommandContext(ctx, p.cliPath, args...)
cmd.Env = filterSecrets(os.Environ())
```

Or better, use the minimal env approach from `AgentExec` and only add back the specific key the CLI adapter needs (e.g., `ANTHROPIC_API_KEY` for Claude CLI, `OPENAI_API_KEY` for Codex). Add a `RequiredEnvKeys() []string` method to the `CLIAdapter` interface.

## References

- `pkg/provider/pty.go:L105` — PTYBridge.streamCLI
- `pkg/provider/subprocess.go:L88` — SubprocessBridge.streamCLI
- `internal/sandbox/exec.go:L100-L101,L155,L210-L231` — correct pattern (AgentExec/UserExec)
- `internal/sandbox/exec.go:L37-L44` — secretKeyPatterns list
- Cross-ref: `provider-abstractions` finding (Gemini key in URL) — different leak vector for the same class of secret
- Cross-ref: `mcp-client-transport` — same env inheritance issue in MCP stdio transport
