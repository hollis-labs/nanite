package builders

import (
	"fmt"
	"strings"
)

// BuilderStep defines a single step in a builder flow.
type BuilderStep struct {
	Name      string                // unique step identifier
	Prompt    string                // what to ask the user
	Field     string                // which field this fills in the inputs map
	Required  bool                  // whether the value must be non-empty
	Default   string                // default value if none provided
	Validator func(string) error    // optional validation function
}

// BuildResult holds the output of a completed builder flow.
type BuildResult struct {
	Resource    any               `json:"resource"`
	Summary     string            `json:"summary"`
}

// Builder defines a multi-step creation flow for a resource type.
type Builder struct {
	Name        string
	Description string
	Steps       []BuilderStep
	BuildFunc   func(inputs map[string]string) (*BuildResult, error)
}

// StepByName returns the step with the given name, or nil if not found.
func (b *Builder) StepByName(name string) *BuilderStep {
	for i := range b.Steps {
		if b.Steps[i].Name == name {
			return &b.Steps[i]
		}
	}
	return nil
}

// NextStep returns the step after the named step, or nil if it was the last.
func (b *Builder) NextStep(currentName string) *BuilderStep {
	for i, s := range b.Steps {
		if s.Name == currentName && i+1 < len(b.Steps) {
			return &b.Steps[i+1]
		}
	}
	return nil
}

// FirstStep returns the first step or nil if there are no steps.
func (b *Builder) FirstStep() *BuilderStep {
	if len(b.Steps) == 0 {
		return nil
	}
	return &b.Steps[0]
}

// IsLastStep returns true if the named step is the final step.
func (b *Builder) IsLastStep(name string) bool {
	if len(b.Steps) == 0 {
		return false
	}
	return b.Steps[len(b.Steps)-1].Name == name
}

// ValidateStep validates a value for a given step. Returns an error if the
// value is invalid (empty when required, or fails the step's Validator).
func (b *Builder) ValidateStep(stepName, value string) error {
	step := b.StepByName(stepName)
	if step == nil {
		return fmt.Errorf("unknown step %q in builder %q", stepName, b.Name)
	}

	value = strings.TrimSpace(value)
	if step.Required && value == "" {
		return fmt.Errorf("%s is required", step.Field)
	}

	if step.Validator != nil && value != "" {
		if err := step.Validator(value); err != nil {
			return fmt.Errorf("invalid %s: %w", step.Field, err)
		}
	}

	return nil
}
