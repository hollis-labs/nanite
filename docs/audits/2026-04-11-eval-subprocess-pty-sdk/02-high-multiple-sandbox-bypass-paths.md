# [High] Multiple exec.Command call sites bypass sandbox.AgentExec

**Scope:** Subprocess execution paths
**Topic:** Security — sandbox bypass
**Date:** 2026-04-11

## Problem

Several packages execute subprocesses via raw `exec.Command` or `exec.CommandContext` without routing through `sandbox.AgentExec` or `sandbox.UserExec`. This bypasses the denylist, environment filtering, CWD restriction, and OS-level sandbox that `AgentExec` provides.

## Evidence

The following non-test, non-sandbox `exec.Command` call sites were identified:

### 1. `internal/shell/exec.go:L51` — shell.Exec
```go
cmd := exec.CommandContext(ctx, shell, "-c", command)
```
Used by `internal/api/shell.go:L181,189` for `git rev-parse` and `git status`. Runs with the user's full environment minus nothing. No denylist check, no secret filtering. Called by an API handler (`handleGetWorkingDirectory`).

### 2. `internal/mcp/dev_tools.go:L527` — dev_bash tool
```go
cmd := exec.CommandContext(ctx, "sh", "-c", command)
```
The `dev_bash` built-in MCP tool executes arbitrary shell commands. No denylist, no sandbox wrapping. The command string comes from LLM tool arguments (trust boundary #3).

### 3. `internal/skill/context.go:L38` — skill context resolution
```go
cmd := exec.Command("/bin/sh", "-c", command)
```
Executes shell commands embedded in skill templates. The command comes from skill definition YAML files, which may be user-authored.

### 4. `internal/tool/yaml_loader.go:L180` — YAML tool exec
```go
cmd := exec.CommandContext(ctx, "sh", "-c", rendered)
```
Executes Go-template-rendered shell commands from YAML tool definitions. The `rendered` string incorporates user-controlled input via template expansion (trust boundary #3 + #5).

### 5. `internal/tool/yaml_loader.go:L233` — Hadron blueprint exec
```go
cmd := exec.CommandContext(ctx, "hadron", args...)
```
Runs Hadron blueprints. Input values pass through Go templates before becoming CLI arguments.

### 6. `internal/mcp/stdio_transport.go:L46` — MCP stdio subprocess
```go
t.cmd = exec.Command(t.command, t.args...)
```
Launches MCP server processes. Command comes from database-persisted MCP server configs or auto-discovery.

### 7. `cmd/nanite/plugin_cmd.go:L126` — plugin install via git clone
```go
cmd := exec.Command("git", "clone", "--depth", "1", repoURL, target)
```
The `repoURL` comes from user CLI input. No URL validation.

### 8. `internal/api/plugins.go:L228` — API plugin install
```go
cmd := exec.Command("git", "clone", "--depth", "1", repoURL, target)
```
Same as above but triggered via HTTP API. Trust boundary #1.

The PTY and Subprocess bridges (`pkg/provider/pty.go:L105`, `pkg/provider/subprocess.go:L88`) also bypass `AgentExec`, but those are purpose-built for CLI agent spawning. The findings above are general subprocess invocations that should go through the sandbox.

## Impact

- `dev_bash` (item 2) accepts LLM-generated commands and runs them unsandboxed. A prompt injection attack via LLM response could execute arbitrary commands with the host's full privileges and env vars.
- YAML tool execution (item 4) performs Go template rendering on user input before shell execution. Template injection could produce arbitrary commands.
- Plugin install (items 7, 8) clones arbitrary git URLs without validation. A malicious URL could trigger git credential prompts or exploit git protocol handlers.

## Recommendation

Priority triage:

1. **dev_bash** — Route through `sandbox.UserExec` or add equivalent denylist + secret filtering. This is the widest attack surface.
2. **YAML tool exec** — Add input sanitization before template rendering. Consider running through `sandbox.UserExec`.
3. **Plugin install** — Validate `repoURL` against an allowlist of URL schemes (`https://` only, no `file://`, no `git://`).
4. **shell.Exec** — Used only for read-only git commands; lower risk but should still filter secrets from env.
5. **MCP stdio** — Server configs are admin-controlled; Medium risk. Add denylist check on command path.

## References

- `internal/sandbox/exec.go` — `AgentExec` and `UserExec` as reference sandbox implementations
- Reviewer context: trust boundaries #1, #3, #5, #8
- `2026-04-10-dev-tools-input-validation` audit — may have covered `dev_bash`; cross-check for overlap
