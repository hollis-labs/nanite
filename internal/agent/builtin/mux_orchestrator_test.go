//go:build devmode

package builtin

import (
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/agent"
)

func TestMuxOrchestrator_Parses(t *testing.T) {
	def, err := MuxOrchestratorAgent()
	if err != nil {
		t.Fatal(err)
	}
	if def.Slug != "mux-orchestrator" {
		t.Fatalf("want slug=mux-orchestrator, got %q", def.Slug)
	}
	if !strings.Contains(def.SystemPrompt, "mux_send") {
		t.Fatal("system prompt should reference mux_send")
	}
	if def.Source != SourceInternal {
		t.Fatalf("source = %q, want %q", def.Source, SourceInternal)
	}
	class := agent.NewClassification("", "").Classify(def.Source, def.SourceRef)
	if class != agent.ManageClassInternal || class.Editable() {
		t.Fatalf("classification = %q (editable=%t), want internal/read-only", class, class.Editable())
	}
}
