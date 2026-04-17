# [Medium] SessionSource builds context in reverse chronological order

**Scope:** contextbroker
**Topic:** Correctness
**Date:** 2026-04-11

## Problem

`SessionSource.Fetch` iterates messages from newest to oldest and appends each line to a `strings.Builder`. The resulting context string has the most recent message first and the oldest message last. When this is injected into the system prompt, the LLM reads the conversation in reverse order, which is confusing and may degrade response quality.

## Evidence

```go
// internal/contextbroker/source_session.go:L49-L68
for i := len(messages) - 1; i >= 0; i-- {
    msg := messages[i]
    if msg.Role != "user" && msg.Role != "assistant" {
        continue
    }

    content := msg.Content
    if len(content) > 500 {
        content = content[:500] + "..."
    }

    line := fmt.Sprintf("%s: %s\n", msg.Role, content)
    lineTokens := EstimateTokens(line)

    if usedTokens+lineTokens > budget {
        break
    }

    sb.WriteString(line)
    usedTokens += lineTokens
}
```

The loop starts at `len(messages) - 1` (newest) and decrements. Messages are appended to `sb` in that order.

## Impact

The session context reads backward. Under tight budgets, only the most recent messages fit, which is arguably the right priority. But the ordering within the output is inverted: message N+1 appears before message N. The LLM may misinterpret conversational flow or causality.

## Recommendation

Either reverse the output after building it, or iterate forward within the budget-constrained tail:

```go
// Find the starting index that fits within budget, then iterate forward.
start := len(messages)
budgetLeft := budget
for i := len(messages) - 1; i >= 0; i-- {
    // ... estimate tokens, subtract from budgetLeft, set start = i when fits
}
for i := start; i < len(messages); i++ {
    // append to sb in chronological order
}
```

## References

- `internal/contextbroker/source_session.go:L49-L68` — reverse iteration
