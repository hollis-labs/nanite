package api

import "github.com/hollis-labs/nanite/internal/store"

// RoleView is a role as the API returns it. Field order is the store row's.
type RoleView struct {
	ID                 string `json:"id"`
	Slug               string `json:"slug"`
	Name               string `json:"name"`
	SystemPrompt       string `json:"system_prompt"`
	DefaultClass       string `json:"default_class"`
	DefaultModel       string `json:"default_model"`
	DefaultProvider    string `json:"default_provider"`
	DefaultTools       string `json:"default_tools"`
	DefaultSkills      string `json:"default_skills"`
	DefaultPermissions string `json:"default_permissions"`
	CreatedAt          string `json:"created_at"`
	UpdatedAt          string `json:"updated_at"`
	PluginID           string `json:"plugin_id"`
}

func roleToView(r *store.Role) RoleView {
	return RoleView{
		ID:                 r.ID,
		Slug:               r.Slug,
		Name:               r.Name,
		SystemPrompt:       r.SystemPrompt,
		DefaultClass:       r.DefaultClass,
		DefaultModel:       r.DefaultModel,
		DefaultProvider:    r.DefaultProvider,
		DefaultTools:       r.DefaultTools,
		DefaultSkills:      r.DefaultSkills,
		DefaultPermissions: r.DefaultPermissions,
		CreatedAt:          r.CreatedAt,
		UpdatedAt:          r.UpdatedAt,
		PluginID:           r.PluginID,
	}
}

// rolesToView keeps nil as nil and an empty list as [], as the store row
// slice encoded.
func rolesToView(roles []store.Role) []RoleView {
	if roles == nil {
		return nil
	}
	out := make([]RoleView, len(roles))
	for i := range roles {
		out[i] = roleToView(&roles[i])
	}
	return out
}
