# [High] Sandbox subprocess has no process group; timeouts and cancellations leave orphan children on macOS

**Scope:** sandbox / subprocess lifecycle / timeout handling
**Topic:** Concurrency correctness, resource leaks
**Date:** 2026-04-10

## Problem

`sandbox.AgentExec` and `sandbox.UserExec` build `exec.CommandContext` without setting `SysProcAttr{Setpgid: true}` or equivalent. When the context deadline fires, `exec.CommandContext`'s default behavior is to send `SIGKILL` to the **direct child** only. Any grandchildren spawned by the sandboxed script survive.

On macOS, `sandbox-exec` is the direct child, and the script interpreter (`sh`, `python3`, `node`) is its child. `SIGKILL` kills `sandbox-exec`. Whether the interpreter dies depends on `sandbox-exec`'s behavior, which is NOT documented to forward signals — and in practice, does not forward them reliably. The interpreter continues; its children (e.g. anything it forked) continue. Orphans are reparented to `launchd`/init. They keep running until they complete or exhaust the timeout-blind network/filesystem.

On Linux, the `--die-with-parent` flag in the bwrap invocation saves this case: bwrap uses `PR_SET_PDEATHSIG` so the entire process group in the namespace dies when the namespace parent dies. Good. macOS has no equivalent, so this is a macOS-specific orphan problem.

Additionally, `cmd.Cancel` is not set, so there is no opportunity to send SIGTERM first and then escalate — just the default SIGKILL on ctx cancel.

## Evidence

```go
// internal/sandbox/exec.go:123-140
timeout := clampTimeout(opts.Timeout, defaultAgentTimeout, maxAgentTimeout)
ctx, cancel := context.WithTimeout(context.Background(), timeout)
defer cancel()

cmd := exec.CommandContext(ctx, opts.Command, opts.Args...)
cmd.Dir = sandboxDir
cmd.Env = env

cleanup, err := applyOSSandbox(cmd, sandboxDir, opts.NetworkAllow)
if err != nil {
    return nil, fmt.Errorf("sandbox: os-level setup: %w", err)
}
defer cleanup()

return runCmd(ctx, cmd, timeout)
```

No `cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}`. No `cmd.Cancel = func() error { ... syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) ... }`. No `cmd.WaitDelay` for grace period before SIGKILL.

`os_darwin.go` wraps with sandbox-exec as the direct child. `sandbox-exec` is not documented to forward SIGKILL to its child process tree. In testing (not verified here, but behavior is well-known from Apple's tooling), a `sleep 60` inside a `sandbox-exec sh -c 'sleep 60'` may leave the `sleep` alive after sandbox-exec is killed.

Note: the tests only verify `exit code 124` on timeout (`exec_test.go:65-69`), which is Nanite's own synthesized exit code — they don't verify actual process-tree cleanup. The test is satisfied even if children orphan.

## Impact

- **Who:** any macOS user whose AgentExec timeouts on a script that spawned children.
- **What:** orphan processes continue running after timeout. Holding network connections, writing to disk (inside the sandbox dir), holding FDs. The timed-out "done" signal to the caller is a lie — real work is still in flight.
- **Blast radius:** typically small per-occurrence (a few orphan sleeps / curls), but accumulates over a long Nanite session. Under adversarial prompt injection, an attacker can deliberately spawn background processes that outlive the sandbox timeout, effectively getting persistent compute. Combined with the Linux silent-fallback and the SSRF proxy findings, this is a staging ground for a persistence implant.
- **Observability:** poor. The user sees a clean "timeout" and moves on; the orphans are invisible in the Nanite UI.
- **Reproducibility:** easy on macOS. `nanite_code_execute` with `code = 'nohup sleep 300 > /tmp/orphan.log 2>&1 &'` and `timeout = 1` — the sleep survives.

## Recommendation

1. **Set `SysProcAttr.Setpgid = true` on the cmd before exec.** This places the child and all its descendants in a new process group whose PGID equals the child's PID.

   ```go
   cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
   ```

2. **Override `cmd.Cancel` to signal the whole process group on context cancel.** Go 1.20+ supports `cmd.Cancel` and `cmd.WaitDelay`:

   ```go
   cmd.Cancel = func() error {
       if cmd.Process == nil {
           return os.ErrProcessDone
       }
       return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
   }
   cmd.WaitDelay = 2 * time.Second  // after SIGTERM, wait 2s then force
   ```

   The negative PID targets the entire process group. SIGTERM first gives interpreters a chance to clean up, then Go's internal path sends SIGKILL via `WaitDelay`.

3. **Add a test** that spawns a child via the sandbox with a short timeout, then verifies no process from the script's pgid survives. On macOS this test will fail without the fix. Use `syscall.Getpgid` or parse `ps -o pgid= -p <child>`.

4. **Document the macOS-specific behavior** in the package godoc until the fix lands: sandbox-exec does not cascade SIGKILL by default.

5. **Linux side:** `--die-with-parent` handles most cases, but also add Setpgid for defense in depth. Consistency across platforms is worth it.

## References

- `internal/sandbox/exec.go:123-140`
- `internal/sandbox/os_darwin.go` — sandbox-exec is the direct child
- `os/exec.Cmd.Cancel`, `WaitDelay` docs (Go 1.20+)
- `PR_SET_PDEATHSIG` / bwrap `--die-with-parent`
- Related: `04-high-proxy-connect-goroutine-leak-and-host-header.md` (different lifecycle bug in the same subsystem)
