// CW-20260509-0046: agent-broker call-site wiring upstream of chat-harness
// prompt assembly. This file owns the seam where chat_generate.go consults
// `broker.Decide()` BEFORE the chat-loop entry to decide whether the turn
// dispatches to a worker/planner subagent or is handled by the chat agent
// directly.
//
// Boundary (load-bearing per decisions.nanite.architecture.agent_broker_v1):
//
//   - The agent broker is the dispatch DECISION (this seam).
//   - The reflex/grounding/inner-broker scaffold in
//     internal/mcp/self_tools_dispatch.go::callExecuteTask enriches the
//     dispatch CALL once a dispatch is in flight.
//
// Both layers run. Don't collapse them. The upstream broker decides
// whether to dispatch at all + which slug; the downstream layer projects
// reflex hints, prepends grounding-recall memories, and logs the inner
// "broker_decision" event_log entry.
//
// Sibling tickets in this wave:
//   - CW-20260509-0045 — go-agent-broker v0.2.0 (deterministic broker.New).
//   - CW-20260509-0047 — agent_broker_decisions schema +
//     Store.InsertAgentBrokerDecision helper.
//   - CW-20260509-0048 — SSE `agent_broker_decision` event emission. Lands
//     a typed wire event alongside the agent_broker_decisions row and the
//     companion event_log entry so dev-mode inspectors and FE audit
//     consumers see broker activity in real time. The row-write +
//     event_log contract is unchanged; the SSE event is additive.
//   - CW-20260509-0049 — admin CLI for ListRecentAgentBrokerDecisions.

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"

	agentbroker "github.com/hollis-labs/go-agent-broker/broker"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/classify"
	"github.com/hollis-labs/nanite/internal/reflex"
	"github.com/hollis-labs/nanite/internal/store"
)

// agentBrokerDecisionEventType is the SSE stream event Type emitted on
// every consultation of the agent broker (CW-20260509-0048). The name
// matches the agent_broker_decisions table and the companion event_log
// entry so wire-level tracing aligns with both durable records.
//
// Direct-stream event (not a plugin_envelope wrap): this is a signal
// for FE inspectors / audit panels, not a renderable card. The FE
// inspector follow-up consumes the same payload shape from the wire —
// no envelope-manifest registration is required because no FE component
// dispatch keys off it. Mirrors the rate_budget_pause / notify_pause
// pattern in chat_rate_budget_pause.go.
const agentBrokerDecisionEventType = "agent_broker_decision"

// agentBrokerDecisionPayload is the JSON shape carried on the SSE Data
// field for agent_broker_decision events. Documented here so downstream
// consumers (FE inspector card, telemetry tools) have a single canonical
// reference. Field names mirror the agent_broker_decisions columns +
// the companion event_log metadata blob, so a consumer that already
// reads the row or the log entry can decode the wire event with the
// same struct.
//
// All fields are omitempty so chat-direct decisions (where the chat
// agent handles the turn — Decision == "") emit a compact event without
// noisy zero values.
type agentBrokerDecisionPayload struct {
	// AgentBrokerDecisionID is the auto-increment row id assigned by
	// Store.InsertAgentBrokerDecision. Lets a consumer correlate the
	// wire event with the durable row + the event_log entry.
	AgentBrokerDecisionID int64 `json:"agent_broker_decision_id"`

	SessionID string `json:"session_id"`
	TurnID    string `json:"turn_id,omitempty"`

	// Decision is the broker's chosen agent profile slug ("worker",
	// "planner", "chat", ...) — empty when AgentProfile == ProfileChat
	// (chat-direct). Mirrors agent_broker_decisions.decision.
	Decision string `json:"decision,omitempty"`

	// Reason is the broker's rule label ("default-chat-handle",
	// "mode=work", "reflex-match", ...). Always populated.
	Reason string `json:"reason"`

	// Confidence is the broker's [0, 1] confidence in the decision.
	// 0 for default-chat-handle / unmatched rules.
	Confidence float64 `json:"confidence"`

	// ModeSignal is the per-turn classified mode ("work", "chat", ...)
	// from classify.ClassifyMode. Empty when no mode signal fired.
	ModeSignal string `json:"mode_signal,omitempty"`

	// ScopeTier is the loop-state's classified scope tier
	// ("small", "medium", "large", ...). Empty when classifyAndAttach
	// did not populate the loop state (degenerate paths).
	ScopeTier string `json:"scope_tier,omitempty"`

	// ReflexID is the reflex-catalog match id when a reflex matched
	// the user input. Empty when no reflex match fired.
	ReflexID string `json:"reflex_id,omitempty"`

	// CreatedAt is the server-side timestamp the row was written, read
	// back from agent_broker_decisions.created_at by the Store helper.
	// Lets consumers order events without trusting the wire-arrival
	// time.
	CreatedAt string `json:"created_at,omitempty"`
}

