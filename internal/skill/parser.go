package skill

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Definition is the typed representation of a skill MD file.
// YAML frontmatter fields map to struct tags; the markdown body
// after the closing --- becomes the Prompt.
type Definition struct {
	// Identity
	Name        string   `yaml:"name"`
	Slug        string   `yaml:"slug"`
	Description string   `yaml:"description"`
	Tags        []string `yaml:"tags"`

	// Behavior
	ArgumentHint string   `yaml:"argument-hint"` // hint text for argument input
	AllowedTools []string `yaml:"allowed-tools"`  // tool allowlist during execution
	Model        string   `yaml:"model"`          // preferred model override
	Effort       string   `yaml:"effort"`         // low, medium, high
	Context      string   `yaml:"context"`        // "inline" (default) or "fork"
	BrokerHints  []string `yaml:"broker-hints"`   // Nanite extension: hints for broker mode

	// Prompt is the markdown body below the YAML frontmatter.
	// May contain !`command` dynamic context markers.
	Prompt string `yaml:"-"`

	// Metadata set by the loader, not parsed from file.
	Source    string `yaml:"-"` // "builtin", "project", "user", "plugin", "nanite", "claude"
	SourceRef string `yaml:"-"` // file path or "embedded:*.md"
}

var frontmatterDelim = []byte("---")

// ParseMD parses a markdown file with YAML frontmatter into a Definition.
func ParseMD(data []byte) (*Definition, error) {
	fm, body, err := splitFrontmatter(data)
	if err != nil {
		return nil, err
	}

	var def Definition
	if err := yaml.Unmarshal(fm, &def); err != nil {
		return nil, fmt.Errorf("skill: invalid YAML frontmatter: %w", err)
	}

	// Slug is validated in ParseMDFile after filename fallback.
	// ParseMD callers that don't use ParseMDFile must ensure slug is set.

	def.Prompt = strings.TrimSpace(string(body))

	// Default context mode is inline.
	if def.Context == "" {
		def.Context = "inline"
	}

	return &def, nil
}

// ParseMDFile reads a file from disk and parses it as a skill definition.
// If the parsed definition has no slug, the filename (without extension) is used.
func ParseMDFile(path string) (*Definition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("skill: read %s: %w", path, err)
	}

	def, err := ParseMD(data)
	if err != nil {
		return nil, fmt.Errorf("skill: parse %s: %w", path, err)
	}

	// Fall back to filename-derived slug when frontmatter omits it.
	if def.Slug == "" {
		def.Slug = SlugFromFilename(path)
	}
	if def.Slug == "" {
		return nil, fmt.Errorf("skill: %s has no slug in frontmatter and none could be derived from filename", path)
	}

	def.SourceRef = path
	return def, nil
}

// SlugFromFilename derives a slug from a markdown filename.
func SlugFromFilename(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// splitFrontmatter splits data into YAML frontmatter and markdown body.
func splitFrontmatter(data []byte) (frontmatter, body []byte, err error) {
	data = bytes.TrimLeft(data, "\n\r")

	if !bytes.HasPrefix(data, frontmatterDelim) {
		return nil, nil, fmt.Errorf("skill: file does not start with --- frontmatter delimiter")
	}

	rest := data[len(frontmatterDelim):]
	if _, after, ok := bytes.Cut(rest, []byte("\n")); ok {
		rest = after
	} else {
		return nil, nil, fmt.Errorf("skill: no content after opening --- delimiter")
	}

	before, after, found := bytes.Cut(rest, append([]byte("\n"), frontmatterDelim...))
	if !found {
		return nil, nil, fmt.Errorf("skill: missing closing --- delimiter")
	}

	frontmatter = before

	if _, bodyPart, ok := bytes.Cut(after, []byte("\n")); ok {
		body = bodyPart
	}

	return frontmatter, body, nil
}
