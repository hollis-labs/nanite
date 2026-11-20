package agent

import (
	"errors"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/agent/steer"
	"github.com/hollis-labs/nanite/internal/provider/fake"
)

// The host sets the mode through the conversation; it reaches the model and
// survives reopening the agent.
func TestConversationModeThroughTheAgent(t *testing.T) {
	dir := agentDirOfKind(t, "kind: embedded\nlifetime: application\napplication_id: steer-test\n")
	reg, err := steer.NewRegistry(steer.DefaultPresets()...)
	if err != nil {
		t.Fatal(err)
	}
	p := &recordingProvider{Provider: fake.New(fake.Text("ok"))}
	a, err := Open(t.Context(), Options{Dir: dir, GlobalSkillsDir: "-", Provider: p, Steering: &Steering{Registry: reg}})
	if err != nil {
		t.Fatal(err)
	}
	c, err := a.NewConversation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetMode(t.Context(), "triage"); err != nil {
		t.Fatal(err)
	}
	run, err := c.Send(t.Context(), Input{Text: "what is waiting?"})
	if err != nil {
		t.Fatal(err)
	}
	var steered Steered
	for ev := range run.Events() {
		if s, ok := ev.(Steered); ok {
			steered = s
		}
	}
	if _, err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	if steered.Mode != "triage" || steered.ModeSource != steer.SourceDeliberate {
		t.Fatalf("steered %+v", steered)
	}
	var system strings.Builder
	for _, b := range p.last.SlotBlocks {
		system.WriteString(b.Content)
	}
	if !strings.Contains(system.String(), "Mode: triage.") {
		t.Fatalf("mode not sent: %s", system.String())
	}
	id := c.ID()
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}

	b, err := Open(t.Context(), Options{Dir: dir, GlobalSkillsDir: "-", Provider: fake.New(), Steering: &Steering{Registry: reg}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close() }()
	c2, err := b.Conversation(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if mode, err := c2.Mode(t.Context()); err != nil || mode != "triage" {
		t.Fatalf("mode after reopening: %q %v", mode, err)
	}
}

func TestModeNeedsSteering(t *testing.T) {
	dir := agentDirOfKind(t, "kind: embedded\nlifetime: application\napplication_id: steer-test\n")
	a, err := Open(t.Context(), Options{Dir: dir, GlobalSkillsDir: "-", Provider: fake.New()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	c, err := a.NewConversation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetMode(t.Context(), "focus"); !errors.Is(err, ErrNoSteering) {
		t.Fatalf("without steering: %v", err)
	}
	other := agentDirOfKind(t, "kind: embedded\nlifetime: application\napplication_id: steer-test\n")
	if _, err := Open(t.Context(), Options{Dir: other, GlobalSkillsDir: "-", Provider: fake.New(), Steering: &Steering{}}); err == nil || !strings.Contains(err.Error(), "Registry is required") {
		t.Fatalf("steering without a registry: %v", err)
	}
}
