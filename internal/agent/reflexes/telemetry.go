package reflexes

import (
	"context"
	"log/slog"
	"time"

	shared "github.com/hollis-labs/go-reflexes"
)

// TraceStore is the narrow persistence surface EmitFirings needs: the
// unified event_log write, and the fired_count/last_fired_at bump.
// *store.Store satisfies both methods. Kept as a two-method interface —
// not the full *store.Store — so this package's dependency surface stays
// exactly what telemetry needs, matching the same narrowing pattern
// ActionKindLookup/CooldownFunc already use in resolve.go.
type TraceStore interface {
	LogEvent(ctx context.Context, sessionID, eventType, category, detail, metadata string)
	BumpAgentReflexFired(ctx context.Context, id string, now time.Time) error
}

// FiringContext supplies caller-local identity and additional audit fields.
// ExtraMetadata must not collide with the library trace record's own keys.
type FiringContext struct {
	AgentID       string
	AgentClass    string
	ExtraMetadata map[string]any
}

// EmitFirings delegates trace construction and firing bookkeeping to the
// library. Persistence and plugin observers remain Nanite-owned seams.
func EmitFirings(ctx context.Context, ts TraceStore, hooks PluginHooks, applied AppliedActions, outcomes []CandidateOutcome, state State, fc FiringContext, logger *slog.Logger) {
	considered := make([]shared.CandidateOutcome, len(outcomes))
	for i, oc := range outcomes {
		considered[i] = shared.CandidateOutcome(oc)
	}
	var observers shared.Filters
	if hooks != nil {
		observers = firingObservers{hooks}
	}
	shared.EmitFirings(ctx, ts, observers, libraryApplied(applied), considered, libraryState(state), shared.FiringContext(fc), logger)
}

// Only emission uses this adapter. State and action filtering remain in the
// host Engine so plugin callbacks receive Nanite's own concrete value types.
type firingObservers struct{ hooks PluginHooks }

func (f firingObservers) FilterState(_ context.Context, s shared.State) (shared.State, error) {
	return s, nil
}
func (f firingObservers) FilterAction(_ context.Context, a shared.AppliedAction, _ map[string]any) (shared.AppliedAction, error) {
	return a, nil
}
func (f firingObservers) Fired(sessionID string, data map[string]any) {
	hostEventAction(data)
	f.hooks.EmitReflexFired(sessionID, data)
}
func (f firingObservers) Staged(sessionID string, data map[string]any) {
	hostEventAction(data)
	f.hooks.EmitReflexActionStaged(sessionID, data)
}
func hostEventAction(data map[string]any) {
	if action, ok := data["action"].(shared.AppliedAction); ok {
		data["action"] = AppliedAction(action)
	}
}
