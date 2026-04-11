# [Medium] Stdio transport inherits the full host environment into every MCP subprocess — secret leakage surface

**Scope:** MCP client transport — subprocess environment
**Topic:** Security
**Date:** 2026-04-10

## Problem

`StdioTransport.start` at `internal/mcp/stdio_transport.go:L47-L49`:

```go
if len(t.env) > 0 {
    t.cmd.Env = append(t.cmd.Environ(), t.env...)
}
```

`t.cmd.Environ()` returns the host process's environment — which on Nanite includes every API key and secret the user has configured: `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `OPENROUTER_API_KEY`, `GIPHY_API_KEY`, `NANITE_AUTH_USER`, `NANITE_AUTH_PASSWORD`, `SUPPORT_DATABASE_URL`, `HTTP_PROXY`, etc. (Reviewer-backend context trust-boundary item 9 enumerates these.)

This means every MCP subprocess inherits every API key Nanite holds. A user-installed MCP server (a binary the user downloaded from anywhere) can `os.Getenv("ANTHROPIC_API_KEY")` and phone it home. There is no "clean env" mode, no allowlist, no opt-in mechanism to share specific vars.

Additionally, when `t.env` is empty (the no-extra-env case), the transport falls through to `cmd` default behavior, which is also "inherit everything." So the inheritance is the default even for the empty case — L47's `if` check only affects whether user-supplied extras are appended.

## Evidence

`internal/mcp/stdio_transport.go:L46-L68`:

```go
t.cmd = exec.Command(t.command, t.args...)
if len(t.env) > 0 {
    t.cmd.Env = append(t.cmd.Environ(), t.env...)
}
// ... when t.env is empty, t.cmd.Env stays nil, which means
// "inherit everything" per os/exec docs
```

From `os/exec` godoc: "If Env is nil, the new process uses the current process's environment."

The `MCPServerConfig` struct persisted at `internal/store/mcp_servers.go` has an `Env` field, and users populate it via the API (`POST /api/mcp-servers`). That field gets JSON-unmarshalled into a `[]string` of `"KEY=VALUE"` pairs and appended to the inherited env. **There is no way for a user to specify "run this server with a clean environment, with only these specific vars passed in."**

For comparison, Nanite's own `DevToolsTransport` and `CodeExecTransport` (in the sandbox audit scope) use seatbelt/bwrap with environment scoping. The MCP client's subprocess path has no such scoping.

## Impact

- **Hostile MCP server exfiltrates keys.** A malicious stdio MCP server binary prints `os.Environ()` to a remote URL on first call. The user installed it intending to get weather info; they leaked every API key.
- **Accidental leakage.** A debugging-flavor MCP server logs its environment to stderr for diagnostics (some well-known MCPs do this). Combined with finding 08 (stderr currently discarded), the leak is temporarily hidden; when finding 08 is fixed, it becomes visible in the host log.
- **Supply-chain risk.** An MCP server with a legitimate use case gets taken over in a supply-chain attack. The attacker's payload reads env vars on first call.

This is Medium because: (a) the mitigation requires the user to install a hostile server, which is a trust decision they already made, but (b) the principle of least privilege says environment sharing should be opt-in per-server, and (c) the alternative (clean env + user-declared passthroughs) is a well-known pattern and easy to implement.

## Recommendation

1. **Clean env by default.** Change `StdioTransport` to start subprocesses with an empty environment plus only user-declared passthroughs:

   ```go
   baseEnv := []string{
       "PATH=" + os.Getenv("PATH"),  // minimum for command resolution
       "HOME=" + os.Getenv("HOME"),  // minimum for typical tool behavior
   }
   t.cmd.Env = append(baseEnv, t.env...)
   ```

2. **Add an `InheritAll bool` escape hatch.** On `MCPServerConfig`, add an opt-in for "trust this server with the full host env." Default false. User must explicitly set it when they really do need it (rare).

3. **Document the change.** Update `.mcp.json` export/import to carry the flag so configs round-trip.

4. **Secret scrub on log lines.** Unrelated to this finding but related class: if stderr draining (finding 08) lands, consider a simple regex scrub that masks anything matching common API-key patterns (`sk-*`, `xoxb-*`, `AIza*`, etc.) before logging.

5. **Mention in docs.** The reviewer-backend context's trust-boundary item 9 lists env vars explicitly. Beta docs for installing MCP servers should mention that they run with host env by default today and will switch to clean env later.

## References

- `internal/mcp/stdio_transport.go:L46-L68` — env inheritance path
- Reviewer-backend context `.nanite/agents/reviewer-backend.md` trust-boundary item 9
- Related: finding 08 (stderr draining interacts with secret visibility)
