package registryagent

import (
	"crypto/rand"
	"testing"
)

func TestIsStageMatchesOnlyThisDestination(t *testing.T) {
	prefix := ".nanite-stage-agent-"
	token := rand.Text()
	for name, want := range map[string]bool{
		prefix + token:                        true,
		".nanite-stage-agent-bar-" + token:    false, // a sibling folder named agent-bar
		prefix + token[:25]:                   false,
		prefix + token + "x":                  false,
		prefix + "abcdefghijklmnopqrstuvwxyz": false, // lowercase is not rand.Text
		".nanite-backup-agent-" + token:       false,
	} {
		if got := isStage(name, prefix); got != want {
			t.Errorf("isStage(%q) = %v, want %v", name, got, want)
		}
	}
}
