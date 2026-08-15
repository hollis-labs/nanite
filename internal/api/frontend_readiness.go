package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/hollis-labs/agentkit/agentruntime/runtimekind"
	"github.com/hollis-labs/nanite/internal/bootprofile"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

type startSurfaceCapabilities struct {
	SchemaVersion       int                          `json:"schema_version"`
	LifecycleClasses    []enumOption                 `json:"lifecycle_classes"`
	DurableStatuses     []enumOption                 `json:"durable_statuses"`
	LaunchSources       []enumOption                 `json:"launch_sources"`
	AttachmentRelations []enumOption                 `json:"attachment_relations"`
	RuntimeKinds        []runtimeKindOption          `json:"runtime_kinds"`
	RecipeKinds         []enumOption                 `json:"recipe_kinds"`
	WakeReasons         []enumOption                 `json:"wake_reasons"`
	SessionPolicies     []enumOption                 `json:"session_policies"`
	Recipes             []service.DurableAgentRecipe `json:"recipes"`
	DurableAgents       []store.DurableAgentInstance `json:"durable_agents"`
	Profiles            []store.AgentProfile         `json:"profiles"`
	Providers           []store.ProviderConfig       `json:"providers"`
	Models              []store.Model                `json:"models"`
	BootProfiles        []bootProfileOption          `json:"boot_profiles"`
	WorkRootHints       []workRootHint               `json:"work_root_hints"`
}

type enumOption struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Legacy      bool   `json:"legacy,omitempty"`
}

type runtimeKindOption struct {
	Value             string `json:"value"`
	Label             string `json:"label"`
	ManagedAutomation bool   `json:"managed_automation"`
	ProductSupported  bool   `json:"product_supported"`
	Description       string `json:"description,omitempty"`
}

type bootProfileOption struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Provider string `json:"provider"`
	WorkRoot string `json:"work_root,omitempty"`
}

type workRootHint struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Path        string `json:"path"`
	Description string `json:"description,omitempty"`
}

type sessionDetailsResponse struct {
	Session              *store.Session                           `json:"session"`
	Mode                 *store.Mode                              `json:"mode,omitempty"`
	PrimaryAgent         *store.AgentProfile                      `json:"primary_agent,omitempty"`
	DurableAttachments   []store.DurableAgentInstanceSessionState `json:"durable_attachments"`
	CurrentDurableAgent  *store.DurableAgentInstance              `json:"current_durable_agent,omitempty"`
	ActivityState        string                                   `json:"activity_state"`
	LastActivityAt       string                                   `json:"last_activity_at,omitempty"`
	LastUsefulActivityAt string                                   `json:"last_useful_activity_at,omitempty"`
	Halt                 sessionHaltDetail                        `json:"halt"`
	Usage                *store.SessionUsageSummary               `json:"usage,omitempty"`
	RecentDurableEvents  []store.DurableAgentEvent                `json:"recent_durable_events"`
	Runtime              sessionRuntimeDetail                     `json:"runtime"`
	BootSource           string                                   `json:"boot_source"`
	ImmutableStartFields []string                                 `json:"immutable_start_fields"`
	Checkpoint           checkpointDetail                         `json:"checkpoint"`
}

type sessionRuntimeDetail struct {
	State             string `json:"state"`
	RuntimeID         string `json:"runtime_id,omitempty"`
	RuntimeKind       string `json:"runtime_kind,omitempty"`
	Provider          string `json:"provider,omitempty"`
	Mode              string `json:"mode,omitempty"`
	PID               int    `json:"pid,omitempty"`
	BootDir           string `json:"boot_dir,omitempty"`
	WorkspaceDir      string `json:"workspace_dir,omitempty"`
	ProviderSessionID string `json:"provider_session_id,omitempty"`
	FailureReason     string `json:"failure_reason,omitempty"`
	StartedAt         string `json:"started_at,omitempty"`
	UpdatedAt         string `json:"updated_at,omitempty"`
}

type sessionHaltDetail struct {
	IsHalted     bool   `json:"is_halted"`
	HaltedAt     string `json:"halted_at,omitempty"`
	HaltedReason string `json:"halted_reason,omitempty"`
}

type checkpointDetail struct {
	Status string `json:"status"`
}

