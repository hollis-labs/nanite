package api

import "github.com/hollis-labs/nanite/internal/store"

// The capability views are the API-owned wire shapes of an agent's known
// tools, known skills, procedures and knowledge seeds. Their keys match what
// the store rows used to emit directly, so the wire did not change when the
// rows stopped riding onto it; a new column reaches the wire only once it is
// added here and to its translator. TestAgentCapabilityViewJSONKeys pins the
// key sets.
//
// The list translators always return a non-nil slice: the store lists return
// an empty slice, never nil, so an empty list has always serialized as [].

// AgentKnownToolView is the wire shape of one known-tool row.
type AgentKnownToolView struct {
	AgentID         string `json:"agent_id"`
	ToolName        string `json:"tool_name"`
	Pinned          bool   `json:"pinned"`
	SortOrder       int64  `json:"sort_order"`
	ActivationCount int64  `json:"activation_count"`
	LastUsedAt      string `json:"last_used_at"`
	AddedAt         string `json:"added_at"`
	TTLSeconds      int64  `json:"ttl_seconds"`
	Reason          string `json:"reason"`
}

func knownToolToView(r *store.AgentKnownTool) AgentKnownToolView {
	return AgentKnownToolView{
		AgentID:         r.AgentID,
		ToolName:        r.ToolName,
		Pinned:          r.Pinned,
		SortOrder:       r.SortOrder,
		ActivationCount: r.ActivationCount,
		LastUsedAt:      r.LastUsedAt,
		AddedAt:         r.AddedAt,
		TTLSeconds:      r.TTLSeconds,
		Reason:          r.Reason,
	}
}

func knownToolsToView(rows []store.AgentKnownTool) []AgentKnownToolView {
	out := make([]AgentKnownToolView, 0, len(rows))
	for i := range rows {
		out = append(out, knownToolToView(&rows[i]))
	}
	return out
}

// AgentKnownSkillView is the wire shape of one known-skill row, including its
// grant state.
type AgentKnownSkillView struct {
	AgentID             string `json:"agent_id"`
	SkillName           string `json:"skill_name"`
	Pinned              bool   `json:"pinned"`
	ActivationCount     int64  `json:"activation_count"`
	LastUsedAt          string `json:"last_used_at"`
	AddedAt             string `json:"added_at"`
	TTLSeconds          int64  `json:"ttl_seconds"`
	Reason              string `json:"reason"`
	ApprovedContentHash string `json:"approved_content_hash"`
	GrantedAt           string `json:"granted_at"`
	GrantedBy           string `json:"granted_by"`
	CapabilitiesGranted string `json:"capabilities_granted"`
}

func knownSkillToView(r *store.AgentKnownSkill) AgentKnownSkillView {
	return AgentKnownSkillView{
		AgentID:             r.AgentID,
		SkillName:           r.SkillName,
		Pinned:              r.Pinned,
		ActivationCount:     r.ActivationCount,
		LastUsedAt:          r.LastUsedAt,
		AddedAt:             r.AddedAt,
		TTLSeconds:          r.TTLSeconds,
		Reason:              r.Reason,
		ApprovedContentHash: r.ApprovedContentHash,
		GrantedAt:           r.GrantedAt,
		GrantedBy:           r.GrantedBy,
		CapabilitiesGranted: r.CapabilitiesGranted,
	}
}

func knownSkillsToView(rows []store.AgentKnownSkill) []AgentKnownSkillView {
	out := make([]AgentKnownSkillView, 0, len(rows))
	for i := range rows {
		out = append(out, knownSkillToView(&rows[i]))
	}
	return out
}

// AgentProcedureView is the wire shape of one procedure row.
type AgentProcedureView struct {
	AgentID   string `json:"agent_id"`
	Name      string `json:"name"`
	Body      string `json:"body"`
	Scope     string `json:"scope"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func procedureToView(r *store.AgentProcedure) AgentProcedureView {
	return AgentProcedureView{
		AgentID:   r.AgentID,
		Name:      r.Name,
		Body:      r.Body,
		Scope:     r.Scope,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
}

func proceduresToView(rows []store.AgentProcedure) []AgentProcedureView {
	out := make([]AgentProcedureView, 0, len(rows))
	for i := range rows {
		out = append(out, procedureToView(&rows[i]))
	}
	return out
}

// AgentKnowledgeSeedView is the wire shape of one knowledge-seed row.
type AgentKnowledgeSeedView struct {
	AgentID   string `json:"agent_id"`
	SeedKey   string `json:"seed_key"`
	Namespace string `json:"namespace"`
	Body      string `json:"body"`
	TagsJSON  string `json:"tags_json"`
	AppliedAt string `json:"applied_at"`
	CreatedAt string `json:"created_at"`
}

func knowledgeSeedToView(r *store.AgentKnowledgeSeed) AgentKnowledgeSeedView {
	return AgentKnowledgeSeedView{
		AgentID:   r.AgentID,
		SeedKey:   r.SeedKey,
		Namespace: r.Namespace,
		Body:      r.Body,
		TagsJSON:  r.TagsJSON,
		AppliedAt: r.AppliedAt,
		CreatedAt: r.CreatedAt,
	}
}

func knowledgeSeedsToView(rows []store.AgentKnowledgeSeed) []AgentKnowledgeSeedView {
	out := make([]AgentKnowledgeSeedView, 0, len(rows))
	for i := range rows {
		out = append(out, knowledgeSeedToView(&rows[i]))
	}
	return out
}
