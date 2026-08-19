# Exclude Card data from replayed conversation history (the highest-leverage item in this phase)

**Phase:** 6
**Status:** not-started
**Depends on:** none
**Touches:** `internal/chat/context_client.go:256-267` (`AssembleSlotSources`'s message-history build — **the actual fix site, not `internal/context/compaction.go`**, see Context), `internal/service/chat_generate.go:1801,1868-1972,2004-2008` (the emit/persist path, read-only reference — confirms the shape of what needs filtering, not itself the fix site), `internal/chat/structured.go` (`StructuredMessage`, `EnvelopeRef`, `MarshalContent`)

## Context

Architecture doc `08-cards.md`: *"Tool-auto-emitted card data is already excluded from the *current* turn's tool-result content the agent sees. But the full data still gets appended into the assistant's own persisted response text, which *is* replayed into every subsequent turn's conversation history... This is the concrete mechanism that actually makes the harness-injects-rich-data-agent-context-stays-light vision hold at session scale."* Architecture doc `06-session-lifecycle-and-recovery.md` names the same gap. TASKS.md's own wording ("Card data excluded from replayed conversation history") makes this Phase 5's job, explicitly flagged as the single highest-leverage item in the phase.

### The full pipeline, traced end to end — corrects the architecture doc's implied fix location

1. **Emit**: `chat_generate.go:1801` appends a raw ```` ```nanite-envelope\n{...}\n``` ```` fence block to `responseContent` for pending envelopes (fence tag confirmed post-Phase-0-#33: only `nanite-envelope` recognized, `volon-envelope`/`fragments-envelope` gone). `chat.ParseEnvelopes(responseContent)` (`internal/chat/envelope.go:228`) then extracts the fence into `cleanContent` — the raw fenced text does **not** survive into persisted message text. This part already works correctly.
2. **But**: `chat_generate.go:1868-1972` builds `envRefs []chat.EnvelopeRef`, and `EnvelopeRef.Data` (`internal/chat/structured.go:60`) is `json.RawMessage` — the **full, untruncated card payload**, not a pointer or summary. `chat.WrapResponse(cleanContent, tier, toolCalls, envRefs, ...)` builds a `StructuredMessage{Text: cleanContent, Envelopes: envRefs, ...}`, and `structured.MarshalContent()` — the entire JSON, **envelopes-with-full-data included** — becomes `store.Message.Content` (`chat_generate.go:2004-2008`).
3. **The actual replay mechanism, and the real fix site**: `internal/chat/context_client.go:256-267` — `ListMessages(session.ID, 200)`, then `chatMessages[i] = llmtypes.ChatMessage{Role: role, Content: m.Content}`. **`m.Content` is used verbatim** — the full `StructuredMessage` JSON, every historical envelope's complete `data` payload included, becomes the literal text of that turn in every subsequent turn's LLM-facing history. `StructuredMessage.Envelopes` has **zero readers anywhere else** in `internal/chat`/`internal/service`/`internal/context` (verified by grep) — it is write-only except for this one blind pass-through.
4. **None of the four compaction stages reach this** — `internal/context/compaction.go`'s `stageDropEnrichment`/`stageSummarizeOldest`/`stageStripToolBlocks`/`stageDedupeToolResults` all operate on `m.ContentBlocks` (typed `tool_use`/`tool_result` blocks); a message built via the plain-`Content` path at `context_client.go:266` has `len(m.ContentBlocks) == 0`, so every stage's guard clause skips it entirely — **compaction stages structurally cannot reach this data, confirmed by direct inspection of `stageStripToolBlocks`'s guard clause.** This is also why the fix does not belong in `compaction.go`: compaction only fires when a session is over budget, but this problem exists on **every single turn**, regardless of budget.

## What to do

1. At `internal/chat/context_client.go:266` (or wherever the equivalent history-build now lives), change the message-content construction: parse `m.Content` as `StructuredMessage` and use only `.Text` for history replay — discard `.Envelopes` entirely from what reaches the LLM. Preserve a fallback for non-JSON legacy message content (older rows may not be `StructuredMessage`-shaped).
2. **Verify before assuming Text-only is safe for every case**: check whether the model needs visibility into its own just-emitted envelope somewhere else in the current turn's loop (e.g. to reason about what it just showed the user) — if so, confirm that visibility comes from a different, already-correct path (the current-turn tool-result exclusion the architecture doc says already works) rather than needing envelope data to survive in history replay.
3. Confirm this fix doesn't interact badly with `StructuredMessage.Envelopes`' write path — the field can keep being written (it may have other future consumers, or be needed for exact reconstruction/audit purposes), this task only changes what gets *read* for LLM-facing history replay.
4. Measure the real context-size impact: pick a long session with several historical cards (e.g. a table-card with substantial row data) and confirm token usage for a late-session turn drops meaningfully once envelope data is excluded from replay.

## Done means

- Replayed conversation history sent to the LLM contains only a message's `Text`, never its historical `Envelopes` payload data.
- A real, long session with multiple historical cards shows a measurable context-size reduction on later turns (report the before/after numbers in this file's Work Log).
- No regression in the agent's ability to reason about the current turn's own just-emitted card (verified the current-turn exclusion path, already correct per the architecture doc, remains intact and is what's actually relied on, not accidentally broken by this change).
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
