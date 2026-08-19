package service

import (
	"context"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/hollis-labs/nanite/internal/store"
)

const DurableAgentWakeScheduled = "scheduled_wake"

type DurableAgentWakeDueItem struct {
	InstanceID       string              `json:"instance_id"`
	InstanceName     string              `json:"instance_name"`
	LifecycleClass   string              `json:"lifecycle_class"`
	CurrentSessionID string              `json:"current_session_id"`
	Schedule         store.AgentSchedule `json:"schedule"`
	WakeReason       string              `json:"wake_reason"`
	Due              bool                `json:"due"`
	SkipReason       string              `json:"skip_reason,omitempty"`
	ProjectID        string              `json:"project_id,omitempty"`
}

type DurableAgentWakeRequest struct {
	ProjectID   string
	WakePayload DurableAgentWakePayload
}

type DurableAgentWakeResult struct {
	InstanceID    string                    `json:"instance_id"`
	ScheduleID    string                    `json:"schedule_id,omitempty"`
	WakeReason    string                    `json:"wake_reason"`
	Skipped       bool                      `json:"skipped"`
	SkipReason    string                    `json:"skip_reason,omitempty"`
	LaunchResult  *DurableAgentLaunchResult `json:"launch_result,omitempty"`
	FailureReason string                    `json:"failure_reason,omitempty"`
}

type DurableAgentWakeRunRequest struct {
	Now    time.Time
	DryRun bool
}

type DurableAgentWakeRunResult struct {
	Now     string                   `json:"now"`
	DryRun  bool                     `json:"dry_run"`
	Results []DurableAgentWakeResult `json:"results"`
}

type DurableAgentWakeService interface {
	ListDue(ctx context.Context, now time.Time) ([]DurableAgentWakeDueItem, error)
	RunDue(ctx context.Context, req DurableAgentWakeRunRequest) (*DurableAgentWakeRunResult, error)
	Wake(ctx context.Context, instanceID string, req DurableAgentWakeRequest) (*DurableAgentWakeResult, error)
	ListSchedules(ctx context.Context, instanceID string) ([]store.AgentSchedule, error)
	UpdateScheduleStatus(ctx context.Context, instanceID, scheduleID, status string) error
}

type durableWakeStore interface {
	ListDurableAgentInstances(includeArchived bool) ([]store.DurableAgentInstance, error)
	GetDurableAgentInstance(id string) (*store.DurableAgentInstance, error)
	ListAgentSchedules(ctx context.Context, agentID string) ([]store.AgentSchedule, error)
	UpdateAgentScheduleStatus(ctx context.Context, id, status string) error
	BumpAgentScheduleFireCount(ctx context.Context, id string, now time.Time) error
	ListDurableAgentInstanceSessions(instanceID string) ([]store.DurableAgentInstanceSession, error)
	GetSession(id string) (*store.Session, error)
	CreateDurableAgentEvent(event *store.DurableAgentEvent) error
	// GetAgent backs activationModeForInstance's lookup of the
	// composition-level activation_mode wakeSkipReason now reads (Phase 1
	// item 02, TASKS/phase-1/02-add-agents-composition-columns.md).
	GetAgent(id string) (*store.AgentProfile, error)
}

type durableWakeService struct {
	store   durableWakeStore
	durable DurableAgentService
}

func NewDurableAgentWakeService(st durableWakeStore, durable DurableAgentService) DurableAgentWakeService {
	return &durableWakeService{store: st, durable: durable}
}

func (s *durableWakeService) ListDue(ctx context.Context, now time.Time) ([]DurableAgentWakeDueItem, error) {
	instances, err := s.store.ListDurableAgentInstances(false)
	if err != nil {
		return nil, err
	}
	items := make([]DurableAgentWakeDueItem, 0)
	for _, inst := range instances {
		if !wakeManagedLifecycle(inst.LifecycleClass) {
			continue
		}
		schedules, err := s.store.ListAgentSchedules(ctx, inst.ProfileID)
		if err != nil {
			return nil, err
		}
		scopeSession, _ := s.resolveWakeScope(&inst)
		activationMode := s.activationModeForInstance(&inst)
		for _, schedule := range schedules {
			due, err := wakeScheduleDue(schedule, now)
			if err != nil || !due {
				continue
			}
			item := DurableAgentWakeDueItem{
				InstanceID:       inst.ID,
				InstanceName:     firstWakeNonEmpty(inst.Name, inst.Slug, inst.ID),
				LifecycleClass:   inst.LifecycleClass,
				CurrentSessionID: inst.CurrentSessionID,
				Schedule:         schedule,
				WakeReason:       wakeReasonForInstance(&inst),
				Due:              true,
			}
			if scopeSession != nil {
				item.ProjectID = scopeSession.ProjectID
			}
			if skipReason := wakeSkipReason(&inst, activationMode); skipReason != "" {
				item.SkipReason = skipReason
			}
			items = append(items, item)
		}
	}
	return items, nil
}

