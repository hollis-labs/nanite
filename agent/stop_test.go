package agent_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/agent"
	"github.com/hollis-labs/nanite/internal/control"
	"github.com/hollis-labs/nanite/internal/provider/fake"
)

func TestLocalControlStopsOpenAgent(t *testing.T) {
	dir := t.TempDir()
	writeAgent(t, dir)
	p := fake.New(fake.Response{Events: []llmtypes.StreamEvent{{Type: llmtypes.EventDelta, Content: "running"}}, Stall: true})
	a, err := agent.Open(t.Context(), agent.Options{Dir: dir, GlobalSkillsDir: "-", Provider: p})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	c, err := a.NewConversation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	run, err := c.Send(t.Context(), agent.Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	started := false
	for event := range run.Events() {
		if _, ok := event.(agent.TextDelta); ok {
			started = true
			break
		}
	}
	if !started {
		t.Fatal("model stream did not start")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := control.Stop(ctx, filepath.Join(dir, ".nanite")); err != nil {
		t.Fatal(err)
	}
	if _, err := run.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatalf("active run: %v", err)
	}
	if _, err := c.Send(t.Context(), agent.Input{Text: "again"}); !errors.Is(err, agent.ErrStopped) {
		t.Fatalf("admission after stop: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if err := control.Stop(ctx, filepath.Join(dir, ".nanite")); err == nil {
		t.Fatal("endpoint survived Close")
	}
}

func TestOpenWithoutControlTempDirectory(t *testing.T) {
	dir := t.TempDir()
	writeAgent(t, dir)
	t.Setenv("TMPDIR", filepath.Join(dir, "missing"))
	a, err := agent.Open(t.Context(), agent.Options{Dir: dir, GlobalSkillsDir: "-", Provider: fake.New()})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.StopAll(t.Context()); err != nil {
		t.Fatal(err)
	}
}
