package api

import (
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestVisibleProviderRows_allowlist(t *testing.T) {
	rows := []store.ProviderConfig{
		{ProviderType: "openai"},
		{ProviderType: "anthropic"},
	}

	t.Run("unset shows everything", func(t *testing.T) {
		t.Setenv("NANITE_VISIBLE_PROVIDERS", "")
		if got := visibleProviderRows(append([]store.ProviderConfig(nil), rows...)); len(got) != 2 {
			t.Fatalf("got %d, want 2", len(got))
		}
	})

	t.Run("narrows to the named type", func(t *testing.T) {
		t.Setenv("NANITE_VISIBLE_PROVIDERS", "openai")
		got := visibleProviderRows(append([]store.ProviderConfig(nil), rows...))
		if len(got) != 1 || got[0].ProviderType != "openai" {
			t.Fatalf("got %v, want openai only", got)
		}
	})

	t.Run("case and spacing do not matter", func(t *testing.T) {
		t.Setenv("NANITE_VISIBLE_PROVIDERS", " OpenAI , ")
		if got := visibleProviderRows(append([]store.ProviderConfig(nil), rows...)); len(got) != 1 {
			t.Fatalf("got %d, want 1", len(got))
		}
	})
}

func TestVisibleModelRows_allowlist(t *testing.T) {
	rows := []store.Model{
		{ModelID: "gpt-5.6-terra", ProviderType: "openai"},
		{ModelID: "gpt-4o", ProviderType: "openai"},
		{ModelID: "gpt-5.6-luna", ProviderType: "openai"},
	}

	t.Run("unset shows everything", func(t *testing.T) {
		t.Setenv("NANITE_VISIBLE_MODELS", "")
		if got := visibleModelRows(append([]store.Model(nil), rows...)); len(got) != 3 {
			t.Fatalf("got %d, want 3", len(got))
		}
	})

	t.Run("narrows to the named model", func(t *testing.T) {
		// The case this exists for: the picker offered gpt-5.6-luna, the
		// gateway served only gpt-5.6-terra, and choosing luna failed as
		// "the provider stopped before completing this response".
		t.Setenv("NANITE_VISIBLE_MODELS", "gpt-5.6-terra")
		got := visibleModelRows(append([]store.Model(nil), rows...))
		if len(got) != 1 || got[0].ModelID != "gpt-5.6-terra" {
			t.Fatalf("got %v, want terra only", got)
		}
	})

	t.Run("a list that parses to nothing shows everything", func(t *testing.T) {
		// Safer direction: a typo must not empty the picker.
		t.Setenv("NANITE_VISIBLE_MODELS", " , , ")
		if got := visibleModelRows(append([]store.Model(nil), rows...)); len(got) != 3 {
			t.Fatalf("got %d, want 3", len(got))
		}
	})
}
