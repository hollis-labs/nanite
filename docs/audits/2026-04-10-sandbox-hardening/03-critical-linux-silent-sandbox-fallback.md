# [Critical] Linux without `bwrap` silently falls back to no OS sandbox; AgentExec runs unisolated

**Scope:** sandbox / Linux platform support
**Topic:** Security — silent failure of primary security boundary
**Date:** 2026-04-10

## Problem

On Linux, `applyOSSandbox` tries to resolve `bwrap` via `exec.LookPath`. If bubblewrap is not installed, the function logs one warning (via `sync.Once`) and returns a no-op `cleanup` with `nil` error. `AgentExec` then proceeds as if the sandbox were applied.

The result: on any Linux host that hasn't installed bubblewrap (which is NOT in the default package set of most distros and is NOT listed as a hard dependency in `go.mod`, docs, or install scripts), the "primary security boundary" of Nanite's sandbox is silently absent. `AgentExec` still sets the restricted PATH, the filtered env, and runs the command from the sandbox directory — that's Tier 1 "convention" sandboxing — but the process has full network access, full filesystem read/write (only user-permission-limited), and can fork/exec arbitrary other processes. The denylist (see `05-medium-denylist-framing-and-bypasses.md`) is then the **only** remaining control, and it is not a real control.

The reviewer context explicitly says "Sandbox-first design: OS sandbox is primary boundary." That invariant is violated on any Linux dev machine without bwrap. Since the beta audience is "developer friends," it is very likely some of them will hit this. There is no loud error, no UI indication that the sandbox is degraded, just one log line at startup.

This is distinct from the platform-level `os_other.go` no-op (Windows, *BSD) — that at least doesn't claim to support OS-level sandboxing on those platforms. For Linux, the code claims bwrap support and then silently degrades.

## Evidence

```go
// internal/sandbox/os_linux.go:13-25
var bwrapWarnOnce sync.Once

func applyOSSandbox(cmd *exec.Cmd, sandboxDir string, networkAllow []string) (cleanup func(), err error) {
    bwrapPath, lookErr := exec.LookPath("bwrap")
    if lookErr != nil {
        bwrapWarnOnce.Do(func() {
            log.Println("sandbox: bwrap not found — install bubblewrap for OS-level isolation (using Tier 1 only)")
        })
        return func() {}, nil
    }
    ...
```

`AgentExec` does not check the return value as anything other than a cleanup + error:

```go
// internal/sandbox/exec.go:131-139
// Apply OS-level sandbox (no-op on unsupported platforms).
cleanup, err := applyOSSandbox(cmd, sandboxDir, opts.NetworkAllow)
if err != nil {
    return nil, fmt.Errorf("sandbox: os-level setup: %w", err)
}
defer cleanup()

return runCmd(ctx, cmd, timeout)
```

Neither the call site nor `AgentExec`'s return value distinguishes "sandbox applied" from "sandbox intentionally skipped." The caller (e.g. `nanite_code_execute`) has no way to refuse to run when the sandbox is absent.

Also: `os_other.go:15-19` does the same silent thing on Windows/BSD/etc. The comment even acknowledges it — "falls through to Tier 1 (convention-level) sandbox only." That's arguably OK on unsupported platforms because the project has not promised sandbox support there. But there is no mechanism anywhere in the codebase that lets an administrator require an OS sandbox: even on macOS, if `/usr/bin/sandbox-exec` is somehow missing (corporate-managed machine with sandbox-exec disabled, which does happen), the `cmd.Path = "/usr/bin/sandbox-exec"` will fail at `exec.Cmd.Run` time with a cryptic "fork/exec: no such file" error — better than silent, but still not a clean fail-closed.

No installer script or docs verified for this review — but: the beta instructions need to be clear that `bwrap` is a hard requirement on Linux, and the code should enforce it.

## Impact

- **Who:** any Linux user running Nanite beta without bubblewrap installed. Includes most new developer installs on Fedora without `bubblewrap`, most Debian/Ubuntu systems without explicit `apt install bubblewrap`, most Arch without `bubblewrap`. macOS is not affected (sandbox-exec is stock), but see note above.
- **What:** sandboxed agent tool execution silently runs unsandboxed. Every follow-on sandbox finding in this audit (path traversal, seatbelt injection, proxy SSRF) is only defended by the OS sandbox where it applies; on degraded Linux those defenses are shared across Tier 1 + denylist only, i.e. none of them effective.
- **Blast radius:** matches whatever a prompt-injected agent can do as the Nanite user: read all user files, write all user files, reach the network, spawn persistent processes.
- **Release blocker:** yes. The product cannot ship a "sandbox-first" beta in a configuration where the sandbox is optional without the user knowing.

## Recommendation

Fix in two layers:

1. **Fail closed by default on Linux when `bwrap` is missing.** Change `applyOSSandbox` on Linux to return an error, not a warning:

   ```go
   bwrapPath, lookErr := exec.LookPath("bwrap")
   if lookErr != nil {
       return nil, fmt.Errorf("sandbox: bwrap (bubblewrap) not found; install it or set NANITE_ALLOW_UNSANDBOXED_AGENT_EXEC=1 to opt out")
   }
   ```

   `AgentExec` propagates the error. The caller (MCP tool, workflow step, chat generate setup) sees a clear "sandbox unavailable" error instead of silently getting unsandboxed execution.

2. **Add an explicit opt-out env var for CI and intentional dev setups.** `NANITE_ALLOW_UNSANDBOXED_AGENT_EXEC=1` (or similar) turns the hard error into the current warning. This gives developer friends a clear, documented foot-gun instead of an undocumented silent one. Log the warning **every time** in that mode, not via `sync.Once`, so it's visible in logs for incident response.

3. **Document the dependency in docs/install path.** The installer (or first-run check) should detect `bwrap` on Linux and prompt the user to install it. This is a docs issue, mentioned here only because step 1 makes it immediately visible — without step 1 the docs are just a hopeful suggestion.

4. **Add a startup check.** In `cmd/nanite/main.go`, on boot, probe the sandbox by calling `applyOSSandbox` with a dummy command and discarding the result. If it fails (and the opt-out is not set), refuse to start. Report a one-line diagnostic with the bwrap install instructions.

5. **Audit `os_darwin.go` for sandbox-exec absence.** Add `exec.LookPath("/usr/bin/sandbox-exec")` at `applyOSSandbox` entry; fail closed there too. Currently it assumes the path exists.

Add a regression test:

- Stub `exec.LookPath` (or override `PATH` to exclude `bwrap`) and assert `applyOSSandbox` returns an error unless the opt-out env var is set.

## References

- `internal/sandbox/os_linux.go:13-25`
- `internal/sandbox/os_darwin.go:63-92`
- `internal/sandbox/os_other.go:15-19`
- `internal/sandbox/exec.go:131-139`
- Reviewer context `.nanite/agents/reviewer-backend.md:93-95` — "OS sandbox is primary boundary"
- CWE-755 (improper handling of exceptional conditions), CWE-693 (protection mechanism failure)
- Related: `05-medium-denylist-framing-and-bypasses.md`, `06-high-linux-network-isolation-gap.md`
