//go:build eval

package eval

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hollis-labs/go-providers/provider"
)

// ChatProvider is the minimal interface the runner needs. It matches the
// provider.Provider interface so real providers drop in without adaptation.
type ChatProvider interface {
	Complete(ctx context.Context, req provider.ChatRequest) (string, error)
}

// RunOptions configures a Run call.
type RunOptions struct {
	// Model is the model name passed to the provider (e.g. "claude-sonnet-4-5").
	// May be overridden per scenario in future; v1 uses one model for all.
	Model string
}

// Run executes all scenarios against p and returns one Result per scenario.
// Context cancellation propagates to individual completions.
func Run(ctx context.Context, scenarios []Scenario, p ChatProvider, opts RunOptions) []Result {
	results := make([]Result, 0, len(scenarios))
	for _, s := range scenarios {
		r := runOne(ctx, s, p, opts)
		results = append(results, r)
	}
	return results
}

func runOne(ctx context.Context, s Scenario, p ChatProvider, opts RunOptions) Result {
	// Build tool definitions from stubs.
	tools := make([]provider.ToolDefinition, 0, len(s.ToolsOffered))
	for _, stub := range s.ToolsOffered {
		tools = append(tools, provider.ToolDefinition{
			Name:        stub.Name,
			Description: stub.Description,
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		})
	}

	// Build message history (prev turns + current user message).
	messages := make([]provider.ChatMessage, 0, len(s.PrevTurns)+1)
	for _, t := range s.PrevTurns {
		messages = append(messages, provider.ChatMessage{
			Role:    t.Role,
			Content: t.Content,
		})
	}
	messages = append(messages, provider.ChatMessage{
		Role:    "user",
		Content: s.UserMessage,
	})

	req := provider.ChatRequest{
		Model:    opts.Model,
		Messages: messages,
		Tools:    tools,
	}

	response, err := p.Complete(ctx, req)
	if err != nil {
		return Result{
			ScenarioID: s.ID,
			Benchmark:  s.Benchmark,
			Passed:     false,
			Score:      0.0,
			Reason:     fmt.Sprintf("provider error: %v", err),
		}
	}

	return Score(s, response)
}

// Summarize aggregates a slice of Results into per-benchmark summaries plus
// an "overall" summary. The returned slice is sorted by Benchmark name;
// "overall" is always last.
func Summarize(results []Result) []BenchmarkSummary {
	byBenchmark := map[string]*BenchmarkSummary{}
	for _, r := range results {
		b := r.Benchmark
		if byBenchmark[b] == nil {
			byBenchmark[b] = &BenchmarkSummary{Benchmark: b}
		}
		byBenchmark[b].Total++
		if r.Passed {
			byBenchmark[b].Passed++
		}
	}
	for _, s := range byBenchmark {
		if s.Total > 0 {
			s.Score = float64(s.Passed) / float64(s.Total)
		}
	}

	summaries := make([]BenchmarkSummary, 0, len(byBenchmark)+1)
	for _, s := range byBenchmark {
		summaries = append(summaries, *s)
	}
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].Benchmark < summaries[j].Benchmark
	})

	// Append overall.
	var totalTotal, totalPassed int
	for _, s := range summaries {
		totalTotal += s.Total
		totalPassed += s.Passed
	}
	overall := BenchmarkSummary{Benchmark: "overall", Total: totalTotal, Passed: totalPassed}
	if totalTotal > 0 {
		overall.Score = float64(totalPassed) / float64(totalTotal)
	}
	summaries = append(summaries, overall)

	return summaries
}

// PrintReport writes a formatted score table to the provided strings.Builder.
func PrintReport(summaries []BenchmarkSummary) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%-20s  %9s  %6s  %6s\n", "Benchmark", "Scenarios", "Passed", "Score")
	sb.WriteString(strings.Repeat("-", 48) + "\n")
	for _, s := range summaries {
		fmt.Fprintf(&sb, "%-20s  %9d  %6d  %6.2f\n", s.Benchmark, s.Total, s.Passed, s.Score)
	}
	return sb.String()
}
