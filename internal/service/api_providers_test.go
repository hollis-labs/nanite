package service

import (
	"testing"
)

func TestAPIProviderSpecByID(t *testing.T) {
	for _, spec := range APIProviderSpecs() {
		got, ok := APIProviderSpecByID(spec.ProviderID)
		if !ok || got.Name != spec.Name {
			t.Fatalf("APIProviderSpecByID(%q) = %q, %v; want %q", spec.ProviderID, got.Name, ok, spec.Name)
		}
		if spec.NewProvider("k") == nil {
			t.Fatalf("%s: NewProvider returned nil", spec.Name)
		}
	}
	if _, ok := APIProviderSpecByID("gemini-api-001"); ok {
		t.Fatal("an adapter-less row resolved to a spec")
	}
}
