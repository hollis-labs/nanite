# [Critical] `ScopeGuard` is dead code — the prompt-injection defense doesn't exist

**Scope:** chat-engine
**Topic:** Security
**Date:** 2026-04-10

## Problem

`ScopeGuard`, documented in the reviewer-backend context as the "line of defense" against prompt injection escaping the intended session scope, is **never instantiated** outside its own tests. Nothing in `internal/chat/`, `internal/service/`, or anywhere else constructs an `EventReactionPipeline` that would wire a `ScopeGuard` into the stream. The chat engine dispatches provider streams straight from `prov.StreamChatWithTools` without any reaction pipeline.

Even if it were wired, the implementation is a keyword-match over stream deltas and a glob-pattern allowlist over tool names. It does not, and cannot, meaningfully defend against prompt injection, sandbox escape, or scope violations by a coordinating LLM.

## Evidence

`pkg/provider/scope_guard.go` defines `ScopeGuard` with two detection paths:

```go
// pkg/provider/scope_guard.go:L75-L93 — tool use check
dangerousTools := []string{
    "rm", "delete", "remove", "unlink",
    "sudo", "su", "chmod", "chown",
    "wget", "curl", "git", "npm", "pip",
    "docker", "systemctl", "service",
}
for _, dangerous := range dangerousTools {
    if strings.Contains(toolName, dangerous) {
        if !sg.isAllowedOperation(toolName) {
            return &ScopeViolation{ ... }
        }
    }
}
```

```go
// pkg/provider/scope_guard.go:L113-L131 — text delta check
suspiciousPatterns := []string{
    `/etc/`, `/usr/`, `/bin/`, `/var/`,
    `sudo `, `rm -rf`, `chmod `,
    `~/.*/.env`, `~/.ssh/`,
}
for _, pattern := range suspiciousPatterns {
    if matched, _ := regexp.MatchString(pattern, content); matched {
        ...
```

The guard is plumbed exclusively through `EventReactionPipeline` in `pkg/provider/event_pipeline.go`:

```go
// pkg/provider/event_pipeline.go:L63-L74
func NewEventReactionPipeline(provider Provider, config EventReactionConfig) *EventReactionPipeline {
    pipeline := &EventReactionPipeline{
        provider: provider,
        config:   config,
        active:   true,
    }
    if config.EnableScopeGuard {
        pipeline.scopeGuard = NewScopeGuard(config.AllowedScopes, config.ScopeViolationMode)
    }
    ...
```

But `NewEventReactionPipeline` is only called from tests. Grep of the entire repo:

```
pkg/provider/event_pipeline_test.go:22:    pipeline := NewEventReactionPipeline(mockProvider, config)
pkg/provider/event_pipeline_test.go:63:    pipeline := NewEventReactionPipeline(mockProvider, config)
```

No production wiring. The chat engine calls providers directly:

```go
// internal/service/chat_generate.go:L337-L339
if len(tools) > 0 {
    ...
    provCh, err = prov.StreamChatWithTools(provCtx, systemPrompt, chatMessages, model, tools)
} else {
    provCh, err = prov.StreamChat(provCtx, systemPrompt, chatMessages, model)
}
```

No pipeline wrap, no `EventReactionPipeline`, no `ScopeGuard`. Provider stream → chat loop directly.

Even the default config is a no-op: `DefaultEventReactionConfig()` at `pkg/provider/event_pipeline.go:L32-L46` sets `AllowedScopes: []string{"*"}` (allow everything) and `ScopeViolationMode: "log"` (never terminate). The claimed defense runs nowhere and, where it could run, allows everything.

## Impact

- The reviewer-backend context (`.nanite/agents/reviewer-backend.md:L63-L65`) documents `scope_guard.go` as "guardrails against prompt injection escaping the intended session scope" and directs the reviewer to flag "any weakness" as a security finding. The weakness is total: the file exists as dead code in the wrong package for the framing.
- Any threat model or documentation that references `ScopeGuard` as a defense is false. Plans, design docs, or compliance claims built on its existence are invalid.
- The actual chat-engine trust boundary (user messages → LLM → tool dispatch → MCP execution → tool result → LLM) has **no centralized scope enforcement** at the stream level. Permission checks happen per-tool in `chat_tool_executor.preCheckTools` (that layer is real), but the reactive content-inspection claim attached to `ScopeGuard` does not exist in production.
- A maintainer reading the file naturally assumes defense-in-depth at the stream layer. Any code change that relies on that assumption is reasoning from a false premise.

The deeper concern: even a hypothetical wiring of the current `ScopeGuard` would not constitute a prompt-injection defense. Keyword-matching on text deltas for substrings like `sudo ` or `/etc/` is defeated by any model output that varies case, adds whitespace, encodes paths, uses homoglyphs, or simply doesn't emit the phrase in its reasoning trace. Scope_guard's design also confuses two different concerns — filesystem scope (which is a sandbox concern, already addressed by `internal/sandbox/`) and prompt-injection detection (which keyword matching does not solve).

## Recommendation

Two viable paths; pick one:

**Option A — delete and own it.** Remove `pkg/provider/scope_guard.go`, `EventReactionPipeline`, and their tests. Update the reviewer-backend context to remove the claim. Document explicitly that prompt-injection defense is layered elsewhere (permission engine, sandbox, denylist) and that the chat stream has no reactive content inspection. This is the minimum honest state.

**Option B — design a real defense.** If a stream-level inspector is wanted, the current file is not the starting point. A real design would need:
  - A clear threat model (what's the adversary? what can they influence? what's the blast radius?) — the current file has none.
  - Token-level tool-argument validation at the dispatch point, not post-hoc string matching on text deltas.
  - Integration with the permission engine, not a parallel allowlist.
  - A failure mode that degrades safely (default-deny on ambiguous tool names, not default-allow with `*`).

Either way, the current state — a file that looks like a defense, is wired to a pipeline that nothing instantiates, with a permissive default — is worse than having no file at all, because it creates false confidence.

**Recommended:** Option A. The permission engine in `internal/permission/` and the sandbox in `internal/sandbox/` are real enforcement points. The chat engine's job is to call them consistently, not to add a parallel keyword matcher.

## References

- `pkg/provider/scope_guard.go` — the dead defense
- `pkg/provider/event_pipeline.go:L48-L93` — the never-constructed wrapper
- `pkg/provider/event_pipeline_test.go` — only callers
- `internal/service/chat_generate.go:L337-L339` — where a real wrap would belong if Option B is chosen
- `.nanite/agents/reviewer-backend.md:L63-L65` — the claim that triggered this finding