// brokerDispatchOutcome is the typed result of attemptBrokerDispatch — a
// readable shape for tests asserting what the seam did. Consumers in
// production code currently only consult Dispatched (logging path), but
// the rest of the fields keep the contract self-documenting and let the
// CW-20260509-0048 SSE follow-up bolt on without reshaping the helper.
type brokerDispatchOutcome struct {
	// Consulted is true when the broker ran (i.e. AgentBroker is wired,
	// the call did not error). False when the broker is unwired (nil) or
	// when Decide returned an error.
	Consulted bool

	// Decision is the raw broker.Decision. Zero value when Consulted is
	// false. AgentProfile == "" means the chat agent handles the turn
	// directly (see broker.ProfileChat).
	Decision agentbroker.Decision

	// Dispatched is true when Decision.AgentProfile != "" — the broker
	// asked the call site to bypass the chat agent's discretion and
	// hand the turn to the named subagent. The seam emits a
	// `plugin_envelope` SSE event (mirroring attemptRouteDispatch) so
	// the chat agent's stream sees the resulting envelope as if it had
	// called task_execute itself.
	Dispatched bool

	// EmittedEnvelope is true when the seam pushed a plugin_envelope SSE
	// event onto the channel. False when Dispatched=true but the
	// downstream task_execute call returned an error or empty envelope
	// (the chat-direct LLM loop runs as the fallback in either case).
	EmittedEnvelope bool

	// AgentBrokerDecisionID is the auto-increment row id assigned by
	// Store.InsertAgentBrokerDecision. Zero when no row was written
	// (Consulted=false or store insert failed). Carried in the outcome
	// so CW-20260509-0048 can reference the row id in the SSE payload.
	AgentBrokerDecisionID int64

	// EmittedSSEDecision is true when the seam pushed an
	// `agent_broker_decision` SSE event onto the channel
	// (CW-20260509-0048). Independent of EmittedEnvelope — the
	// agent_broker_decision wire event fires on every consultation
	// (chat-direct AND dispatch), the plugin_envelope only fires on
	// dispatch decisions with a valid envelope output.
	EmittedSSEDecision bool
}

