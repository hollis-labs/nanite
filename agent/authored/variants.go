package authored

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// MaxLastN bounds a variant's last_n and a subtask call's context.last_n.
const MaxLastN = 50

// BuiltinVariants names the variants every agent has. An authored variant
// cannot take one of these names.
var BuiltinVariants = []string{"code", "planner", "research", "worker"}

// Variant is an authored variant, variants/<name>.md: a role prompt (the
// body), the tools it may use, an optional provider and model, and budgets.
type Variant struct {
	Name        string
	Description string
	// Prompt is the body, trimmed: the variant's role prompt.
	Prompt string
	// ReadOnly offers only read-only tools.
	ReadOnly bool
	// Allow, when set, offers only tools matching one of its patterns; Deny
	// removes tools matching any. A pattern is a tool name or a path.Match
	// glob such as "mcp__*".
	Allow, Deny []string
	// Provider and Model select what the subtask runs on, like a use in
	// providers.yaml. Model alone runs on the provider of the "subtask" use.
	Provider, Model string
	Budgets         Budgets
	// LastN is how many parent messages the subtask sees by default (zero is
	// the engine's default); at most MaxLastN.
	LastN int
	// ResultCap is the result size cap in tokens; zero uses the agent's.
	ResultCap int
	// CanSpawn lets the variant start subtasks itself, one level deeper.
	CanSpawn bool
}

// Budgets bound one subtask. Zero means the agent's own limit applies.
type Budgets struct {
	Steps  int
	Tokens int
	Wall   time.Duration
	Cost   float64
}

var variantName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)

// CheckVariantName reports why name cannot name an authored variant: it must
// be lower-case letters, digits and dashes, start with a letter, be at most
// 40 characters, and be neither "chat" nor a built-in variant.
func CheckVariantName(name string) error {
	if !variantName.MatchString(name) || name == "chat" {
		return fmt.Errorf("invalid variant name %q: use lower-case letters, digits and dashes", name)
	}
	if slices.Contains(BuiltinVariants, name) {
		return fmt.Errorf("%q is a built-in variant; choose another name", name)
	}
	return nil
}

type frontmatter struct {
	Description string `yaml:"description"`
	Tools       struct {
		Allow []string `yaml:"allow"`
		Deny  []string `yaml:"deny"`
	} `yaml:"tools"`
	ReadOnly bool   `yaml:"read_only"`
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
	Budgets  struct {
		Steps  int     `yaml:"steps"`
		Tokens int     `yaml:"tokens"`
		Wall   string  `yaml:"wall"`
		Cost   float64 `yaml:"cost"`
	} `yaml:"budgets"`
	LastN     int  `yaml:"last_n"`
	ResultCap int  `yaml:"result_cap"`
	CanSpawn  bool `yaml:"can_spawn"`
}

// ParseVariant parses and validates the content of variants/<name>.md.
func ParseVariant(name string, data []byte) (Variant, error) {
	if err := CheckVariantName(name); err != nil {
		return Variant{}, err
	}
	head, body, err := split(data)
	if err != nil {
		return Variant{}, err
	}
	var meta frontmatter
	decoder := yaml.NewDecoder(bytes.NewReader(head))
	decoder.KnownFields(true)
	if err := decoder.Decode(&meta); err != nil && !errors.Is(err, io.EOF) {
		return Variant{}, fmt.Errorf("frontmatter: %w", err)
	}
	v := Variant{Name: name, Description: strings.TrimSpace(meta.Description), Prompt: strings.TrimSpace(body),
		ReadOnly: meta.ReadOnly, Allow: meta.Tools.Allow, Deny: meta.Tools.Deny, Provider: strings.TrimSpace(meta.Provider), Model: strings.TrimSpace(meta.Model),
		Budgets: Budgets{Steps: meta.Budgets.Steps, Tokens: meta.Budgets.Tokens, Cost: meta.Budgets.Cost},
		LastN:   meta.LastN, ResultCap: meta.ResultCap, CanSpawn: meta.CanSpawn}
	if v.Prompt == "" {
		return Variant{}, fmt.Errorf("the body (the role prompt) is empty")
	}
	if v.Provider != "" && v.Model == "" {
		return Variant{}, fmt.Errorf("provider %q needs a model", v.Provider)
	}
	if v.ReadOnly && v.CanSpawn {
		// subtask is not read-only, and its children could do what this one cannot.
		return Variant{}, fmt.Errorf("read_only and can_spawn cannot be combined")
	}
	if meta.Budgets.Wall != "" {
		if v.Budgets.Wall, err = time.ParseDuration(meta.Budgets.Wall); err != nil || v.Budgets.Wall <= 0 {
			return Variant{}, fmt.Errorf("budgets.wall %q: use a positive duration such as 10m", meta.Budgets.Wall)
		}
	}
	for _, f := range []struct {
		field string
		n     float64
	}{{"budgets.steps", float64(v.Budgets.Steps)}, {"budgets.tokens", float64(v.Budgets.Tokens)},
		{"budgets.cost", v.Budgets.Cost}, {"last_n", float64(v.LastN)}, {"result_cap", float64(v.ResultCap)}} {
		if f.n < 0 {
			return Variant{}, fmt.Errorf("%s must not be negative", f.field)
		}
	}
	if v.LastN > MaxLastN {
		return Variant{}, fmt.Errorf("last_n must be at most %d", MaxLastN)
	}
	for _, p := range slices.Concat(v.Allow, v.Deny) {
		if _, err := path.Match(p, ""); err != nil || strings.TrimSpace(p) == "" {
			return Variant{}, fmt.Errorf("invalid tool pattern %q", p)
		}
	}
	return v, nil
}

func split(raw []byte) (head []byte, body string, err error) {
	text := string(raw)
	first, rest, ok := strings.Cut(text, "\n")
	if !ok || strings.TrimSuffix(first, "\r") != "---" {
		return nil, text, nil
	}
	remaining := rest
	for remaining != "" {
		line, next, _ := strings.Cut(remaining, "\n")
		if strings.TrimSuffix(line, "\r") == "---" {
			return []byte(rest[:len(rest)-len(remaining)]), next, nil
		}
		remaining = next
	}
	return nil, "", fmt.Errorf("unterminated frontmatter")
}

// LoadVariants reads the authored variants in dir/variants (*.md; other
// files are ignored). It returns every variant that loaded and, joined, an
// error for each file that did not, so a bad file never hides the others.
// A missing variants directory is no variants.
func LoadVariants(dir string) (map[string]Variant, error) {
	out := map[string]Variant{}
	entries, err := os.ReadDir(filepath.Join(dir, "variants"))
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return out, fmt.Errorf("read variants: %w", err)
	}
	var errs []error
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".md")
		data, err := os.ReadFile(filepath.Join(dir, "variants", entry.Name())) // #nosec G304 -- An authored file in the agent directory.
		if err == nil {
			var v Variant
			if v, err = ParseVariant(name, data); err == nil {
				out[name] = v
				continue
			}
		}
		errs = append(errs, fmt.Errorf("variants/%s: %w", entry.Name(), err))
	}
	return out, errors.Join(errs...)
}
