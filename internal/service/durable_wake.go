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
	WorkspaceID      string              `json:"workspace_id,omitempty"`
	ProjectID        string              `json:"project_id,omitempty"`
}

type DurableAgentWakeRequest struct {
	WorkspaceID string
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
		scopeWorkspaceID := ""
		if scopeSession != nil {
			scopeWorkspaceID = scopeSession.WorkspaceID
		}
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
				item.WorkspaceID = scopeSession.WorkspaceID
				item.ProjectID = scopeSession.ProjectID
			}
			// The scheduled-tick path (this one) has no external caller
			// supplying a WorkspaceID, so the skip check here is scoped
			// purely to prior-session state — unlike Wake() below, which
			// also honors a caller-supplied WorkspaceID.
			if skipReason := wakeSkipReason(&inst, scopeWorkspaceID); skipReason != "" {
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
			WorkspaceID: item.WorkspaceID,
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
	workspaceID := req.WorkspaceID
	projectID := req.ProjectID
	if workspaceID == "" && scopeSession != nil {
		workspaceID = scopeSession.WorkspaceID
		projectID = scopeSession.ProjectID
	}
	// CW-20260816-0020 finding: a fresh durable-agent instance has no prior
	// session, so scopeSession is always nil on its very first-ever wake.
	// The skip check must honor an explicit caller-supplied WorkspaceID
	// (already folded into workspaceID above) in that case, not just a
	// pre-existing session's — otherwise the first callback/API wake of any
	// newly-seeded process-class instance always skips with "workspace
	// unavailable", even when the caller passed one. This generalizes past
	// Loom Curator to any durable-agent instance woken for the first time
	// with an explicit WorkspaceID.
	if reason := wakeSkipReason(inst, workspaceID); reason != "" {
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
		WorkspaceID: workspaceID,
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
		if sess, err := s.store.GetSession(inst.CurrentSessionID); err == nil && sess != nil && sess.WorkspaceID != "" {
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
		if err == nil && sess != nil && sess.WorkspaceID != "" {
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

// wakeSkipReason returns why a wake should be skipped, or "" to proceed.
// workspaceID is the value that will actually be handed to
// DurableAgentService.Start (a caller-supplied WorkspaceID, or one inherited
// from a prior scoped session — see call sites) so this check reflects
// reality: an instance with no prior session can still wake successfully if
// the caller supplied a WorkspaceID explicitly.
//
// CW-20260817 finding: nothing anywhere in this codebase ever transitions a
// durable_agent_instances row back out of "active" once Start() sets it —
// there is no completion hook from chat's async generation (launchGeneration,
// chat.go) back into durable_agent_instances.status. For an
// advisor/harness-class instance (SessionPolicyReuseLatestOrCreate /
// ReuseManaged) that's fine: "active" genuinely means "has a live, reusable
// session", which stays true indefinitely. But a process-class instance uses
// SessionPolicyFreshPerWake (durableAgentLaunchPolicyFor) — every wake spins
// up its own independent session, so there is no session-reuse collision for
// "active" to be guarding against. Treating it as a permanent block meant
// every process-class agent (Loom Curator, Atlas Curator, Torque Supervisor
// — grep class: process under .nanite/agents/) was wakeable exactly once,
// ever: the very first wake (callback or scheduled) set status to Active and
// no later wake — including CW-20260816-0021's own daily scheduled tick —
// could ever fire again. Only the genuinely in-flight launch states
// (Starting/StartRequested/ResumeRequested, which Start() only holds for the
// duration of the synchronous session-creation section) still guard against
// a real concurrent-launch race.
func wakeSkipReason(inst *store.DurableAgentInstance, workspaceID string) string {
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
		if inst.LifecycleClass != store.DurableAgentClassProcess {
			return "wake already active"
		}
	case store.DurableAgentStatusStarting, store.DurableAgentStatusStartRequested, store.DurableAgentStatusResumeRequested:
		return "wake already active"
	}
	if workspaceID == "" {
		return "workspace unavailable"
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