// attemptBrokerDispatch is the upstream agent-broker call site
// (CW-20260509-0046). Runs once per turn before the chat-loop entry,
// projects nanite's classify/reflex outputs into the broker's primitive
// `Input` shape, and consults `s.agentBroker.Decide()`.
//
// On every consultation the outcome is appended to agent_broker_decisions
// (telemetry — read by the v2 peer-agent escape-hatch decision). On
// dispatch decisions (AgentProfile != ""), the seam synthesizes a
// task_execute invocation by calling s.tools.Execute(...) directly so
// the existing reflex/grounding/inner-broker enrichment in callExecuteTask
// fires; the resulting envelope is pushed as a `plugin_envelope` SSE
// event (same side-channel attemptRouteDispatch uses).
//
// Pass-through behavior preserved:
//
//   - When agentBroker is nil → no row written, no dispatch, returns the
//     zero outcome.
//   - When the broker returns AgentProfile == "" → row written, no
//     dispatch, the chat-direct LLM loop runs as before.
//   - When the broker returns a non-empty AgentProfile → row written,
//     task_execute is invoked, the envelope (if any) is emitted on the
//     stream. The chat-direct LLM loop ALSO runs (no short-circuit) —
//     mirrors attemptRouteDispatch's "informative not prohibitive"
//     stance per docs/architecture/classifier-routing.md §1. Short-
//     circuit semantics are reserved for v2 / a Phase-2 graduation.
func (s *chatServiceImpl) attemptBrokerDispatch(
	ctx context.Context,
	sessionID, turnID, userContent, agentID string,
	ls *loopState,
	ch chan chat.StreamEvent,
) brokerDispatchOutcome {
	if s.agentBroker == nil {
		// Production wiring installs the deterministic broker
		// (broker.New()). The nil-safe branch keeps tests + bootstrap
		// configs that haven't wired the broker yet from regressing.
		slog.Debug("chat-service: agent-broker not wired, skipping upstream dispatch decision",
			"session_id", sessionID,
		)
		return brokerDispatchOutcome{}
	}
	if ls == nil {
		// Defensive — every production path through generateResponse
		// initializes ls before reaching this seam. Kept symmetric
		// with attemptRouteDispatch.
		return brokerDispatchOutcome{}
	}

	// Build the broker.Input from nanite's per-turn signals. Each
	// projection is intentionally narrow (string / float64) so the
	// go-agent-broker module stays dependency-free per
	// decisions.nanite.architecture.agent_broker_v1.
	input := s.buildBrokerInput(userContent, ls)

	decision, derr := s.agentBroker.Decide(ctx, input)
	if derr != nil {
		// Non-fatal: a broker failure must not kill the turn. Log,
		// fall through to the chat-direct loop.
		slog.Warn("chat-service: agent-broker decide failed, falling back to chat-direct",
			"session_id", sessionID,
			"err", derr,
		)
		return brokerDispatchOutcome{}
	}

	out := brokerDispatchOutcome{
		Consulted: true,
		Decision:  decision,
	}

	// Persist the telemetry row regardless of which branch we take.
	// Append-only — readers (admin CLI, future FE inspector) tail this
	// table to understand routing behavior.
	row := s.persistAgentBrokerDecision(sessionID, turnID, input, decision)
	if row != nil {
		out.AgentBrokerDecisionID = row.ID
	}

	// CW-20260509-0048: emit the agent_broker_decision SSE event on
	// every consultation (chat-direct AND dispatch). This is the live
	// wire signal alongside the durable agent_broker_decisions row +
	// the companion event_log entry. FE inspectors and audit panels
	// consume the wire event for real-time broker visibility; the
	// FE inspector card itself is intentionally a follow-up
	// (CW-20260509-0048 scope §"Boundary").
	//
	// Emitted whether or not a row was written — when the store insert
	// failed, the SSE event still surfaces the broker's decision so
	// the wire signal isn't dropped because of a telemetry-table
	// outage. AgentBrokerDecisionID = 0 in that case (consumers tolerate).
	if s.emitAgentBrokerDecisionSSE(ch, sessionID, turnID, input, decision, row) {
		out.EmittedSSEDecision = true
	}

	if decision.AgentProfile == agentbroker.ProfileChat {
		// Chat-direct path. No dispatch, no envelope emission. The LLM
		// loop runs unchanged. Logged at info so production logs can
		// confirm the seam fired.
		slog.Info("chat-service: agent-broker decision — chat-direct",
			"session_id", sessionID,
			"reason", decision.Reason,
			"confidence", decision.Confidence,
		)
		return out
	}

	out.Dispatched = true

	slog.Info("chat-service: agent-broker decision — dispatch",
		"session_id", sessionID,
		"agent_profile", decision.AgentProfile,
		"reason", decision.Reason,
		"confidence", decision.Confidence,
	)

	// Synthesize the task_execute call. Routing through s.tools.Execute
	// hits the SelfToolsTransport's task_execute handler
	// (callExecuteTask), which runs the existing grounding-recall +
	// reflex-match + inner-broker scaffold layers — the load-bearing
	// boundary the exec prompt calls out: broker is the dispatch
	// DECISION, reflex+grounding enrich the dispatch CALL.
	//
	// session_id and message are the only required args; the chat
	// agent's ID is the canonical parent_agent_id when known.
	taskInput := map[string]any{
		"session_id":      sessionID,
		"parent_agent_id": agentID,
		"message":         userContent,
	}
	if turnID != "" {
		// Forwarded so callExecuteTask can plumb it into the grounding
		// consultation rows + reflex match log. Schema-agnostic from
		// the broker's perspective.
		taskInput["turn_id"] = turnID
	}

	if s.tools == nil {
		slog.Warn("chat-service: agent-broker dispatch — ToolService not wired",
			"session_id", sessionID,
		)
		return out
	}

	res, terr := s.tools.Execute(ctx, agentID, "task_execute", taskInput)
	if terr != nil {
		slog.Warn("chat-service: agent-broker dispatch — task_execute failed, falling back to chat-direct",
			"session_id", sessionID,
			"agent_profile", decision.AgentProfile,
			"err", terr,
		)
		return out
	}
	if res == nil || res.IsError {
		summary := ""
		if res != nil {
			summary = res.Output
		}
		slog.Info("chat-service: agent-broker dispatch — task_execute returned error envelope, falling back to chat-direct",
			"session_id", sessionID,
			"agent_profile", decision.AgentProfile,
			"summary", chat.TruncateStr(summary, 200),
		)
		return out
	}

	// task_execute returns the envelope JSON as the tool result Output.
	// We re-emit it as a plugin_envelope SSE event so the FE's
	// plugin_envelope handler routes it to the configured drawer/panel
	// — the same side-channel attemptRouteDispatch uses for the B2
	// classifier-route handoff.
	if ch != nil && res.Output != "" {
		// Validate the JSON shape so we don't push a malformed payload
		// onto the stream. Minimal parse — we don't need the structured
		// envelope, only confirmation it's a JSON object.
		var probe map[string]any
		if err := json.Unmarshal([]byte(res.Output), &probe); err != nil {
			slog.Warn("chat-service: agent-broker dispatch — task_execute output is not JSON, skipping envelope emit",
				"session_id", sessionID,
				"err", err,
			)
			return out
		}
		ch <- chat.StreamEvent{
			Type:     "plugin_envelope",
			Envelope: res.Output,
		}
		out.EmittedEnvelope = true
	}

	return out
}

