package builders

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/hollis-labs/mentat-chat/internal/store"
)

// slugRegexp validates URL-safe slugs: lowercase letters, digits, and hyphens.
var slugRegexp = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// nameToSlug converts a display name to a URL-safe slug.
func nameToSlug(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	// Replace spaces and underscores with hyphens.
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "_", "-")
	// Remove anything that isn't alphanumeric or hyphen.
	safe := regexp.MustCompile(`[^a-z0-9-]`)
	s = safe.ReplaceAllString(s, "")
	// Collapse multiple hyphens.
	multi := regexp.MustCompile(`-{2,}`)
	s = multi.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// NewAgentBuilder returns a Builder that creates AgentProfile records.
func NewAgentBuilder(s *store.Store) *Builder {
	return &Builder{
		Name:        "agent",
		Description: "Create a new agent profile with a name, system prompt, model, and description.",
		Steps: []BuilderStep{
			{
				Name:     "name",
				Prompt:   "What should this agent be called? (e.g. \"Code Reviewer\", \"Writing Assistant\")",
				Field:    "name",
				Required: true,
			},
			{
				Name:     "slug",
				Prompt:   "URL-safe slug for this agent (leave blank to auto-generate from name):",
				Field:    "slug",
				Required: false,
				Validator: func(val string) error {
					if !slugRegexp.MatchString(val) {
						return fmt.Errorf("slug must contain only lowercase letters, digits, and hyphens (e.g. \"code-reviewer\")")
					}
					return nil
				},
			},
			{
				Name:     "system_prompt",
				Prompt:   "System prompt for this agent (defines its behavior and personality):",
				Field:    "system_prompt",
				Required: true,
			},
			{
				Name:    "model",
				Prompt:  "Default model (leave blank for claude-sonnet-4-20250514):",
				Field:   "model",
				Default: "claude-sonnet-4-20250514",
			},
			{
				Name:   "description",
				Prompt: "Short description of what this agent does (optional):",
				Field:  "description",
			},
		},
		BuildFunc: func(inputs map[string]string) (*BuildResult, error) {
			name := strings.TrimSpace(inputs["name"])
			slug := strings.TrimSpace(inputs["slug"])
			if slug == "" {
				slug = nameToSlug(name)
			}
			if slug == "" {
				return nil, fmt.Errorf("could not generate a valid slug from name %q", name)
			}

			model := strings.TrimSpace(inputs["model"])
			if model == "" {
				model = "claude-sonnet-4-20250514"
			}

			agent := &store.AgentProfile{
				Name:         name,
				Slug:         slug,
				SystemPrompt: strings.TrimSpace(inputs["system_prompt"]),
				DefaultModel: model,
				Description:  strings.TrimSpace(inputs["description"]),
			}

			if err := s.CreateAgent(agent); err != nil {
				return nil, fmt.Errorf("create agent: %w", err)
			}

			return &BuildResult{
				Resource: agent,
				Summary:  fmt.Sprintf("Agent %q created (slug: %s, model: %s)", agent.Name, agent.Slug, agent.DefaultModel),
			}, nil
		},
	}
}
