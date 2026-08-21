package agent

// acp_session.go — TASKS/agent-host-acp/11-nanite-per-agent-protocol-
// transport-config.md: the real, direct acp.Client-driving composition for
// an agent configured with agent_profiles.protocol="acp" (factory.go's
// useACPProtocol).
//
// # Why this bypasses wrapper.Wrapper.Run entirely — deliberate, not an
// oversight
//
// task 09's adapters/opencodeacp and task 10's adapters/copilotacp
// (libs/go-agent-wrapper) both ship an adapters.RuntimeAdapter.CLIAdapter()
// glue so their Adapter is structurally dispatchable through
// wrapper.Wrapper.Run's existing ProtocolACP+TransportStdio
// runtime_dispatch.go entry (task 08) — but both packages' own doc
// comments candidly document that composition as "real but capture-only":
// provider.CLIAdapter.BuildArgs runs before the child process exists and
// ParseLine is read-only, so neither method has a writer available to
// drive the real ACP initialize/session/new/session/prompt handshake.
// Wiring nativeAdapter-style through wrapper.New would spawn the real
// opencode acp / copilot --acp subprocess but never actually perform that
// handshake — a session that looks launched but can never complete a real
// turn, failing this task's own "verified end-to-end, not just at the
// config-storage layer" requirement.
//
// The one composition both packages' own real, non-mocked end-to-end tests
// exercise instead is driving acp.Client (Launch/Prompt/Cancel/Events)
// directly. This file is that composition, translated onto Nanite's own
// Session/EventFanout/TypedEventCallback seams (mirroring wrapper_sink.go's
// job for the native path) so an ACP-configured agent gets a genuinely
// working turn.
//
// # Payload-shape note
//
// opencodeacp and copilotacp's own runtimeevents.Event payload shapes for
// KindAgentToolUse/KindAgentToolResult diverge from each other (task 09:
// flat tool_call_id/title/kind/status keys; task 10: nested
// {"tool_use":{"id","name",...}} matching wrapper/event_translator.go's own
// convention) — a real, independently-arrived-at divergence between the
// two sibling packages, not a bug in either. extractToolUse/extractToolResult
// below read both shapes rather than assuming either one.
import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/go-agent-wrapper/acp"
	"github.com/hollis-labs/go-agent-wrapper/adapters"
	"github.com/hollis-labs/go-agent-wrapper/adapters/copilotacp"
	"github.com/hollis-labs/go-agent-wrapper/adapters/opencodeacp"
	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/go-providers/provider/events"
	runtimeevents "github.com/hollis-labs/go-runtime-events/runtimeevents"
)

// acpSupportedProviders lists the providerName values newACPClient knows
// how to build a real acp.Client for — the native ACP adapters this batch
// ships (task 09 OpenCode, task 10 Copilot CLI). Bridge-mediated providers
// (Claude/Codex/Pi via a third-party ACP bridge) are Phase 4 scope
// (TASKS/agent-host-acp/12-15), not yet selectable here.
var acpSupportedProviders = map[string]bool{
	"opencode": true,
	"copilot":  true,
}

// newACPClient builds the real acp.Client for providerName, per
// factory.go's useACPProtocol/effectiveACPTransport dispatch. Returns a
// clear error for any provider this batch's native adapters don't cover
// yet, rather than silently falling back to the native runtime — an
// operator who explicitly configured protocol="acp" on an unsupported
// provider should see why launch failed, not a silent downgrade.
func newACPClient(providerName string, transport adapters.Transport) (acp.Client, error) {
	switch normalizeProviderName(providerName) {
	case "opencode":
		return opencodeacp.NewClient(), nil
	case "copilot":
		return copilotacp.NewClient(transport), nil
	default:
		return nil, fmt.Errorf(
			"agent: protocol=acp is not supported for provider %q yet (supported: opencode, copilot; "+
				"claude/codex/pi ACP bridges are TASKS/agent-host-acp Phase 4)", providerName,
		)
	}
}

// acpSession is the ACP-client-driven backend for one agent.Session,
// mutually exclusive with the native path's *wrapper.Wrapper (see
// Session.acp's field doc in agent.go). Owns exactly one acp.Client for
// the life of the session.
type acpSession struct {
	client acp.Client

	fanout  chan<- llmtypes.StreamEvent
	typedCB provider.EventsCallback
}

// drain reads client.Events() until the channel closes (either Stop's
// explicit client.Close, or the underlying process exiting on its own —
// both packages document closing Events exactly once regardless of which
// happens first), translating each event onto Nanite's existing
// EventFanout/TypedEventCallback surfaces. Runs on its own goroutine for
// the life of the session (started by bootACP); returns once the channel
// closes.
func (a *acpSession) drain(ctx context.Context) {
	for ev := range a.client.Events() {
		a.handleEvent(ctx, ev)
	}
}

