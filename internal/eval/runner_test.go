//go:build eval

package eval

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
)

// mockProvider implements ChatProvider with a configurable response function.
type mockProvider struct {
	fn func(req llmtypes.ChatRequest) (string, error)
}

func (m *mockProvider) Complete(ctx context.Context, req llmtypes.ChatRequest) (string, error) {
	return m.fn(req)
}

// scenariosDir returns the absolute path to the baked-in scenario files.
// It is computed relative to this file's location so tests work regardless of
// cwd (go test changes cwd to the package dir, but runtime.Caller gives us the
// source-file path which is stable for local runs).
func scenariosDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// internal/eval/runner_test.go → ../../evals/scenarios
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	dir := filepath.Join(repoRoot, "evals", "scenarios")
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("scenarios dir not found at %s: %v", dir, err)
	}
	return dir
}

// TestLoadScenarios verifies that all three baked-in scenario files load
// without error and each has a non-empty ID and Benchmark.
func TestLoadScenarios(t *testing.T) {
	dir := scenariosDir(t)
	scenarios, err := LoadScenariosDir(dir)
	if err != nil {
		t.Fatalf("LoadScenariosDir: %v", err)
	}
	if len(scenarios) < 3 {
		t.Fatalf("expected at least 3 scenarios, got %d", len(scenarios))
	}
	for _, s := range scenarios {
		if s.ID == "" {
			t.Errorf("scenario missing id: %+v", s)
		}
		if s.Benchmark == "" {
			t.Errorf("scenario %s missing benchmark", s.ID)
		}
	}
}

// TestRunNoisyToolBench_Pass feeds a mock response that names get_weather but
// none of the distractor tools; expects score 1.0.
func TestRunNoisyToolBench_Pass(t *testing.T) {
	s := noisyToolBenchScenario(t)
	p := &mockProvider{fn: func(_ llmtypes.ChatRequest) (string, error) {
		return `I'll call get_weather to fetch the current conditions.`, nil
	}}
	result := runOne(context.Background(), s, p, RunOptions{Model: "mock"})
	if !result.Passed {
		t.Errorf("expected pass, got fail: %s", result.Reason)
	}
	if result.Score != 1.0 {
		t.Errorf("score = %.2f, want 1.0", result.Score)
	}
}

// TestRunNoisyToolBench_Fail feeds a mock response that calls a distractor
// tool; expects score 0.0.
func TestRunNoisyToolBench_Fail(t *testing.T) {
	s := noisyToolBenchScenario(t)
	p := &mockProvider{fn: func(_ llmtypes.ChatRequest) (string, error) {
		return `Let me use web_search to look that up.`, nil
	}}
	result := runOne(context.Background(), s, p, RunOptions{Model: "mock"})
	if result.Passed {
		t.Error("expected fail, got pass")
	}
	if result.Score != 0.0 {
		t.Errorf("score = %.2f, want 0.0", result.Score)
	}
}

// TestRunCARBench_Pass feeds a response that asks a clarifying question.
func TestRunCARBench_Pass(t *testing.T) {
	s := carBenchScenario(t)
	p := &mockProvider{fn: func(_ llmtypes.ChatRequest) (string, error) {
		return `Could you clarify what you'd like me to move?`, nil
	}}
	result := runOne(context.Background(), s, p, RunOptions{Model: "mock"})
	if !result.Passed {
		t.Errorf("expected pass, got fail: %s", result.Reason)
	}
}

// TestRunCARBench_Fail feeds a response that acts without clarifying.
func TestRunCARBench_Fail(t *testing.T) {
	s := carBenchScenario(t)
	p := &mockProvider{fn: func(_ llmtypes.ChatRequest) (string, error) {
		return `Done! I moved it to the top.`, nil
	}}
	result := runOne(context.Background(), s, p, RunOptions{Model: "mock"})
	if result.Passed {
		t.Error("expected fail, got pass")
	}
}

// TestRunClarifyMT_Pass feeds a response that schedules without further asking.
func TestRunClarifyMT_Pass(t *testing.T) {
	s := clarifyMTScenario(t)
	p := &mockProvider{fn: func(_ llmtypes.ChatRequest) (string, error) {
		return `I've scheduled the meeting with the backend team for tomorrow at 2pm.`, nil
	}}
	result := runOne(context.Background(), s, p, RunOptions{Model: "mock"})
	if !result.Passed {
		t.Errorf("expected pass, got fail: %s", result.Reason)
	}
}

// TestRunClarifyMT_Fail feeds a response that still asks for clarification.
func TestRunClarifyMT_Fail(t *testing.T) {
	s := clarifyMTScenario(t)
	p := &mockProvider{fn: func(_ llmtypes.ChatRequest) (string, error) {
		return `Which backend team did you mean? Could you clarify?`, nil
	}}
	result := runOne(context.Background(), s, p, RunOptions{Model: "mock"})
	if result.Passed {
		t.Error("expected fail, got pass")
	}
}

// TestSummarize verifies per-benchmark aggregation and the overall row.
func TestSummarize(t *testing.T) {
	results := []Result{
		{ScenarioID: "n01", Benchmark: "noisytoolbench", Passed: true, Score: 1.0},
		{ScenarioID: "c01", Benchmark: "carbench", Passed: true, Score: 1.0},
		{ScenarioID: "m01", Benchmark: "clarifymt", Passed: false, Score: 0.0},
	}
	summaries := Summarize(results)
	// Should have 3 benchmark rows + overall.
	if len(summaries) != 4 {
		t.Fatalf("expected 4 summaries (3 benchmarks + overall), got %d", len(summaries))
	}
	last := summaries[len(summaries)-1]
	if last.Benchmark != "overall" {
		t.Errorf("last summary should be 'overall', got %q", last.Benchmark)
	}
	if last.Total != 3 {
		t.Errorf("overall total = %d, want 3", last.Total)
	}
	if last.Passed != 2 {
		t.Errorf("overall passed = %d, want 2", last.Passed)
	}
	wantScore := 2.0 / 3.0
	if last.Score < wantScore-0.001 || last.Score > wantScore+0.001 {
		t.Errorf("overall score = %.4f, want %.4f", last.Score, wantScore)
	}
}

// TestLiveEval runs the full suite against the real provider when
// NANITE_EVAL_LIVE=1 is set. Skipped in CI by default.
func TestLiveEval(t *testing.T) {
	if os.Getenv("NANITE_EVAL_LIVE") != "1" {
		t.Skip("NANITE_EVAL_LIVE not set; skipping live eval")
	}
	// Live mode wires in the real Anthropic provider. Build constraints on
	// ANTHROPIC_API_KEY are left to the caller; the test fails naturally if the
	// key is absent.
	t.Log("live mode: integrate real provider here in v2")
	t.Skip("live provider integration not implemented in v1 pilot")
}

// ---- helpers ----

func noisyToolBenchScenario(t *testing.T) Scenario {
	t.Helper()
	return loadByID(t, "n01_distractor_tools")
}

func carBenchScenario(t *testing.T) Scenario {
	t.Helper()
	return loadByID(t, "c01_ambiguous_referent")
}

func clarifyMTScenario(t *testing.T) Scenario {
	t.Helper()
	return loadByID(t, "m01_two_turn_resolution")
}

func loadByID(t *testing.T, id string) Scenario {
	t.Helper()
	dir := scenariosDir(t)
	scenarios, err := LoadScenariosDir(dir)
	if err != nil {
		t.Fatalf("LoadScenariosDir: %v", err)
	}
	for _, s := range scenarios {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("scenario %q not found in %s", id, dir)
	return Scenario{}
}
