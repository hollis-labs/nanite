package agent

import (
	"testing"
	"time"

	"github.com/hollis-labs/libs/agent/agentdir"
)

// host.yaml's compaction and stream keys apply unless an Option sets them.
func TestRuntimeSettingsOptionWinsOverHostYAML(t *testing.T) {
	file := agentdir.Runtime{CompactAt: 110_000, CompactKeepTokens: 22_000, CompactPrompt: "from host.yaml",
		CompactInputTokens: 80_000, IdleTimeout: "3m"}
	got, err := runtimeSettings(Options{}, file)
	if err != nil {
		t.Fatal(err)
	}
	want := runtimeConfig{compactAt: 110_000, keepTokens: 22_000, inputTokens: 80_000, prompt: "from host.yaml", idleTimeout: 3 * time.Minute}
	if got != want {
		t.Fatalf("from host.yaml: %+v, want %+v", got, want)
	}
	got, err = runtimeSettings(Options{CompactAt: 50_000, CompactKeepTokens: 9_000, CompactionPrompt: "from options",
		CompactionInputTokens: 40_000, IdleTimeout: 2 * time.Minute}, file)
	if err != nil {
		t.Fatal(err)
	}
	want = runtimeConfig{compactAt: 50_000, keepTokens: 9_000, inputTokens: 40_000, prompt: "from options", idleTimeout: 2 * time.Minute}
	if got != want {
		t.Fatalf("options: %+v, want %+v", got, want)
	}
	if got, err := runtimeSettings(Options{}, agentdir.Runtime{}); err != nil || got != (runtimeConfig{}) {
		t.Fatalf("neither: %+v, %v", got, err)
	}
	if _, err := runtimeSettings(Options{}, agentdir.Runtime{IdleTimeout: "soon"}); err == nil {
		t.Fatal("a bad idle_timeout was accepted")
	}
}
