package skill

import (
	"encoding/json"
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
		InputSchema:          inputSchemaFromParameters(d.Parameters),
		SourceTier:           sourceTierFromDefinition(d),
		Version:              1,
		Enabled:              true,
		DeclaredDependencies: "[]",
		InstalledAt:          now,
		UpdatedAt:            now,
	}
}

// inputSchemaFromParameters builds a minimal JSON Schema object describing
// a skill's declared Parameters — TASKS/skills/02's own note on
// store.Skill.InputSchema: "schema for declared parameters, now sourced
// from the package's own frontmatter via task 04's parser, not
// agent-authored." Returns "{}" (matching the pre-parameters default) when
// the skill declares no parameters at all, so a skill with no
// `parameters:` frontmatter round-trips identically to before this field
// existed.
func inputSchemaFromParameters(params []ParameterSpec) string {
	if len(params) == 0 {
		return "{}"
	}

	properties := make(map[string]any, len(params))
	required := make([]string, 0, len(params))
	for _, p := range params {
		prop := map[string]any{"type": "string"}
		if p.Description != "" {
			prop["description"] = p.Description
		}
		properties[p.Name] = prop
		if p.Required {
			required = append(required, p.Name)
		}
	}

	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}

	data, err := json.Marshal(schema)
	if err != nil {
		// Marshal of a map[string]any built entirely from strings/bools
		// cannot realistically fail; fall back to the pre-parameters
		// default rather than propagating an error from a conversion
		// function with no error return.
		return "{}"
	}
	return string(data)
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
