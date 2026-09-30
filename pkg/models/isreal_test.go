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

func TestIsCLIWrapperModel(t *testing.T) {
	for id, want := range map[string]bool{
		"claude-cli": true, "codex-cli": true, "gemini-cli": true, "copilot-cli": true, "aider-cli": true,
		"claude-opus-5": false, "": false,
		"no-such-model": false, // unknown is not a wrapper: it may just be newer than the registry
	} {
		if got := IsCLIWrapperModel(id); got != want {
			t.Errorf("IsCLIWrapperModel(%q) = %v, want %v", id, got, want)
		}
	}
}

// A model that only the synced catalog knows is a real model once it has synced.
func TestIsRealModelSeesTheCatalogOverlay(t *testing.T) {
	const id = "overlay-only-model-for-test"
	if IsRealModel(id) {
		t.Fatal("precondition: the model must be unknown")
	}
	SyncFromCatalog(CatalogInput{ContextWindows: map[string]int{id: 1_000_000}})
	t.Cleanup(func() { SyncFromCatalog(CatalogInput{}) })
	if !IsRealModel(id) || IsCLIWrapperModel(id) {
		t.Error("an overlay-only model was not recognized after the catalog synced")
	}
}
