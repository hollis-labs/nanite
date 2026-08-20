package reflexes

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	pluginpkg "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/store"
)

type PluginHooks interface {
	ApplyFilter(name string, data interface{}, ctx pluginpkg.FilterContext) (interface{}, error)
	EmitReflexFired(sessionID string, data map[string]any)
	EmitReflexActionStaged(sessionID string, data map[string]any)
}

// Engine ties StateCollector + Evaluator + Executor together. Used by
// the monitor loop and the operator UI.
type Engine struct {
	Store     *store.Store
	Collector *StateCollector
	Executor  *Executor
	Logger    *slog.Logger
	Plugins   PluginHooks

	// actionKindsMu guards actionKinds. reflex_action_kinds changes rarely
	// (it has no CRUD surface as of TASKS/reflex-taxonomy/
	// 01-taxonomy-schema-foundation.md — seed data only), so it is loaded
	// once at construction and re-read from an in-memory map on every
	// EvaluateState call rather than queried per-reflex-per-turn. See
	// docs/engineering/architecture/00-overview.md's hot-reload guidance:
	// worth doing for things that change with meaningful frequency, fine
	// to skip for things that rarely do.
	actionKindsMu sync.RWMutex
	actionKinds   map[string]*store.ReflexActionKind
}

// NewEngine builds a default-wired Engine. Hooks on the Executor stay
// nil until the caller (typically service.NewContainer) sets them. The
// reflex_action_kinds cache (used to resolve the Facet 4 recurrence
// cascade's kind-level tier, see recurrence.go) is loaded eagerly here on
// a best-effort basis; a load failure (e.g. a pre-migration-124 database
// in a test fixture) leaves the cache empty rather than failing
// construction — EffectiveCooldown treats an absent kind-level override
// the same as an unset one, so an empty cache degrades to "every kind
// uses the system default," not a crash.
func NewEngine(st *store.Store, logger *slog.Logger) *Engine {
	if logger == nil {
		logger = slog.Default()
	}
	e := &Engine{
		Store:     st,
		Collector: &StateCollector{Store: st, Window: 5},
		Executor:  &Executor{Logger: logger},
		Logger:    logger,
	}
	if st != nil {
		if err := e.RefreshActionKindCache(context.Background()); err != nil {
			logger.Warn("reflex engine: initial action-kind cache load failed", "err", err)
		}
	}
	return e
}

// RefreshActionKindCache reloads the in-memory reflex_action_kinds cache
// from the store. Called once at construction (NewEngine); exposed as a
// manual hook for the rare case an operator edits reflex_action_kinds
// directly (no CRUD API exists for that table as of this task) and wants
// a running Engine to pick up the change without a restart.
func (e *Engine) RefreshActionKindCache(ctx context.Context) error {
	if e.Store == nil {
		return fmt.Errorf("reflex engine: RefreshActionKindCache: Store is nil")
	}
	kinds, err := e.Store.ListReflexActionKinds(ctx)
	if err != nil {
		return fmt.Errorf("refresh reflex action kind cache: %w", err)
	}
	m := make(map[string]*store.ReflexActionKind, len(kinds))
	for i := range kinds {
		k := kinds[i]
		m[k.Name] = &k
	}
	e.actionKindsMu.Lock()
	e.actionKinds = m
	e.actionKindsMu.Unlock()
	return nil
}

// ActionKindDefaultRecurrenceSeconds returns the cached
// reflex_action_kinds.default_recurrence_seconds for actionKind (one of
// the store.ReflexAction* constants), or nil if the kind is unknown or
// the cache hasn't been populated. Exported so the two dispatch_to_agent
// call sites that cannot go through EvaluateState — attemptReflexDispatch
// (internal/service/chat_reflex_dispatch.go) and
// matchDispatchToAgentReflex (internal/mcp/self_tools_dispatch.go) — can
// resolve the same kind-level cascade tier EvaluateState uses, via the
// same cache, rather than issuing their own per-turn lookup.
func (e *Engine) ActionKindDefaultRecurrenceSeconds(actionKind string) *int64 {
	e.actionKindsMu.RLock()
	defer e.actionKindsMu.RUnlock()
	if k, ok := e.actionKinds[actionKind]; ok {
		return k.DefaultRecurrenceSeconds
	}
	return nil
}

func (e *Engine) SetPluginHooks(h PluginHooks) {
	e.Plugins = h
}