func (a *API) handleStartSurfaceCapabilities(w http.ResponseWriter, r *http.Request) {
	recipes, err := a.Services.DurableAgentRecipes.List(r.Context())
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	durableAgents, err := a.Services.DurableAgents.List(r.Context(), false)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	profiles, err := a.Services.Store.ListAgents()
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	providers, err := a.providersForStartSurface()
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	models, err := a.modelsForStartSurface()
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, startSurfaceCapabilities{
		SchemaVersion:       1,
		LifecycleClasses:    lifecycleClassOptions(),
		DurableStatuses:     durableStatusOptions(),
		LaunchSources:       launchSourceOptions(),
		AttachmentRelations: attachmentRelationOptions(),
		RuntimeKinds:        runtimeKindOptions(),
		RecipeKinds:         recipeKindOptions(),
		WakeReasons:         wakeReasonOptions(),
		SessionPolicies:     sessionPolicyOptions(),
		Recipes:             nonNilSlice(recipes),
		DurableAgents:       nonNilSlice(durableAgents),
		Profiles:            nonNilSlice(profiles),
		Providers:           nonNilSlice(providers),
		Models:              nonNilSlice(models),
		BootProfiles:        nonNilSlice(a.bootProfileOptions()),
		WorkRootHints:       []workRootHint{{ID: "operator-provided", Label: "Operator provided", Description: "Frontend should prompt for a project or working directory when the recipe/start path needs one."}},
	})
}

func nonNilSlice[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}

func (a *API) handleGetSessionDetails(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	details, err := a.sessionDetails(id)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "session not found")
		return
	}
	a.jsonResp(w, http.StatusOK, details)
}

func (a *API) sessionDetails(id string) (sessionDetailsResponse, error) {
	sess, err := a.Services.Store.GetSession(id)
	if err != nil {
		return sessionDetailsResponse{}, err
	}
	details := sessionDetailsResponse{
		Session:              sess,
		Runtime:              sessionRuntimeDetail{State: "none"},
		ActivityState:        "idle",
		LastActivityAt:       sess.LastActivity,
		LastUsefulActivityAt: sess.LastActivity,
		Halt:                 sessionHaltDetail{IsHalted: false},
		RecentDurableEvents:  []store.DurableAgentEvent{},
		BootSource:           inferBootSource(sess),
		ImmutableStartFields: []string{"provider", "model", "runtime_kind", "boot_profile", "recipe", "lifecycle_class", "work_root"},
		Checkpoint:           checkpointDetail{Status: "unknown"},
	}
	if halt, err := a.Services.Store.GetSessionHalt(id); err == nil && halt != nil {
		details.Halt = haltDetailFromStore(halt)
	}
	if mode, err := a.Services.Store.GetSessionMode(id); err == nil {
		details.Mode = mode
	}
	if primary, err := a.Services.Store.GetSessionPrimaryAgent(id); err == nil {
		if agent, err := a.Services.Store.GetAgent(primary.AgentID); err == nil {
			details.PrimaryAgent = agent
		}
	}
	if rels, err := a.Services.Store.ListDurableAgentSessionStatesForSession(id); err == nil {
		details.DurableAttachments = rels
		if len(rels) > 0 {
			if inst, err := a.Services.Store.GetDurableAgentInstance(rels[0].InstanceID); err == nil {
				details.CurrentDurableAgent = inst
				if events, err := a.Services.Store.ListDurableAgentEvents(inst.ID, 5); err == nil {
					details.RecentDurableEvents = nonNilSlice(events)
					if len(events) > 0 {
						details.LastUsefulActivityAt = laterTimestamp(
							details.LastUsefulActivityAt,
							events[0].CreatedAt.UTC().Format(time.RFC3339Nano),
						)
					}
				}
			}
		}
	}
	if usage, err := a.Services.Store.GetSessionUsage(id); err == nil && usage != nil && usage.MessageCount > 0 {
		details.Usage = usage
	}
	if rows, err := a.Services.Store.ListAgentRuntimeRowsForSession(id); err == nil && len(rows) > 0 {
		row := rows[0]
		details.Runtime = sessionRuntimeDetail{
			State:             normalizeRuntimeState(row.State),
			RuntimeID:         row.ID,
			RuntimeKind:       row.RuntimeKind,
			Provider:          row.Provider,
			Mode:              row.Mode,
			PID:               row.PID,
			BootDir:           row.Workdir,
			WorkspaceDir:      row.Workdir,
			ProviderSessionID: row.ProviderSessionID,
			FailureReason:     row.FailureReason,
			StartedAt:         formatRuntimeTime(row.StartedAt),
			UpdatedAt:         formatRuntimeTime(row.UpdatedAt),
		}
		details.LastUsefulActivityAt = laterTimestamp(details.LastUsefulActivityAt, details.Runtime.UpdatedAt)
	}
	details.ActivityState = deriveSessionActivityState(sess, details.Runtime.State, details.Halt, details.CurrentDurableAgent)
	return details, nil
}

