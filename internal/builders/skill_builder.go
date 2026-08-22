package builders

import (
	"fmt"
	"strings"

	"github.com/hollis-labs/nanite/internal/store"
)

// NewSkillBuilder returns a Builder that creates Skill index rows.
//
// TASKS/skills/02: the "tool_bindings" step is dropped along with
// store.Skill.ToolBindings itself — an index-only row (docs/engineering/
// architecture/20-skills.md's "The model") has no tool-binding column to
// populate. This builder's remaining scope (name/description/category) is
// otherwise unchanged; it was already flagged out of this batch's scope by
// TASKS/skills/01's own Work Log ("the builder wizard's own skill-creation
// path... is untouched and out of scope") as a bare index-row creator, not
// a real authored-package installer (tasks 04/05 own that).
func NewSkillBuilder(s *store.Store) *Builder {
	return &Builder{
		Name:        "skill",
		Description: "Create a new skill with a name, description, and category.",
		Steps: []BuilderStep{
			{
				Name:     "name",
				Prompt:   "What should this skill be called? (e.g. \"Code Review\", \"Web Search\")",
				Field:    "name",
				Required: true,
			},
			{
				Name:     "description",
				Prompt:   "Describe what this skill does:",
				Field:    "description",
				Required: true,
			},
			{
				Name:    "category",
				Prompt:  "Category for this skill (e.g. \"dev\", \"general\", \"research\"):",
				Field:   "category",
				Default: "general",
			},
		},
		BuildFunc: func(inputs map[string]string) (*BuildResult, error) {
			name := strings.TrimSpace(inputs["name"])
			slug := nameToSlug(name)
			if slug == "" {
				return nil, fmt.Errorf("could not generate a valid slug from name %q", name)
			}

			category := strings.TrimSpace(inputs["category"])
			if category == "" {
				category = "general"
			}

			skill := &store.Skill{
				Name:        name,
				Slug:        slug,
				Description: strings.TrimSpace(inputs["description"]),
				Category:    category,
			}

			if err := s.CreateSkill(skill); err != nil {
				return nil, fmt.Errorf("create skill: %w", err)
			}

			return &BuildResult{
				Resource: skill,
				Summary:  fmt.Sprintf("Skill %q created (slug: %s, category: %s)", skill.Name, skill.Slug, skill.Category),
			}, nil
		},
	}
}
