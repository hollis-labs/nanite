// Mutable dispatch-to-agent policy is retired. Native continuation reads only
// its immutable definition; fabric dispatch waits for an adopted issuer.
package service

import (
	"context"

	"github.com/hollis-labs/nanite/internal/chat"
)

// reflexDispatchOutcome is the typed result of attemptReflexDispatch — a
// readable shape for tests and for parity with the retired
// brokerDispatchOutcome. Matched mirrors the old outcome's Consulted+
// Dispatched bits collapsed into one (a dispatch_to_agent reflex firing
// IS the decision, unlike the old broker which "consulted" on every turn
// including chat-direct ones).
type reflexDispatchOutcome struct {
	// Matched is true when a dispatch_to_agent reflex's trigger fired
	// this turn. False on every turn where none did (the former Rule 6
	// "default, no dispatch" case — no row is written, no event_log
	// entry, no task_execute call; the chat-direct LLM loop runs as if
	// this seam did not exist).
	Matched bool

	ReflexID   string
	ReflexName string
	// AgentSlug is the fired reflex's declared target — see design note
	// (4) above for why this is intent/telemetry, not an enforced
	// override, for this migration.
	AgentSlug  string
	Confidence float64
	Reason     string

	// Invoked is true when the synthesized task_execute call was made
	// (Matched=true and s.tools was wired). False when Matched=true but
	// s.tools is nil (degenerate/test wiring).
	Invoked bool

	// EmittedEnvelope is true when the seam pushed a plugin_envelope SSE
	// event onto the channel. False when Invoked=true but task_execute
	// returned an error, an error envelope, or non-JSON output — the
	// chat-direct LLM loop runs as the fallback in every such case.
	EmittedEnvelope bool
}

// attemptReflexDispatch is the dispatch_to_agent call site — the direct
// replacement for the retired attemptBrokerDispatch. Runs once per turn,
// at the same pre-loop position (after ls/classifyAndAttach, before the
// chat-loop entry) the old broker call used.
//
// Pass-through behavior:
//   - reflexEngine, store, or ls nil/unset → zero outcome, no-op.
//   - No dispatch_to_agent reflex fires → zero outcome (Matched=false),
//     the chat-direct LLM loop runs unchanged. No event_log row for a
//     non-firing turn — see design note (1): reflexes are only ever
//     visible when they actually fire, matching every other action
//     kind's behavior (an inject_reminder reflex that doesn't fire
//     doesn't log anything either).
//   - A reflex fires → event_log row written (decision log §14
//     write-site-discipline: real confidence/alternatives-considered/
//     reason, not a bare event name), fired_count bumped, task_execute
//     synthesized, plugin_envelope emitted on success.
func (s *chatServiceImpl) attemptReflexDispatch(
	ctx context.Context,
	sessionID, turnID, userContent, agentID, agentClass string,
	ls *loopState,
	ch chan chat.StreamEvent,
) reflexDispatchOutcome {
	// Retained mutable dispatch rules cannot supply execution authority. Native
	// continuation uses its immutable pin; no fabric issuer is adopted here.
	return reflexDispatchOutcome{}
}
