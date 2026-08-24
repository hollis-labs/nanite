package workflow

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// yamlPipeline is the intermediate representation for YAML parsing.
type yamlPipeline struct {
	Name           string            `yaml:"name"`
	Description    string            `yaml:"description"`
	DefaultTimeout string            `yaml:"default_timeout"`
	Env            map[string]string `yaml:"env"`
	Steps          []yamlStep        `yaml:"steps"`
}

type yamlStep struct {
	ID        string     `yaml:"id"`
	Name      string     `yaml:"name"`
	Type      string     `yaml:"type"` // shell, skill, parallel, loop
	Required  *bool      `yaml:"required"`
	DependsOn []string   `yaml:"depends_on"`
	Timeout   string     `yaml:"timeout"`
	Retry     *yamlRetry `yaml:"retry"`

	// Shell step fields.
	Command string   `yaml:"command"`
	Args    []string `yaml:"args"`
	Dir     string   `yaml:"dir"`

	// Skill step fields.
	Skill  string `yaml:"skill"`
	Prompt string `yaml:"prompt"`

	// Parallel step fields.
	Steps      []yamlStep `yaml:"steps"`
	FailPolicy string     `yaml:"fail_policy"`

	// Loop step fields.
	Body    *yamlStep `yaml:"body"`
	MaxIter int       `yaml:"max_iter"`
}

type yamlRetry struct {
	MaxAttempts int     `yaml:"max_attempts"`
	Delay       string  `yaml:"delay"`
	Backoff     float64 `yaml:"backoff"`
}

// LoadFile parses a YAML workflow definition file into a Pipeline.
func LoadFile(path string) (*Pipeline, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("workflow load: read %s: %w", path, err)
	}
	return Load(data)
}

// Load parses YAML bytes into a Pipeline.
func Load(data []byte) (*Pipeline, error) {
	var raw yamlPipeline
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("workflow load: parse yaml: %w", err)
	}

	p := &Pipeline{
		Name:        raw.Name,
		Description: raw.Description,
		Env:         raw.Env,
	}

	if raw.DefaultTimeout != "" {
		d, err := time.ParseDuration(raw.DefaultTimeout)
		if err != nil {
			return nil, fmt.Errorf("workflow load: invalid default_timeout %q: %w", raw.DefaultTimeout, err)
		}
		p.DefaultTimeout = d
	}

	// Convert steps.
	seen := make(map[string]bool)
	for _, ys := range raw.Steps {
		step, err := convertStep(ys, seen)
		if err != nil {
			return nil, err
		}
		p.Steps = append(p.Steps, *step)
	}

	// Validate dependencies reference existing steps.
	for _, s := range p.Steps {
		for _, dep := range s.DependsOn {
			if !seen[dep] {
				return nil, fmt.Errorf("workflow load: step %q depends on unknown step %q", s.ID, dep)
			}
		}
	}

	// Check for cycles.
	if err := validateNoCycles(p.Steps); err != nil {
		return nil, err
	}

	return p, nil
}

// convertStep converts a yamlStep to a Step with the appropriate handler.
func convertStep(ys yamlStep, seen map[string]bool) (*Step, error) {
	if ys.ID == "" {
		return nil, fmt.Errorf("workflow load: step missing id")
	}
	if seen[ys.ID] {
		return nil, fmt.Errorf("workflow load: duplicate step id %q", ys.ID)
	}
	seen[ys.ID] = true

	step := &Step{
		ID:        ys.ID,
		Name:      ys.Name,
		Required:  true, // default
		DependsOn: ys.DependsOn,
	}

	if ys.Required != nil {
		step.Required = *ys.Required
	}

	if ys.Timeout != "" {
		d, err := time.ParseDuration(ys.Timeout)
		if err != nil {
			return nil, fmt.Errorf("workflow load: step %q invalid timeout %q: %w", ys.ID, ys.Timeout, err)
		}
		step.Timeout = d
	}

	if ys.Retry != nil {
		rp := &RetryPolicy{
			MaxAttempts: ys.Retry.MaxAttempts,
			Backoff:     ys.Retry.Backoff,
		}
		if ys.Retry.Delay != "" {
			d, err := time.ParseDuration(ys.Retry.Delay)
			if err != nil {
				return nil, fmt.Errorf("workflow load: step %q invalid retry delay %q: %w", ys.ID, ys.Retry.Delay, err)
			}
			rp.Delay = d
		}
		step.Retry = rp
	}

	switch ys.Type {
	case "shell":
		if ys.Command == "" {
			return nil, fmt.Errorf("workflow load: shell step %q missing command", ys.ID)
		}
		step.Handler = &ShellStep{
			Command: ys.Command,
			Args:    ys.Args,
			Dir:     ys.Dir,
		}

	case "skill":
		if ys.Skill == "" {
			return nil, fmt.Errorf("workflow load: skill step %q missing skill slug", ys.ID)
		}
		step.Handler = &SkillStep{
			SkillSlug: ys.Skill,
			Prompt:    ys.Prompt,
		}

	case "parallel":
		if len(ys.Steps) == 0 {
			return nil, fmt.Errorf("workflow load: parallel step %q has no sub-steps", ys.ID)
		}
		subSeen := make(map[string]bool)
		var subSteps []Step
		for _, sub := range ys.Steps {
			ss, err := convertStep(sub, subSeen)
			if err != nil {
				return nil, fmt.Errorf("workflow load: parallel step %q: %w", ys.ID, err)
			}
			subSteps = append(subSteps, *ss)
		}
		fp := FailPolicyAll
		if ys.FailPolicy != "" {
			fp = FailPolicy(ys.FailPolicy)
		}
		step.Handler = &ParallelStep{
			Steps:      subSteps,
			FailPolicy: fp,
		}

	case "loop":
		if ys.Body == nil {
			return nil, fmt.Errorf("workflow load: loop step %q missing body", ys.ID)
		}
		bodySeen := make(map[string]bool)
		bodyStep, err := convertStep(*ys.Body, bodySeen)
		if err != nil {
			return nil, fmt.Errorf("workflow load: loop step %q body: %w", ys.ID, err)
		}
		step.Handler = &LoopStep{
			Body:    *bodyStep,
			MaxIter: ys.MaxIter,
			// Gate is not configurable via YAML — set programmatically after loading.
		}

	default:
		return nil, fmt.Errorf("workflow load: step %q has unknown type %q", ys.ID, ys.Type)
	}

	return step, nil
}

// validateNoCycles checks for dependency cycles using Kahn's algorithm.
func validateNoCycles(steps []Step) error {
	inDegree := make(map[string]int)
	dependents := make(map[string][]string)

	for _, s := range steps {
		inDegree[s.ID] = len(s.DependsOn)
		for _, dep := range s.DependsOn {
			dependents[dep] = append(dependents[dep], s.ID)
		}
	}

	_, err := topoSort(steps, inDegree, dependents)
	if err != nil {
		return fmt.Errorf("workflow load: %w", err)
	}
	return nil
}
