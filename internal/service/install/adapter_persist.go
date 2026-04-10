package install

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"gopkg.in/yaml.v3"
)

// loadProjectConfig reads and parses the project's .nanite/config.yaml
// file, returning a *projectConfig that preserves the tri-state nature
// of the Adapters field (nil = absent, empty pointer = explicit none,
// populated = active list).
//
// Returns an empty *projectConfig (no error) if the file doesn't exist
// — the caller treats this the same as a fresh install.
func loadProjectConfig(path string) (*projectConfig, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &projectConfig{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read project config %s: %w", path, err)
	}
	var cfg projectConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse project config %s: %w", path, err)
	}
	return &cfg, nil
}

// persistAdapterList writes the given adapters slice into the
// .nanite/config.yaml file at path, under the `adapters:` key. All other
// top-level keys (nanite_version, agents, etc.) are preserved.
//
// An empty adapters slice (length 0, non-nil) is written as `adapters: []`
// to preserve the tri-state semantic — distinguishable from a missing key.
//
// The implementation uses yaml.Node to preserve key ordering and most
// formatting. Comments may shift slightly when a new adapters key is
// inserted into a mapping that didn't have one.
func persistAdapterList(path string, adapters []string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config %s: %w", path, err)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}

	// Root is a Document node; its first content is the mapping.
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return fmt.Errorf("config %s is not a yaml document", path)
	}
	mapping := root.Content[0]
	if mapping.Kind != yaml.MappingNode {
		return fmt.Errorf("config %s top-level is not a mapping", path)
	}

	// Build the new adapters value node.
	adaptersValue := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	if len(adapters) == 0 {
		adaptersValue.Style = yaml.FlowStyle // renders as []
	}
	for _, slug := range adapters {
		adaptersValue.Content = append(adaptersValue.Content, &yaml.Node{
			Kind:  yaml.ScalarNode,
			Tag:   "!!str",
			Value: slug,
		})
	}

	// Find the existing adapters key, replace its value.
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		key := mapping.Content[i]
		if key.Kind == yaml.ScalarNode && key.Value == "adapters" {
			mapping.Content[i+1] = adaptersValue
			return writeYAMLBack(path, &root)
		}
	}

	// Adapters key not present — insert it as the first key (for visibility).
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "adapters"}
	newContent := make([]*yaml.Node, 0, len(mapping.Content)+2)
	newContent = append(newContent, keyNode, adaptersValue)
	newContent = append(newContent, mapping.Content...)
	mapping.Content = newContent

	return writeYAMLBack(path, &root)
}

func writeYAMLBack(path string, root *yaml.Node) error {
	out, err := yaml.Marshal(root)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	return nil
}
