package reflexes

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

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

// Evaluate refuses retired mutable behavior before collection, plugin filters,
// host effects or telemetry. Authored pinned policies use the native handler.
func (e *Engine) Evaluate(ctx context.Context, sessionID, agentID, agentClass string) (AppliedActions, error) {
	return AppliedActions{}, store.ErrImmutableAgentProfile
}

func (e *Engine) EvaluateState(ctx context.Context, agentID, agentClass string, state State) (AppliedActions, error) {
	return AppliedActions{}, store.ErrImmutableAgentProfile
}
