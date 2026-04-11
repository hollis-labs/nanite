# [Medium] Sandbox failures are under-observable; proxy denies, sandbox skips, and timeouts are not surfaced for audit

**Scope:** sandbox / observability
**Topic:** Observability — security blind spot
**Date:** 2026-04-10

## Problem

The sandbox package logs selectively and inconsistently, and the events that matter most for security incident response are the least visible.

Concrete gaps:

1. **Denylist blocks return an error to the caller but do not log.** `CheckDenylist` returns `(blocked, reason)`; `AgentExec`/`UserExec` wrap it into an error (`"sandbox: agent-exec denied: ..."`). The caller may or may not log — `nanite_code_execute` turns it into an error-shaped tool result and the chat engine surfaces it to the user. No entry in any audit log. An attacker probing the denylist leaves no trace.

2. **Proxy denials log to stdout via `log.Printf`.** `log.Printf("proxy: denied CONNECT to %s", r.Host)` and `log.Printf("proxy: denied %s to %s", r.Method, r.URL.Host)`. These go to the Go default logger, which may or may not be captured depending on how Nanite is launched. They do NOT reach the OpenTelemetry tracing layer that the rest of the backend uses. There is no structured event type `sandbox.network.denied` that a dashboard can count.

3. **`bwrap not found` warning uses `sync.Once`.** A single log line at process startup, then silence forever. An incident responder investigating why a sandbox leak happened cannot tell from logs whether the sandbox was ever applied. Should log at every `AgentExec` invocation when the sandbox was degraded, not just once.

4. **`os_other.go` warn-once is the same gap on BSD/Windows.**

5. **Sandbox population errors in `chat_generate.go:895-899` log via `log.Printf`** and then continue as if nothing happened (`ctx = provider.WithSandboxDir(ctx, sbDir)` still runs). A caller downstream has no way to know the sandbox was partially populated or that adapter files are missing. The reviewer-context `.nanite/agents/reviewer-backend.md:102` flags this behavior.

6. **Timeouts synthesize exit code 124 and set `TimedOut=true`.** The caller gets the truthful signal, but the actual process-tree cleanup status (did all children exit? did we orphan some?) is not reported anywhere.

7. **Seatbelt profile content is never logged.** If there's a profile injection or misconfiguration, the only artifact is the temp file — which is removed by the cleanup. No audit trail of "this is the profile we enforced for this session." For a security review, this is unusable.

8. **No metric for "sandbox applied vs. skipped per exec."** A simple counter would make (3), (4), (5) visible in aggregate.

## Evidence

```go
// internal/sandbox/os_linux.go:20-25
bwrapWarnOnce.Do(func() {
    log.Println("sandbox: bwrap not found — install bubblewrap for OS-level isolation (using Tier 1 only)")
})
return func() {}, nil

// internal/sandbox/proxy.go:90-92
if !p.domainAllowed(host) {
    log.Printf("proxy: denied CONNECT to %s", r.Host)
    ...

// internal/service/chat_generate.go:891-901
if sbDir, err := sandbox.Dir(sessionID); err != nil {
    log.Printf("chat-service: sandbox dir error: %v", err)
} else {
    if err := sandbox.Populate(sbDir, agent, mode, sandbox.PopulateOpts{...}); err != nil {
        log.Printf("chat-service: sandbox populate error: %v", err)
    }
    ctx = provider.WithSandboxDir(ctx, sbDir)
}
```

No use of `otel.Tracer("sandbox")` or structured logging helpers in `internal/sandbox/*.go`. The reviewer-context `.nanite/agents/reviewer-backend.md:20-22` confirms OpenTelemetry is the standard observability layer.

## Impact

- **Who:** operators and incident responders.
- **What:** cannot determine, after the fact, whether the sandbox was applied, whether a denylist entry fired, whether a domain was blocked by the proxy, or whether a populate failure degraded the sandbox. No correlation with session IDs.
- **Blast radius:** every other finding in this audit becomes harder to detect and harder to recover from. The Linux silent fallback (finding 03) is Critical partly because there's no audit trail.
- **Incident cost:** the beta is going to developer friends. When one of them reports "something weird happened," the reviewer will have no way to check whether the sandbox was degraded at the time.

## Recommendation

1. **Emit structured events for sandbox decisions.** Add an `otel.Tracer("sandbox")` span around each `AgentExec` and `UserExec`. Record as span events:

   - `sandbox.applied` with `platform=darwin|linux|other`, `bwrap_present=true|false`, `profile_hash=<sha256 of seatbelt profile>`
   - `sandbox.denylist_hit` with `reason=<reason>` (and the command, if the operator is comfortable recording it — consider hashing instead to avoid logging secrets)
   - `sandbox.proxy_denied` with `method`, `host`, `port`, `allowlist_size`
   - `sandbox.populate_error` with `adapter_name`, `error`
   - `sandbox.timeout` with `duration_ms`, `exit_code`

   Metrics counters for each of those in addition to events. `sandbox_degraded_total{reason="bwrap_missing"}` makes a dashboard alert trivial.

2. **Log bwrap-missing on every invocation, not once.** `sync.Once` is the wrong pattern for this. One log at startup + one per call is fine; the "warning spam" concern is smaller than the incident-response concern.

3. **Persist the seatbelt profile** for the duration of the process (sandbox dir) and reference it from the span event. Clean up at session end, not after each exec. This is a small footprint and a big audit-trail improvement.

4. **Fail loudly on sandbox-population errors.** The `chat_generate.go:894-898` path should at minimum return an error visible to the session, not just a log line. The reviewer-context already flags this behavior as "should NOT silently leave the sandbox partially populated."

5. **Add a `sandbox_status` API endpoint** returning `{platform, os_sandbox: applied|degraded, bwrap_available, reason}`. The frontend can display a security-warning badge when the sandbox is degraded. Low-cost, high-value UX.

6. **Expose a one-line startup banner** when the sandbox is degraded: at `cmd/nanite/main.go` startup, probe `applyOSSandbox` and print a prominent warning if it returns the degraded path. Users running `nanite serve` in a terminal will see it.

## References

- `internal/sandbox/os_linux.go:20-25`
- `internal/sandbox/os_other.go:16-19`
- `internal/sandbox/proxy.go:90, 147`
- `internal/sandbox/sandbox.go` (no tracing hooks)
- `internal/service/chat_generate.go:891-901`
- `.nanite/agents/reviewer-backend.md:20-22, 102`
- Related: `03-critical-linux-silent-sandbox-fallback.md`
