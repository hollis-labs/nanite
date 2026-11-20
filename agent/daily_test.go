package agent_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/agent"
	"github.com/hollis-labs/nanite/internal/provider/fake"
)

func TestDailyBudgetPersistsAcrossOpen(t *testing.T) {
	dir := t.TempDir()
	writeAgent(t, dir)
	response := fake.Text("done")
	response.Events = append([]llmtypes.StreamEvent{{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{InputTokens: 80, OutputTokens: 20}}}, response.Events...)
	opts := agent.Options{Dir: dir, GlobalSkillsDir: "-", Provider: fake.New(response), DailyTokenBudget: 100}
	a, err := agent.Open(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	c, err := a.NewConversation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.Send(t.Context(), agent.Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = agent.Open(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	c, err = a.NewConversation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Send(t.Context(), agent.Input{Text: "blocked"}); !errors.Is(err, agent.ErrDailyBudget) {
		t.Fatalf("restart cap: %v", err)
	}
}

func TestDailyBudgetHostFallbackAndOverride(t *testing.T) {
	for _, override := range []int{0, 200} {
		t.Run(fmt.Sprint(override), func(t *testing.T) {
			dir := t.TempDir()
			writeAgent(t, dir)
			file := filepath.Join(dir, "host.yaml")
			raw, err := os.ReadFile(file) // #nosec G304 -- Fixed fixture name in t.TempDir.
			if err != nil {
				t.Fatal(err)
			}

			if err := os.WriteFile(file, []byte(strings.Replace(string(raw), "runtime:", "runtime:\n  daily_token_budget: 100", 1)), 0600); err != nil { // #nosec G703 -- Fixed fixture name in t.TempDir.
				t.Fatal(err)
			}
			response := fake.Text("done")
			response.Events = append([]llmtypes.StreamEvent{{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{InputTokens: 100}}}, response.Events...)
			a, err := agent.Open(t.Context(), agent.Options{Dir: dir, GlobalSkillsDir: "-", Provider: fake.New(response, fake.Text("second")), DailyTokenBudget: override})
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			c, err := a.NewConversation(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			r, err := c.Send(t.Context(), agent.Input{Text: "go"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := r.Wait(); err != nil {
				t.Fatal(err)
			}
			r, err = c.Send(t.Context(), agent.Input{Text: "next"})
			if override == 0 {
				if !errors.Is(err, agent.ErrDailyBudget) {
					t.Fatalf("host cap: %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if _, err := r.Wait(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
