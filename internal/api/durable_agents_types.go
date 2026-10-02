package api

import (
	"time"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// DurableAgentInstanceView preserves the public JSON contract independently of storage.
type DurableAgentInstanceView struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Slug             string     `json:"slug"`
	ProfileID        string     `json:"profile_id"`
	LifecycleClass   string     `json:"lifecycle_class"`
	Provider         string     `json:"provider"`
	Model            string     `json:"model"`
	RuntimeKind      string     `json:"runtime_kind"`
	LaunchSourceType string     `json:"launch_source_type"`
	LaunchSourceID   string     `json:"launch_source_id"`
	WorkRoot         string     `json:"work_root"`
	Status           string     `json:"status"`
	CurrentSessionID string     `json:"current_session_id"`
	URN              string     `json:"urn"`
	FailureReason    string     `json:"failure_reason"`
	MetadataJSON     string     `json:"metadata_json"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	ArchivedAt       *time.Time `json:"archived_at,omitempty"`
}

func durableAgentInstanceToView(r *store.DurableAgentInstance) *DurableAgentInstanceView {
	if r == nil {
		return nil
	}
	return &DurableAgentInstanceView{
		ID:               r.ID,
		Name:             r.Name,
		Slug:             r.Slug,
		ProfileID:        r.ProfileID,
		LifecycleClass:   r.LifecycleClass,
		Provider:         r.Provider,
		Model:            r.Model,
		RuntimeKind:      r.RuntimeKind,
		LaunchSourceType: r.LaunchSourceType,
		LaunchSourceID:   r.LaunchSourceID,
		WorkRoot:         r.WorkRoot,
		Status:           r.Status,
		CurrentSessionID: r.CurrentSessionID,
		URN:              r.URN,
		FailureReason:    r.FailureReason,
		MetadataJSON:     r.MetadataJSON,
		CreatedAt:        r.CreatedAt,
		UpdatedAt:        r.UpdatedAt,
		ArchivedAt:       r.ArchivedAt,
	}
}
func durableAgentInstanceToViews(rows []store.DurableAgentInstance) []DurableAgentInstanceView {
	if rows == nil {
		return nil
	}
	out := make([]DurableAgentInstanceView, len(rows))
	for i := range rows {
		out[i] = *durableAgentInstanceToView(&rows[i])
	}
	return out
}

// DurableAgentInstanceSessionStateView preserves the public JSON contract independently of storage.
type DurableAgentInstanceSessionStateView struct {
	InstanceID           string     `json:"instance_id"`
	SessionID            string     `json:"session_id"`
	Relation             string     `json:"relation"`
	AttachedAt           time.Time  `json:"attached_at"`
	DetachedAt           *time.Time `json:"detached_at,omitempty"`
	SessionStatus        string     `json:"session_status"`
	Provider             string     `json:"provider"`
	Model                string     `json:"model"`
	RuntimeState         string     `json:"runtime_state"`
	RuntimeFailureReason string     `json:"runtime_failure_reason,omitempty"`
	HaltedAt             string     `json:"halted_at,omitempty"`
	HaltedReason         string     `json:"halted_reason,omitempty"`
}

func durableAgentInstanceSessionStateToView(r *store.DurableAgentInstanceSessionState) *DurableAgentInstanceSessionStateView {
	if r == nil {
		return nil
	}
	return &DurableAgentInstanceSessionStateView{
		InstanceID:           r.InstanceID,
		SessionID:            r.SessionID,
		Relation:             r.Relation,
		AttachedAt:           r.AttachedAt,
		DetachedAt:           r.DetachedAt,
		SessionStatus:        r.SessionStatus,
		Provider:             r.Provider,
		Model:                r.Model,
		RuntimeState:         r.RuntimeState,
		RuntimeFailureReason: r.RuntimeFailureReason,
		HaltedAt:             r.HaltedAt,
		HaltedReason:         r.HaltedReason,
	}
}
func durableAgentInstanceSessionStateToViews(rows []store.DurableAgentInstanceSessionState) []DurableAgentInstanceSessionStateView {
	if rows == nil {
		return nil
	}
	out := make([]DurableAgentInstanceSessionStateView, len(rows))
	for i := range rows {
		out[i] = *durableAgentInstanceSessionStateToView(&rows[i])
	}
	return out
}

// DurableAgentEventView preserves the public JSON contract independently of storage.
type DurableAgentEventView struct {
	ID           string    `json:"id"`
	InstanceID   string    `json:"instance_id"`
	EventType    string    `json:"event_type"`
	StatusBefore string    `json:"status_before"`
	StatusAfter  string    `json:"status_after"`
	SessionID    string    `json:"session_id"`
	Source       string    `json:"source"`
	Message      string    `json:"message"`
	MetadataJSON string    `json:"metadata_json"`
	CreatedAt    time.Time `json:"created_at"`
}

func durableAgentEventToView(r *store.DurableAgentEvent) *DurableAgentEventView {
	if r == nil {
		return nil
	}
	return &DurableAgentEventView{
		ID:           r.ID,
		InstanceID:   r.InstanceID,
		EventType:    r.EventType,
		StatusBefore: r.StatusBefore,
		StatusAfter:  r.StatusAfter,
		SessionID:    r.SessionID,
		Source:       r.Source,
		Message:      r.Message,
		MetadataJSON: r.MetadataJSON,
		CreatedAt:    r.CreatedAt,
	}
}
func durableAgentEventToViews(rows []store.DurableAgentEvent) []DurableAgentEventView {
	if rows == nil {
		return nil
	}
	out := make([]DurableAgentEventView, len(rows))
	for i := range rows {
		out[i] = *durableAgentEventToView(&rows[i])
	}
	return out
}

type DurableAgentLaunchResultView struct {
	Instance       *DurableAgentInstanceView        `json:"instance"`
	Policy         service.DurableAgentLaunchPolicy `json:"policy"`
	Session        *SessionView                     `json:"session,omitempty"`
	CreatedSession bool                             `json:"created_session"`
	ReusedSession  bool                             `json:"reused_session"`
}

func durableAgentLaunchResultToView(r *service.DurableAgentLaunchResult) *DurableAgentLaunchResultView {
	if r == nil {
		return nil
	}
	return &DurableAgentLaunchResultView{
		Instance:       durableAgentInstanceToView(r.Instance),
		Policy:         r.Policy,
		Session:        sessionToViewPtr(r.Session),
		CreatedSession: r.CreatedSession,
		ReusedSession:  r.ReusedSession,
	}
}

type DurableAgentRecipePlanView struct {
	RecipeID            string                           `json:"recipe_id"`
	RecipeSchemaVersion int                              `json:"recipe_schema_version"`
	Instance            DurableAgentInstanceView         `json:"instance"`
	LaunchPolicy        service.DurableAgentLaunchPolicy `json:"launch_policy"`
	WakePayload         service.DurableAgentWakePayload  `json:"wake_payload"`
	SessionPolicy       string                           `json:"session_policy"`
	WouldCreateSession  bool                             `json:"would_create_session"`
	WouldReuseSession   bool                             `json:"would_reuse_session"`
	MissingRequirements []string                         `json:"missing_requirements,omitempty"`
	Unsupported         []string                         `json:"unsupported,omitempty"`
	Injections          []service.RecipeInjectionPlan    `json:"injections,omitempty"`
	Ready               bool                             `json:"ready"`
}

func durableAgentRecipePlanToView(r *service.DurableAgentRecipePlan) *DurableAgentRecipePlanView {
	if r == nil {
		return nil
	}
	return &DurableAgentRecipePlanView{
		RecipeID:            r.RecipeID,
		RecipeSchemaVersion: r.RecipeSchemaVersion,
		Instance:            *durableAgentInstanceToView(&r.Instance),
		LaunchPolicy:        r.LaunchPolicy,
		WakePayload:         r.WakePayload,
		SessionPolicy:       r.SessionPolicy,
		WouldCreateSession:  r.WouldCreateSession,
		WouldReuseSession:   r.WouldReuseSession,
		MissingRequirements: r.MissingRequirements,
		Unsupported:         r.Unsupported,
		Injections:          r.Injections,
		Ready:               r.Ready,
	}
}

type DurableAgentRecipeApplyResultView struct {
	Plan         DurableAgentRecipePlanView    `json:"plan"`
	Instance     *DurableAgentInstanceView     `json:"instance"`
	LaunchResult *DurableAgentLaunchResultView `json:"launch_result,omitempty"`
}

func durableAgentRecipeApplyResultToView(r *service.DurableAgentRecipeApplyResult) *DurableAgentRecipeApplyResultView {
	if r == nil {
		return nil
	}
	return &DurableAgentRecipeApplyResultView{
		Plan:         *durableAgentRecipePlanToView(&r.Plan),
		Instance:     durableAgentInstanceToView(r.Instance),
		LaunchResult: durableAgentLaunchResultToView(r.LaunchResult),
	}
}
