# [High] Environment variable secret exposure across multiple subsystems

**Scope:** Multiple — trust boundary #9 (Environment variables)
**Topic:** Security
**Date:** 2026-04-11

## Problem

API keys and credentials are read from environment variables and the OS keychain at startup and stored in provider struct fields. Multiple subsystems leak or expose these values through different vectors. The `filterSecrets` and `isSecretKey` functions in `internal/sandbox/exec.go` are only applied to sandbox-spawned subprocesses — other subprocess spawn sites inherit the full environment.

## Evidence

### 1. Secret pattern matching is incomplete

`internal/sandbox/exec.go:L37-L44`:

```go
var secretKeyPatterns = []string{
	"KEY",
	"SECRET",
	"TOKEN",
	"PASSWORD",
	"CREDENTIAL",
	"AUTH",
}
```

This misses:
- `SUPPORT_DATABASE_URL` (contains database connection string — no "KEY"/"SECRET"/"TOKEN" substring)
- `HTTP_PROXY` / `HTTPS_PROXY` (if set to an authenticated proxy URL with credentials in the URL)
- `NANITE_DB` (database path — not a secret per se, but reveals filesystem layout)
- Any env var with "API" in the name but not "KEY" (e.g., hypothetical `GIPHY_API_ENDPOINT`)

### 2. Plugin config EnvVar exfiltration

`internal/plugin/config.go:L151-L161` — `PluginConfig.Get` reads arbitrary env vars specified by the plugin manifest's `env_var` field:

```go
if entry.EnvVar != "" {
    if v := os.Getenv(entry.EnvVar); v != "" {
        return v, nil
    }
}
```

And `internal/plugin/loader.go:L156-L161` — subprocess plugin config resolution:

```go
for key, entry := range m.Config {
    if entry.EnvVar != "" {
        if v := os.Getenv(entry.EnvVar); v != "" {
            config[key] = v
            continue
        }
    }
}
```

A malicious plugin manifest can specify `env_var: ANTHROPIC_API_KEY` for any config entry and the host will dutifully read it and pass it to the plugin.

### 3. Error messages expose env var names

`internal/plugin/config.go:L176`:

```go
return "", fmt.Errorf("required config key %q not set for plugin %s (env: %s)", key, pc.pluginID, entry.EnvVar)
```

This error message includes the expected environment variable name. If the error propagates to an API response (through plugin load failure → API status), it reveals which env vars the system expects.

### 4. Auth credentials read once at middleware init, never rotated

`internal/server/auth.go:L16-L17`:

```go
user := os.Getenv(brand.Env("AUTH_USER"))
pass := os.Getenv(brand.Env("AUTH_PASSWORD"))
```

These are captured in a closure at middleware creation time. If the env vars are changed during runtime (e.g., via a compromised subprocess that has env write access), the middleware continues using the old values. This is a minor issue but relevant to the trust model.

### 5. Subprocess env inheritance (cross-subsystem)

Already detailed in finding 02, but summarizing the full surface:
- PTY bridge: inherits full env (finding 02)
- Subprocess bridge: inherits full env (finding 02)
- MCP stdio transport: `cmd.Environ()` + additions (`internal/mcp/stdio_transport.go:L48`)
- Plugin subprocess manager: `cmd.Environ()` + additions (`internal/plugin/subprocess/manager.go:L130`)
- Plugin install via git clone: `exec.Command("git", "clone", ...)` in `internal/api/plugins.go:L228` — inherits full env, git receives all API keys

## Impact

1. Any subprocess spawned by nanite (PTY, MCP, plugin, git clone) receives all API keys unless it goes through `sandbox.AgentExec` or `sandbox.UserExec`.
2. A malicious plugin manifest can exfiltrate any env var via the config `env_var` mechanism.
3. The `filterSecrets` pattern list has gaps that allow `SUPPORT_DATABASE_URL` and similar non-obvious secrets through.
4. Error messages may reveal env var names to API callers.

## Recommendation

1. Centralize secret filtering: extract `filterSecrets` / `isSecretKey` from `sandbox/exec.go` into a shared `internal/secrets/env.go` package. Use it everywhere a subprocess is spawned.
2. Add `DATABASE_URL`, `DB`, `DSN`, `CONN`, `PROXY` to `secretKeyPatterns`.
3. Restrict plugin manifest `env_var` to a namespaced prefix (e.g., `NANITE_PLUGIN_*`).
4. Apply `filterSecrets` to PTY bridge, subprocess bridge, MCP stdio transport, plugin subprocess manager, and git clone commands.

## References

- `internal/sandbox/exec.go:L37-L44` — secretKeyPatterns
- `internal/sandbox/exec.go:L234-L258` — filterSecrets / isSecretKey
- `internal/plugin/config.go:L151-L161,L176` — env var read and error exposure
- `internal/plugin/loader.go:L156-L161` — subprocess config env read
- `internal/server/auth.go:L16-L17` — auth env read
- `internal/api/plugins.go:L228` — git clone inherits full env
- Cross-ref: finding 02 (PTY/subprocess bridge full env inheritance)
- Cross-ref: finding 01 (plugin manifest EnvVar exfiltration)
