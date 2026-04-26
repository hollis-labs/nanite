package service

import "testing"

func TestNewRuntimeAdapterRegistry_RegistersBuiltins(t *testing.T) {
	reg := newRuntimeAdapterRegistry()

	for _, name := range []string{"claude", "codex", "gemini", "opencode", "nanite-native"} {
		if _, ok := reg.GetAdapter(name); !ok {
			t.Fatalf("expected adapter %q to be registered", name)
		}
	}
}