// buildBrokerInput projects nanite's classify/reflex per-turn signals
// into the broker's primitive `Input` shape. Each field is independently
// nil/zero-safe per the broker.Input contract — populating a subset
// produces well-defined (degenerate) behavior in the deterministic
// rule set.
//
// The projection composes the same primitives the chat-loop strategy
// path consumes (classify.ClassifyMode + classify.Classify + reflex.Match
// over reflex.BuiltinReflexes). It does NOT consult a session-mode
// pointer — SessionMode is the workspace persistent mode and the
// deterministic v1 broker keys off the per-turn classified mode (see
// CW-20260509-0045 implementer report §"Distinct Mode vs SessionMode").
func (s *chatServiceImpl) buildBrokerInput(userContent string, ls *loopState) agentbroker.Input {
	in := agentbroker.Input{
		UserText: userContent,
	}

	// Per-turn mode classification. Same call ChatService already runs
	// upstream of this seam (the mode_suggestion emit on
	// chat_generate.go:420), but called fresh here so the broker has a
	// stable input even if the upstream emit was skipped (e.g. the
	// classified mode matched the session mode and the suggestion was
	// suppressed).
	mode := classify.ClassifyMode(userContent)
	if mode.Suggested != "" {
		in.Mode = mode.Suggested
		in.ModeConfidence = mode.Confidence
	}

	// Pre-loop scope/pattern classification. Read from loopState because
	// classifyAndAttach already populated it earlier in generateResponse;
	// re-running classify.Classify here would be wasted work and risks
	// disagreement with the value the rest of the loop sees.
	if ls != nil {
		tier, pattern := ls.Classification()
		if tier.IsValid() {
			in.ScopeTier = tier.String()
		}
		if pattern.IsValid() {
			in.ExecutionPattern = pattern.String()
		}
	}

	// Reflex matcher projection. Uses the BuiltinReflexes fallback
	// (matching the chat-strategy convention from chat_strategy.go:61).
	// Production wiring layers user-loaded reflexes via SelfToolsTransport
	// — that downstream set is consulted again in callExecuteTask after
	// the broker's decision routes through task_execute. Running the
	// builtin set here is sufficient for the broker's decision input;
	// the dispatch CALL gets the merged set's enrichment.
	tier, pattern := classifyForReflex(ls)
	if match, ok := reflex.Match(userContent, tier, pattern, reflex.BuiltinReflexes()); ok {
		in.ReflexMatchID = match.Reflex.ID
		in.ReflexAgentSlug = match.Reflex.ResolvesTo.Profile
		in.ReflexConfidence = float64(match.Reflex.Priority) / 100.0
		// Priority is documented as a 0-100 integer in
		// docs/agent-reflex-catalog.md; projecting via /100 gives the
		// broker's [0, 1] confidence band a meaningful stand-in until
		// reflex catalog entries grow per-match confidence (tracked
		// follow-up in the v0.2.0 release notes — "Rule 5 confidence
		// tuning").
		if in.ReflexConfidence > 1.0 {
			in.ReflexConfidence = 1.0
		}
	}

	return in
}

