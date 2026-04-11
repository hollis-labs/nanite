# [Medium] `ParseAgentConstraints` silently discards JSON errors and uses zero values

**Scope:** chat-engine
**Topic:** Error handling / Configuration
**Date:** 2026-04-10

## Problem

`ParseAgentConstraints` discards the `json.Unmarshal` error. If an agent profile has a malformed `Constraints` JSON column, the function returns a zero-value `AgentConstraints` struct without logging the failure, and the downstream loop runs with default limits instead of whatever the operator configured. An agent with a misconfigured max-turns setting silently gets 25 turns when the operator wanted 5, or vice versa.

## Evidence

```go
// internal/chat/engine.go:L24-L33
// ParseAgentConstraints parses the constraints JSON from an agent profile.
// Returns zero-value struct on empty/invalid input (no constraints enforced).
func ParseAgentConstraints(raw string) AgentConstraints {
    var c AgentConstraints
    if raw == "" || raw == "{}" {
        return c
    }
    json.Unmarshal([]byte(raw), &c)
    return c
}
```

The error return from `json.Unmarshal` is dropped entirely. There is no `log.Printf`, no error return, no attempt to distinguish "the JSON is malformed" from "the JSON is valid but fields are zero".

This struct is directly consumed downstream without further validation:

```go
// internal/service/chat_generate.go:L94-L100
// Parse agent constraints (schema v2).
constraints := chat.ParseAgentConstraints(agent.Constraints)

if constraints.MaxTimeSeconds > 0 {
    agentTimeout := time.Duration(constraints.MaxTimeSeconds) * time.Second
    ctx, cancel = context.WithTimeout(ctx, agentTimeout)
    defer cancel()
}
```

```go
// internal/service/chat_loop_state.go:L130-L164
func resolveIterationLimits(c chat.AgentConstraints) iterationLimits {
    lim := iterationLimits{
        maxTurns:           defaultMaxTurns,
        hardCeiling:        defaultHardCeiling,
        ...
    }

    // MaxTurns: 0 = use default, -1 = unlimited, >0 = use value.
    if c.MaxTurns > 0 {
        lim.maxTurns = c.MaxTurns
    } else if c.MaxTurns == -1 {
        lim.maxTurns = -1
    }
    ...
```

`resolveIterationLimits` interprets zero as "use default". A parse failure that returns a zero struct is indistinguishable from "operator wants defaults". Operators have no signal that their configuration wasn't applied.

### Related: error linting

`go vet ./internal/chat/` would not flag this (the error is silently dropped, not assigned-and-ignored). `errcheck ./internal/chat/` would. The codebase's stated tooling per `.nanite/agents/reviewer-backend.md:L22` includes `golangci-lint`; whether the project's `.golangci.yml` enables `errcheck` is out of scope for this audit — recommend verifying in the follow-up `chat-engine-tooling-and-tests` scope.

## Impact

- **Configuration drift goes silent.** Operators edit `agent.Constraints`, save, and then chat runs with defaults. They have no log line telling them the JSON didn't parse. The symptom is "my max_turns setting isn't working" with no clue why.
- **Correctness of tool-use loop limits matter for beta.** The loop guard is the primary defense against runaway tool loops — the reviewer-backend release context explicitly emphasizes reliability for "first beta for developer friends". A silently-ignored hard ceiling defeats a safety control.
- **Contained blast radius** — this only affects the specific agent whose constraints failed to parse. Hence Medium, not High. But it's a silent failure, and silent failures accumulate.

## Recommendation

Return an error or at minimum log it:

```go
// internal/chat/engine.go
func ParseAgentConstraints(raw string) AgentConstraints {
    var c AgentConstraints
    if raw == "" || raw == "{}" {
        return c
    }
    if err := json.Unmarshal([]byte(raw), &c); err != nil {
        log.Printf("chat: agent constraints parse failed (raw=%q): %v — using defaults", raw, err)
    }
    return c
}
```

Optionally, return an error and have the caller choose how to handle it — `chat_generate.go:L94` can log-and-continue, or fail the session. Given the loop's safety semantics, fail-closed (refuse the request and surface the error to the user) is probably the right choice for operator-authored constraints, with the caller still logging and falling back to defaults if the operator expected to be resilient.

**Minimum recommended fix:** log the error. The larger redesign (typed return, validation, operator visibility into parse failures) is a small amount of work but nice-to-have.

While here, consider adding a `validate` tag or a `Validate()` method on `AgentConstraints` that checks that `MaxTurns >= -1`, `HardCeiling >= 0`, `ConsecutiveFailCap >= 0`, etc. Current code trusts the parsed values without a bounds check.

## References

- `internal/chat/engine.go:L24-L33` — the drop
- `internal/service/chat_generate.go:L94-L100` — first downstream consumer
- `internal/service/chat_loop_state.go:L130-L164` — where zero-vs-explicit-default matters
- `chat-engine-tooling-and-tests` recommended follow-up — run `errcheck ./internal/chat/...` to catch the rest of this pattern