func haltDetailFromStore(halt *store.HaltStatus) sessionHaltDetail {
	if halt == nil {
		return sessionHaltDetail{IsHalted: false}
	}
	out := sessionHaltDetail{IsHalted: halt.IsHalted()}
	if halt.HaltedAt != nil {
		out.HaltedAt = *halt.HaltedAt
	}
	if halt.HaltedReason != nil {
		out.HaltedReason = *halt.HaltedReason
	}
	return out
}

func normalizeRuntimeState(state string) string {
	switch state {
	case "":
		return "none"
	case "launching":
		return "starting"
	case "done":
		return "stopped"
	case "failed", "orphaned":
		return "failed"
	default:
		return state
	}
}

func deriveSessionActivityState(sess *store.Session, runtimeState string, halt sessionHaltDetail, inst *store.DurableAgentInstance) string {
	if sess == nil {
		return "idle"
	}
	if sess.Status == "archived" {
		return "archived"
	}
	if halt.IsHalted {
		return "halted"
	}
	if sess.Status == "stopped" || sess.Status == "done" {
		return "stopped"
	}
	if sess.Status == "failed" || sess.Status == "error" || runtimeState == "failed" {
		return "failed"
	}
	if inst != nil {
		switch inst.Status {
		case store.DurableAgentStatusArchived:
			return "archived"
		case store.DurableAgentStatusStopped:
			return "stopped"
		case store.DurableAgentStatusFailed:
			return "failed"
		case store.DurableAgentStatusStartRequested, store.DurableAgentStatusResumeRequested, store.DurableAgentStatusStarting:
			return "working"
		}
	}
	if runtimeState == "running" || runtimeState == "starting" {
		return "online"
	}
	return "idle"
}

func formatRuntimeTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func laterTimestamp(current, candidate string) string {
	if candidate == "" {
		return current
	}
	if current == "" {
		return candidate
	}
	currentTime, currentErr := time.Parse(time.RFC3339Nano, current)
	candidateTime, candidateErr := time.Parse(time.RFC3339Nano, candidate)
	if currentErr != nil || candidateErr != nil {
		return current
	}
	if candidateTime.After(currentTime) {
		return candidate
	}
	return current
}

func (a *API) providersForStartSurface() ([]store.ProviderConfig, error) {
	providers, err := a.Services.Store.ListProviders()
	if err != nil {
		return nil, err
	}
	providers = visibleProviderRows(providers)
	if reg := a.Services.BootProfiles; reg != nil {
		for _, spec := range reg.List() {
			if spec.Provider != "" {
				providers = append(providers, bootProfileProviderRow(spec))
			}
		}
	}
	return providers, nil
}

func (a *API) modelsForStartSurface() ([]store.Model, error) {
	models, err := a.Services.Store.ListModels()
	if err != nil {
		return nil, err
	}
	models = visibleModelRows(models)
	if reg := a.Services.BootProfiles; reg != nil {
		for _, spec := range reg.List() {
			if spec.Provider != "" {
				models = append(models, bootProfileModelRow(spec))
			}
		}
	}
	return models, nil
}

func (a *API) bootProfileOptions() []bootProfileOption {
	var out []bootProfileOption
	if reg := a.Services.BootProfiles; reg != nil {
		for _, spec := range reg.List() {
			if spec.Provider == "" {
				continue
			}
			id := bootprofile.EncodeProviderID(spec.ProfileID)
			label := spec.UILabel
			if label == "" {
				label = spec.ProfileID
			}
			out = append(out, bootProfileOption{ID: id, Label: label, Provider: spec.Provider, WorkRoot: spec.Workdir})
		}
	}
	return out
}

func inferBootSource(sess *store.Session) string {
	switch {
	case sess == nil:
		return "unknown"
	case sess.ContextType == "durable_agent":
		return "durable_agent"
	case bootprofile.IsProviderID(sess.Provider):
		return "boot_profile"
	case strings.HasPrefix(sess.Provider, "pty-") || strings.HasPrefix(sess.Provider, "sub-") || sess.Provider == "codex" || sess.Provider == "opencode":
		return "legacy_cli"
	default:
		return "api_default"
	}
}

func lifecycleClassOptions() []enumOption {
	return []enumOption{
		{Value: store.DurableAgentClassAdvisor, Label: "Advisor"},
		{Value: store.DurableAgentClassProcess, Label: "Process"},
		{Value: store.DurableAgentClassTemplate, Label: "Template"},
		{Value: store.DurableAgentClassHarness, Label: "Harness"},
	}
}

