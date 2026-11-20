package subagent

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Variant is a kind of subtask: a role prompt, the tools it may use, an
// optional model and its budgets. Built-in variants ship with Nanite; an
// agent authors more as variants/<name>.md in its directory.
type Variant struct {
	Name        string
	Description string
	Prompt      string
	// ReadOnly offers only read-only tools.
	ReadOnly bool
	// Allow, when set, offers only tools matching one of its patterns; Deny
	// removes tools matching any of its patterns. A pattern is a tool name or
	// a path.Match glob such as "mcp__*". A variant only ever narrows the
	// tools the parent agent has; it cannot add one.
	Allow, Deny []string
	// Provider and Model select what the child runs on, like a use in
	// providers.yaml. Model alone keeps the agent's subagent provider (the
	// "subagent" use, else chat), which must serve that model.
	Provider, Model string
	Budgets         Budgets
	// LastN is how many parent messages the child sees by default; zero
	// means DefaultLastN.
	LastN int
	// ResultCap is the result size cap in tokens; zero uses the agent's cap
	// or DefaultResultCap.
	ResultCap int
	// CanSpawn lets this variant run subtask itself, one level deeper.
	CanSpawn bool
	Builtin  bool
}

// Budgets bound one subtask. Zero means the agent's own limit applies.
type Budgets struct {
	Steps  int
	Tokens int
	Wall   time.Duration
	// Cost is recorded and validated; it is enforced once usage carries a
	// price (the shared fan-out budget, B5).
	Cost float64
}

// Allows reports whether the variant offers a tool.
func (v Variant) Allows(name string, readOnly bool) bool {
	if v.ReadOnly && !readOnly {
		return false
	}
	if len(v.Allow) > 0 && !matchAny(v.Allow, name) {
		return false
	}
	return !matchAny(v.Deny, name)
}

func matchAny(patterns []string, name string) bool {
	return slices.ContainsFunc(patterns, func(p string) bool {
		ok, err := path.Match(p, name)
		return err == nil && ok
	})
}

const contract = " Return only what the return spec asks for, in its shape; never paste raw tool output, and keep what you return short enough for your parent to use. If you could not finish, return status partial or error and say what is missing. End by calling return_result: it is the only thing your parent receives."

// Builtins are the variants every agent has.
func Builtins() map[string]Variant {
	return map[string]Variant{
		"code": {Name: "code", Builtin: true, Description: "Tool-heavy work: run tools, edit files, return the outcome.",
			Prompt: "You are a code subtask. Do the tool work needed to reach the goal and report the outcome, not the steps: what changed (name the files), what you ran to check it, and what you did not verify." + contract},
		"research": {Name: "research", Builtin: true, Description: "Broad reading and search; returns findings with sources.",
			Prompt: "You are a research subtask. Read and search broadly, then return findings, each with its source (a path, URL or record id) so your parent can check it. Say which findings you inferred rather than read." + contract},
		"worker": {Name: "worker", Builtin: true, Description: "General work on a focused task.",
			Prompt: "You are a worker subtask. Complete the assigned goal and report the result to your parent." + contract},
		// MCP tools are denied: a server's readOnlyHint is the server's claim,
		// not Nanite's, so it does not make a tool safe for a read-only
		// planner. An authored variant may allow them explicitly.
		"planner": {Name: "planner", Builtin: true, ReadOnly: true, Deny: []string{"mcp__*"}, Description: "Read-only; gathers context and returns a plan.",
			Prompt: "You are a planner subtask. Use read-only tools to gather context and return a concrete plan: ordered steps that name the files, commands or records each one touches, and the open questions you could not settle. You cannot change anything." + contract},
	}
}

var validName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)

// Load returns the built-in variants plus those authored in dir/variants.
// It returns every variant that loaded and, joined, an error for each file
// that did not; a bad file never hides the others.
func Load(dir string) (map[string]Variant, error) {
	out := Builtins()
	entries, err := os.ReadDir(filepath.Join(dir, "variants"))
	if errors.Is(err, os.ErrNotExist) {
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
		file := filepath.Join(dir, "variants", entry.Name())
		v, err := parse(strings.TrimSuffix(entry.Name(), ".md"), file)
		if err != nil {
			errs = append(errs, fmt.Errorf("variants/%s: %w", entry.Name(), err))
			continue
		}
		if existing, ok := out[v.Name]; ok && existing.Builtin {
			errs = append(errs, fmt.Errorf("variants/%s: %q is a built-in variant; choose another name", entry.Name(), v.Name))
			continue
		}
		out[v.Name] = v
	}
	return out, errors.Join(errs...)
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

func parse(name, file string) (Variant, error) {
	if !validName.MatchString(name) || name == "chat" {
		return Variant{}, fmt.Errorf("invalid variant name %q: use lower-case letters, digits and dashes", name)
	}
	raw, err := os.ReadFile(file) // #nosec G304 -- An authored file in the agent directory.
	if err != nil {
		return Variant{}, err
	}
	head, body, err := split(raw)
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
	for field, n := range map[string]float64{"budgets.steps": float64(v.Budgets.Steps), "budgets.tokens": float64(v.Budgets.Tokens),
		"budgets.cost": v.Budgets.Cost, "last_n": float64(v.LastN), "result_cap": float64(v.ResultCap)} {
		if n < 0 {
			return Variant{}, fmt.Errorf("%s must not be negative", field)
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

func names(variants map[string]Variant) string {
	return strings.Join(slices.Sorted(maps.Keys(variants)), ", ")
}

// Names lists the variants, sorted.
func Names(variants map[string]Variant) []string { return slices.Sorted(maps.Keys(variants)) }
