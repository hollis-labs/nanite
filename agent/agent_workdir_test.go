package agent_test

import (
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/agent"
	"github.com/hollis-labs/nanite/internal/provider/fake"
)

func TestRelativeWorkDirIsRefused(t *testing.T) {
	_, err := agent.Open(t.Context(), agent.Options{Dir: t.TempDir(), WorkDir: "work", GlobalSkillsDir: "-", Provider: fake.New(fake.Text("x"))})
	if err == nil || !strings.Contains(err.Error(), "WorkDir") {
		t.Fatalf("relative WorkDir: %v", err)
	}
}
