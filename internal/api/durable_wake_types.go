package api

import "github.com/hollis-labs/nanite/internal/service"

type DurableAgentWakeDueItemView struct {
	InstanceID       string            `json:"instance_id"`
	InstanceName     string            `json:"instance_name"`
	LifecycleClass   string            `json:"lifecycle_class"`
	CurrentSessionID string            `json:"current_session_id"`
	Schedule         AgentScheduleView `json:"schedule"`
	WakeReason       string            `json:"wake_reason"`
	Due              bool              `json:"due"`
	SkipReason       string            `json:"skip_reason,omitempty"`
	ProjectID        string            `json:"project_id,omitempty"`
}

func durableAgentWakeDueItemToView(r *service.DurableAgentWakeDueItem) *DurableAgentWakeDueItemView {
	if r == nil {
		return nil
	}
	return &DurableAgentWakeDueItemView{
		InstanceID:       r.InstanceID,
		InstanceName:     r.InstanceName,
		LifecycleClass:   r.LifecycleClass,
		CurrentSessionID: r.CurrentSessionID,
		Schedule:         agentScheduleToView(&r.Schedule),
		WakeReason:       r.WakeReason,
		Due:              r.Due,
		SkipReason:       r.SkipReason,
		ProjectID:        r.ProjectID,
	}
}
func durableAgentWakeDueItemToViews(rows []service.DurableAgentWakeDueItem) []DurableAgentWakeDueItemView {
	if rows == nil {
		return nil
	}
	out := make([]DurableAgentWakeDueItemView, len(rows))
	for i := range rows {
		out[i] = *durableAgentWakeDueItemToView(&rows[i])
	}
	return out
}

type DurableAgentWakeResultView struct {
	InstanceID    string                        `json:"instance_id"`
	ScheduleID    string                        `json:"schedule_id,omitempty"`
	WakeReason    string                        `json:"wake_reason"`
	Skipped       bool                          `json:"skipped"`
	SkipReason    string                        `json:"skip_reason,omitempty"`
	LaunchResult  *DurableAgentLaunchResultView `json:"launch_result,omitempty"`
	FailureReason string                        `json:"failure_reason,omitempty"`
}

func durableAgentWakeResultToView(r *service.DurableAgentWakeResult) *DurableAgentWakeResultView {
	if r == nil {
		return nil
	}
	return &DurableAgentWakeResultView{
		InstanceID:    r.InstanceID,
		ScheduleID:    r.ScheduleID,
		WakeReason:    r.WakeReason,
		Skipped:       r.Skipped,
		SkipReason:    r.SkipReason,
		LaunchResult:  durableAgentLaunchResultToView(r.LaunchResult),
		FailureReason: r.FailureReason,
	}
}
func durableAgentWakeResultToViews(rows []service.DurableAgentWakeResult) []DurableAgentWakeResultView {
	if rows == nil {
		return nil
	}
	out := make([]DurableAgentWakeResultView, len(rows))
	for i := range rows {
		out[i] = *durableAgentWakeResultToView(&rows[i])
	}
	return out
}

type DurableAgentWakeRunResultView struct {
	Now     string                       `json:"now"`
	DryRun  bool                         `json:"dry_run"`
	Results []DurableAgentWakeResultView `json:"results"`
}

func durableAgentWakeRunResultToView(r *service.DurableAgentWakeRunResult) *DurableAgentWakeRunResultView {
	if r == nil {
		return nil
	}
	return &DurableAgentWakeRunResultView{
		Now:     r.Now,
		DryRun:  r.DryRun,
		Results: durableAgentWakeResultToViews(r.Results),
	}
}