// classifyForReflex returns the (tier, pattern) pair the reflex matcher
// consumes. Reads from loopState when available (prefilled by
// classifyAndAttach); falls back to TierInvalid/PatternInvalid which
// the matcher's ScopeTierHint guard handles correctly (no match for
// guard-carrying reflexes).
func classifyForReflex(ls *loopState) (classify.ScopeTier, classify.ExecutionPattern) {
	if ls == nil {
		return classify.TierInvalid, classify.PatternInvalid
	}
	return ls.Classification()
}

// persistAgentBrokerDecision writes the per-turn telemetry row to
// agent_broker_decisions. Returns the persisted row pointer (with
// auto-increment ID and server-side CreatedAt populated by the Store
// helper), or nil when the store is unwired or the insert failed.
// Errors are logged and swallowed — the broker decision MUST NOT block
// the turn.
//
// user_input_hash is sha256(broker.Input.UserText) — documented in the
// CW-20260509-0047 schema follow-up so v2 telemetry analysis isn't
// guessing at the hash function. SHA-256 over the raw text gives a
// stable identifier for "same input string seen twice across turns"
// without persisting the input itself (which may contain user data the
// telemetry consumer shouldn't see).
//
// Returning the row pointer lets the SSE emission step
// (CW-20260509-0048) include the server-side created_at on the wire
// without an extra DB round-trip — consumers can order broker events
// by the persisted timestamp instead of trusting wire arrival.
func (s *chatServiceImpl) persistAgentBrokerDecision(
	sessionID, turnID string,
	input agentbroker.Input,
	decision agentbroker.Decision,
) *store.AgentBrokerDecision {
	if s.store == nil {
		return nil
	}

	row := &store.AgentBrokerDecision{
		SessionID:     sessionID,
		TurnID:        turnID,
		UserInputHash: hashUserInput(input.UserText),
		ModeSignal:    input.Mode,
		ScopeTier:     input.ScopeTier,
		ReflexID:      input.ReflexMatchID,
		Decision:      decision.AgentProfile,
		Reason:        decision.Reason,
		Confidence:    decision.Confidence,
	}

	if err := s.store.InsertAgentBrokerDecision(row); err != nil {
		slog.Warn("chat-service: insert agent_broker_decisions failed",
			"session_id", sessionID,
			"turn_id", turnID,
			"err", err,
		)
		// Best-effort: also drop a sentinel into event_log so the
		// failure is correlated with the turn that experienced it.
		// LogEvent is fire-and-forget (no return).
		s.store.LogEvent(sessionID, "agent_broker_decision_error", "error", err.Error(),
			fmt.Sprintf(`{"turn_id":%q,"reason":%q}`, turnID, decision.Reason))
		return nil
	}

	// Mirror the pre-existing inner-broker convention from
	// callExecuteTask (line 190): emit an event_log row alongside the
	// telemetry table so log-tailers and admin CLIs observe broker
	// activity without joining tables.
	meta := fmt.Sprintf(
		`{"agent_broker_decision_id":%d,"agent_profile":%q,"reason":%q,"confidence":%g,"mode":%q,"scope_tier":%q,"reflex_id":%q}`,
		row.ID, decision.AgentProfile, decision.Reason, decision.Confidence,
		input.Mode, input.ScopeTier, input.ReflexMatchID,
	)
	s.store.LogEvent(sessionID, "agent_broker_decision", "info", decision.Reason, meta)

	return row
}

