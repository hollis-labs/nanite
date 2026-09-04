package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/hollis-labs/nanite/internal/dispatcher"
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
	ListDurableAgentInstances(ctx context.Context, includeArchived bool) ([]store.DurableAgentInstance, error)
	GetDurableAgentInstance(ctx context.Context, id string) (*store.DurableAgentInstance, error)
	ListAgentSchedules(ctx context.Context, agentID string) ([]store.AgentSchedule, error)
	UpdateAgentScheduleStatus(ctx context.Context, id, status string) error
	BumpAgentScheduleFireCount(ctx context.Context, id string, now time.Time) error
	ListDurableAgentInstanceSessions(ctx context.Context, instanceID string) ([]store.DurableAgentInstanceSession, error)
	GetSession(ctx context.Context, id string) (*store.Session, error)
	CreateDurableAgentEvent(ctx context.Context, event *store.DurableAgentEvent) error
	// GetAgent backs activationModeForInstance's lookup of the
	// composition-level activation_mode wakeSkipReason now reads (Phase 1
	// item 02, TASKS/phase-1/02-add-agents-composition-columns.md).
	GetAgent(ctx context.Context, id string) (*store.AgentProfile, error)
}

type durableWakeService struct {
	store   durableWakeStore
	durable DurableAgentService
}

func NewDurableAgentWakeService(st durableWakeStore, durable DurableAgentService) DurableAgentWakeService {
	return &durableWakeService{store: st, durable: durable}
}

func (s *durableWakeService) ListDue(ctx context.Context, now time.Time) ([]DurableAgentWakeDueItem, error) {
	instances, err := s.store.ListDurableAgentInstances(ctx, false)
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
			if !scheduleDueByNextRun(schedule, now) {
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

// RunDue is a manual "fire everything due right now" surface — real,
// still-live callers: POST /api/durable-agent-wake/run-due
// (internal/api/durable_agent_wake.go) and its "Run due wake pass" button
// in the durable-agent admin panel (ui/src/components/settings/
// DurableAgentAdminPanel.tsx). It predates and is independent of the
// automatic dispatch mechanism: originally the *only* dispatch mechanism
// (per this file's own CW-20260816-0005 history), then also driven by a
// 2-minute background ticker (cmd/nanite/main.go's now-removed
// "durable-agent-wake-tick"), and as of TASKS/scheduling/
// 05-engine-wiring-and-full-replace.md, superseded for automatic dispatch
// by the go-scheduler.Engine wired into cmd/nanite/main.go — which claims
// and fires due schedules on its own 1-second tick via atomic durable Fire
// materialization (internal/scheduler.StoreAdapter.CreateFire), advancing
// next_run in the same transaction. RunDue is kept as a manual operator convenience,
// not retired, because it's a real, still-used admin-panel affordance (not
// a dead/undocumented surface); it is deliberately NOT repointed onto the
// Engine's own claim path (e.g. Engine.TickNow), because doing so would
// drop this method's per-item Results/DryRun/SkipReason reporting that the
// admin panel and this service's own tests rely on.
//
// This does mean RunDue's own dispatch (via Wake, below) does not go
// through the Engine's CAS claim and does not itself advance next_run —
// accepted, not a new correctness gap this task introduces: even before
// the Engine existed, both the old ticker and this manual endpoint called
// Wake directly with no coordination between them, and Wake's own
// per-instance "already active" skip (wakeSkipReason) was always the sole
// protection against a genuine double-fire. In practice, since the Engine
// now ticks every 1 second, a manually-triggered RunDue call on an
// already-due cron schedule will very likely race the Engine's own next
// tick for the same row; whichever call wins gets the real dispatch, the
// other observes the instance already Active and skips gracefully (a
// normal, logged "wake already active" skip event, not an error). A
// one_shot schedule cannot double-fire this way: RunDue sets its status to
// expired directly on success, which excludes it from the Engine's own
// ListDueSchedules query (status='active' filter) immediately, regardless
// of next_run.
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
			// Phase 4 task 04: this skip short-circuits before ever
			// calling Wake (a ListDue-precomputed skip, avoiding a
			// wasted Wake call for a known-skippable item in a bulk
			// sweep) — log the same shared outcome shape Wake itself
			// reports below so a skip is visible under "agent-run:
			// outcome" regardless of which of RunDue's two skip paths
			// caught it.
			dispatcher.LogOutcome(dispatcher.AgentRunResult{
				CallerType:      dispatcher.CallerBackground,
				Completion:      dispatcher.CompletionQueued,
				TargetSessionID: item.CurrentSessionID,
				Status:          dispatcher.RunStatusSkipped,
				Err:             errors.New(item.SkipReason),
			})
			out.Results = append(out.Results, result)
			continue
		}
		if req.DryRun {
			out.Results = append(out.Results, result)
			continue
		}
		// CW-20260816-0021 finding: AgentSchedule.Body's doc comment
		// used to describe a "composer (FU-27)" that folds Body into a
		// per-tick procedure body — no such composer ever existed in this
		// codebase (GetDueSchedules, the API it implied, had zero
		// production call sites and was removed outright by
		// TASKS/scheduling/01-schema-schedule-kind-collapse-and-retry-
		// columns.md). The only real,
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
			if err := s.store.UpdateAgentScheduleStatus(ctx, item.Schedule.ID, store.ScheduleStatusExpired); err != nil {
				slog.Warn("durable wake: failed to expire one-shot schedule",
					"schedule_id", item.Schedule.ID,
					"instance_id", item.InstanceID,
					"err", err,
				)
			}
		}
	}
	return out, nil
}

