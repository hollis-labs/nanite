// Package contextbroker — assembly decider.
//
// SP-20260512-0008 W1A (CW-20260512-0104): the Context Broker's primary
// role is to DECIDE which slots ship this turn, not to fetch content.
// External content retrieval (Vanta memory, Conduit, PCC) remains in the
// `*Broker.Fetch` path; that's a sub-step the broker may use to populate
// memory/context slots. The assembly decider takes the full slot store
// (already produced by chat.AssembleSlotSources) plus an intent + mode
// signal and returns the ordered (slot, content) pairs to ship, with a
// stash of unsent slots and substitution markers for oversized content.
//
// Architectural intent (per design artifact at
// agent-workspaces/exploration/nanite/2026-05-12-opencode-architecture-comparison/opencode-architectural-deltas.md):
// keep a stable cacheable prefix across turns. Slot positions are fixed
// (universal=0, system=1, agent=2, ...); the *content* of a slot may
// change (or be empty) per turn, but the position is load-bearing for
// Anthropic's cacheable_prefix_tokens math. The decider's job is to
// shape the per-turn payload without disturbing those positions —
// skipping a slot means emitting empty content at that position, which
// the downstream ContextWindow.Assemble step then drops from the wire
// (provider-adapter handles the gap as if the slot were absent — the
// adapter does not embed position indexes).
//
// Pre-launch contract per `feedback_no_compat_shims`: when the broker
// decides to skip a slot, the slot is simply not populated. No alias,
// no deprecation marker, no "we used to send this" comment.
package contextbroker

import (
	"fmt"
	"sort"
	"strings"
)

// SlotAction is the per-slot decision made by the assembly decider.
type SlotAction int

const (
	// ActionShip — include the slot's content as-is in this turn's payload.
	ActionShip SlotAction = iota

	// ActionSkip — omit the slot from this turn's payload. The slot's
	// content (if any) is moved to the stash so a future turn can recall
	// it without re-running the producer. Skip is the broker's way of
	// saying "this content exists but isn't needed for THIS turn's intent".
	ActionSkip

	// ActionPointer — replace the slot's content with a pointer/ref. Used
	// when the content exceeds the per-slot budget and full inlining
	// would blow the cacheable prefix. The pointer text follows the
	// `<ref:artifact_id, N tokens, available via dev_read>` convention
	// from the ticket. Sprint 2 / T2.6 wires the actual artifact store;
	// for now the pointer is a synthetic marker the agent can act on.
	ActionPointer
)

// String renders SlotAction for telemetry and tests.
func (a SlotAction) String() string {
	switch a {
	case ActionShip:
		return "ship"
	case ActionSkip:
		return "skip"
	case ActionPointer:
		return "pointer"
	default:
		return "unknown"
	}
}

// SlotDecision is the broker's per-slot output. For ActionShip the Content
// is the slot's effective payload. For ActionPointer it's the synthetic
// pointer marker. For ActionSkip Content is empty and the original payload
// (if any) lives in the AssemblyPlan.Stash keyed by SlotName.
type SlotDecision struct {
	SlotName string
	Content  string
	Action   SlotAction
	// ReasonTag is a short stable tag for telemetry/tests describing why
	// this decision was made (e.g. "needed", "skipped_no_intent_match",
	// "skipped_no_content", "pointer_oversized").
	ReasonTag string
}

// AssemblyPlan is the broker's complete per-turn plan: the ordered slot
// decisions and a stash of unshipped content keyed by slot name.
type AssemblyPlan struct {
	// Decisions are emitted one-per-SlotOrder entry — positional stability
	// is the contract. The decider walks AssemblyInput.SlotOrder and emits
	// a decision at each position regardless of whether AssemblyInput.Sources
	// has an entry for that slot; slots absent from Sources (or with empty
	// content) receive ActionSkip with ReasonTag="skipped_no_content".
	// Callers can rely on len(Decisions) == len(SlotOrder) and on
	// Decisions[i].SlotName == SlotOrder[i] across turns, which preserves
	// the cacheable-prefix position math (Anthropic cacheable_prefix_tokens)
	// at the decider layer. Downstream Assemble drops empty-content slots
	// from the wire, but position semantics are preserved here.
	Decisions []SlotDecision

	// Stash maps slot_name → original-content for slots the broker
	// decided to skip. Future turns or recovery flows can consult the
	// stash to re-materialize a slot without re-running the producer.
	Stash map[string]string
}

