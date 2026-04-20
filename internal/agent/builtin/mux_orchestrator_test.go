package builtin

import (
	"strings"
	"testing"
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
}
