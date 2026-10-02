package api

import "github.com/hollis-labs/nanite/internal/store"

// ProjectView is a project as the API returns it. Field order is the store
// row's.
type ProjectView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	RepoPath    string `json:"repo_path"`
	Settings    string `json:"settings"`
	SortOrder   int    `json:"sort_order"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func projectToView(p *store.Project) ProjectView {
	return ProjectView{
		ID:          p.ID,
		Name:        p.Name,
		Description: p.Description,
		RepoPath:    p.RepoPath,
		Settings:    p.Settings,
		SortOrder:   p.SortOrder,
		CreatedAt:   p.CreatedAt,
		UpdatedAt:   p.UpdatedAt,
	}
}

// projectsToView keeps nil as nil and an empty list as [], as the store row
// slice encoded.
func projectsToView(projects []store.Project) []ProjectView {
	if projects == nil {
		return nil
	}
	out := make([]ProjectView, len(projects))
	for i := range projects {
		out[i] = projectToView(&projects[i])
	}
	return out
}

// ProjectSessionRefView identifies a session that prevents project deletion.
type ProjectSessionRefView struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

func projectSessionRefsToView(rows []store.ProjectSessionRef) []ProjectSessionRefView {
	if rows == nil {
		return nil
	}
	out := make([]ProjectSessionRefView, len(rows))
	for i, r := range rows {
		out[i] = ProjectSessionRefView{ID: r.ID, Title: r.Title, Status: r.Status}
	}
	return out
}
