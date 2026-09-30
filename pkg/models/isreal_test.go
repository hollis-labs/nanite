package models

import "testing"

func TestIsRealModel(t *testing.T) {
	for id, want := range map[string]bool{
		"claude-opus-5": true, "claude-sonnet-5": true,
		"claude-cli": false, "codex-cli": false, "gemini-cli": false, "copilot-cli": false, "aider-cli": false, // wrappers
		"": false, "no-such-model": false, "bootprofile:claude-smoke": false,
	} {
		if got := IsRealModel(id); got != want {
			t.Errorf("IsRealModel(%q) = %v, want %v", id, got, want)
		}
	}
}
