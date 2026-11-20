package subagent

import (
	"maps"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/hollis-labs/nanite/agent/authored"
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
	// providers.yaml. Model alone runs on the provider of the "subtask" use
	// (else chat), which must serve that model.
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

// Load returns the built-in variants plus those authored in dir/variants,
// which agent/authored parses and validates. It returns every variant that
// loaded and, joined, an error for each file that did not; a bad file never
// hides the others.
func Load(dir string) (map[string]Variant, error) {
	out := Builtins()
	authoredVariants, err := authored.LoadVariants(dir)
	for name, a := range authoredVariants {
		out[name] = Variant{Name: a.Name, Description: a.Description, Prompt: a.Prompt, ReadOnly: a.ReadOnly,
			Allow: a.Allow, Deny: a.Deny, Provider: a.Provider, Model: a.Model,
			Budgets: Budgets{Steps: a.Budgets.Steps, Tokens: a.Budgets.Tokens, Wall: a.Budgets.Wall, Cost: a.Budgets.Cost},
			LastN:   a.LastN, ResultCap: a.ResultCap, CanSpawn: a.CanSpawn}
	}
	return out, err
}

func names(variants map[string]Variant) string {
	return strings.Join(slices.Sorted(maps.Keys(variants)), ", ")
}

// Names lists the variants, sorted.
func Names(variants map[string]Variant) []string { return slices.Sorted(maps.Keys(variants)) }