func (s *durableWakeService) RunDue(ctx context.Context, req DurableAgentWakeRunRequest) (*DurableAgentWakeRunResult, error) {
	now := req.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	items, err := s.ListDue(ctx, now)
	if err != nil {
		return nil, err
	}
	out := &DurableAgentWakeRunResult{
		Now:     now.Format(time.RFC3339Nano),
		DryRun:  req.DryRun,
		Results: make([]DurableAgentWakeResult, 0, len(items)),
	}
	for _, item := range items {
		result := DurableAgentWakeResult{
			InstanceID: item.InstanceID,
			ScheduleID: item.Schedule.ID,
			WakeReason: item.WakeReason,
		}
		if item.SkipReason != "" {
			result.Skipped = true
			result.SkipReason = item.SkipReason
			s.recordWakeEvent(item.InstanceID, store.DurableAgentEventWakeSkipped, "", item.SkipReason, map[string]string{"schedule_id": item.Schedule.ID})
			out.Results = append(out.Results, result)
			continue
		}
		if req.DryRun {
			out.Results = append(out.Results, result)
			continue
		}
		// CW-20260816-0021 finding: AgentSchedule.Body's doc comment
		// describes a "composer (FU-27)" that folds Body into a per-tick
		// procedure body — no such composer exists anywhere in this
		// codebase (GetDueSchedules, the API it implies, has zero
		// production call sites; only tests use it). The only real,
		// wired delivery mechanism from a scheduled agent_schedules row
		// into the woken session is DurableAgentWakePayload.Prompt,
		// which Start/Resume's deliverWakePrompt injects as a genuine
		// user turn (durable_agents.go). Forwarding Body here is what
		// makes a scheduled tick's instructions actually reach the
		// agent — general fix, not Loom-specific: it benefits any
		// class:process instance with a real schedule row, including
		// Atlas Curator whenever it gets one.
		wakeResult, err := s.Wake(ctx, item.InstanceID, DurableAgentWakeRequest{
			ProjectID:   item.ProjectID,
			WakePayload: DurableAgentWakePayload{Reason: item.WakeReason, Prompt: item.Schedule.Body},
		})
		if wakeResult != nil {
			wakeResult.ScheduleID = item.Schedule.ID
			out.Results = append(out.Results, *wakeResult)
		}
		if err != nil {
			continue
		}
		if err := s.store.BumpAgentScheduleFireCount(ctx, item.Schedule.ID, now); err == nil && item.Schedule.ScheduleKind == store.ScheduleKindOneShot {
			_ = s.store.UpdateAgentScheduleStatus(ctx, item.Schedule.ID, store.ScheduleStatusExpired)
		}
	}
	return out, nil
}

