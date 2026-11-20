package definition

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/hollis-labs/libs/agent/agentdir"
	"github.com/hollis-labs/nanite/internal/provider"
	"gopkg.in/yaml.v3"
)

type Context struct{ Name, Text string }

type Definition struct {
	Snapshot agentdir.Snapshot
	Runtime  agentdir.Runtime
	Skills   map[string]agentdir.SkillRef
	Context  []Context
	Rules    []byte
	// Providers are the named providers from providers.yaml.
	Providers map[string]provider.Spec
	// Use selects a provider and model per use (chat, summarizer, subtask, desk)
	// from providers.yaml. A use not listed falls back to chat; chat not
	// listed uses host.yaml's runtime.provider and runtime.model.
	Use map[string]Selection
}

// Uses a provider can be selected for.
const (
	UseChat       = "chat"
	UseSummarizer = "summarizer"
	UseSubtask    = "subtask"
	// UseDesk is the front desk's planner (chatrunner DeskOptions.PlanWithModel).
	UseDesk = "desk"
)

// Selection names a provider (built in or named) and a model.
type Selection struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
}

// providersFile is the authored providers.yaml.
type providersFile struct {
	Providers map[string]provider.Spec `yaml:"providers"`
	Use       map[string]Selection     `yaml:"use"`
}

// loadProviders reads and validates providers.yaml in dir. A missing file
// is no providers and no selections.
func loadProviders(dir string) (map[string]provider.Spec, map[string]Selection, error) {
	// Read through a root so a symlink cannot reach outside the directory.
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = root.Close() }() // Read-only.
	raw, err := root.ReadFile("providers.yaml")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("providers.yaml: %w", err)
	}
	var file providersFile
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&file); err != nil && !errors.Is(err, io.EOF) {
		return nil, nil, fmt.Errorf("providers.yaml: %w", err)
	}
	specs := make(map[string]provider.Spec, len(file.Providers))
	for name, spec := range file.Providers {
		spec.Name = name
		if err := spec.Validate(); err != nil {
			return nil, nil, fmt.Errorf("providers.yaml: %w", err)
		}
		specs[name] = spec
	}
	for use, sel := range file.Use {
		switch use {
		case UseChat, UseSummarizer, UseSubtask, UseDesk:
		default:
			return nil, nil, fmt.Errorf("providers.yaml: use %q must be chat, summarizer, subtask or desk", use)
		}
		// A name not defined here may be one the host supplies; the engine
		// reports a name nobody defines when the use is resolved.
		if sel.Provider == "" || sel.Model == "" {
			return nil, nil, fmt.Errorf("providers.yaml: use %s needs provider and model", use)
		}
	}
	return specs, file.Use, nil
}

func Load(dir, globalSkillsDir string) (Definition, error) {
	snap, err := agentdir.Load(dir)
	if err != nil {
		return Definition{}, err
	}
	out := Definition{Snapshot: snap, Runtime: snap.Profile.Runtime, Skills: make(map[string]agentdir.SkillRef)}
	if out.Providers, out.Use, err = loadProviders(dir); err != nil {
		return Definition{}, err
	}
	if out.Runtime.Provider == "" {
		out.Runtime.Provider = "anthropic"
	}
	if out.Runtime.Model == "" {
		out.Runtime.Model = os.Getenv("NANITE_DEFAULT_MODEL")
		if out.Runtime.Model == "" {
			out.Runtime.Model = "claude-sonnet-5-5"
		}
	}
	if out.Runtime.MaxSteps == 0 {
		out.Runtime.MaxSteps = 50
	}
	if out.Runtime.ToolConcurrency == 0 {
		out.Runtime.ToolConcurrency = 4
	}
	if globalSkillsDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Definition{}, err
		}
		globalSkillsDir = filepath.Join(home, ".nanite", "skills")
	}
	if globalSkillsDir != "-" {
		global, err := agentdir.ListSkills(globalSkillsDir)
		if err != nil {
			return Definition{}, fmt.Errorf("load global skills: %w", err)
		}
		for _, ref := range global {
			out.Skills[ref.Name] = ref
		}
	}
	for _, ref := range snap.Skills {
		out.Skills[ref.Name] = ref
	}
	for _, file := range snap.Context {
		out.Context = append(out.Context, Context{Name: file.Name, Text: file.Body})
	}
	out.Rules = snap.PermissionsRaw
	return out, nil
}
