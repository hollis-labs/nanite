//go:build eval

// Command nanite-eval runs the interaction-quality eval suite and prints a
// per-benchmark score table.
//
// Usage:
//
//	go run -tags eval ./cmd/nanite-eval [--scenarios <dir>]
//
// Or via make:
//
//	make eval
//
// The suite is deterministic by default (mock provider). Set NANITE_EVAL_LIVE=1
// to run against the real provider (requires ANTHROPIC_API_KEY or equivalent).
//
// I3 / CW-20260420-0028.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/eval"
)

func main() {
	scenariosDir := flag.String("scenarios", defaultScenariosDir(), "directory of scenario YAML files (recursive)")
	flag.Parse()

	scenarios, err := eval.LoadScenariosDir(*scenariosDir)
	if err != nil {
		log.Fatalf("loading scenarios: %v", err)
	}
	if len(scenarios) == 0 {
		log.Fatalf("no scenarios found in %s", *scenariosDir)
	}
	fmt.Printf("Loaded %d scenario(s) from %s\n\n", len(scenarios), *scenariosDir)

	p := buildProvider()
	results := eval.Run(context.Background(), scenarios, p, eval.RunOptions{Model: "mock"})

	// Print per-scenario detail.
	for _, r := range results {
		status := "PASS"
		if !r.Passed {
			status = "FAIL"
		}
		fmt.Printf("[%s] %s (%s)", status, r.ScenarioID, r.Benchmark)
		if !r.Passed {
			fmt.Printf(" — %s", r.Reason)
		}
		fmt.Println()
	}
	fmt.Println()

	summaries := eval.Summarize(results)
	fmt.Print(eval.PrintReport(summaries))

	// Exit non-zero if any scenario failed.
	for _, r := range results {
		if !r.Passed {
			os.Exit(1)
		}
	}
}

// buildProvider returns a mock provider unless NANITE_EVAL_LIVE=1, in which
// case it returns a stub that panics with a message directing the caller to
// implement live wiring in v2.
func buildProvider() eval.ChatProvider {
	if os.Getenv("NANITE_EVAL_LIVE") == "1" {
		panic("live provider not implemented in v1 pilot; see docs/evals.md for v2 roadmap")
	}
	return &deterministicMock{}
}

// deterministicMock is a stub provider that returns a recognisable placeholder
// response. Real scoring only fires in runner_test.go where per-scenario mocks
// inject targeted responses; this mock is for smoke-testing the CLI pipeline.
type deterministicMock struct{}

func (d *deterministicMock) Complete(_ context.Context, req provider.ChatRequest) (string, error) {
	return "mock response: no live provider configured", nil
}

// defaultScenariosDir returns the path to evals/scenarios relative to this
// binary's source file. Works for `go run` and installed binaries built from
// a checkout.
func defaultScenariosDir() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "evals/scenarios"
	}
	// cmd/nanite-eval/main.go → ../../evals/scenarios
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "evals", "scenarios")
}
