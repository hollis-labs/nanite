package mcp

import (
	"context"
	"testing"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/manifest"
	"github.com/hollis-labs/nanite/internal/plugin/subprocess"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
)

func TestManifestAnnotationsKeepOmissionsAndExplicitFalse(t *testing.T) {
	f := false
	declaration := declaredFixtureTool("reviewed_write", pluginapi.ToolEffectWrite)
	declaration.Annotations = &manifest.ToolAnnotations{Title: "Reviewed title", IdempotentHint: &f, OpenWorldHint: &f}
	transport, err := newManifestPluginTransport(&manifestToolFixture{}, []manifest.Tool{declaration}, nil)
	if err != nil {
		t.Fatal(err)
	}
	f = true
	tools, err := transport.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	hints := tools[0].Annotations
	if hints["idempotentHint"] != false || hints["openWorldHint"] != false || hints["title"] != "Reviewed title" {
		t.Fatalf("reviewed annotation values changed: %#v", hints)
	}
	if _, inferred := hints["readOnlyHint"]; inferred {
		t.Fatal("inferred an omitted read-only hint from the effect")
	}
	if _, inferred := hints["destructiveHint"]; inferred {
		t.Fatal("inferred an omitted destructive hint from the effect")
	}
}

func TestManifestEffectControlsPolicyIndependentlyOfPublicHints(t *testing.T) {
	manager := NewManager()
	t.Cleanup(func() { manager.RemoveServersByPlugin("writer") })
	declaration := declaredFixtureTool("reviewed_write", pluginapi.ToolEffectWrite)
	readOnly := true
	declaration.Annotations = &manifest.ToolAnnotations{ReadOnlyHint: &readOnly}
	if err := manager.AddPluginTools("writer", []manifest.Tool{declaration}, "auto", subprocess.NewSubprocessPluginForTest("writer", nil), nil); err != nil {
		t.Fatal(err)
	}
	read, destructive, ok := manager.ToolBehavior("reviewed_write")
	if !ok || read || destructive {
		t.Fatal("public hints overrode the reviewed write effect")
	}
	ro, d := manager.ToolDeclaredHints("reviewed_write")
	if ro == nil || *ro || d == nil || *d {
		t.Fatal("approval classification lost the reviewed write declaration")
	}
	tools, err := manager.servers["plugin_writer"].ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := tools[0].Annotations["readOnlyHint"]; got != true {
		t.Fatal("policy classification rewrote the public MCP hint")
	}
}

func TestManifestAbsentAnnotationsStayAbsent(t *testing.T) {
	declaration := declaredFixtureTool("reviewed_read", pluginapi.ToolEffectRead)
	declaration.Annotations = nil
	transport, err := newManifestPluginTransport(&manifestToolFixture{}, []manifest.Tool{declaration}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tools, err := transport.ListTools(context.Background())
	if err != nil || tools[0].Annotations != nil {
		t.Fatalf("absent annotations changed: %#v, %v", tools, err)
	}
}
