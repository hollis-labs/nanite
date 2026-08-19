# Add `FilterToolSelection`

**Phase:** 3
**Status:** not-started
**Depends on:** Phase 0 #22 (`22-remove-skill-and-tool-broker-abstractions.md`) should already be landed — this task is that removal's explicitly named forward-looking replacement mechanism for the go-toolbroker enricher's cut `OverrideBlock` feature, not a mechanism that needs to coexist with it.
**Touches:** `internal/plugin/filter.go` (new `FilterToolSelection` constant, alongside the existing 8), `internal/service/tool.go` (`toolServiceImpl` struct and `SelectForAgent` — needs a design decision on where the filter applies, see Context), `internal/service/chat_generate.go` (likely insertion point, see Context), `internal/service/container.go` (wiring if `toolServiceImpl` gains a `pluginHost` field).

## Context

TASKS.md Phase 3 (final item): *"Add `FilterToolSelection`."* Architecture doc `09-plugin-system.md`: *"a filter exists for tool *results* (post-execution) but not tool *selection* — a plugin can't currently add, remove, or reshape which tools get offered to the model for a turn, only react after the fact. Add `FilterToolSelection` alongside the tool-selection filter stack."* This closes the one real gap the plugin-system review found in an otherwise "close to comprehensive" filter/hook chain.

### The existing filter chain, verified

`internal/plugin/filter.go:199-212` — 8 named filter points: `FilterSystemPrompt`, `FilterUserMessage`, `FilterToolResult`, `FilterAssistantResponse`, `FilterContextWindow`, `FilterEnvelopeData`, `FilterReflexState`, `FilterReflexAction`. No `FilterToolSelection` exists. The registry (`FilterRegistry.Register`/`RegisterWithView`/`Apply`, same file, lines 86-168) is generic — adding a new named filter point requires no registry changes, only a new constant plus a real call site.

Existing call sites, all `s.pluginHost.ApplyFilter(pluginpkg.Filter<X>, data, fctx)`: `FilterSystemPrompt` (`chat_generate.go:475`), `FilterUserMessage` (`:484`), `FilterContextWindow` (`:986`, on `[]llmtypes.ChatMessage`), `FilterAssistantResponse` (`:1791`), `FilterEnvelopeData` (`:1872`); `FilterToolResult` lives separately in `internal/service/chat_tool_executor.go:523`.

### The insertion-point decision this task must make explicit

Tool selection happens in `toolServiceImpl.SelectForAgent` (`internal/service/tool.go:219-...`), called from `chat_generate.go:357`, returning `*ToolSelection{Tools []llmtypes.ToolDefinition, Catalog string, Progressive bool, OverrideBlock string}` (`tool.go:32-37`). The function internally runs: broker selection → permission filter (`filterToolsByPermissions`, `:277`) → allowlist + chat-surface filter (`:299-302`) → `FinalizeToolSelection` cap (`:311`) → description rendering (`:328`) → progressive-discovery repackaging (`:342+`).

**`toolServiceImpl` has no `pluginHost` field today** (struct at `tool.go:109-125`), unlike `chatServiceImpl` (`chat.go:277`, has `pluginHost PluginEventSink`). So applying `FilterToolSelection` *inside* `SelectForAgent` requires first wiring plugin-host access into `toolServiceImpl` (new constructor param/setter + `container.go` update). The lower-friction alternative is applying the filter in `chat_generate.go` immediately after `SelectForAgent` returns (`:357`), operating on `selection.Tools` — `chatServiceImpl` already has `s.pluginHost` in scope there, matching the exact pattern the other five `chat_generate.go`-resident filters already use. **Pick one and document why in this file's Work Log** — don't leave it as an implementation-time coin flip; the two options have different implications for whether a plugin can see (and override) the pre-cap, pre-allowlist tool set or only the fully-resolved final list.

Also confirm before starting: `ToolSelection.OverrideBlock` (`tool.go:36`, the go-toolbroker enricher's markdown block) should already be gone by the time this task runs, since Phase 0 #22 cuts it and explicitly names `FilterToolSelection` as its replacement, not something meant to coexist with it. If `OverrideBlock` is still present, that's a signal Phase 0 #22 hasn't actually landed — flag it rather than building around a field that's supposed to already be dead.

## What to do

1. Add the `FilterToolSelection` constant to `internal/plugin/filter.go`.
2. Decide and implement the insertion point per the Context discussion above (inside `SelectForAgent` with new `pluginHost` wiring, or in `chat_generate.go` on `selection.Tools` post-call) — document the choice and reasoning.
3. Wire the actual filter call, following the exact pattern of the existing 5 `chat_generate.go`-resident filters (or the `chat_tool_executor.go` pattern if choosing the in-`SelectForAgent` approach).
4. Confirm a plugin can genuinely add, remove, or reshape the tool list for a turn via this filter — write or extend a test plugin (if one exists in the test suite already, use it; if not, a minimal one for this test is in scope).

## Done means

- `FilterToolSelection` exists as a real filter point, callable by plugins, wired into the actual tool-selection path.
- A real plugin (test or otherwise) can demonstrably add/remove/reshape the offered tool list for a turn via this filter, verified by an actual filtered turn, not just that the hook fires.
- The insertion-point decision (inside `SelectForAgent` vs. post-call in `chat_generate.go`) is documented in this file's Work Log with the reasoning.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
