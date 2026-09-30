package api

import "github.com/hollis-labs/nanite/internal/store"

// TeamView is a team definition as the API returns it. Field order is the
// store row's.
type TeamView struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	SlotsJSON     string `json:"slots_json"`
	AuthorityJSON string `json:"authority_json"`
	RoutingJSON   string `json:"routing_json"`
	PhasesJSON    string `json:"phases_json"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
	CreatedBy     string `json:"created_by"`
}

func teamToView(t *store.Team) TeamView {
	return TeamView{
		ID:            t.ID,
		Name:          t.Name,
		Description:   t.Description,
		SlotsJSON:     t.SlotsJSON,
		AuthorityJSON: t.AuthorityJSON,
		RoutingJSON:   t.RoutingJSON,
		PhasesJSON:    t.PhasesJSON,
		CreatedAt:     t.CreatedAt,
		UpdatedAt:     t.UpdatedAt,
		CreatedBy:     t.CreatedBy,
	}
}

// teamsToView keeps nil as nil and an empty list as [].
func teamsToView(teams []store.Team) []TeamView {
	if teams == nil {
		return nil
	}
	out := make([]TeamView, len(teams))
	for i := range teams {
		out[i] = teamToView(&teams[i])
	}
	return out
}

// TeamRunMemberView is a resolved team-run member as the API returns it.
// Field order is the store row's.
type TeamRunMemberView struct {
	ID            string `json:"id"`
	WorkflowRunID string `json:"workflow_run_id"`
	SlotName      string `json:"slot_name"`
	AgentID       string `json:"agent_id"`
	SessionID     string `json:"session_id"`
	ResolvedAt    string `json:"resolved_at"`
	Status        string `json:"status"`
}

// teamRunMembersToView keeps nil as nil and an empty list as [].
func teamRunMembersToView(members []store.TeamRunMember) []TeamRunMemberView {
	if members == nil {
		return nil
	}
	out := make([]TeamRunMemberView, len(members))
	for i, m := range members {
		out[i] = TeamRunMemberView{
			ID:            m.ID,
			WorkflowRunID: m.WorkflowRunID,
			SlotName:      m.SlotName,
			AgentID:       m.AgentID,
			SessionID:     m.SessionID,
			ResolvedAt:    m.ResolvedAt,
			Status:        m.Status,
		}
	}
	return out
}
