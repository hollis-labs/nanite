package main

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/evals"
)

func TestOfflineRunWritesResults(t *testing.T) {
	out := t.TempDir()
	if err := run(context.Background(), []string{"-scenario", "code-search", "-out", out}, nil, nil); err != nil {
		t.Fatal(err)
	}
	md, err := os.ReadFile(filepath.Join(out, "results.md")) //nolint:gosec // the test's own temp dir
	if err != nil || !strings.Contains(string(md), "| code-search | subtask | yes |") || !strings.Contains(string(md), "Model: scripted") {
		t.Fatalf("results.md: %v\n%s", err, md)
	}
	var rep evals.Report
	data, err := os.ReadFile(filepath.Join(out, "results.json")) //nolint:gosec // the test's own temp dir
	if err != nil || json.Unmarshal(data, &rep) != nil || len(rep.Results) != 2 {
		t.Fatalf("results.json: %v\n%s", err, data)
	}
}

// TestInterruptedRunStillWrites: a cancelled run writes what it has.
func TestInterruptedRunStillWrites(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	if err := run(ctx, []string{"-scenario", "chore"}, &out, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Stopped early: interrupted") {
		t.Fatalf("output:\n%s", out.String())
	}
}

func TestBadFlags(t *testing.T) {
	for _, args := range [][]string{{"-mode", "both"}, {"-scenario", "nope"}} {
		if err := run(context.Background(), args, nil, nil); err == nil {
			t.Errorf("%v: no error", args)
		}
	}
}

// TestLiveNeedsBounds: no live run without a finite dollar cap of at most
// $50, a token cap, positive prices and a model of the provider's own.
func TestLiveNeedsBounds(t *testing.T) {
	p := evals.ConservativePrice
	ok := func(f func(*evals.Price)) evals.Price { q := p; f(&q); return q }
	for name, tc := range map[string]struct {
		provider, model string
		usd             float64
		tokens          int
		price           evals.Price
	}{
		"no cap":        {"anthropic", "claude-sonnet-5-5", 0, 1000, p},
		"infinite cap":  {"anthropic", "claude-sonnet-5-5", math.Inf(1), 1000, p},
		"cap over 50":   {"anthropic", "claude-sonnet-5-5", 51, 1000, p},
		"no token cap":  {"anthropic", "claude-sonnet-5-5", 10, 0, p},
		"zero price":    {"anthropic", "claude-sonnet-5-5", 10, 1000, ok(func(q *evals.Price) { q.CacheRead = 0 })},
		"wrong model":   {"anthropic", "gpt-6", 10, 1000, p},
		"wrong model 2": {"openai", "claude-sonnet-5-5", 10, 1000, p},
		"bad provider":  {"other", "x", 10, 1000, p},
	} {
		if err := checkLive(tc.provider, tc.model, tc.usd, tc.tokens, tc.price); err == nil {
			t.Errorf("%s: allowed", name)
		}
	}
	if err := checkLive("anthropic", "claude-sonnet-5-5", 10, 150000, p); err != nil {
		t.Errorf("the documented command is refused: %v", err)
	}
	if err := run(context.Background(), []string{"-live", "-max-usd", "0"}, nil, nil); err == nil {
		t.Error("a live run without a cap started")
	}
}
