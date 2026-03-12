package builders

import (
	"fmt"
	"strings"

	"github.com/hollis-labs/mentat/internal/store"
)

// NewSkillBuilder returns a Builder that creates Skill records.
func NewSkillBuilder(s *store.Store) *Builder {
	return &Builder{
		Name:        "skill",
		Description: "Create a new skill with a name, description, category, and tool bindings.",
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
				Name:   "category",
				Prompt: "Category for this skill (e.g. \"dev\", \"general\", \"research\"):",
				Field:  "category",
				Default: "general",
			},
			{
				Name:   "tool_bindings",
				Prompt: "Comma-separated list of tool names this skill uses (e.g. \"dev_read,dev_write\"):",
				Field:  "tool_bindings",
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

			// Parse comma-separated tool bindings into a JSON array.
			toolBindings := "[]"
			if raw := strings.TrimSpace(inputs["tool_bindings"]); raw != "" {
				parts := strings.Split(raw, ",")
				var cleaned []string
				for _, p := range parts {
					p = strings.TrimSpace(p)
					if p != "" {
						cleaned = append(cleaned, fmt.Sprintf("%q", p))
					}
				}
				if len(cleaned) > 0 {
					toolBindings = "[" + strings.Join(cleaned, ",") + "]"
				}
			}

			skill := &store.Skill{
				Name:         name,
				Slug:         slug,
				Description:  strings.TrimSpace(inputs["description"]),
				Category:     category,
				ToolBindings: toolBindings,
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
