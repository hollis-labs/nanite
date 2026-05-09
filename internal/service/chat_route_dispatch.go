package service

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/classify"
	"github.com/hollis-labs/nanite/internal/dispatch"
	"github.com/hollis-labs/nanite/internal/envelope"
)

// init wires the production passive-renderable envelope-type provider
// so classify.ClassifyRoute's tool-availability gate consults the live
// allow-list (envelope.PassiveRenderableTypes) instead of the v1
// baseline. The provider is a no-op slice copy — cheap to call on every
// classification.
//
// Defined here rather than in the classify package to keep classify
// import-edge-free of envelope (the test-overridable provider pattern
// in route.go documents this constraint). The service package already
// depends on both, making it the natural composition seam.
func init() {
	classify.SetEnvelopeTypeProvider(func() []string {
		return envelope.PassiveRenderableTypes
	})
}

// routeDispatchOutcome is the result of attemptRouteDispatch — what the
// dispatch seam learned and what (if anything) it emitted to the SSE
// stream. Pulled out as a typed value so the wiring is testable
// without spinning up the full generateResponse goroutine.
type routeDispatchOutcome struct {
	// Attempted is true when the route was non-chat-direct AND a wired
	// executor was available; the helper called DispatchExecutor.
	Attempted bool

	// Dispatched is true when DispatchExecutor returned an envelope (no
	// failure code). Future short-circuit paths (B5) gate on this.
	Dispatched bool

	// Response is the raw ExecutorResponse, including failures. Useful
	// for telemetry (B6) and for tests asserting exact failure shapes.
	// nil when the route was chat-direct or no executor was wired.
	Response *dispatch.ExecutorResponse

	// EmittedEnvelope is true when the helper emitted a plugin_envelope
	// SSE event for the executor's envelope. Useful for tests that
	// assert the side-channel emit happened.
	EmittedEnvelope bool
}

