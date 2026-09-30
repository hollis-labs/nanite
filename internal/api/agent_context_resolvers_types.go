package api

import "github.com/hollis-labs/nanite/internal/store"

// ContextResolverView is an agent context resolver as the API returns it.
// Field order is the store row's. headers_json and run are returned as
// stored (CW-20260930-0186 decides whether headers should be redacted).
type ContextResolverView struct {
	ID             string `json:"id"`
	AgentID        string `json:"agent_id"`
	SlotName       string `json:"slot_name"`
	Kind           string `json:"kind"`
	Run            string `json:"run"`
	CWD            string `json:"cwd"`
	Timeout        string `json:"timeout"`
	URL            string `json:"url"`
	HeadersJSON    string `json:"headers_json"`
	ResponseFormat string `json:"response_format"`
	JSONPath       string `json:"json_path"`
	Enabled        bool   `json:"enabled"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

func contextResolverToView(r *store.AgentContextResolver) ContextResolverView {
	return ContextResolverView{
		ID:             r.ID,
		AgentID:        r.AgentID,
		SlotName:       r.SlotName,
		Kind:           r.Kind,
		Run:            r.Run,
		CWD:            r.CWD,
		Timeout:        r.Timeout,
		URL:            r.URL,
		HeadersJSON:    r.HeadersJSON,
		ResponseFormat: r.ResponseFormat,
		JSONPath:       r.JSONPath,
		Enabled:        r.Enabled,
		CreatedAt:      r.CreatedAt,
		UpdatedAt:      r.UpdatedAt,
	}
}

// contextResolversToView keeps nil as nil and an empty list as [].
func contextResolversToView(rows []store.AgentContextResolver) []ContextResolverView {
	if rows == nil {
		return nil
	}
	out := make([]ContextResolverView, len(rows))
	for i := range rows {
		out[i] = contextResolverToView(&rows[i])
	}
	return out
}
