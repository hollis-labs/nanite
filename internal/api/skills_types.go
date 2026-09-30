package api

import "github.com/hollis-labs/nanite/internal/store"

// SkillView is the API-owned wire shape of a skills-index row. Its keys match
// what store.Skill used to emit directly, so the wire did not change when the
// row stopped riding onto it; a new column reaches the wire only once it is
// added here and to skillToView. TestSkillViewJSONKeys pins the key set.
type SkillView struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	Slug                 string `json:"slug"`
	Description          string `json:"description"`
	Category             string `json:"category"`
	Icon                 string `json:"icon"`
	InputSchema          string `json:"input_schema"`
	SourceTier           string `json:"source_tier"`
	ContentHash          string `json:"content_hash"`
	Version              int    `json:"version"`
	Enabled              bool   `json:"enabled"`
	DeclaredDependencies string `json:"declared_dependencies"`
	InstalledAt          string `json:"installed_at"`
	UpdatedAt            string `json:"updated_at"`
}

func skillToView(sk *store.Skill) SkillView {
	return SkillView{
		ID:                   sk.ID,
		Name:                 sk.Name,
		Slug:                 sk.Slug,
		Description:          sk.Description,
		Category:             sk.Category,
		Icon:                 sk.Icon,
		InputSchema:          sk.InputSchema,
		SourceTier:           sk.SourceTier,
		ContentHash:          sk.ContentHash,
		Version:              sk.Version,
		Enabled:              sk.Enabled,
		DeclaredDependencies: sk.DeclaredDependencies,
		InstalledAt:          sk.InstalledAt,
		UpdatedAt:            sk.UpdatedAt,
	}
}

// skillsToView always returns a non-nil slice: the store's skill lists
// return an empty slice, never nil, so an empty list has always been [].
func skillsToView(rows []store.Skill) []SkillView {
	out := make([]SkillView, 0, len(rows))
	for i := range rows {
		out = append(out, skillToView(&rows[i]))
	}
	return out
}
