# [Critical] /compact API handler is an MVP stub — CompactionPipeline never invoked

**Scope:** context-management
**Topic:** Correctness / Claimed-vs-actual
**Date:** 2026-04-11

## Problem

The `/compact` slash command is registered in the chat UI (`commands.go:70`) and wired to a `POST /api/sessions/{id}/compact` API endpoint (`api.go:70`). The API handler (`sessions.go:252-289`) is labeled "MVP" in a code comment and performs naive string concatenation truncated at 2000 characters. It does **not** invoke the `CompactionPipeline` defined in `internal/context/compaction.go`, does **not** call an LLM summarizer, and does **not** fire the `EmitPreCompact`/`EmitPostCompact` event hooks.

Meanwhile, `internal/context/compaction.go` defines a sophisticated 3-stage escalation pipeline (drop enrichment, summarize oldest via LLM, strip tool blocks) with mode-aware summarization prompts and a `Summarizer` interface. This pipeline is **never instantiated or called from any production code path**.

## Evidence

The actual compact handler:

```go
// internal/api/sessions.go:252-289
func (a *API) handleCompactSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")

	messages, err := a.Services.Store.ListMessages(sessionID, 1000)
	// ...

	// MVP: concatenate all message contents, truncate to 2000 chars.
	var total int
	var summary string
	for _, m := range messages {
		if total+len(m.Content) > 2000 {
			summary += m.Content[:2000-total]
			total = 2000
			break
		}
		summary += m.Content + "\n"
		total += len(m.Content) + 1
	}

	// Save compaction summary on session.
	if err := a.Services.Store.UpdateSessionCompaction(sessionID, summary); err != nil { ... }

	// Mark all messages as compacted.
	for _, m := range messages {
		if !m.IsCompacted {
			_ = a.Services.Store.UpdateMessageContent(m.ID, m.Content, true)
		}
	}

	a.jsonResp(w, http.StatusOK, map[string]string{"summary": summary})
}
```

The `CompactionPipeline` callers:

```
$ rg "CompactionPipeline" --type go
internal/context/compaction.go:42:type CompactionPipeline struct {
internal/context/compaction_test.go:42:    p := &CompactionPipeline{
internal/context/compaction_test.go:68:    p := &CompactionPipeline{
internal/context/compaction_test.go:99:    p := &CompactionPipeline{
internal/context/compaction_test.go:150:   p := &CompactionPipeline{
```

Only the definition and test file. No production code instantiates the pipeline.

The `Summarizer` interface callers:

```
$ rg "Summarizer" --type go
internal/context/compaction.go:34:type Summarizer interface {
internal/context/compaction.go:45:    Summarizer Summarizer
internal/context/compaction_test.go:13:type mockSummarizer struct {
```

No production code implements or provides a `Summarizer`.

## Impact

When a user types `/compact`, they get a truncated concatenation of raw message content stored as a "compaction summary" on the session. This does not reduce context size for subsequent turns — the messages are marked `is_compacted=true` but their content is **not replaced** (line 284: `UpdateMessageContent(m.ID, m.Content, true)` writes the same content back with the compacted flag). The `AssembleContext` path at `context_client.go:148-149` does check `IsCompacted` but only for tool-role messages, not for the messages marked by the compact handler.

The result: `/compact` marks messages as compacted, stores a truncated summary on the session row, but does not actually reduce the context sent to the LLM on the next turn.

## Recommendation

Wire the `CompactionPipeline` into the compact API handler:

1. Implement `Summarizer` using the existing utility provider/model (`s.UtilityProvider`/`s.UtilityModel`).
2. Replace the MVP handler body with a call to `CompactionPipeline.Run()`.
3. Fire `EmitPreCompact` before and `EmitPostCompact` after the pipeline runs.
4. Actually replace compacted message content in the database (or mark them for exclusion from `AssembleContext`).

Alternatively, if the slot system is wired first (finding 01), the pipeline integrates naturally through `ContextWindow.NeedsCompaction()`.

## References

- `internal/api/sessions.go:L252-L289` — MVP compact handler
- `internal/context/compaction.go` — CompactionPipeline (never called)
- `internal/chat/commands.go:70` — `/compact` slash command registration (nil handler = client-side)
- `internal/api/api.go:70` — route registration
- Related: [01-critical-slot-system-unwired.md](01-critical-slot-system-unwired.md)
