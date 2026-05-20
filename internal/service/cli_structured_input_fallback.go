package service

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/hollis-labs/nanite/internal/chat"
)

// CW-20260519-0051: AskUserQuestion and the rest of claude-CLI's structured-
// input built-in tools (today: just AskUserQuestion) cannot round-trip through
// a Nanite-harnessed CLI-launch session.
//
// The failure mode is silent: claude in `--print --input-format=stream-json
// --output-format=stream-json` mode owns the AskUserQuestion round-trip
// internally (it would normally prompt the user via TTY in interactive mode).
// When headless, there is no TTY to prompt and the Nanite GUI is not wired
// to (a) render the structured question component when claude emits the
// tool_use, and (b) feed the selected answers back into the claude process
// over its NDJSON stdin. Claude therefore auto-resolves the tool call with
// no answers (the user sees "Answer questions?" with an empty body in the
// tool_call SSE preview, and the agent — observing an empty tool result —
// falls back to asking in prose).
//
// Same failure family as CW-20260516-0044 (panel_signal) and CW-20260516-0074
// (card_show landing late): CLI-launch structured outputs/inputs do not fully
// round-trip through the Nanite GUI.
//
// This file ships the graceful-fallback path for that gap. Detection happens
// at the typed-event-callback seam (agent_deps.go's typedCallback handles
// events.ToolUse for every CLI runtime — by construction it never fires for
// HTTP providers, so any ToolUse reaching it is from a CLI-launch session).
// When a tool name matches the unsupported set, we broadcast a one-line
// info-card envelope as a `plugin_envelope` SSE event so the GUI shows the
// operator a clear notice instead of a silent empty-answer result. The
// existing per-tool SSE emission (tool_call / tool_result) keeps firing
// unchanged — the fallback envelope is additive.
//
// The agent itself is intentionally NOT signaled here. Claude already learns
// "the tool returned nothing" via the empty result it observes and falls
// back to prose on its own; injecting an additional signal would either
// pollute the next turn (the streaming-stdio SendInput shape only wraps
// content as a plain user message, not as a tool_result block referencing
// the original tool_use_id) or require a new framing variant + a wider
// refactor across the runtime/agent → streaming-stdio seam (see the
// follow-up section of the CW-20260519-0051 commit message).
//
// Future option-A (full bridge): render the structured question as an
// interactive envelope and route answers back into claude's NDJSON stdin
// as a `user` frame with a `tool_result` block. That requires (i) a
// per-(sessionID, tool_use_id) EnvelopeInstance persisted from the typed-
// callback site, (ii) a new response handler that resolves the active
// runtime session via chatServiceImpl.activeSessions and calls a new
// runtime SendInput variant that frames the payload as
// `{"type":"user","message":{"role":"user","content":[{"type":"tool_result",...}]}}`,
// and (iii) plumbing through internal/runtime/agent/manager.go +
// factory.go (only streamingStdioUserFrame exists today and it only
// frames plain text). The full bridge is also unverifiable without a real
// claude-CLI session against a running service, so the graceful fallback
// ships first.

// cliStructuredInputUnsupportedTools is the set of CLI-agent tool names
// whose host-side round-trip is NOT wired through the Nanite GUI for a
// CLI-launch session. Match is case-insensitive on the canonical claude
// tool name.
//
// Today only AskUserQuestion is on the list; extend deliberately if more
// claude built-ins surface the same silent-failure family.
var cliStructuredInputUnsupportedTools = map[string]struct{}{
	"askuserquestion": {},
}

// isCLIStructuredInputUnsupported reports whether the named tool, when
// emitted by a CLI-launch agent, is a structured-input built-in whose
// host-side round-trip is unwired.
func isCLIStructuredInputUnsupported(toolName string) bool {
	_, ok := cliStructuredInputUnsupportedTools[strings.ToLower(strings.TrimSpace(toolName))]
	return ok
}

// cliStructuredInputFallbackEnvelopeType is the envelope `type` field
// emitted by the fallback path. info-card is a core envelope (declared
// in the go-envelopes manifest); reusing it avoids needing a new
// manifest entry from inside this repo.
const cliStructuredInputFallbackEnvelopeType = "info-card"

// cliStructuredInputFallbackPayload mirrors the info-card data shape
// (title, body, variant). Kept as a typed struct so a future schema
// tightening catches drift at compile time.
type cliStructuredInputFallbackPayload struct {
	Title   string `json:"title"`
	Body    string `json:"body"`
	Variant string `json:"variant"`
}

// emitCLIStructuredInputFallback broadcasts a one-line info-card envelope
// to the session's SSE stream when an unsupported structured-input tool
// fires. The envelope is stream-only (not persisted via
// CreateEnvelopeInstance) — it's an observability signal, not a card the
// user responds to.
//
// Caller is the typed-event-callback (agent_deps.go's typedCallback);
// invoked once per ToolUse hit, in addition to the existing tool_call
// SSE emission.
func (b *agentEventBridge) emitCLIStructuredInputFallback(sessionID, toolName, toolID string) {
	body := fmt.Sprintf(
		"The agent invoked `%s`, but structured-question UIs aren't wired for CLI-launch sessions today. "+
			"The agent will fall back to asking in prose; the previous tool call returned no answers. "+
			"Tracked under CW-20260519-0051.",
		toolName,
	)
	payload := cliStructuredInputFallbackPayload{
		Title:   "Structured input not available in this session",
		Body:    body,
		Variant: "warning",
	}
	data, err := json.Marshal(payload)
	if err != nil {
		slog.Warn("chat-service: marshal cli-structured-input-fallback payload",
			"session", sessionID, "tool", toolName, "tool_id", toolID, "err", err)
		return
	}
	streamWrap, err := buildPluginEnvelopeWrap("", cliStructuredInputFallbackEnvelopeType, data, EnvelopeRouting{
		DisplayClass: EnvelopeDisplayClassAlert,
	})
	if err != nil {
		slog.Warn("chat-service: marshal cli-structured-input-fallback wrap",
			"session", sessionID, "tool", toolName, "tool_id", toolID, "err", err)
		return
	}
	if b == nil || b.streams == nil {
		// Defensive: typed callback can fire during teardown ordering.
		return
	}
	b.streams.BroadcastSessionStreamEvent(sessionID, chat.StreamEvent{
		Type:     "plugin_envelope",
		Envelope: string(streamWrap),
	})
	slog.Info("chat-service: surfaced cli-structured-input fallback",
		"session_id", sessionID, "tool", toolName, "tool_id", toolID)
}
