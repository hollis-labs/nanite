package builders

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/hollis-labs/nanite/internal/store"
)

// NewPromptTemplateBuilder returns a Builder that creates PromptTemplate records.
func NewPromptTemplateBuilder(s *store.Store) *Builder {
	return &Builder{
		Name:        "prompt_template",
		Description: "Create a new prompt template with a name, scope, template text, and priority.",
		Steps: []BuilderStep{
			{
				Name:     "name",
				Prompt:   "What should this prompt template be called? (e.g. \"Safety Guardrails\", \"Code Style Guide\")",
				Field:    "name",
				Required: true,
			},
			{
				Name:    "scope",
				Prompt:  "Scope for this template (system, mode, skill, or context):",
				Field:   "scope",
				Default: "system",
				Validator: func(val string) error {
					switch val {
					case "system", "mode", "skill", "context":
						return nil
					default:
						return fmt.Errorf("scope must be one of: system, mode, skill, context")
					}
				},
			},
			{
				Name:     "template",
				Prompt:   "Template text (use {{variable_name}} for placeholders):",
				Field:    "template",
				Required: true,
			},
			{
				Name:   "variables",
				Prompt: "Comma-separated list of variable names used in the template (e.g. \"agent_name,project\"):",
				Field:  "variables",
			},
			{
				Name:    "priority",
				Prompt:  "Priority (lower numbers are composed first, default 50):",
				Field:   "priority",
				Default: "50",
				Validator: func(val string) error {
					n, err := strconv.Atoi(val)
					if err != nil {
						return fmt.Errorf("priority must be an integer")
					}
					if n < 0 || n > 1000 {
						return fmt.Errorf("priority must be between 0 and 1000")
					}
					return nil
				},
			},
		},
		BuildFunc: func(inputs map[string]string) (*BuildResult, error) {
			name := strings.TrimSpace(inputs["name"])
			slug := nameToSlug(name)
			if slug == "" {
				return nil, fmt.Errorf("could not generate a valid slug from name %q", name)
			}

			scope := strings.TrimSpace(inputs["scope"])
			if scope == "" {
				scope = "system"
			}

			priority := 50
			if raw := strings.TrimSpace(inputs["priority"]); raw != "" {
				if n, err := strconv.Atoi(raw); err == nil {
					priority = n
				}
			}

			// Parse comma-separated variables into a JSON array.
			variables := "[]"
			if raw := strings.TrimSpace(inputs["variables"]); raw != "" {
				parts := strings.Split(raw, ",")
				var cleaned []string
				for _, p := range parts {
					p = strings.TrimSpace(p)
					if p != "" {
						cleaned = append(cleaned, fmt.Sprintf("%q", p))
					}
				}
				if len(cleaned) > 0 {
					variables = "[" + strings.Join(cleaned, ",") + "]"
				}
			}

			pt := &store.PromptTemplate{
				Name:      name,
				Slug:      slug,
				Scope:     scope,
				Template:  strings.TrimSpace(inputs["template"]),
				Variables: variables,
				Priority:  priority,
			}

			if err := s.CreatePromptTemplate(pt); err != nil {
				return nil, fmt.Errorf("create prompt template: %w", err)
			}

			return &BuildResult{
				Resource: pt,
				Summary:  fmt.Sprintf("Prompt template %q created (slug: %s, scope: %s, priority: %d)", pt.Name, pt.Slug, pt.Scope, pt.Priority),
			}, nil
		},
	}
}
