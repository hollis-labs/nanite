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

	agentsessions "github.com/hollis-labs/agentkit/agentsessions"
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

	// processExitErr captures a genuine, unprompted ACP subprocess crash
	// (see handleProcessExited's doc comment for exactly which case this
	// is — an intentional Stop never reaches it) — TASKS/agent-host-acp/
	// 11's Finding 2 fix. Written exactly once, on drain's own goroutine,
	// strictly before drain's for-range loop returns; bootACP's draining
	// goroutine (agent_acp.go) reads it exactly once, strictly after
	// drain(...) returns — single-writer-then-single-reader, so no lock
	// is needed here, mirroring Session.runErr's own happens-before
	// argument (agent.go's Session doc comment).
	processExitErr error
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
		a.sendFanout(ctx, extractDeltaEvent(ev.Payload))
	case runtimeevents.KindAgentToolUse:
		a.emitToolUse(ev.Payload)
	case runtimeevents.KindAgentToolResult:
		a.emitToolResult(ev.Payload)
	case runtimeevents.KindTurnCompleted:
		a.handleTurnCompleted(ctx, ev.Payload)
	case runtimeevents.KindTurnFailed:
		a.sendFanout(ctx, llmtypes.StreamEvent{Type: llmtypes.EventError, Error: extractError(ev.Payload)})
	case runtimeevents.KindProcessExited:
		// TASKS/agent-host-acp/11 Finding 2: unlike every other
		// process/session-lifecycle kind (default branch below), this one
		// carries real crash-detection signal — captured, not no-op'd.
		a.handleProcessExited(ev.Payload)
	default:
		// session/permission lifecycle kinds (and, for copilotacp, the
		// process-lifecycle kind too — it never emits KindProcessExited)
		// -- no EventFanout/TypedEventCallback analog today, matching
		// runtimeEventSink.Write's identical default no-op for the native
		// path.
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

// acpDeltaPayload reads the "content" key both opencodeacp's and
// copilotacp's KindAgentDelta payloads share, plus each package's own
// reasoning/thinking tagging convention — opencodeacp tags a reasoning
// chunk {"content":...,"phase":"thought"} (see opencodeacp/translate.go's
// agent_thought_chunk case, as distinct from the "phase":"message" tag on
// ordinary content); copilotacp tags the same distinction
// {"content":...,"thinking":true} (see copilotacp/translate.go's
// acpUpdateThinking case). Both tagging conventions are checked —
// TASKS/agent-host-acp/11's Finding 2 review found that reading only
// "content" and always emitting llmtypes.EventDelta (this file's prior
// behavior) let reasoning/thought text flush as ordinary narration,
// persisted as part of the visible answer instead of being routed to
// chat_generate.go's thinkingBlocks/chat.PhaseThinking like the native
// path (wrapper_sink.go's handleDelta) already does.
type acpDeltaPayload struct {
	Content  string `json:"content"`
	Phase    string `json:"phase"`
	Thinking bool   `json:"thinking"`
}

// extractDeltaEvent mirrors wrapper_sink.go's handleDelta: a
// thinking/thought-tagged chunk becomes llmtypes.EventThinking rather
// than llmtypes.EventDelta, so chat_generate.go's shared streamLoop
// excludes it from the persisted answer the same way it does for every
// native-path adapter.
func extractDeltaEvent(raw json.RawMessage) llmtypes.StreamEvent {
	var p acpDeltaPayload
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &p)
	}
	if p.Phase == "thought" || p.Thinking {
		return llmtypes.StreamEvent{
			Type:          llmtypes.EventThinking,
			ThinkingBlock: &llmtypes.ThinkingBlock{Thinking: p.Content},
		}
	}
	return llmtypes.StreamEvent{Type: llmtypes.EventDelta, Content: p.Content}
}

