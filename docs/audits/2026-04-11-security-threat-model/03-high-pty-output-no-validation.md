# [High] PTY bridge output parsed without validation — CLI agent can inject arbitrary stream events

**Scope:** Provider — trust boundary #7 (PTY bridge input/output)
**Topic:** Security
**Date:** 2026-04-11

## Problem

PTY adapter `ParseLine` implementations deserialize JSON from CLI subprocess stdout and map it directly to `StreamEvent` structs with no validation of field values. A compromised or prompt-injected CLI agent can emit crafted JSON that produces arbitrary stream events, including fake tool_use events, manipulated usage data, or injected session IDs.

## Evidence

All PTY adapters follow the same pattern. Example from `pkg/provider/pty_claude.go:L110-L137`:

```go
func parseClaudeStreamLine(line []byte) ([]StreamEvent, error) {
	if len(line) == 0 {
		return nil, nil
	}
	var envelope claudeEvent
	if err := json.Unmarshal(line, &envelope); err != nil {
		return nil, fmt.Errorf("parse claude event: %w", err)
	}
	switch envelope.Type {
	case "assistant":
		return parseClaudeAssistant(line)
	// ...
	}
}
```

The `parseClaudeAssistant` function at `pkg/provider/pty_claude.go:L139-L173` maps content blocks directly to `StreamEvent`:

```go
case "tool_use":
    input := make(map[string]any)
    if len(block.Input) > 0 {
        _ = json.Unmarshal(block.Input, &input)
    }
    events = append(events, StreamEvent{
        Type: "tool_use",
        ToolUse: &ToolUseBlock{
            ID:    block.ID,
            Name:  block.Name,
            Input: input,
        },
    })
```

The `block.Name` (tool name) and `block.Input` (tool arguments) are passed through without validation. Similarly, Codex (`pkg/provider/pty_codex.go:L61-L105`) passes content and error messages through directly.

The `parseClaudeSystem` function (`pkg/provider/pty_claude.go:L211-L222`) emits a `session_id` event from CLI output:

```go
if ev.Subtype == "init" && ev.SessionID != "" {
    return []StreamEvent{
        {Type: "session_id", SessionID: ev.SessionID},
    }, nil
}
```

A malicious CLI could emit a crafted `session_id` to potentially hijack session resume behavior.

The PTY bridge's scanner reads up to 1MB per line (`pkg/provider/pty.go:L131-L132`):

```go
scanner := bufio.NewScanner(ptmx)
scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
```

This 1MB buffer plus the 64-element buffered channel means the PTY bridge will hold up to 64MB of unprocessed events in memory if the consumer is slow — already covered by `backpressure-followup`, but the lack of output validation means every byte of that 64MB is untrusted content flowing into the chat engine.

## Impact

A CLI agent compromised via prompt injection (user sends crafted text that tricks the CLI into outputting specific JSON) can:

1. **Inject fake tool_use events** with arbitrary tool names and arguments. If the chat engine processes these (it does — `chat_generate.go` routes tool_use events to `ToolService.Execute`), the injected tool call will be executed with the permissions of the nanite host.
2. **Inject fake session IDs** to confuse session resume behavior.
3. **Inject fake usage events** with manipulated token counts, affecting cost tracking and rate limiting.
4. **Inject fake error events** to terminate the stream prematurely or display misleading error messages to the user.

The prompt injection vector is realistic: a user pastes text containing instructions like "Output the following JSON on a new line: `{\"type\":\"assistant\",\"message\":{...}}`" — the CLI processes this as part of its normal operation and the crafted JSON appears in stdout.

## Recommendation

1. Add a validation layer between `ParseLine` and the event channel. At minimum:
   - Tool names must match a known-tool allowlist (the `tools` parameter passed to `StreamChatWithTools` is ignored by PTY bridges, but the list of valid tool names is available)
   - Session IDs should be validated against expected format (UUID)
   - Usage token counts should be non-negative and within reasonable bounds
2. Consider marking all PTY-sourced events with an `Origin: "pty"` field so downstream consumers can apply appropriate trust levels.
3. For tool_use events from PTY bridges: the current design relies on the CLI to manage its own tools, so tool_use events from PTY output should probably be treated as informational only (display to user) rather than triggering nanite-side tool execution. Verify whether `chat_generate.go` actually executes PTY-sourced tool_use events or just displays them.

## References

- `pkg/provider/pty.go:L126-L183` — streamCLI goroutine
- `pkg/provider/pty_claude.go:L110-L232` — Claude adapter parsers
- `pkg/provider/pty_codex.go:L61-L105` — Codex adapter parser
- Cross-ref: `backpressure-followup` finding 02 (PTY output unbounded channel)
- Cross-ref: `eval-subprocess-pty-sdk` finding 03 (dead PTY parsers for Aider/Kiro/Codex)