// Wake is durable-agent wake's real entry point (surface C of Phase 4
// task 04's three run-another-agent surfaces). Unlike REST delegation
// and LLM-triggered subagent dispatch, it fundamentally cannot
// synchronously drain the dispatched turn — Start/Resume queue the
// wake prompt via deliverWakePrompt and return once the turn is
// merely queued, not once it finishes (chat.HandleMessage's
// launchGeneration runs the actual turn in its own goroutine). Every
// AgentRunResult this method reports therefore uses
// dispatcher.CompletionQueued, and a successful wake is honestly
// dispatcher.RunStatusQueued rather than borrowing
// durable_agent_instances.status=Active (which means "has a live,
// reusable session", not "this turn completed" — see wakeSkipReason's
// doc comment for the documented gap around that distinction). This
// is the "give the unified result type an honest, non-misleading
// status" resolution Phase 4 task 04 calls for: representing "queued,
// poll status separately" as the legitimate terminal shape for this
// surface rather than attempting a full durable-agent lifecycle
// completion-tracking redesign, which is out of this task's scope.
func (s *durableWakeService) Wake(ctx context.Context, instanceID string, req DurableAgentWakeRequest) (*DurableAgentWakeResult, error) {
	inst, err := s.store.GetDurableAgentInstance(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	scopeSession, _ := s.resolveWakeScope(inst)
	projectID := req.ProjectID
	if projectID == "" && scopeSession != nil {
		projectID = scopeSession.ProjectID
	}
	// Phase 4 task 04: the shared AgentRunRequest for this surface.
	// TargetSessionID is unknown until Start/Resume resolves (or
	// reuses) a session below, so it's filled in on each AgentRunResult
	// individually rather than carried on runReq itself.
	runReq := dispatcher.AgentRunRequest{
		CallerType: dispatcher.CallerBackground,
		Completion: dispatcher.CompletionQueued,
		Prompt:     req.WakePayload.Prompt,
	}
	if reason := wakeSkipReason(inst, s.activationModeForInstance(inst)); reason != "" {
		s.recordWakeEvent(inst.ID, store.DurableAgentEventWakeSkipped, inst.CurrentSessionID, reason, nil)
		dispatcher.LogOutcome(dispatcher.AgentRunResult{
			CallerType:      runReq.CallerType,
			Completion:      runReq.Completion,
			TargetSessionID: inst.CurrentSessionID,
			Status:          dispatcher.RunStatusSkipped,
			Err:             errors.New(reason),
		})
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
		dispatcher.LogOutcome(dispatcher.AgentRunResult{
			CallerType:      runReq.CallerType,
			Completion:      runReq.Completion,
			TargetSessionID: inst.CurrentSessionID,
			Status:          dispatcher.RunStatusFailed,
			Err:             err,
		})
		return &DurableAgentWakeResult{
			InstanceID:    inst.ID,
			WakeReason:    payload.Reason,
			FailureReason: err.Error(),
		}, err
	}
	targetSessionID := inst.CurrentSessionID
	if launchResult != nil && launchResult.Session != nil {
		targetSessionID = launchResult.Session.ID
	}
	dispatcher.LogOutcome(dispatcher.AgentRunResult{
		CallerType:      runReq.CallerType,
		Completion:      runReq.Completion,
		TargetSessionID: targetSessionID,
		Status:          dispatcher.RunStatusQueued,
	})
	return &DurableAgentWakeResult{
		InstanceID:   inst.ID,
		WakeReason:   payload.Reason,
		LaunchResult: launchResult,
	}, nil
}

func (s *durableWakeService) ListSchedules(ctx context.Context, instanceID string) ([]store.AgentSchedule, error) {
	inst, err := s.store.GetDurableAgentInstance(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	return s.store.ListAgentSchedules(ctx, inst.ProfileID)
}

func (s *durableWakeService) UpdateScheduleStatus(ctx context.Context, instanceID, scheduleID, status string) error {
	if _, err := s.store.GetDurableAgentInstance(ctx, instanceID); err != nil {
		return err
	}
	return s.store.UpdateAgentScheduleStatus(ctx, scheduleID, status)
}

func (s *durableWakeService) resolveWakeScope(inst *store.DurableAgentInstance) (*store.Session, error) {
	if inst.CurrentSessionID != "" {
		if sess, err := s.store.GetSession(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, inst.CurrentSessionID); err == nil && sess != nil {
			return sess, nil
		}
	}
	rels, err := s.store.ListDurableAgentInstanceSessions(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, inst.ID)
	if err != nil {
		return nil, err
	}
	for _, rel := range rels {
		if rel.DetachedAt != nil {
			continue
		}
		sess, err := s.store.GetSession(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, rel.SessionID)
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
	profile, err := s.store.GetAgent(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, inst.ProfileID)
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

// scheduleDueByNextRun reports whether schedule is due at now, per its
// persisted next_run column (added by TASKS/scheduling/
// 01-schema-schedule-kind-collapse-and-retry-columns.md's migration 127).
//
// Replaces wakeScheduleDue (removed by TASKS/scheduling/
// 05-engine-wiring-and-full-replace.md) — that function recomputed due-ness
// from schedule_spec on every call, cron-kind rows via a 15-minute lookback
// window from last_fired_at/created_at as a heuristic substitute for real
// dispatch tracking (docs/engineering/architecture/12-scheduling.md's "Full
// replace, not dual-run" section names this exact fragility as what the
// go-scheduler.Engine's CAS-claim mechanism retires). next_run is now the
// single real source of truth for "when is this schedule next due" —
// go-scheduler.Engine's own Store adapter (internal/scheduler.StoreAdapter)
// is the sole writer of this column once a schedule has fired at least once
// under the new engine (CreateFire); this
// function only ever reads it, exactly like the engine's own
// ListDueSchedules query (internal/store/agent_schedules.go's
// ListDueAgentSchedules) does — same due-ness definition, no second
// independent due-check.
//
// ListDue/RunDue's own callers (the GET /api/durable-agent-wake/due
// introspection endpoint and the POST .../run-due manual-trigger endpoint,
// both real and still live per that task's Work Log) keep working on this
// same field the automatic Engine maintains, rather than a second,
// independently-computed notion of due-ness.
func scheduleDueByNextRun(schedule store.AgentSchedule, now time.Time) bool {
	if schedule.Status != store.ScheduleStatusActive {
		return false
	}
	if schedule.ExpiresAt != "" {
		if expiresAt, err := time.Parse(time.RFC3339, schedule.ExpiresAt); err == nil && !expiresAt.After(now) {
			return false
		}
	}
	if schedule.NextRun == "" {
		return false
	}
	nextRun, err := time.Parse(time.RFC3339, schedule.NextRun)
	if err != nil {
		return false
	}
	return !nextRun.After(now)
}

func (s *durableWakeService) recordWakeEvent(instanceID, eventType, sessionID, message string, metadata map[string]string) {
	_ = s.store.CreateDurableAgentEvent(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, &store.DurableAgentEvent{
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
