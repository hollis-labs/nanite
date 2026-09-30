package api

import "github.com/hollis-labs/nanite/internal/store"

// AgentScheduleView is the API-owned wire shape of an agent_schedules row.
// Its keys match what store.AgentSchedule used to emit directly, so the wire
// did not change when the row stopped riding onto it; a new column reaches
// the wire only once it is added here and to agentScheduleToView.
// TestAgentScheduleViewJSONKeys pins the key set.
type AgentScheduleView struct {
	ID           string `json:"id"`
	AgentID      string `json:"agent_id"`
	SessionID    string `json:"session_id"`
	Name         string `json:"name"`
	ScheduleKind string `json:"schedule_kind"`
	ScheduleSpec string `json:"schedule_spec"`
	Body         string `json:"body"`
	Priority     int64  `json:"priority"`
	Status       string `json:"status"`
	ExpiresAt    string `json:"expires_at"`
	FiredCount   int64  `json:"fired_count"`
	LastFiredAt  string `json:"last_fired_at"`
	CreatedAt    string `json:"created_at"`
	CreatedBy    string `json:"created_by"`
	MaxRetries   int64  `json:"max_retries"`
	OnFail       string `json:"on_fail"`
	NextRun      string `json:"next_run"`
	JobType      string `json:"job_type"`
	JobPayload   string `json:"job_payload"`
}

func agentScheduleToView(s *store.AgentSchedule) AgentScheduleView {
	return AgentScheduleView{
		ID:           s.ID,
		AgentID:      s.AgentID,
		SessionID:    s.SessionID,
		Name:         s.Name,
		ScheduleKind: s.ScheduleKind,
		ScheduleSpec: s.ScheduleSpec,
		Body:         s.Body,
		Priority:     s.Priority,
		Status:       s.Status,
		ExpiresAt:    s.ExpiresAt,
		FiredCount:   s.FiredCount,
		LastFiredAt:  s.LastFiredAt,
		CreatedAt:    s.CreatedAt,
		CreatedBy:    s.CreatedBy,
		MaxRetries:   s.MaxRetries,
		OnFail:       s.OnFail,
		NextRun:      s.NextRun,
		JobType:      s.JobType,
		JobPayload:   s.JobPayload,
	}
}

// agentSchedulesToView always returns a non-nil slice: the store lists
// return an empty slice, never nil, so an empty list has always been [].
func agentSchedulesToView(rows []store.AgentSchedule) []AgentScheduleView {
	out := make([]AgentScheduleView, 0, len(rows))
	for i := range rows {
		out = append(out, agentScheduleToView(&rows[i]))
	}
	return out
}
