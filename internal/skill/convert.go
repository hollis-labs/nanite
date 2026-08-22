package skill

import (
	"time"

	"github.com/hollis-labs/nanite/internal/store"
)

const fileIDPrefix = "file-"

// IsFileBasedID returns true if the skill ID was generated from a file-based definition.
func IsFileBasedID(id string) bool {
	return len(id) > len(fileIDPrefix) && id[:len(fileIDPrefix)] == fileIDPrefix
}

// SlugFromFileID extracts the slug from a file-based skill ID.
func SlugFromFileID(id string) string {
	if !IsFileBasedID(id) {
		return ""
	}
	return id[len(fileIDPrefix):]
}

// ToStoreSkill converts a Definition to a store.Skill index row.
// The ID is deterministic: "file-{slug}".
//
// TASKS/skills/02: this shrinks significantly against the redesigned,
// index-only store.Skill shape (docs/engineering/architecture/20-skills.md's
// "The model: DB is an index, a vendored store is content") — the body
// (Prompt), tool bindings, and the free-form execution-config blob (former
// Settings: model/effort/context/argument-hint) no longer have anywhere to
// live on this struct at all; that content stays in the file/vendored
// package itself, read live at materialization time (task 06/08), never
// flattened into this row. This function now produces only what an
// index-only row can hold: identity, category, source tier, and enablement.
func (d *Definition) ToStoreSkill() *store.Skill {
	now := time.Now().UTC().Format(time.RFC3339)

	return &store.Skill{
		ID:                   fileIDPrefix + d.Slug,
		Name:                 d.Name,
		Slug:                 d.Slug,
		Description:          d.Description,
		Category:             categoryFromTags(d.Tags),
		Icon:                 "",
		InputSchema:          "{}",
		SourceTier:           sourceTierFromDefinition(d),
		Version:              1,
		Enabled:              true,
		DeclaredDependencies: "[]",
		InstalledAt:          now,
		UpdatedAt:            now,
	}
}

// sourceTierFromDefinition maps the file-based Definition's Source field
// (set by the loader, not parsed from frontmatter — "builtin", "project",
// "user", "plugin", "nanite", "claude") onto SourceTier, defaulting to
// "user" when unset, matching store.Skill.CreateSkill's own default.
func sourceTierFromDefinition(d *Definition) string {
	if d.Source == "" {
		return "user"
	}
	return d.Source
}

// categoryFromTags returns the first tag as the category, or empty string.
func categoryFromTags(tags []string) string {
	if len(tags) > 0 {
		return tags[0]
	}
	return ""
}