func (s *durableWakeService) Wake(ctx context.Context, instanceID string, req DurableAgentWakeRequest) (*DurableAgentWakeResult, error) {
	inst, err := s.store.GetDurableAgentInstance(instanceID)
	if err != nil {
		return nil, err
	}
	scopeSession, _ := s.resolveWakeScope(inst)
	projectID := req.ProjectID
	if projectID == "" && scopeSession != nil {
		projectID = scopeSession.ProjectID
	}
	if reason := wakeSkipReason(inst, s.activationModeForInstance(inst)); reason != "" {
		s.recordWakeEvent(inst.ID, store.DurableAgentEventWakeSkipped, inst.CurrentSessionID, reason, nil)
		return &DurableAgentWakeResult{
			InstanceID: inst.ID,
			WakeReason: req.WakePayload.Reason,
			Skipped:    true,
			SkipReason: reason,
		}, nil
	}
	payload := req.WakePayload
	if payload.Reason == "" {
		payload.Reason = wakeReasonForInstance(inst)
	}
	s.recordWakeEvent(inst.ID, store.DurableAgentEventWakeRequested, inst.CurrentSessionID, "", map[string]string{"reason": payload.Reason})
	s.recordWakeEvent(inst.ID, store.DurableAgentEventWakeStarted, inst.CurrentSessionID, "", map[string]string{"reason": payload.Reason})
	startReq := DurableAgentStartRequest{
		ProjectID:   projectID,
		WakePayload: payload,
	}
	launchResult, err := s.durable.Start(ctx, inst.ID, startReq)
	if err != nil {
		s.recordWakeEvent(inst.ID, store.DurableAgentEventWakeFailed, inst.CurrentSessionID, err.Error(), map[string]string{"reason": payload.Reason})
		return &DurableAgentWakeResult{
			InstanceID:    inst.ID,
			WakeReason:    payload.Reason,
			FailureReason: err.Error(),
		}, err
	}
	return &DurableAgentWakeResult{
		InstanceID:   inst.ID,
		WakeReason:   payload.Reason,
		LaunchResult: launchResult,
	}, nil
}

func (s *durableWakeService) ListSchedules(ctx context.Context, instanceID string) ([]store.AgentSchedule, error) {
	inst, err := s.store.GetDurableAgentInstance(instanceID)
	if err != nil {
		return nil, err
	}
	return s.store.ListAgentSchedules(ctx, inst.ProfileID)
}

func (s *durableWakeService) UpdateScheduleStatus(ctx context.Context, instanceID, scheduleID, status string) error {
	if _, err := s.store.GetDurableAgentInstance(instanceID); err != nil {
		return err
	}
	return s.store.UpdateAgentScheduleStatus(ctx, scheduleID, status)
}

func (s *durableWakeService) resolveWakeScope(inst *store.DurableAgentInstance) (*store.Session, error) {
	if inst.CurrentSessionID != "" {
		if sess, err := s.store.GetSession(inst.CurrentSessionID); err == nil && sess != nil {
			return sess, nil
		}
	}
	rels, err := s.store.ListDurableAgentInstanceSessions(inst.ID)
	if err != nil {
		return nil, err
	}
	for _, rel := range rels {
		if rel.DetachedAt != nil {
			continue
		}
		sess, err := s.store.GetSession(rel.SessionID)
		if err == nil && sess != nil {
			return sess, nil
		}
	}
	return nil, nil
}

func wakeManagedLifecycle(lifecycle string) bool {
	return lifecycle == store.DurableAgentClassProcess || lifecycle == store.DurableAgentClassTemplate
}

func wakeReasonForInstance(inst *store.DurableAgentInstance) string {
	if inst != nil && inst.LifecycleClass == store.DurableAgentClassProcess {
		return DurableAgentWakeProcessTick
	}
	return DurableAgentWakeScheduled
}

// activationModeForInstance resolves the agent_profiles.activation_mode
// value for a durable-agent instance's bound composition (Phase 1 item 02,
// TASKS/phase-1/02-add-agents-composition-columns.md — wakeSkipReason's
// real, previously-nonexistent consumer for this column). Returns "" on any
// lookup failure (nil instance, empty ProfileID, unknown profile, store
// error) so wakeAllowsConcurrentActive's default case (block) applies —
// the same conservative fallback GetRole/roleForProfile use elsewhere in
// this task.
func (s *durableWakeService) activationModeForInstance(inst *store.DurableAgentInstance) string {
	if inst == nil || inst.ProfileID == "" {
		return ""
	}
	profile, err := s.store.GetAgent(inst.ProfileID)
	if err != nil || profile == nil {
		return ""
	}
	return profile.ActivationMode
}

// wakeAllowsConcurrentActive reports whether the given agent_profiles.
// activation_mode value permits waking an instance that's already Active.
// Every value except the blocking default ("singleton", or an empty/
// unrecognized value, which resolves to the same default) allows it.
func wakeAllowsConcurrentActive(activationMode string) bool {
	switch activationMode {
	case "fresh-per-wake", "concurrent":
		return true
	default:
		return false
	}
}