// attemptRouteDispatch consults the route hint on loopState and, when
// the route is non-chat-direct AND an envelope-render executor is
// wired, builds an ExecutorRequest, calls DispatchExecutor, and emits
// the resulting envelope (if any) onto the SSE stream as a
// plugin_envelope event. The chat-direct LLM loop is NOT
// short-circuited — the route is INFORMATIVE per
// docs/architecture/classifier-routing.md §1, and the chat agent always
// retains the chat-direct fallback.
//
// This is the B2 wiring surface (CW-20260429-0031). Short-circuit
// behavior — treating a successful dispatch as a complete turn and
// suppressing the LLM loop — is reserved for B5 / Phase 2 graduation
// (executor-handoff.md §6), when the chat agent's prompt is updated to
// dispatch via a `dispatch_executor` tool rather than via runtime
// injection. Until then, both the executor's envelope and the
// chat-direct LLM response can reach the user; the dispatch-seam log
// + B6 telemetry record which path the classifier preferred.
//
// Returns a typed outcome. The boolean "Attempted" tells the caller
// whether the seam fired at all (for log filtering); "Dispatched" tells
// the caller whether the executor returned an envelope.
//
// Nil-safe: when ls is nil OR the route is RouteChatDirect OR no
// executor is injected, returns the zero outcome (Attempted=false).
func (s *chatServiceImpl) attemptRouteDispatch(
	ctx context.Context,
	sessionID, userContent string,
	ls *loopState,
	ch chan chat.StreamEvent,
) routeDispatchOutcome {
	if ls == nil {
		return routeDispatchOutcome{}
	}
	decision := ls.RouteDecision()
	if decision.Route == "" || decision.Route == classify.RouteChatDirect {
		// Conservative default — empty / chat-direct routes don't
		// trigger dispatch. Logged at debug only to keep production
		// logs clean.
		slog.Debug("chat-service: route dispatch — chat-direct, no executor handoff",
			"session_id", sessionID,
			"route", decision.Route.String(),
		)
		return routeDispatchOutcome{}
	}

	if s.envelopeRenderExecutor == nil {
		slog.Info("chat-service: route dispatch — executor not wired, route is informative only",
			"session_id", sessionID,
			"route", decision.Route.String(),
			"target_envelope_type", decision.TargetEnvelopeType,
		)
		return routeDispatchOutcome{}
	}

	// Build the ExecutorRequest from the chat turn. Verbatim user
	// message preserves the user's exact framing for grounding (per
	// dispatch.ExecutorRequest godoc — "the executor reads this, NOT
	// the Chat agent's paraphrase").
	intent := routeToIntent(decision.Route)
	if intent == "" {
		// Forward-compatibility: a route value the v1 dispatch seam
		// doesn't yet know how to translate. Log and bail rather than
		// dispatching with an empty intent (which would just echo back
		// invalid_intent).
		slog.Warn("chat-service: route dispatch — unknown route value, no intent mapping",
			"session_id", sessionID,
			"route", decision.Route.String(),
		)
		return routeDispatchOutcome{}
	}
	req := dispatch.ExecutorRequest{
		Intent:             intent,
		TargetEnvelopeType: decision.TargetEnvelopeType,
		UserRequest:        userContent,
		SessionID:          sessionID,
		SyntheticAllowed:   decision.SyntheticAllowed,
	}

	resp, err := dispatch.DispatchExecutor(ctx, s.envelopeRenderExecutor, req)
	if err != nil {
		// Harness-level wiring failure (nil executor, etc.). Already
		// nil-checked above — this branch covers an Executor.Execute
		// returning a Go error (reserved for nil dependencies). Log
		// and fall through.
		slog.Warn("chat-service: route dispatch — DispatchExecutor returned error",
			"session_id", sessionID,
			"route", decision.Route.String(),
			"err", err,
		)
		return routeDispatchOutcome{Attempted: true}
	}

	outcome := routeDispatchOutcome{Attempted: true, Response: resp}

	if resp != nil && resp.Failure != nil {
		// Typed failure — the executor decided it can't satisfy the
		// request (missing_context, invalid_intent, etc.). Log
		// telemetry and fall through to the chat-direct loop. This is
		// the load-bearing "informative not prohibitive" path —
		// classifier may emit non-chat-direct routes that the executor
		// can't yet productively handle (e.g. RouteExecutorEnvelopeRender
		// without pre-resolved Data → missing_context). The chat
		// agent's LLM loop runs as normal.
		slog.Info("chat-service: route dispatch — executor returned typed failure, falling back to chat-direct",
			"session_id", sessionID,
			"route", decision.Route.String(),
			"failure_code", string(resp.Failure.Code),
			"failure_message", resp.Failure.Message,
			"summary", resp.Summary,
		)
		return outcome
	}

	if resp == nil || resp.Envelope == nil {
		// No envelope, no failure — the executor may have returned a
		// text-only Result (B3 reserves Result for non-envelope
		// intents). Future intents can populate Result; for v1 we just
		// log and let the chat-direct loop run.
		slog.Info("chat-service: route dispatch — executor returned no envelope, falling back to chat-direct",
			"session_id", sessionID,
			"route", decision.Route.String(),
			"summary", summaryOrEmpty(resp),
		)
		return outcome
	}

	// Envelope returned — emit it as a plugin_envelope SSE event. The
	// FE's plugin_envelope handler (chat_generate.go:1570 comment trail
	// — "broadcast a plugin_envelope SSE event for every parsed
	// envelope that carries a panel-routing hint") routes it to the
	// configured drawer/panel. The chat-direct LLM loop still runs
	// after this — short-circuit is deferred to B5.
	envBytes, jerr := json.Marshal(resp.Envelope)
	if jerr != nil {
		slog.Warn("chat-service: route dispatch — failed to marshal executor envelope",
			"session_id", sessionID,
			"route", decision.Route.String(),
			"err", jerr,
		)
		return outcome
	}
	if ch != nil {
		ch <- chat.StreamEvent{
			Type:     "plugin_envelope",
			Envelope: string(envBytes),
		}
		outcome.EmittedEnvelope = true
	}
	outcome.Dispatched = true
	slog.Info("chat-service: route dispatch — executor returned envelope, emitted on stream",
		"session_id", sessionID,
		"route", decision.Route.String(),
		"target_envelope_type", decision.TargetEnvelopeType,
		"envelope_type", resp.Envelope.Type,
		"summary", resp.Summary,
	)
	return outcome
}

// routeToIntent maps a classify.Route value to the executor-recognized
// intent vocabulary token. Empty return means the route is unmapped
// (forward-compatibility — a future Route value the v1 dispatch seam
// doesn't know about yet); the caller logs and bails.
func routeToIntent(r classify.Route) string {
	switch r {
	case classify.RouteExecutorEnvelopeRender:
		// Matches envelope_render.IntentRenderEnvelope. Defined as a
		// string literal here to avoid an import edge in the service
		// package; envelope_render.IntentRenderEnvelope is the
		// authoritative definition.
		return "render_envelope"
	}
	return ""
}

// summaryOrEmpty is a nil-safe accessor for response.Summary. Pulled
// out for log readability.
func summaryOrEmpty(resp *dispatch.ExecutorResponse) string {
	if resp == nil {
		return ""
	}
	return resp.Summary
}