// acpTurnCompletedPayload mirrors opencodeacp's finishTurn payload shape
// ({"stop_reason","usage"} — "usage" present only when the JSON-RPC
// session/prompt response itself carried one, per opencodeacp/client.go's
// finishTurn) and copilotacp's own KindTurnCompleted payload
// ({"stop_reason"} only — copilotacp's awaitPromptResult never populates
// a "usage" key today, confirmed directly). opencodeacp's own package doc
// never claimed the session/prompt response's "usage" field was observed
// in real traffic (only the unrelated usage_update session/update
// NOTIFICATION variant was, and that one is deliberately skipped as
// informational — see opencodeacp/translate.go) — live-verified directly
// during this fix's own re-verification dogfeed (real opencode 1.15.6,
// TASKS/agent-host-acp/11's Work Log addendum): the real response DOES
// carry a "usage" object, and its field names line up with
// llmtypes.Usage's own Go-style names closely enough that a plain
// json.Unmarshal (no tag translation) round-trips real, non-zero
// input/output token counts end to end into chat_generate.go's
// finalUsage and the persisted stream_end payload.
type acpTurnCompletedPayload struct {
	Usage *llmtypes.Usage `json:"usage"`
}

// handleTurnCompleted sends EventUsage (when the payload carries usage
// data) then the terminal EventDone — TASKS/agent-host-acp/11's Finding
// 3 fix. Unlike wrapper_sink.go's handleTurnCompleted (which receives
// usage and done as two SEPARATE Write calls from the native translator
// and must pick one or the other per call — see its own doc comment),
// ACP's own KindTurnCompleted event fires exactly once per turn, so both
// events are sent from this single combined payload when usage data is
// present; EventUsage first so chat_generate.go's finalUsage observes it
// before the terminal EventDone closes out the turn.
func (a *acpSession) handleTurnCompleted(ctx context.Context, raw json.RawMessage) {
	var p acpTurnCompletedPayload
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &p)
	}
	if p.Usage != nil {
		a.sendFanout(ctx, llmtypes.StreamEvent{Type: llmtypes.EventUsage, Usage: p.Usage})
	}
	a.sendFanout(ctx, llmtypes.StreamEvent{Type: llmtypes.EventDone})
}

// processExitedPayload reads the "error" key opencodeacp's waitProcess
// populates with cmd.Wait()'s error string on an abnormal/unprompted
// subprocess exit — empty/absent for a clean exit (Wait() returned nil;
// see opencodeacp/client.go's waitProcess). copilotacp's Client does not
// emit KindProcessExited at all today — a documented go-agent-wrapper
// gap, not something this Nanite-side fix can close (see agent_acp.go's
// bootACP doc comment) — so this case simply never fires for a
// Copilot-CLI-driven session; processExitErr correctly stays nil for the
// life of such a session, same as before this fix.
type processExitedPayload struct {
	Error string `json:"error"`
}

// handleProcessExited captures an abnormal ACP subprocess exit so
// bootACP's draining goroutine can surface it through Session.Wait() as a
// non-nil error — TASKS/agent-host-acp/11's Finding 2 fix. An intentional
// Stop never reaches here: acpSession.Stop's Close() call closes the
// Events channel synchronously (opencodeacp's Client.Close calls
// closeEvents() immediately, well before it ever waits on the
// subprocess) — by the time waitProcess's own later KindProcessExited
// emit fires, the channel is already closed and the emit is a silent
// no-op (see Client.emit's eventsClosed guard). So this only ever fires
// for a genuine, unprompted process death — the exact case
// internal/recovery/broker exists to catch.
//
// Constructs a real (if minimally populated) *agentsessions.ExitError
// rather than a bare error: internal/recovery/broker's Classify requires
// errors.As(err, &xe) against that exact concrete type (verified directly
// against classifier.go) to route a crash to the broker at all — a plain
// error would satisfy Session.Wait()'s "non-nil" contract but be
// silently invisible to the broker, reproducing this exact "silent
// fail-open" bug class one layer deeper. ACP's wire protocol carries no
// structured exit-code/signal info (only the wrapped process error's
// string), so Code is set to -1 — ExitError's own documented convention
// for "no real exit code available" — which is enough for Classify's
// final default branch (xe.Code != 0) to route this to the broker as an
// unclassified crash (transient on the first attempt, permanent on
// retry), same as it would for any other unclassified non-zero exit.
func (a *acpSession) handleProcessExited(raw json.RawMessage) {
	var p processExitedPayload
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &p)
	}
	if p.Error == "" {
		return
	}
	a.processExitErr = fmt.Errorf("acpSession: process exited abnormally (%s): %w",
		p.Error, &agentsessions.ExitError{Code: -1})
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