func (a *acpSession) handleEvent(ctx context.Context, ev runtimeevents.Event) {
	switch ev.Kind {
	case runtimeevents.KindAgentDelta:
		a.sendFanout(ctx, llmtypes.StreamEvent{Type: llmtypes.EventDelta, Content: extractContent(ev.Payload)})
	case runtimeevents.KindAgentToolUse:
		a.emitToolUse(ev.Payload)
	case runtimeevents.KindAgentToolResult:
		a.emitToolResult(ev.Payload)
	case runtimeevents.KindTurnCompleted:
		// Unconditionally the terminal "turn done" signal for the fanout
		// channel — deliberately NOT mirroring wrapper_sink.go's
		// handleTurnCompleted, which sends EventUsage instead of EventDone
		// whenever the payload's "usage" key is present. opencodeacp's own
		// KindTurnCompleted payload ({"stop_reason","usage"}) always
		// includes a "usage" key when the turn had one, which would make
		// that branch never send the terminal EventDone a chat consumer
		// needs to know the turn ended. See this file's package doc.
		a.sendFanout(ctx, llmtypes.StreamEvent{Type: llmtypes.EventDone})
	case runtimeevents.KindTurnFailed:
		a.sendFanout(ctx, llmtypes.StreamEvent{Type: llmtypes.EventError, Error: extractError(ev.Payload)})
	default:
		// session/process/permission lifecycle kinds -- no EventFanout/
		// TypedEventCallback analog today, matching runtimeEventSink.Write's
		// identical default no-op for the native path.
	}
}

func (a *acpSession) sendFanout(ctx context.Context, ev llmtypes.StreamEvent) {
	if a.fanout == nil {
		return
	}
	select {
	case a.fanout <- ev:
	case <-ctx.Done():
	}
}

// extractContent reads the "content" key both opencodeacp's and
// copilotacp's KindAgentDelta payloads share (message and thinking chunks
// alike — see this file's package doc; phase/thinking distinction is not
// surfaced separately here, matching the native runtimeEventSink's own
// flattening of the same distinction).
func extractContent(raw json.RawMessage) string {
	var p struct {
		Content string `json:"content"`
	}
	_ = json.Unmarshal(raw, &p)
	return p.Content
}

func extractError(raw json.RawMessage) string {
	var p struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(raw, &p)
	return p.Error
}

// emitToolUse reads either payload shape (see package doc) and forwards a
// events.ToolUse to typedCB, the same surface the native path's
// runtimeEventSink.handleToolUse drives tool_call SSE from.
func (a *acpSession) emitToolUse(raw json.RawMessage) {
	if a.typedCB == nil || len(raw) == 0 {
		return
	}
	// copilotacp's nested shape: {"tool_use":{"id","name",...}}.
	var nested struct {
		ToolUse *struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"tool_use"`
	}
	if err := json.Unmarshal(raw, &nested); err == nil && nested.ToolUse != nil {
		a.typedCB(events.ToolUse{ID: nested.ToolUse.ID, Name: nested.ToolUse.Name})
		return
	}
	// opencodeacp's flat shape: {"tool_call_id","title",...}.
	var flat struct {
		ToolCallID string `json:"tool_call_id"`
		Title      string `json:"title"`
	}
	if err := json.Unmarshal(raw, &flat); err == nil && flat.ToolCallID != "" {
		a.typedCB(events.ToolUse{ID: flat.ToolCallID, Name: flat.Title})
	}
}

// emitToolResult mirrors emitToolUse for KindAgentToolResult, forwarding a
// events.ToolResult the same surface the native path's
// runtimeEventSink.handleToolResult drives tool_result SSE from.
func (a *acpSession) emitToolResult(raw json.RawMessage) {
	if a.typedCB == nil || len(raw) == 0 {
		return
	}
	// copilotacp's nested shape: {"tool_result":{"id","status","content_preview"}}.
	var nested struct {
		ToolResult *struct {
			ID             string `json:"id"`
			ContentPreview string `json:"content_preview"`
		} `json:"tool_result"`
	}
	if err := json.Unmarshal(raw, &nested); err == nil && nested.ToolResult != nil {
		a.typedCB(events.ToolResult{ID: nested.ToolResult.ID, ContentPreview: nested.ToolResult.ContentPreview})
		return
	}
	// opencodeacp's flat shape: {"tool_call_id","status","result",...}.
	var flat struct {
		ToolCallID string `json:"tool_call_id"`
		IsError    bool   `json:"is_error"`
	}
	if err := json.Unmarshal(raw, &flat); err == nil && flat.ToolCallID != "" {
		a.typedCB(events.ToolResult{ID: flat.ToolCallID, IsError: flat.IsError})
	}
}

// SendInput implements the Session.SendInput contract for the ACP backend
// — acp.Client.Prompt (ACP's session/prompt), the direct analog of the
// native path's wr.SendInput. No streaming-stdio NDJSON framing applies
// here (that's a claude-native-protocol concern; ACP has its own framing
// entirely internal to acp.Client).
func (a *acpSession) SendInput(ctx context.Context, payload []byte) error {
	return a.client.Prompt(ctx, string(payload))
}

// Stop implements the Session.Stop contract for the ACP backend. Cancel
// first (ACP's session/cancel — despite the wire method's name, spec'd as
// turn-scoped; this is Turn.Cancel's analog, not Session.Stop's, per
// docs/engineering/GLOSSARY.md's "Turn.Cancel vs. Session.Stop..." entry)
// so any in-flight turn is asked to abort cooperatively, then Close (the
// real whole-session-end analog of wr.Stop) — best-effort on Cancel's own
// error (mirrors wr.Stop's own interrupt-then-terminate shape; a session
// with no in-flight turn simply no-ops on Cancel per both Client
// implementations' own doc comments).
func (a *acpSession) Stop(ctx context.Context) error {
	_ = a.client.Cancel(ctx)
	return a.client.Close(ctx)
}
