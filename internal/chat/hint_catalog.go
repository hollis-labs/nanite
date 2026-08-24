package chat

// hint_catalog.go — Think-hint catalog loader (F5 / CW-20260420-0022).
//
// Loads the YAML hint catalog embedded from internal/chat/hints/hints.yaml.
// Exposes BuiltinHints() as the runtime catalog; the hint-selector peer agent
// consumes a summary of this catalog at dispatch time.
//
// Schema: see internal/chat/hints/hints.yaml for field documentation.

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// HintTriggers defines the optional structured matching conditions for a
// hint. All conditions are ANDed; empty/nil conditions are skipped.
type HintTriggers struct {
	// ScopeTierIn is the set of scope-tier strings that bias toward this hint.
	// Values: trivial, small, medium, large, open. Empty means "any tier".
	ScopeTierIn []string `yaml:"scope_tier_in"`

	// ReflexIDIn is the set of reflex IDs whose match biases toward this hint.
	// Empty means "any reflex (or none)".
	ReflexIDIn []string `yaml:"reflex_id_in"`

	// TextPattern is a lower-case substring matched against the normalized
	// user input. Empty means "no text constraint".
	TextPattern string `yaml:"text_pattern"`
}

// Hint is a single affordance entry in the think-hint catalog.
type Hint struct {
	// ID is the unique slug (a-z, 0-9, -).
	ID string `yaml:"id"`

	// Affordance is the display name (e.g. "scratchpad", "memory_recall").
	Affordance string `yaml:"affordance"`

	// Body is the prompt text injected when this hint is selected.
	// Kept to ~50 tokens per hint so multiple hints fit within the 200-token budget.
	Body string `yaml:"body"`

	// Triggers holds optional structured matching conditions.
	Triggers HintTriggers `yaml:"triggers"`

	// Priority is the tiebreak integer (0–100). Higher wins.
	Priority int `yaml:"priority"`
}

// hintFile is the top-level YAML structure expected in each hint catalog file.
type hintFile struct {
	Hints []Hint `yaml:"hints"`
}

//go:embed hints/*.yaml
var embeddedHints embed.FS

// builtinHints is the singleton loaded catalog, populated once.
var (
	builtinHintsOnce  sync.Once
	builtinHintsCache []Hint
	builtinHintsErr   error
)

// BuiltinHints returns the hint catalog loaded from the embedded YAML files.
// The catalog is loaded once and cached. On load failure an empty slice is
// returned (callers fall back to v1 static block).
//
// Callers must not mutate the returned slice.
func BuiltinHints() []Hint {
	builtinHintsOnce.Do(func() {
		hints, err := loadHintsFromFS(embeddedHints)
		if err != nil {
			builtinHintsErr = err
			builtinHintsCache = nil
			return
		}
		builtinHintsCache = hints
	})
	return builtinHintsCache
}

// BuiltinHintsErr returns any error encountered during the initial catalog
// load. Nil means the catalog was loaded successfully.
func BuiltinHintsErr() error {
	BuiltinHints() // ensure init
	return builtinHintsErr
}

// LoadHintsFromYAML parses a single YAML document and returns the hints it
// contains. Exposed for testing and migration tooling.
func LoadHintsFromYAML(data []byte) ([]Hint, error) {
	var f hintFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("hint catalog: yaml parse: %w", err)
	}
	if err := validateHints(f.Hints); err != nil {
		return nil, err
	}
	return f.Hints, nil
}

// loadHintsFromFS reads the catalogs embedded from internal/chat/hints/
// (currently internal/chat/hints/hints.yaml), merges them, and returns the hints.
func loadHintsFromFS(fsys embed.FS) ([]Hint, error) {
	var all []Hint

	err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".yaml") {
			return nil
		}
		data, readErr := fsys.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("hint catalog: read %s: %w", path, readErr)
		}
		hints, parseErr := LoadHintsFromYAML(data)
		if parseErr != nil {
			return fmt.Errorf("hint catalog: parse %s: %w", path, parseErr)
		}
		all = append(all, hints...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return all, nil
}

// validateHints checks each hint for required fields.
func validateHints(hints []Hint) error {
	seen := map[string]bool{}
	for i, h := range hints {
		if h.ID == "" {
			return fmt.Errorf("hint catalog: hint[%d] missing id", i)
		}
		if h.Affordance == "" {
			return fmt.Errorf("hint catalog: hint %q missing affordance", h.ID)
		}
		if h.Body == "" {
			return fmt.Errorf("hint catalog: hint %q missing body", h.ID)
		}
		if seen[h.ID] {
			return fmt.Errorf("hint catalog: duplicate hint id %q", h.ID)
		}
		seen[h.ID] = true
	}
	return nil
}

// HintByID returns the hint with the given ID from the catalog, or the zero
// value and false if not found.
func HintByID(catalog []Hint, id string) (Hint, bool) {
	for _, h := range catalog {
		if h.ID == id {
			return h, true
		}
	}
	return Hint{}, false
}

// HintCatalogSummary returns a compact representation of the catalog for
// embedding in the hint-selector peer's prompt. Each entry contains the
// id, affordance, body, and priority — the peer needs all four to rank.
type HintCatalogSummary struct {
	ID         string `json:"id"`
	Affordance string `json:"affordance"`
	Body       string `json:"body"`
	Priority   int    `json:"priority"`
}

// SummarizeHints produces the catalog summary slice for peer dispatch.
func SummarizeHints(catalog []Hint) []HintCatalogSummary {
	out := make([]HintCatalogSummary, len(catalog))
	for i, h := range catalog {
		out[i] = HintCatalogSummary{
			ID:         h.ID,
			Affordance: h.Affordance,
			Body:       h.Body,
			Priority:   h.Priority,
		}
	}
	return out
}