// ContentByAction returns the (name, content) pairs for slots with the
// given action, preserving the plan's slot order. Helper for downstream
// consumers that want only-shipped or only-pointer slices.
func (p *AssemblyPlan) ContentByAction(action SlotAction) []SlotDecision {
	out := make([]SlotDecision, 0, len(p.Decisions))
	for _, d := range p.Decisions {
		if d.Action == action {
			out = append(out, d)
		}
	}
	return out
}

// AssemblyInput is the decider's input — the full slot store plus signals
// for the per-turn decision (intent, mode, agent identity). Sources is a
// slot_name → content map; the decider walks `SlotOrder` (passed
// explicitly so this package doesn't import internal/context — keeping
// internal/context as the substrate and contextbroker as a downstream
// consumer) and emits a decision for every slot in the order, regardless
// of whether the source has content for that slot.
type AssemblyInput struct {
	// Intent classifies the user's turn (write_code, resume_task, etc.).
	// Drives which slots are needed: e.g. resume_task needs memory but
	// not necessarily the workspace context slot.
	Intent Intent

	// ModeSlug is the session-level mode (chat/plan/work) — Sprint 2 /
	// T2.5 codifies the canonical list. Used today only as a passthrough
	// signal; future deciders may gate slots on mode.
	ModeSlug string

	// SlotOrder is the canonical order to walk. Provided by the caller so
	// this package doesn't import internal/context — internal/context is
	// the substrate that defines SlotOrder/slot names/budgets, and
	// contextbroker is its consumer. The caller passes `ctxpkg.SlotOrder`.
	SlotOrder []string

	// Sources maps slot_name → content. Empty content is allowed and
	// emitted as ActionSkip+ReasonTag="skipped_no_content". The caller
	// (chat.ContextClient.AssembleSlotSources) is responsible for
	// producing every slot's raw content; the decider only chooses
	// which ones to ship.
	Sources map[string]string

	// Budgets maps slot_name → max-tokens ceiling. When a slot's content
	// exceeds its budget, the decider substitutes a pointer marker
	// (ActionPointer). Pass `ctxpkg.DefaultBudgets()` for production.
	// Zero or missing budget means "no ceiling" for that slot.
	Budgets map[string]int

	// AgentID is the requesting agent; available to deciders that may
	// gate slots on agent identity (e.g. subagent profiles may want
	// thinner UserContext). Today's decider doesn't branch on it but
	// the field is reserved.
	AgentID string

	// SessionID is the requesting session, surfaced for telemetry.
	SessionID string
}

// DecideAssembly runs the assembly-decider for one turn. Returns an
// AssemblyPlan ordered by SlotOrder with a stash of unshipped slot
// content. The decider is deterministic — no I/O, no source fetches.
// External content retrieval already happened upstream (in
// AssembleSlotSources via *Broker.Fetch).
//
// The default decision rule is simple by design:
//
//  1. Empty content → ActionSkip (ReasonTag=skipped_no_content). No
//     stash entry — there's nothing to stash.
//  2. Content over the per-slot budget → ActionPointer with a synthetic
//     `<ref:slot=<name>, N tokens, source=context-broker>` marker. The
//     original content lands in Stash[name].
//  3. Context-typed slots (SlotContext) when the intent doesn't need
//     workspace context → ActionSkip (ReasonTag=skipped_no_intent_match).
//     The original content lands in Stash[name].
//  4. Otherwise → ActionShip.
//
// Universal slot (position 0) is always emitted at position 0, even when
// empty — preserves the cacheable prefix across turns. Sprint 2 / T2.4
// will wire actual content; until then the broker emits an ActionSkip
// with empty content, which Assemble drops from the wire (position
// stability lives in SlotOrder, not in wire output).
func DecideAssembly(input AssemblyInput) AssemblyPlan {
	decisions := make([]SlotDecision, 0, len(input.SlotOrder))
	var stash map[string]string

	for _, name := range input.SlotOrder {
		content := input.Sources[name]
		budget := input.Budgets[name]

		if content == "" {
			decisions = append(decisions, SlotDecision{
				SlotName:  name,
				Content:   "",
				Action:    ActionSkip,
				ReasonTag: "skipped_no_content",
			})
			continue
		}

		// Compute token estimate once — EstimateTokens is O(len(content)),
		// so on large slot bodies we avoid doubling the work between the
		// oversized check and the pointer-format call.
		tokens := EstimateTokens(content)

		switch {
		case budget > 0 && tokens > budget:
			// Oversized — substitute pointer and stash original.
			pointer := formatSlotPointer(name, tokens)
			if stash == nil {
				stash = make(map[string]string)
			}
			stash[name] = content
			decisions = append(decisions, SlotDecision{
				SlotName:  name,
				Content:   pointer,
				Action:    ActionPointer,
				ReasonTag: "pointer_oversized",
			})

		case shouldSkipForIntent(name, input.Intent):
			if stash == nil {
				stash = make(map[string]string)
			}
			stash[name] = content
			decisions = append(decisions, SlotDecision{
				SlotName:  name,
				Content:   "",
				Action:    ActionSkip,
				ReasonTag: "skipped_no_intent_match",
			})

		default:
			decisions = append(decisions, SlotDecision{
				SlotName:  name,
				Content:   content,
				Action:    ActionShip,
				ReasonTag: "needed",
			})
		}
	}

	return AssemblyPlan{Decisions: decisions, Stash: stash}
}

