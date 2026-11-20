package authored

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/hollis-labs/nanite/internal/provider"
	"gopkg.in/yaml.v3"
)

// ProviderSpec is a named provider in providers.yaml (the same type as
// agent.ProviderSpec): an endpoint, its wire and a key reference, never a
// key. ProviderSpec.Validate checks one.
type ProviderSpec = provider.Spec

// Uses a provider can be selected for in providers.yaml's use section. A use
// not listed falls back to chat; chat not listed uses host.yaml's
// runtime.provider and runtime.model.
const (
	UseChat       = "chat"
	UseSummarizer = "summarizer"
	UseSubtask    = "subtask"
	// UseDesk is the front desk's planner.
	UseDesk = "desk"
)

// Uses lists every use, in the order the docs give them.
var Uses = []string{UseChat, UseSummarizer, UseSubtask, UseDesk}

// Selection names a provider (built in or named) and a model.
type Selection struct {
	Provider string `yaml:"provider" json:"provider"`
	Model    string `yaml:"model" json:"model"`
}

// Providers is a parsed providers.yaml.
type Providers struct {
	// Named are the providers it defines, by name; each has Name set.
	Named map[string]ProviderSpec
	// Use selects a provider and model per use.
	Use map[string]Selection
}

type providersFile struct {
	Providers map[string]ProviderSpec `yaml:"providers"`
	Use       map[string]Selection    `yaml:"use"`
}

// ParseProviders parses and validates providers.yaml content. Unknown
// fields are errors. A use may name a provider not defined here, since the
// host may supply it; the engine reports a name nobody defines when the use
// is resolved.
func ParseProviders(data []byte) (Providers, error) {
	var file providersFile
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&file); err != nil && !errors.Is(err, io.EOF) {
		return Providers{}, fmt.Errorf("providers.yaml: %w", err)
	}
	out := Providers{Named: make(map[string]ProviderSpec, len(file.Providers)), Use: file.Use}
	for name, spec := range file.Providers {
		spec.Name = name
		if err := spec.Validate(); err != nil {
			return Providers{}, fmt.Errorf("providers.yaml: %w", err)
		}
		out.Named[name] = spec
	}
	for use, sel := range file.Use {
		switch use {
		case UseChat, UseSummarizer, UseSubtask, UseDesk:
		default:
			return Providers{}, fmt.Errorf("providers.yaml: use %q must be chat, summarizer, subtask or desk", use)
		}
		if sel.Provider == "" || sel.Model == "" {
			return Providers{}, fmt.Errorf("providers.yaml: use %s needs provider and model", use)
		}
	}
	return out, nil
}

// LoadProviders reads and validates dir/providers.yaml through a root, so a
// symlink cannot reach outside the directory. A missing file is the zero
// Providers and no error.
func LoadProviders(dir string) (Providers, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return Providers{}, err
	}
	defer func() { _ = root.Close() }() // Read-only.
	raw, err := root.ReadFile("providers.yaml")
	if errors.Is(err, fs.ErrNotExist) {
		return Providers{}, nil
	}
	if err != nil {
		return Providers{}, fmt.Errorf("providers.yaml: %w", err)
	}
	return ParseProviders(raw)
}