// emitAgentBrokerDecisionSSE pushes an `agent_broker_decision` event
// onto the SSE stream so dev-mode inspectors and FE audit panels see
// broker activity in real time (CW-20260509-0048). Fires on every
// consultation — chat-direct decisions emit the event with
// Decision == "" so consumers can count both branches.
//
// Returns true when an event was pushed, false when the channel was
// nil (test paths / degenerate wire-up) or marshal failed.
//
// Direct stream event (Type = "agent_broker_decision", payload in
// Data) — not a plugin_envelope wrap. Mirrors rate_budget_pause /
// notify_pause: signal-shaped wire events that carry their payload
// in the StreamEvent.Data JSON field. The FE inspector card is
// intentionally out of scope for this ticket — the event lands on
// the wire so a follow-up FE consumer can pick it up without
// retroactive backend changes.
//
// Marshal failures are logged and silently swallowed: dropping the
// SSE event is preferable to crashing the turn over a payload-encode
// error. The durable agent_broker_decisions row + the event_log
// entry both still landed, so the broker decision is recoverable.
func (s *chatServiceImpl) emitAgentBrokerDecisionSSE(
	ch chan chat.StreamEvent,
	sessionID, turnID string,
	input agentbroker.Input,
	decision agentbroker.Decision,
	row *store.AgentBrokerDecision,
) bool {
	if ch == nil {
		return false
	}

	payload := agentBrokerDecisionPayload{
		SessionID:  sessionID,
		TurnID:     turnID,
		Decision:   decision.AgentProfile,
		Reason:     decision.Reason,
		Confidence: decision.Confidence,
		ModeSignal: input.Mode,
		ScopeTier:  input.ScopeTier,
		ReflexID:   input.ReflexMatchID,
	}
	if row != nil {
		payload.AgentBrokerDecisionID = row.ID
		payload.CreatedAt = row.CreatedAt
	}

	data, err := json.Marshal(payload)
	if err != nil {
		slog.Warn("chat-service: marshal agent_broker_decision SSE payload failed",
			"session_id", sessionID,
			"turn_id", turnID,
			"err", err,
		)
		return false
	}

	ch <- chat.StreamEvent{
		Type: agentBrokerDecisionEventType,
		Data: string(data),
	}
	return true
}

// hashUserInput returns the SHA-256 hex digest of s. Empty input maps
// to the empty string (not the digest of the empty string) so callers
// can distinguish "no input" from "input that hashes to ...". Pure
// helper — no slog / no store dependency.
func hashUserInput(s string) string {
	if s == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