func durableStatusOptions() []enumOption {
	return []enumOption{
		{Value: store.DurableAgentStatusSleeping, Label: "Sleeping"},
		{Value: store.DurableAgentStatusStarting, Label: "Starting"},
		{Value: store.DurableAgentStatusActive, Label: "Active"},
		{Value: store.DurableAgentStatusPaused, Label: "Paused"},
		{Value: store.DurableAgentStatusStopped, Label: "Stopped"},
		{Value: store.DurableAgentStatusStartRequested, Label: "Start requested"},
		{Value: store.DurableAgentStatusStopRequested, Label: "Stop requested"},
		{Value: store.DurableAgentStatusResumeRequested, Label: "Resume requested"},
		{Value: store.DurableAgentStatusFailed, Label: "Failed"},
		{Value: store.DurableAgentStatusArchived, Label: "Archived"},
	}
}

func launchSourceOptions() []enumOption {
	return []enumOption{
		{Value: store.DurableAgentLaunchAPIChat, Label: "API chat"},
		{Value: store.DurableAgentLaunchCLIHarness, Label: "CLI harness"},
		{Value: store.DurableAgentLaunchBootProfile, Label: "Boot profile"},
		{Value: store.DurableAgentLaunchDurableAdvisor, Label: "Durable advisor"},
		{Value: store.DurableAgentLaunchProcessTick, Label: "Process tick"},
		{Value: store.DurableAgentLaunchTaskTemplateRun, Label: "Task template run"},
	}
}

func attachmentRelationOptions() []enumOption {
	return []enumOption{
		{Value: store.DurableAgentSessionRelationPrimary, Label: "Primary"},
		{Value: store.DurableAgentSessionRelationWake, Label: "Wake"},
		{Value: store.DurableAgentSessionRelationRun, Label: "Run"},
		{Value: store.DurableAgentSessionRelationHarness, Label: "Harness"},
		{Value: store.DurableAgentSessionRelationOwned, Label: "Owned", Legacy: true},
		{Value: store.DurableAgentSessionRelationAttached, Label: "Attached", Legacy: true},
		{Value: store.DurableAgentSessionRelationSpawned, Label: "Spawned", Legacy: true},
	}
}

func runtimeKindOptions() []runtimeKindOption {
	return []runtimeKindOption{
		{Value: string(runtimekind.API), Label: "API", ManagedAutomation: true, ProductSupported: true},
		{Value: string(runtimekind.StreamingStdio), Label: "Streaming stdio", ManagedAutomation: true, ProductSupported: true},
		{Value: string(runtimekind.Subprocess), Label: "Subprocess", ManagedAutomation: true, ProductSupported: true},
		{Value: string(runtimekind.JSONRPCStdio), Label: "JSON-RPC stdio", ManagedAutomation: true, ProductSupported: true},
		{Value: string(runtimekind.ServeHTTP), Label: "Serve HTTP", ManagedAutomation: true, ProductSupported: false},
		{Value: string(runtimekind.PTY), Label: "PTY", ManagedAutomation: false, ProductSupported: false, Description: "Raw terminal/TUI path, not a managed start-surface option."},
		{Value: string(runtimekind.PTYDebug), Label: "PTY debug", ManagedAutomation: false, ProductSupported: false, Description: "Debug-only raw terminal path."},
	}
}

func recipeKindOptions() []enumOption {
	return []enumOption{
		{Value: service.DurableAgentRecipeKindProjectAdvisor, Label: "Project advisor"},
		{Value: service.DurableAgentRecipeKindManagedCLIHarness, Label: "Managed CLI harness"},
		{Value: service.DurableAgentRecipeKindProcessMonitor, Label: "Process monitor"},
		{Value: service.DurableAgentRecipeKindTemplateWorker, Label: "Template worker"},
		{Value: service.DurableAgentRecipeKindOrchestrator, Label: "Orchestrator"},
	}
}

func wakeReasonOptions() []enumOption {
	return []enumOption{
		{Value: service.DurableAgentWakeManual, Label: "Manual"},
		{Value: service.DurableAgentWakeLifecycleStart, Label: "Lifecycle start"},
		{Value: service.DurableAgentWakeLifecycleResume, Label: "Lifecycle resume"},
		{Value: service.DurableAgentWakeProcessTick, Label: "Process tick"},
		{Value: service.DurableAgentWakeScheduled, Label: "Scheduled wake"},
		{Value: service.DurableAgentWakeExternalMessage, Label: "External message"},
	}
}

func sessionPolicyOptions() []enumOption {
	return []enumOption{
		{Value: service.DurableAgentSessionPolicyReuseLatestOrCreate, Label: "Reuse latest or create"},
		{Value: service.DurableAgentSessionPolicyFreshPerWake, Label: "Fresh per wake"},
		{Value: service.DurableAgentSessionPolicyFreshOneShot, Label: "Fresh one-shot"},
		{Value: service.DurableAgentSessionPolicyReuseManaged, Label: "Reuse managed"},
	}
}