// Evaluate is the per-tick entry point. It:
//  1. Collects fresh State for (sessionID, agentID, agentClass).
//  2. Lists active reflexes (class-base + agent-specific) and runs
//     each trigger predicate.
//  3. For every reflex whose trigger fires, invokes the Executor to
//     stage the action.
//  4. Bumps the reflex's fired_count + last_fired_at.
//
// Returns the AppliedActions slice so the caller (monitor loop) can
// translate inject_reminder bodies into <system-reminder> blocks on
// the next composed turn, and so the event-log layer can record what
// fired.
func (e *Engine) Evaluate(ctx context.Context, sessionID, agentID, agentClass string) (AppliedActions, error) {
	if e == nil {
		return AppliedActions{}, fmt.Errorf("Engine is nil")
	}

	state, err := e.Collector.Collect(ctx, sessionID, agentID, agentClass)
	if err != nil {
		return AppliedActions{}, fmt.Errorf("collect state: %w", err)
	}

	return e.EvaluateState(ctx, agentID, agentClass, state)
}

// EvaluateState runs the engine against a pre-built State. Exposed so
// tests can feed synthetic fixtures and the monitor loop can layer in
// MailUnreadCount / TickN / Events before the run.
func (e *Engine) EvaluateState(ctx context.Context, agentID, agentClass string, state State) (AppliedActions, error) {
	if e.Plugins != nil {
		filtered, err := e.Plugins.ApplyFilter(pluginpkg.FilterReflexState, state, pluginpkg.FilterContext{
			SessionID: state.SessionID,
			AgentID:   agentID,
			Metadata: map[string]interface{}{
				"agent_class": agentClass,
			},
		})
		if err != nil {
			e.Logger.Warn("reflex state filter failed", "err", err)
		} else if filteredState, ok := filtered.(State); ok {
			state = filteredState
		}
	}

	reflexRows, err := e.Store.ListAgentReflexesForAgent(ctx, agentID, agentClass)
	if err != nil {
		return AppliedActions{}, fmt.Errorf("list reflexes: %w", err)
	}

	// Phase 4 item 09
	// (TASKS/phase-4/09-fix-dispatch-to-agent-generic-pass-leak.md):
	// dispatch_to_agent rows are architecturally evaluated exclusively
	// through the dedicated attemptReflexDispatch
	// (internal/service/chat_reflex_dispatch.go) and
	// matchDispatchToAgentReflex (internal/mcp/self_tools_dispatch.go)
	// call sites — both call Store.ListAgentReflexesForAgent and
	// Resolve()/Executor.Apply directly, never through this generic
	// per-turn pass. Executor.Apply's dispatch_to_agent case is an
	// explicit, documented no-op (see executor.go) because a real dispatch
	// needs a stream channel + ToolService this package deliberately
	// doesn't depend on. Without this skip, that no-op still returns
	// (applied, nil), and Resolve() would treat it as a real fire: bump
	// fired_count, write a redundant event_log row (via
	// evaluateAndInjectReflexes), and emit
	// EmitReflexFired/EmitReflexActionStaged with no dispatch having
	// actually occurred — inflating telemetry and giving plugins a false
	// "this routing reflex fired" signal. Filter BEFORE calling Resolve —
	// a dispatch_to_agent row should be entirely invisible to this pass,
	// not just short-circuited after being evaluated. This is a property
	// of what THIS caller passes to Resolve(), not something Resolve()
	// itself needs to know about (TASKS/reflex-taxonomy/
	// 03-shared-decision-engine.md's own instruction). This does not
	// affect the query itself — Store.ListAgentReflexesForAgent still
	// returns dispatch_to_agent rows; the dedicated call sites above share
	// that same query and need them.
	candidates := make([]store.AgentReflex, 0, len(reflexRows))
	for _, r := range reflexRows {
		if r.ActionKind == store.ReflexActionDispatchToAgent {
			continue
		}
		candidates = append(candidates, r)
	}

	now := time.Now()
	cooldownFn := func(r store.AgentReflex) bool {
		cooldown := EffectiveCooldown(e.ActionKindDefaultRecurrenceSeconds(r.ActionKind), r.RecurrenceOverrideSeconds)
		return RecentlyFired(r, now, cooldown)
	}
	// TASKS/reflex-taxonomy/08-fix-resolve-fail-open-visibility.md: this
	// closure's own "action kind %q not cached" error used to be built
	// here and then silently discarded by Resolve()'s (formerly bare)
	// err==nil check — zero log line anywhere in the chain. Resolve()
	// itself now Warn-logs any kindLookup error it receives (including
	// this one) before falling open to all_applicable, so the error
	// constructed below is no longer lost; nothing in this closure needed
	// to change beyond this note.
	kindLookup := func(_ context.Context, kind string) (*store.ReflexActionKind, error) {
		e.actionKindsMu.RLock()
		defer e.actionKindsMu.RUnlock()
		if k, ok := e.actionKinds[kind]; ok {
			return k, nil
		}
		return nil, fmt.Errorf("action kind %q not cached", kind)
	}

	resolved, outcomes, resolveErr := Resolve(ctx, candidates, state, e.Executor, cooldownFn, kindLookup)
	if resolveErr != nil {
		return AppliedActions{}, fmt.Errorf("resolve reflexes: %w", resolveErr)
	}
	for _, oc := range outcomes {
		if oc.TriggerError != "" {
			e.Logger.Warn("reflex trigger evaluate failed",
				"session_id", state.SessionID,
				"reflex", oc.ReflexName,
				"err", oc.TriggerError,
			)
		}
		if oc.ApplyError != "" {
			e.Logger.Warn("reflex apply failed",
				"reflex", oc.ReflexName, "err", oc.ApplyError)
		}
	}

	out := AppliedActions{
		Actions:       make([]AppliedAction, 0, len(resolved.Actions)),
		FiredReflexes: make([]store.AgentReflex, 0, len(resolved.FiredReflexes)),
	}
	for i, action := range resolved.Actions {
		r := resolved.FiredReflexes[i]
		if e.Plugins != nil {
			filtered, err := e.Plugins.ApplyFilter(pluginpkg.FilterReflexAction, action, pluginpkg.FilterContext{
				SessionID: state.SessionID,
				AgentID:   agentID,
				Metadata: map[string]interface{}{
					"agent_class": agentClass,
					"reflex_id":   r.ID,
					"reflex_name": r.Name,
				},
			})
			if err != nil {
				e.Logger.Warn("reflex action filter failed", "reflex", r.Name, "err", err)
			} else if filteredAction, ok := filtered.(AppliedAction); ok {
				action = filteredAction
			}
		}
		out.Actions = append(out.Actions, action)
		out.FiredReflexes = append(out.FiredReflexes, r)
	}

	// TASKS/reflex-taxonomy/06-unified-reflex-telemetry.md: the event_log
	// trace write (formerly one layer up, in chat_reflexes.go's
	// evaluateAndInjectReflexes), the fired_count/last_fired_at bump, and
	// the EmitReflexFired/EmitReflexActionStaged plugin hooks (formerly
	// inline in the loop above) are now one centralized call — the same
	// EmitFirings attemptReflexDispatch and matchDispatchToAgentReflex go
	// through — operating on `out` (already FilterReflexAction-filtered
	// above, so hook/trace payloads see the same post-filter action data
	// they did before this change) and `outcomes` (Resolve()'s own
	// per-candidate detail, for AlternativesConsidered/Category).
	EmitFirings(ctx, e.Store, e.Plugins, out, outcomes, state, FiringContext{
		AgentID:    agentID,
		AgentClass: agentClass,
	}, e.Logger)

	return out, nil
}

func reflexEventData(agentID, agentClass string, reflex store.AgentReflex, action AppliedAction, state State) map[string]any {
	return map[string]any{
		"agent_id":          agentID,
		"agent_class":       agentClass,
		"reflex_id":         reflex.ID,
		"reflex_name":       reflex.Name,
		"trigger_kind":      reflex.TriggerKind,
		"action_kind":       reflex.ActionKind,
		"priority":          reflex.Priority,
		"action":            action,
		"messages":          len(state.Messages),
		"user_messages":     len(state.UserMessages),
		"events":            len(state.Events),
		"mail_unread_count": state.MailUnreadCount,
		"tick_n":            state.TickN,
		"prefix_tokens":     state.PrefixTokens,
	}
}

// RecentlyFired's implementation lives in recurrence.go, alongside
// EffectiveCooldown and DefaultReflexCooldown (TASKS/reflex-taxonomy/
// 02-recurrence-cascade.md) — kept in this file's neighborhood
// conceptually (it started life here, unexported, as engine.go's own
// 15-minute debounce helper) but the exported version is now shared by
// the two dispatch_to_agent call sites too, so it moved to its own file
// rather than staying engine.go-private.