// formatSlotPointer renders the pointer marker the broker substitutes
// for an oversized slot. The convention follows the ticket spec:
//
//	<ref:slot=<name>, N tokens, source=context-broker>
//
// Sprint 2 / T2.6 (artifact store) will replace the synthetic marker
// with a real `<ref:artifact_id, N tokens, available via dev_read>`
// pointer that an agent can act on with the dev_read tool. For now the
// marker is informational — the agent sees that content was elided and
// can ask for it explicitly.
func formatSlotPointer(slotName string, tokens int) string {
	return fmt.Sprintf("<ref:slot=%s, %d tokens, source=context-broker>", slotName, tokens)
}

// shouldSkipForIntent encodes the per-intent slot need matrix. This is
// the broker's selection logic. Pre-launch: conservative — only skip the
// SlotContext (workspace context) when the intent doesn't need it.
// Other slots ship per their content presence. Future deciders can add
// finer-grained rules per slot/intent without changing the signature.
//
// SP-20260512-0008 W1A note: the matrix is intentionally narrow. The
// ticket's acceptance test is "a turn that doesn't need workspace
// context doesn't ship the workspace slot". SlotContext is the workspace
// context slot (per the slot.go doc comment: `SlotContext = "context"
// // dynamic enrichment (plugins, context broker)`). Other slots may
// gain skip rules in Sprint 3 once we have the cross-sprint coordination
// signal from the runner (T3.x).
func shouldSkipForIntent(slotName string, intent Intent) bool {
	if slotName != contextSlotName() {
		return false
	}
	// Workspace context is needed for code/debug/plan/boot-project work.
	// For review/recall/resume turns we don't need it — those intents
	// pull their grounding from the session/memory slots.
	switch intent.Type {
	case IntentReviewSession, IntentRecallDecision, IntentResumeTask:
		return true
	default:
		return false
	}
}

// contextSlotName returns the canonical slot name for the dynamic
// workspace-context slot. Defined here as a constant fn rather than
// imported from internal/context to keep the dependency direction
// one-way: internal/context is the substrate (defines SlotOrder, slot
// names, budgets); contextbroker is a consumer that the substrate
// doesn't know about. Importing internal/context here would invert the
// intended layering even though no cycle exists today. If the slot
// name string drifts between packages, the assembly_test.go round-trip
// catches it (it walks ctxpkg.SlotOrder through DecideAssembly).
func contextSlotName() string {
	return "context"
}

// DecisionSummary renders a compact one-line summary of an AssemblyPlan
// for slog telemetry. Format:
//
//	"ship=universal,system,agent,rules,session skip=context,memory pointer=tools"
//
// Stable for grepping; not parsed by any consumer.
func DecisionSummary(plan AssemblyPlan) string {
	if len(plan.Decisions) == 0 {
		return "(empty plan)"
	}
	groups := map[SlotAction][]string{}
	for _, d := range plan.Decisions {
		groups[d.Action] = append(groups[d.Action], d.SlotName)
	}
	for _, names := range groups {
		sort.Strings(names)
	}
	var parts []string
	if ship, ok := groups[ActionShip]; ok && len(ship) > 0 {
		parts = append(parts, "ship="+strings.Join(ship, ","))
	}
	if skip, ok := groups[ActionSkip]; ok && len(skip) > 0 {
		parts = append(parts, "skip="+strings.Join(skip, ","))
	}
	if ptr, ok := groups[ActionPointer]; ok && len(ptr) > 0 {
		parts = append(parts, "pointer="+strings.Join(ptr, ","))
	}
	return strings.Join(parts, " ")
}
