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

// ToStoreSkill converts a Definition to a store.Skill.
// The ID is deterministic: "file-{slug}".
func (d *Definition) ToStoreSkill() *store.Skill {
	now := time.Now().UTC().Format(time.RFC3339)

	sk := &store.Skill{
		ID:          fileIDPrefix + d.Slug,
		Name:        d.Name,
		Slug:        d.Slug,
		Description: d.Description,
		Category:    categoryFromTags(d.Tags),
		IsBuiltin:   true,
		Icon:        "",
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	// ToolBindings as JSON array.
	sk.ToolBindings = marshalSlice(d.AllowedTools)

	// InputSchema — skills use argument-hint, not JSON Schema.
	sk.InputSchema = "{}"

	// Settings: store skill-specific fields for API consumers.
	settings := map[string]any{}
	if d.Model != "" {
		settings["model"] = d.Model
	}
	if d.Effort != "" {
		settings["effort"] = d.Effort
	}
	if d.Context != "" {
		settings["context"] = d.Context
	}
	if d.ArgumentHint != "" {
		settings["argument_hint"] = d.ArgumentHint
	}
	if len(d.BrokerHints) > 0 {
		settings["broker_hints"] = d.BrokerHints
	}
	if d.Source != "" {
		settings["source"] = d.Source
	}
	if d.SourceRef != "" {
		settings["source_ref"] = d.SourceRef
	}
	sk.Settings = marshalJSONOr(settings, "{}")

	return sk
}

// categoryFromTags returns the first tag as the category, or empty string.
func categoryFromTags(tags []string) string {
	if len(tags) > 0 {
		return tags[0]
	}
	return ""
}

// marshalSlice marshals a string slice to JSON, normalizing nil to "[]".
func marshalSlice(v []string) string {
	if v == nil {
		return "[]"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// marshalJSONOr marshals v to JSON, returning fallback on error or nil input.
func marshalJSONOr(v any, fallback string) string {
	if v == nil {
		return fallback
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fallback
	}
	return string(b)
}
