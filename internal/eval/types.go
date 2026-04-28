//go:build eval

// Package eval provides the interaction-quality eval harness for nanite.
//
// The suite is gated behind the "eval" build tag so it never runs during
// default `go test ./...`. Use `go test -tags eval ./internal/eval/...`
// or `make eval` to run it.
//
// Pilot scope (v1): ≥1 scenario each from NoisyToolBench, CAR-bench, and
// ClarifyMT. Full dataset integration is v2. Scoring is deterministic
// (no LLM-as-judge) — binary pass/fail per rubric.
//
// I3 / CW-20260420-0028.
package eval

// Scenario is a single eval test case loaded from a YAML file.
type Scenario struct {
	ID          string `yaml:"id"`
	Benchmark   string `yaml:"benchmark"`   // "noisytoolbench" | "carbench" | "clarifymt"
	Description string `yaml:"description"`

	// Input
	UserMessage  string       `yaml:"user_message"`
	ToolsOffered []ToolDefStub `yaml:"tools_offered,omitempty"`
	PrevTurns    []TurnStub   `yaml:"prev_turns,omitempty"` // ClarifyMT multi-turn

	// Expected
	ExpectedBehavior string `yaml:"expected_behavior"` // human-readable goal
	Rubric           Rubric `yaml:"rubric"`
}

// ToolDefStub is a lightweight tool definition used in eval scenarios.
// Matches the shape callers send to the provider; descriptions are kept
// minimal since scenarios bake in the tool list.
type ToolDefStub struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// TurnStub is a single previous conversation turn for multi-turn scenarios.
type TurnStub struct {
	Role    string `yaml:"role"`    // "user" | "assistant"
	Content string `yaml:"content"`
}

// Rubric holds scoring criteria for a scenario. Only the fields relevant to
// the benchmark need to be set; unused fields are zero-valued and ignored by
// the scorer.
//
// Scoring is binary in v1: all applicable criteria must pass for a 1.0 score.
// LLM-as-judge is deferred to v2.
type Rubric struct {
	// CAR-bench: true → response must contain a clarifying question.
	// false → response must NOT ask for clarification.
	AskedClarification *bool `yaml:"asked_clarification,omitempty"`

	// NoisyToolBench: the tool call the response must contain.
	CalledTool string `yaml:"called_tool,omitempty"`

	// NoisyToolBench: tool names the response must NOT call.
	AvoidedTools []string `yaml:"avoided_tools,omitempty"`

	// General text-presence checks (any match → criterion passes).
	ContainsAny []string `yaml:"contains_any,omitempty"`

	// General text-absence checks (any match → criterion fails).
	DoesNotContain []string `yaml:"does_not_contain,omitempty"`
}

// Result captures the outcome of a single scenario run.
type Result struct {
	ScenarioID string
	Benchmark  string
	Passed     bool
	Score      float64 // 1.0 or 0.0 in v1
	Reason     string  // human-readable failure explanation, empty on pass
	Response   string  // raw model response (truncated for readability)
}

// BenchmarkSummary aggregates results for one benchmark label.
type BenchmarkSummary struct {
	Benchmark string
	Total     int
	Passed    int
	Score     float64
}
