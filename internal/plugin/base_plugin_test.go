package plugin

import (
	"fmt"
	"strings"
	"testing"
)

func TestLoadEmbeddedManifestCachesParsedManifest(t *testing.T) {
	loader := LoadEmbeddedManifest("adapter-test", []byte(`
schema_version: 1
id: adapter-test
name: Adapter Test
version: 0.1.0
runtime: builtin
`))

	first := loader()
	second := loader()

	if first == nil {
		t.Fatal("LoadEmbeddedManifest returned nil")
	}
	if first != second {
		t.Fatal("LoadEmbeddedManifest should cache the parsed manifest")
	}
	if first.ID != "adapter-test" || first.Name != "Adapter Test" || first.Version != "0.1.0" {
		t.Fatalf("unexpected manifest identity: %+v", first)
	}
}

func TestLoadEmbeddedManifestPanicsOnInvalidYAML(t *testing.T) {
	loader := LoadEmbeddedManifest("adapter-bad", []byte("id: ["))

	defer func() {
		got := recover()
		if got == nil {
			t.Fatal("expected invalid embedded manifest to panic")
		}
		if !strings.Contains(fmt.Sprint(got), "adapter-bad: invalid embedded plugin.yaml") {
			t.Fatalf("panic did not include plugin identity: %v", got)
		}
	}()

	_ = loader()
}

func TestBasePluginLifecycleAndMetadata(t *testing.T) {
	loader := LoadEmbeddedManifest("adapter-test", []byte(`
schema_version: 1
id: adapter-test
name: Adapter Test
version: 0.1.0
runtime: builtin
`))

	p := NewBasePlugin(BasePluginConfig{
		ID:           "adapter-test",
		Name:         "Adapter Test",
		Version:      "0.1.0",
		Description:  "test adapter",
		Dependencies: []string{"dep-a"},
		Manifest:     loader,
	})

	if p.ID() != "adapter-test" || p.Name() != "Adapter Test" || p.Version() != "0.1.0" {
		t.Fatalf("unexpected base metadata: id=%q name=%q version=%q", p.ID(), p.Name(), p.Version())
	}
	if p.Description() != "test adapter" {
		t.Fatalf("unexpected description: %q", p.Description())
	}
	deps := p.Dependencies()
	if len(deps) != 1 || deps[0] != "dep-a" {
		t.Fatalf("unexpected dependencies: %v", deps)
	}
	deps[0] = "mutated"
	if got := p.Dependencies()[0]; got != "dep-a" {
		t.Fatalf("Dependencies should return a copy, got %q", got)
	}

	if m := p.Manifest(); m == nil || m.Identifier() != "adapter-test" {
		t.Fatalf("unexpected manifest: %+v", m)
	}

	host := NewHost(nil, NewLogger("base-plugin-test"))
	if err := p.Load(host); err != nil {
		t.Fatalf("Load: %v", err)
	}
	status := p.Status()
	if !status.Loaded || !status.Enabled || status.LoadedAt.IsZero() {
		t.Fatalf("unexpected loaded status: %+v", status)
	}

	if err := p.Unload(); err != nil {
		t.Fatalf("Unload: %v", err)
	}
	status = p.Status()
	if status.Loaded || status.Enabled {
		t.Fatalf("unexpected unloaded status: %+v", status)
	}
}
