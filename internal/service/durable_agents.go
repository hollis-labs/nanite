package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/hollis-labs/agentkit/agentruntime/runtimekind"
	"github.com/hollis-labs/nanite/internal/store"
)

var (
	ErrDurableAgentWorkspaceRequired     = errors.New("durable agent start requires workspace_id when creating a session")
	ErrDurableAgentNoResumableSession    = errors.New("durable agent has no resumable attached session")
	ErrDurableAgentUnsupportedLaunchPlan = errors.New("durable agent launch policy is unsupported")
)

const (
	DurableAgentSessionPolicyReuseLatestOrCreate = "reuse_latest_or_create"
	DurableAgentSessionPolicyFreshPerWake        = "fresh_per_wake"
	DurableAgentSessionPolicyFreshOneShot        = "fresh_one_shot"
	DurableAgentSessionPolicyReuseManaged        = "reuse_managed"

	DurableAgentWakeManual          = "manual"
	DurableAgentWakeLifecycleStart  = "lifecycle_start"
	DurableAgentWakeLifecycleResume = "lifecycle_resume"
	DurableAgentWakeProcessTick     = "process_tick"
	DurableAgentWakeExternalMessage = "external_message"
)

type DurableAgentWakePayload struct {
	Reason   string            `json:"reason" yaml:"reason"`
	Prompt   string            `json:"prompt,omitempty" yaml:"prompt,omitempty"`
	Facts    map[string]string `json:"facts,omitempty" yaml:"facts,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

type DurableAgentLaunchPolicy struct {
	InstanceID         string                  `json:"instance_id"`
	LifecycleClass     string                  `json:"lifecycle_class"`
	SessionPolicy      string                  `json:"session_policy"`
	LaunchSourceType   string                  `json:"launch_source_type"`
	Provider           string                  `json:"provider"`
	Model              string                  `json:"model"`
	RuntimeKind        string                  `json:"runtime_kind"`
	WorkRoot           string                  `json:"work_root"`
	AttachmentRelation string                  `json:"attachment_relation"`
	WakePayload        DurableAgentWakePayload `json:"wake_payload"`
}

type DurableAgentStartRequest struct {
	WorkspaceID string
	ProjectID   string
	WakePayload DurableAgentWakePayload
}

type DurableAgentLaunchResult struct {
	Instance       *store.DurableAgentInstance `json:"instance"`
	Policy         DurableAgentLaunchPolicy    `json:"policy"`
	Session        *store.Session              `json:"session,omitempty"`
	CreatedSession bool                        `json:"created_session"`
	ReusedSession  bool                        `json:"reused_session"`
}

// DurableAgentRuntimeController is the durable-agent service boundary to
// chat/runtime lifecycle operations. StopSession currently maps to the chat
// runtime teardown used by per-session reboot: stop the live process if one is
// tracked, evict it, and let any future turn cold-boot through driveBootSession.
//
// RecoverSession evicts the runtime WITHOUT arming the fresh-boot flag, so the
// next turn cold-boots into auto-recovery (recovery pack + provider resume)
// rather than a clean slate — the durable counterpart to an explicit Recover
// (CW-20260525-0001 Slice 5).
type DurableAgentRuntimeController interface {
	StopSession(ctx context.Context, sessionID string) error
	RebootSession(ctx context.Context, sessionID string) error
	RecoverSession(ctx context.Context, sessionID string) error
	CancelSession(ctx context.Context, sessionID string) error

	// SendMessage delivers content into a session as a real user turn via
	// the existing chat first-turn path (ChatService.HandleMessage) —
	// the same path a human's chat message takes. Used to inject
	// DurableAgentWakePayload.Prompt so a wake's message content actually
	// reaches the woken session instead of only feeding session metadata.
	SendMessage(ctx context.Context, sessionID, content string) error
}

// DurableAgentService owns the durable-agent control-plane surface. Phase 6
// resolves lifecycle intent into explicit launch policy and chat-session
// attachment, while leaving runtime boot on the existing chat first-turn path.
type DurableAgentService interface {
	Create(ctx context.Context, inst *store.DurableAgentInstance) error
	Get(ctx context.Context, id string) (*store.DurableAgentInstance, error)
	List(ctx context.Context, includeArchived bool) ([]store.DurableAgentInstance, error)
	Update(ctx context.Context, id string, upd store.DurableAgentInstanceUpdate) (*store.DurableAgentInstance, error)
	Archive(ctx context.Context, id string) (*store.DurableAgentInstance, error)
	LaunchPlan(ctx context.Context, id string, wake DurableAgentWakePayload) (DurableAgentLaunchPolicy, error)
	Start(ctx context.Context, id string, req DurableAgentStartRequest) (*DurableAgentLaunchResult, error)
	Resume(ctx context.Context, id string, req DurableAgentStartRequest) (*DurableAgentLaunchResult, error)
	RequestStart(ctx context.Context, id string) (*store.DurableAgentInstance, error)
	RequestStop(ctx context.Context, id string) (*store.DurableAgentInstance, error)
	RequestPause(ctx context.Context, id string) (*store.DurableAgentInstance, error)
	RequestResume(ctx context.Context, id string) (*store.DurableAgentInstance, error)
	AttachSession(ctx context.Context, instanceID, sessionID, relation string) error
	ListSessions(ctx context.Context, instanceID string) ([]store.DurableAgentInstanceSession, error)
	ListSessionStates(ctx context.Context, instanceID string) ([]store.DurableAgentInstanceSessionState, error)
	ListEvents(ctx context.Context, instanceID string, limit int) ([]store.DurableAgentEvent, error)
}

type DurableAgentStore interface {
	CreateDurableAgentInstance(inst *store.DurableAgentInstance) error
	GetDurableAgentInstance(id string) (*store.DurableAgentInstance, error)
	ListDurableAgentInstances(includeArchived bool) ([]store.DurableAgentInstance, error)
	UpdateDurableAgentInstance(id string, upd store.DurableAgentInstanceUpdate) (*store.DurableAgentInstance, error)
	SetDurableAgentInstanceStatus(id, status string) (*store.DurableAgentInstance, error)
	SetDurableAgentInstanceLaunchState(id, status, sessionID, failureReason string) (*store.DurableAgentInstance, error)
	ArchiveDurableAgentInstance(id string) (*store.DurableAgentInstance, error)
	AttachDurableAgentInstanceSession(instanceID, sessionID, relation string) error
	ListDurableAgentInstanceSessions(instanceID string) ([]store.DurableAgentInstanceSession, error)
	ListDurableAgentInstanceSessionStates(instanceID string) ([]store.DurableAgentInstanceSessionState, error)
	CreateDurableAgentEvent(event *store.DurableAgentEvent) error
	ListDurableAgentEvents(instanceID string, limit int) ([]store.DurableAgentEvent, error)
	GetAgent(id string) (*store.AgentProfile, error)
	ListAgents() ([]store.AgentProfile, error)
	GetSession(id string) (*store.Session, error)
	CreateSession(sess *store.Session) error
	EnsureSessionAgent(sessionID, agentID, mode string, isPrimary bool) error
}

type durableAgentService struct {
	store   DurableAgentStore
	runtime DurableAgentRuntimeController
}

func NewDurableAgentService(st DurableAgentStore) DurableAgentService {
	return NewDurableAgentServiceWithRuntime(st, nil)
}

func NewDurableAgentServiceWithRuntime(st DurableAgentStore, runtime DurableAgentRuntimeController) DurableAgentService {
	return &durableAgentService{store: st, runtime: runtime}
}

func (s *durableAgentService) Create(_ context.Context, inst *store.DurableAgentInstance) error {
	if err := s.store.CreateDurableAgentInstance(inst); err != nil {
		return err
	}
	s.recordEvent(&store.DurableAgentEvent{
		InstanceID:   inst.ID,
		EventType:    store.DurableAgentEventCreated,
		StatusAfter:  inst.Status,
		SessionID:    inst.CurrentSessionID,
		MetadataJSON: durableAgentEventMetadata(map[string]string{"profile_id": inst.ProfileID}),
	})
	return nil
}

func (s *durableAgentService) Get(_ context.Context, id string) (*store.DurableAgentInstance, error) {
	return s.store.GetDurableAgentInstance(id)
}

func (s *durableAgentService) List(_ context.Context, includeArchived bool) ([]store.DurableAgentInstance, error) {
	if err := s.reconcileProfileBackedInstances(); err != nil {
		return nil, err
	}
	return s.store.ListDurableAgentInstances(includeArchived)
}

func (s *durableAgentService) reconcileProfileBackedInstances() error {
	profiles, err := s.store.ListAgents()
	if err != nil {
		return err
	}
	instances, err := s.store.ListDurableAgentInstances(true)
	if err != nil {
		return err
	}

	byProfileID := make(map[string]struct{}, len(instances))
	bySlug := make(map[string]struct{}, len(instances))
	for _, inst := range instances {
		byProfileID[inst.ProfileID] = struct{}{}
		bySlug[inst.Slug] = struct{}{}
	}

	for _, profile := range profiles {
		if !profileIsDurableCandidate(profile) {
			continue
		}
		if _, ok := byProfileID[profile.ID]; ok {
			continue
		}
		if _, ok := bySlug[profile.Slug]; ok {
			continue
		}
		inst := durableAgentInstanceFromProfile(profile)
		if err := s.store.CreateDurableAgentInstance(inst); err != nil {
			return fmt.Errorf("reconcile durable agent instance for profile %s: %w", profile.ID, err)
		}
		byProfileID[profile.ID] = struct{}{}
		bySlug[profile.Slug] = struct{}{}
	}
	return nil
}

func profileIsDurableCandidate(profile store.AgentProfile) bool {
	if profile.Status != "" && profile.Status != "active" {
		return false
	}
	if profile.Durable {
		return true
	}
	if strings.TrimSpace(profile.Tags) == "" {
		return false
	}
	var tags []string
	if err := json.Unmarshal([]byte(profile.Tags), &tags); err != nil {
		return strings.Contains(profile.Tags, `"durable-agent"`)
	}
	for _, tag := range tags {
		if tag == "durable-agent" {
			return true
		}
	}
	return false
}

func durableAgentInstanceFromProfile(profile store.AgentProfile) *store.DurableAgentInstance {
	lifecycleClass := profile.Class
	switch lifecycleClass {
	case store.DurableAgentClassAdvisor, store.DurableAgentClassProcess, store.DurableAgentClassTemplate, store.DurableAgentClassHarness:
	default:
		lifecycleClass = store.DurableAgentClassAdvisor
	}

	// CW-20260815-0009: agent_profiles.class started accepting "harness"
	// after this profile-reconcile path was written, so a harness-class
	// durable candidate (e.g. orchestrator.md) reconciled here — before its
	// recipe is ever applied — used to silently fall into the default
	// branch above and become lifecycle_class="advisor". A harness agent
	// auto-reconciled here has no recipe-derived launch source yet, so
	// api_chat (the same source an operator-driven recipe apply uses) is
	// the closest honest default.
	launchSourceType := store.DurableAgentLaunchDurableAdvisor
	switch lifecycleClass {
	case store.DurableAgentClassProcess:
		launchSourceType = store.DurableAgentLaunchProcessTick
	case store.DurableAgentClassTemplate:
		launchSourceType = store.DurableAgentLaunchTaskTemplateRun
	case store.DurableAgentClassHarness:
		launchSourceType = store.DurableAgentLaunchAPIChat
	}

	status := profile.DefaultState
	switch status {
	case store.DurableAgentStatusSleeping, store.DurableAgentStatusActive:
	default:
		status = store.DurableAgentStatusSleeping
	}

	// CW-20260526-0003: Provider/Model are left as the profile's literal
	// values (which may be empty). Resolution to the runtime default
	// happens at request time via store.ResolveProviderAndModel — keeping
	// instance rows empty when the profile is empty means a later operator
	// edit to user_settings or providers.default_model is honored without
	// re-seeding the instance. The previous bare-literal fallbacks here
	// were the source of the `claude-sonnet-4` 404 bug.
	return &store.DurableAgentInstance{
		ID:               "legacy-profile-" + profile.ID,
		Name:             profile.Name,
		Slug:             profile.Slug,
		ProfileID:        profile.ID,
		LifecycleClass:   lifecycleClass,
		Provider:         profile.DefaultProvider,
		Model:            profile.DefaultModel,
		RuntimeKind:      string(runtimekind.API),
		LaunchSourceType: launchSourceType,
		LaunchSourceID:   profile.ID,
		Status:           status,
		MetadataJSON:     `{"seeded_from":"agent_profiles","legacy_profile":true,"reconciled":true}`,
	}
}

func (s *durableAgentService) Update(_ context.Context, id string, upd store.DurableAgentInstanceUpdate) (*store.DurableAgentInstance, error) {
	before, err := s.store.GetDurableAgentInstance(id)
	if err != nil {
		return nil, err
	}
	updated, err := s.store.UpdateDurableAgentInstance(id, upd)
	if err != nil {
		return nil, err
	}
	s.recordEvent(&store.DurableAgentEvent{
		InstanceID:   id,
		EventType:    store.DurableAgentEventUpdated,
		StatusBefore: before.Status,
		StatusAfter:  updated.Status,
		SessionID:    updated.CurrentSessionID,
	})
	return updated, nil
}

func (s *durableAgentService) Archive(_ context.Context, id string) (*store.DurableAgentInstance, error) {
	before, err := s.store.GetDurableAgentInstance(id)
	if err != nil {
		return nil, err
	}
	archived, err := s.store.ArchiveDurableAgentInstance(id)
	if err != nil {
		return nil, err
	}
	s.recordEvent(&store.DurableAgentEvent{
		InstanceID:   id,
		EventType:    store.DurableAgentEventArchived,
		StatusBefore: before.Status,
		StatusAfter:  archived.Status,
		SessionID:    archived.CurrentSessionID,
	})
	return archived, nil
}

func (s *durableAgentService) LaunchPlan(_ context.Context, id string, wake DurableAgentWakePayload) (DurableAgentLaunchPolicy, error) {
	inst, err := s.store.GetDurableAgentInstance(id)
	if err != nil {
		return DurableAgentLaunchPolicy{}, err
	}
	return durableAgentLaunchPolicyFor(inst, wake)
}

func (s *durableAgentService) Start(ctx context.Context, id string, req DurableAgentStartRequest) (*DurableAgentLaunchResult, error) {
	before, err := s.store.GetDurableAgentInstance(id)
	if err != nil {
		return nil, err
	}
	inst, err := s.store.SetDurableAgentInstanceStatus(id, store.DurableAgentStatusStarting)
	if err != nil {
		return nil, err
	}
	s.recordEvent(&store.DurableAgentEvent{
		InstanceID:   id,
		EventType:    store.DurableAgentEventStartRequested,
		StatusBefore: before.Status,
		StatusAfter:  inst.Status,
		SessionID:    inst.CurrentSessionID,
	})
	wake := req.WakePayload
	if wake.Reason == "" {
		wake.Reason = DurableAgentWakeLifecycleStart
	}
	policy, err := durableAgentLaunchPolicyFor(inst, wake)
	if err != nil {
		failed, _ := s.store.SetDurableAgentInstanceLaunchState(id, store.DurableAgentStatusFailed, inst.CurrentSessionID, err.Error())
		s.recordFailureEvent(id, store.DurableAgentEventStartFailed, inst.Status, failed, inst.CurrentSessionID, err)
		return nil, err
	}

	session, reused, err := s.selectOrCreateLaunchSession(inst, policy, req)
	if err != nil {
		failed, _ := s.store.SetDurableAgentInstanceLaunchState(id, store.DurableAgentStatusFailed, inst.CurrentSessionID, err.Error())
		s.recordFailureEvent(id, store.DurableAgentEventStartFailed, inst.Status, failed, inst.CurrentSessionID, err)
		return nil, err
	}
	if err := s.store.AttachDurableAgentInstanceSession(inst.ID, session.ID, policy.AttachmentRelation); err != nil {
		failed, _ := s.store.SetDurableAgentInstanceLaunchState(id, store.DurableAgentStatusFailed, inst.CurrentSessionID, err.Error())
		s.recordFailureEvent(id, store.DurableAgentEventStartFailed, inst.Status, failed, session.ID, err)
		return nil, err
	}
	s.recordSessionAttachedEvent(inst, session.ID, policy.AttachmentRelation)
	active, err := s.store.SetDurableAgentInstanceLaunchState(id, store.DurableAgentStatusActive, session.ID, "")
	if err != nil {
		return nil, err
	}
	s.recordEvent(&store.DurableAgentEvent{
		InstanceID:   id,
		EventType:    store.DurableAgentEventStartSucceeded,
		StatusBefore: inst.Status,
		StatusAfter:  active.Status,
		SessionID:    session.ID,
		MetadataJSON: durableAgentEventMetadata(map[string]string{"session_reused": fmt.Sprintf("%t", reused)}),
	})
	s.deliverWakePrompt(ctx, session.ID, wake.Prompt)
	return &DurableAgentLaunchResult{
		Instance:       active,
		Policy:         policy,
		Session:        session,
		CreatedSession: !reused,
		ReusedSession:  reused,
	}, nil
}

func (s *durableAgentService) Resume(ctx context.Context, id string, req DurableAgentStartRequest) (*DurableAgentLaunchResult, error) {
	before, err := s.store.GetDurableAgentInstance(id)
	if err != nil {
		return nil, err
	}
	inst, err := s.store.SetDurableAgentInstanceStatus(id, store.DurableAgentStatusResumeRequested)
	if err != nil {
		return nil, err
	}
	s.recordEvent(&store.DurableAgentEvent{
		InstanceID:   id,
		EventType:    store.DurableAgentEventResumeRequested,
		StatusBefore: before.Status,
		StatusAfter:  inst.Status,
		SessionID:    inst.CurrentSessionID,
	})
	wake := req.WakePayload
	if wake.Reason == "" {
		wake.Reason = DurableAgentWakeLifecycleResume
	}
	policy, err := durableAgentLaunchPolicyFor(inst, wake)
	if err != nil {
		failed, _ := s.store.SetDurableAgentInstanceLaunchState(id, store.DurableAgentStatusFailed, inst.CurrentSessionID, err.Error())
		s.recordFailureEvent(id, store.DurableAgentEventResumeFailed, inst.Status, failed, inst.CurrentSessionID, err)
		return nil, err
	}
	session, err := s.latestAttachedSession(inst, false)
	if err != nil {
		failed, _ := s.store.SetDurableAgentInstanceLaunchState(id, store.DurableAgentStatusFailed, inst.CurrentSessionID, err.Error())
		s.recordFailureEvent(id, store.DurableAgentEventResumeFailed, inst.Status, failed, inst.CurrentSessionID, err)
		return nil, err
	}
	if session == nil {
		failed, _ := s.store.SetDurableAgentInstanceLaunchState(id, store.DurableAgentStatusFailed, inst.CurrentSessionID, ErrDurableAgentNoResumableSession.Error())
		s.recordFailureEvent(id, store.DurableAgentEventResumeFailed, inst.Status, failed, inst.CurrentSessionID, ErrDurableAgentNoResumableSession)
		return nil, ErrDurableAgentNoResumableSession
	}
	if err := s.store.AttachDurableAgentInstanceSession(inst.ID, session.ID, policy.AttachmentRelation); err != nil {
		failed, _ := s.store.SetDurableAgentInstanceLaunchState(id, store.DurableAgentStatusFailed, inst.CurrentSessionID, err.Error())
		s.recordFailureEvent(id, store.DurableAgentEventResumeFailed, inst.Status, failed, session.ID, err)
		return nil, err
	}
	s.recordSessionAttachedEvent(inst, session.ID, policy.AttachmentRelation)

	// CW-20260525-0001 Slice 5 — recovery-aware resume. Evict any live/stale
	// runtime for the reattached session WITHOUT arming the fresh-boot flag so
	// the next turn cold-boots into auto-recovery (recovery pack + provider
	// resume). This lets the durable agent resume with prior context instead of
	// just reattaching the row and waiting for a normal cold turn to answer
	// blind. Best-effort: a recovery hiccup must not fail an otherwise
	// successful resume. For API-backed sessions (no tracked runtime) this is a
	// no-op and the next turn rebuilds context from messages as usual.
	recoveryMeta := map[string]string{}
	if s.runtime != nil {
		if recErr := s.runtime.RecoverSession(ctx, session.ID); recErr != nil {
			recoveryMeta["recovery_armed"] = "false"
			recoveryMeta["recovery_error"] = recErr.Error()
		} else {
			recoveryMeta["recovery_armed"] = "true"
		}
	}

	active, err := s.store.SetDurableAgentInstanceLaunchState(id, store.DurableAgentStatusActive, session.ID, "")
	if err != nil {
		return nil, err
	}
	s.recordEvent(&store.DurableAgentEvent{
		InstanceID:   id,
		EventType:    store.DurableAgentEventResumeSucceeded,
		StatusBefore: inst.Status,
		StatusAfter:  active.Status,
		SessionID:    session.ID,
		MetadataJSON: durableAgentEventMetadata(recoveryMeta),
	})
	s.deliverWakePrompt(ctx, session.ID, wake.Prompt)
	return &DurableAgentLaunchResult{
		Instance:      active,
		Policy:        policy,
		Session:       session,
		ReusedSession: true,
	}, nil
}

// deliverWakePrompt injects a non-empty DurableAgentWakePayload.Prompt into
// the launched/resumed session as a real user turn via the runtime
// controller's existing chat first-turn path, so the wake caller's message
// content actually reaches the woken session instead of only feeding
// session metadata. A no-op when Prompt is empty (today's manual-wake
// behavior) or when no runtime controller is wired (e.g. in unit tests
// constructed via NewDurableAgentService). Best-effort: a delivery failure
// is logged, not surfaced, so it can't turn an otherwise-successful
// start/resume into a failure.
func (s *durableAgentService) deliverWakePrompt(ctx context.Context, sessionID, prompt string) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" || s.runtime == nil {
		return
	}
	if err := s.runtime.SendMessage(ctx, sessionID, prompt); err != nil {
		slog.Warn("durable agent wake: failed to deliver wake prompt as a user turn",
			"session_id", sessionID, "err", err)
	}
}

func (s *durableAgentService) RequestStart(_ context.Context, id string) (*store.DurableAgentInstance, error) {
	before, err := s.store.GetDurableAgentInstance(id)
	if err != nil {
		return nil, err
	}
	inst, err := s.store.SetDurableAgentInstanceStatus(id, store.DurableAgentStatusStartRequested)
	if err != nil {
		return nil, err
	}
	s.recordEvent(&store.DurableAgentEvent{
		InstanceID:   id,
		EventType:    store.DurableAgentEventStartRequested,
		StatusBefore: before.Status,
		StatusAfter:  inst.Status,
		SessionID:    inst.CurrentSessionID,
	})
	return inst, nil
}

func (s *durableAgentService) RequestStop(ctx context.Context, id string) (*store.DurableAgentInstance, error) {
	before, err := s.store.GetDurableAgentInstance(id)
	if err != nil {
		return nil, err
	}
	inst, err := s.store.SetDurableAgentInstanceStatus(id, store.DurableAgentStatusStopRequested)
	if err != nil {
		return nil, err
	}
	s.recordEvent(&store.DurableAgentEvent{
		InstanceID:   id,
		EventType:    store.DurableAgentEventStopRequested,
		StatusBefore: before.Status,
		StatusAfter:  inst.Status,
		SessionID:    inst.CurrentSessionID,
	})
	if inst.CurrentSessionID != "" && s.runtime != nil {
		if err := s.runtime.StopSession(ctx, inst.CurrentSessionID); err != nil {
			failed, setErr := s.store.SetDurableAgentInstanceLaunchState(id, store.DurableAgentStatusFailed, inst.CurrentSessionID, err.Error())
			if setErr != nil {
				return nil, setErr
			}
			s.recordFailureEvent(id, store.DurableAgentEventStopFailed, inst.Status, failed, inst.CurrentSessionID, err)
			return failed, nil
		}
		s.recordEvent(&store.DurableAgentEvent{
			InstanceID:   id,
			EventType:    store.DurableAgentEventRuntimeStopSucceeded,
			StatusBefore: inst.Status,
			StatusAfter:  inst.Status,
			SessionID:    inst.CurrentSessionID,
			Source:       store.DurableAgentEventSourceRuntime,
		})
	}
	stopped, err := s.store.SetDurableAgentInstanceLaunchState(id, store.DurableAgentStatusStopped, inst.CurrentSessionID, "")
	if err != nil {
		return nil, err
	}
	s.recordEvent(&store.DurableAgentEvent{
		InstanceID:   id,
		EventType:    store.DurableAgentEventStopSucceeded,
		StatusBefore: inst.Status,
		StatusAfter:  stopped.Status,
		SessionID:    stopped.CurrentSessionID,
	})
	return stopped, nil
}

func (s *durableAgentService) RequestPause(_ context.Context, id string) (*store.DurableAgentInstance, error) {
	inst, err := s.store.GetDurableAgentInstance(id)
	if err != nil {
		return nil, err
	}
	s.recordEvent(&store.DurableAgentEvent{
		InstanceID:   id,
		EventType:    store.DurableAgentEventPauseRequested,
		StatusBefore: inst.Status,
		StatusAfter:  inst.Status,
		SessionID:    inst.CurrentSessionID,
	})
	paused, err := s.store.SetDurableAgentInstanceLaunchState(id, store.DurableAgentStatusPaused, inst.CurrentSessionID, "")
	if err != nil {
		return nil, err
	}
	s.recordEvent(&store.DurableAgentEvent{
		InstanceID:   id,
		EventType:    store.DurableAgentEventPauseSucceeded,
		StatusBefore: inst.Status,
		StatusAfter:  paused.Status,
		SessionID:    paused.CurrentSessionID,
	})
	return paused, nil
}

func (s *durableAgentService) RequestResume(ctx context.Context, id string) (*store.DurableAgentInstance, error) {
	result, err := s.Resume(ctx, id, DurableAgentStartRequest{})
	if err != nil {
		if result != nil && result.Instance != nil {
			return result.Instance, err
		}
		inst, getErr := s.store.GetDurableAgentInstance(id)
		if getErr == nil {
			return inst, err
		}
		return nil, err
	}
	return result.Instance, nil
}

func (s *durableAgentService) AttachSession(_ context.Context, instanceID, sessionID, relation string) error {
	inst, err := s.store.GetDurableAgentInstance(instanceID)
	if err != nil {
		return err
	}
	if err := s.store.AttachDurableAgentInstanceSession(instanceID, sessionID, relation); err != nil {
		return err
	}
	s.recordSessionAttachedEvent(inst, sessionID, relation)
	return nil
}

func (s *durableAgentService) ListSessions(_ context.Context, instanceID string) ([]store.DurableAgentInstanceSession, error) {
	return s.store.ListDurableAgentInstanceSessions(instanceID)
}

func (s *durableAgentService) ListSessionStates(_ context.Context, instanceID string) ([]store.DurableAgentInstanceSessionState, error) {
	return s.store.ListDurableAgentInstanceSessionStates(instanceID)
}

func (s *durableAgentService) ListEvents(_ context.Context, instanceID string, limit int) ([]store.DurableAgentEvent, error) {
	if _, err := s.store.GetDurableAgentInstance(instanceID); err != nil {
		return nil, err
	}
	return s.store.ListDurableAgentEvents(instanceID, limit)
}

func (s *durableAgentService) recordEvent(event *store.DurableAgentEvent) {
	_ = s.store.CreateDurableAgentEvent(event)
}

func (s *durableAgentService) recordSessionAttachedEvent(inst *store.DurableAgentInstance, sessionID, relation string) {
	if relation == "" {
		relation = store.DurableAgentSessionRelationOwned
	}
	s.recordEvent(&store.DurableAgentEvent{
		InstanceID:   inst.ID,
		EventType:    store.DurableAgentEventSessionAttached,
		StatusBefore: inst.Status,
		StatusAfter:  inst.Status,
		SessionID:    sessionID,
		MetadataJSON: durableAgentEventMetadata(map[string]string{"relation": relation}),
	})
}

func (s *durableAgentService) recordFailureEvent(instanceID, eventType, statusBefore string, failed *store.DurableAgentInstance, sessionID string, err error) {
	statusAfter := store.DurableAgentStatusFailed
	if failed != nil {
		statusAfter = failed.Status
		if sessionID == "" {
			sessionID = failed.CurrentSessionID
		}
	}
	s.recordEvent(&store.DurableAgentEvent{
		InstanceID:   instanceID,
		EventType:    eventType,
		StatusBefore: statusBefore,
		StatusAfter:  statusAfter,
		SessionID:    sessionID,
		Message:      err.Error(),
		MetadataJSON: durableAgentEventMetadata(map[string]string{"failure_reason": err.Error()}),
	})
}

func durableAgentEventMetadata(values map[string]string) string {
	if len(values) == 0 {
		return "{}"
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

func durableAgentLaunchPolicyFor(inst *store.DurableAgentInstance, wake DurableAgentWakePayload) (DurableAgentLaunchPolicy, error) {
	if inst == nil {
		return DurableAgentLaunchPolicy{}, errors.New("durable agent launch policy: nil instance")
	}
	rk := runtimekind.Parse(inst.RuntimeKind)
	if inst.RuntimeKind == "" {
		rk = runtimekind.API
	}
	if !runtimekind.IsManagedAutomation(rk) {
		return DurableAgentLaunchPolicy{}, fmt.Errorf("%w: runtime_kind %q is not managed automation", ErrDurableAgentUnsupportedLaunchPlan, inst.RuntimeKind)
	}
	policy := DurableAgentLaunchPolicy{
		InstanceID:       inst.ID,
		LifecycleClass:   inst.LifecycleClass,
		LaunchSourceType: inst.LaunchSourceType,
		Provider:         inst.Provider,
		Model:            inst.Model,
		RuntimeKind:      string(rk),
		WorkRoot:         inst.WorkRoot,
		WakePayload:      wake,
	}
	switch inst.LifecycleClass {
	case store.DurableAgentClassAdvisor:
		policy.SessionPolicy = DurableAgentSessionPolicyReuseLatestOrCreate
		policy.AttachmentRelation = store.DurableAgentSessionRelationPrimary
	case store.DurableAgentClassProcess:
		policy.SessionPolicy = DurableAgentSessionPolicyFreshPerWake
		policy.AttachmentRelation = store.DurableAgentSessionRelationWake
	case store.DurableAgentClassTemplate:
		policy.SessionPolicy = DurableAgentSessionPolicyFreshOneShot
		policy.AttachmentRelation = store.DurableAgentSessionRelationRun
	case store.DurableAgentClassHarness:
		policy.SessionPolicy = DurableAgentSessionPolicyReuseManaged
		policy.AttachmentRelation = store.DurableAgentSessionRelationHarness
	default:
		return DurableAgentLaunchPolicy{}, fmt.Errorf("%w: lifecycle_class %q", ErrDurableAgentUnsupportedLaunchPlan, inst.LifecycleClass)
	}
	return policy, nil
}

func (s *durableAgentService) selectOrCreateLaunchSession(inst *store.DurableAgentInstance, policy DurableAgentLaunchPolicy, req DurableAgentStartRequest) (*store.Session, bool, error) {
	switch policy.SessionPolicy {
	case DurableAgentSessionPolicyReuseLatestOrCreate, DurableAgentSessionPolicyReuseManaged:
		if sess, err := s.latestAttachedSession(inst, true); err != nil {
			return nil, false, err
		} else if sess != nil {
			return sess, true, nil
		}
	}
	if req.WorkspaceID == "" {
		return nil, false, ErrDurableAgentWorkspaceRequired
	}
	metadata, _ := json.Marshal(map[string]string{"durable_agent_instance_id": inst.ID})
	sessionTitle := strings.TrimSpace(inst.Name)
	if sessionTitle == "" {
		sessionTitle = strings.TrimSpace(inst.Slug)
	}
	if sessionTitle == "" {
		sessionTitle = inst.ID
	}
	sess := &store.Session{
		WorkspaceID: req.WorkspaceID,
		ProjectID:   req.ProjectID,
		Provider:    inst.Provider,
		Model:       inst.Model,
		Title:       sessionTitle,
		ContextType: "durable_agent",
		ContextID:   inst.ID,
		Metadata:    string(metadata),
	}
	if err := s.store.CreateSession(sess); err != nil {
		return nil, false, fmt.Errorf("create durable agent session: %w", err)
	}
	if err := s.store.EnsureSessionAgent(sess.ID, inst.ProfileID, "default", true); err != nil {
		return nil, false, fmt.Errorf("attach durable agent profile to session: %w", err)
	}
	return sess, false, nil
}

func (s *durableAgentService) latestAttachedSession(inst *store.DurableAgentInstance, requireCompatible bool) (*store.Session, error) {
	rels, err := s.store.ListDurableAgentInstanceSessions(inst.ID)
	if err != nil {
		return nil, err
	}
	for _, rel := range rels {
		if rel.DetachedAt != nil {
			continue
		}
		sess, err := s.store.GetSession(rel.SessionID)
		if err != nil {
			continue
		}
		if sess.Status == "archived" {
			continue
		}
		if requireCompatible && !durableAgentSessionCompatible(inst, sess) {
			continue
		}
		return sess, nil
	}
	return nil, nil
}

func durableAgentSessionCompatible(inst *store.DurableAgentInstance, sess *store.Session) bool {
	if inst.Provider != "" && sess.Provider != "" && inst.Provider != sess.Provider {
		return false
	}
	if inst.Model != "" && sess.Model != "" && inst.Model != sess.Model {
		return false
	}
	return true
}
