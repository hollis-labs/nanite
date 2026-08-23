package selftools

import (
	"context"

	"github.com/hollis-labs/nanite/internal/store"
)

// WorkBroadcaster notifies connected UI clients that session-scoped todos or
// plans changed so the Work drawer can refresh.
type WorkBroadcaster interface {
	BroadcastWorkChanged()
}

// WorkTrackingStore is the exact todo/plan persistence surface used by the
// work-tracking self-tools. Scope updates and todo deletion are intentionally
// absent because no approved handler uses them.
type WorkTrackingStore interface {
	CreateTodo(ctx context.Context, todo *store.Todo) error
	GetTodo(ctx context.Context, id string) (*store.Todo, error)
	ListTodos(ctx context.Context, filter store.TodoFilter) ([]store.Todo, error)
	UpdateTodo(ctx context.Context, todo *store.Todo) error
	CreatePlan(ctx context.Context, plan *store.Plan) error
	GetPlan(ctx context.Context, id string) (*store.Plan, error)
	ListPlans(ctx context.Context, filter store.PlanFilter) ([]store.Plan, error)
	UpdatePlan(ctx context.Context, plan *store.Plan) error
	UpdatePlanStep(ctx context.Context, planID, stepID string, updates store.PlanStep) error
	AppendPlanSteps(ctx context.Context, planID string, steps []store.PlanStep) ([]store.PlanStep, error)
	DeletePlan(ctx context.Context, id string) error
}

// SessionProjectLookup is the narrow seam required to derive a project from
// authoritative session state.
type SessionProjectLookup interface {
	GetSession(ctx context.Context, id string) (*store.Session, error)
}

// WorkTrackingTools owns todo/plan persistence and the post-mutation refresh
// invariant. Project lookup remains a shared package policy because reminder
// and pin tools use the same autofill rule.
type WorkTrackingTools struct {
	Store       WorkTrackingStore
	Projects    SessionProjectLookup
	Broadcaster WorkBroadcaster
}

func NewWorkTrackingTools(store WorkTrackingStore, projects SessionProjectLookup, broadcaster WorkBroadcaster) *WorkTrackingTools {
	return &WorkTrackingTools{Store: store, Projects: projects, Broadcaster: broadcaster}
}

func (wt *WorkTrackingTools) notifyWorkChanged() {
	if wt != nil && wt.Broadcaster != nil {
		wt.Broadcaster.BroadcastWorkChanged()
	}
}

// resolveProjectIDFromSession is the one project-autofill policy shared by
// work tracking and the still transport-owned reminder/pin handlers.
func resolveProjectIDFromSession(lookup SessionProjectLookup, sessionID string) string {
	if sessionID == "" || lookup == nil {
		return ""
	}
	sess, err := lookup.GetSession(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, sessionID)
	if err != nil || sess == nil {
		return ""
	}
	return sess.ProjectID
}