// wakeSkipReason returns why a wake should be skipped, or "" to proceed.
// activationMode is the bound composition's agent_profiles.activation_mode
// (resolved by callers via activationModeForInstance) — the real, explicit
// column Phase 1 item 02 added for exactly this decision, retiring the
// hardcoded lifecycle_class special case this function used before.
//
// Phase 0 item 20 (retire workspaces): this used to also gate on a
// workspaceID parameter — "workspace unavailable" when no workspace could
// be resolved for the wake. sessions.workspace_id (and the requirement it
// backed) is retired in full; a wake no longer needs a workspace to create
// a session.
//
// CW-20260817 finding: nothing anywhere in this codebase ever transitions a
// durable_agent_instances row back out of "active" once Start() sets it —
// there is no completion hook from chat's async generation (launchGeneration,
// chat.go) back into durable_agent_instances.status. For a 'singleton'
// composition (SessionPolicyReuseLatestOrCreate / ReuseManaged) that's fine:
// "active" genuinely means "has a live, reusable session", which stays true
// indefinitely. But a 'fresh-per-wake'/'concurrent' composition spins up its
// own independent session on every wake (SessionPolicyFreshPerWake for
// process class, SessionPolicyFreshOneShot for template — see
// durableAgentLaunchPolicyFor), so there is no session-reuse collision for
// "active" to be guarding against. Treating it as a permanent block meant
// every such agent (Loom Curator, Atlas Curator, Torque Supervisor, and —
// under the pre-migration-116 lifecycle_class-only check, which this
// rewrite closes — content-writer/task-planner too, a latent instance of
// the same bug the original CW-20260817 fix never covered since it only
// exempted lifecycle_class == process) was wakeable exactly once, ever: the
// very first wake (callback or scheduled) set status to Active and no later
// wake — including CW-20260816-0021's own daily scheduled tick — could ever
// fire again. Only the genuinely in-flight launch states (Starting/
// StartRequested/ResumeRequested, which Start() only holds for the duration
// of the synchronous session-creation section) still guard against a real
// concurrent-launch race, unconditionally, regardless of activation_mode.
func wakeSkipReason(inst *store.DurableAgentInstance, activationMode string) string {
	if inst == nil {
		return "instance missing"
	}
	switch inst.Status {
	case store.DurableAgentStatusArchived:
		return "instance archived"
	case store.DurableAgentStatusPaused:
		return "instance paused"
	case store.DurableAgentStatusStopped:
		return "instance stopped"
	case store.DurableAgentStatusActive:
		if !wakeAllowsConcurrentActive(activationMode) {
			return "wake already active"
		}
	case store.DurableAgentStatusStarting, store.DurableAgentStatusStartRequested, store.DurableAgentStatusResumeRequested:
		return "wake already active"
	}
	return ""
}

func wakeScheduleDue(schedule store.AgentSchedule, now time.Time) (bool, error) {
	if schedule.Status != store.ScheduleStatusActive {
		return false, nil
	}
	if schedule.ExpiresAt != "" {
		if expiresAt, err := time.Parse(time.RFC3339, schedule.ExpiresAt); err == nil && !expiresAt.After(now) {
			return false, nil
		}
	}
	switch schedule.ScheduleKind {
	case store.ScheduleKindCron:
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		parsed, err := parser.Parse(schedule.ScheduleSpec)
		if err != nil {
			return false, err
		}
		ref := now.Add(-15 * time.Minute)
		if schedule.LastFiredAt != "" {
			if t, err := time.Parse(time.RFC3339, schedule.LastFiredAt); err == nil {
				ref = t
			}
		} else if schedule.CreatedAt != "" {
			if t, err := time.Parse(time.RFC3339, schedule.CreatedAt); err == nil {
				ref = t
			}
		}
		return !parsed.Next(ref).After(now), nil
	case store.ScheduleKindOneShot:
		return schedule.FiredCount == 0, nil
	default:
		return false, nil
	}
}

func (s *durableWakeService) recordWakeEvent(instanceID, eventType, sessionID, message string, metadata map[string]string) {
	_ = s.store.CreateDurableAgentEvent(&store.DurableAgentEvent{
		InstanceID:   instanceID,
		EventType:    eventType,
		SessionID:    sessionID,
		Message:      message,
		MetadataJSON: durableAgentEventMetadata(metadata),
	})
}

func firstWakeNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
