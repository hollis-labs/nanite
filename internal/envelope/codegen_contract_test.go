package envelope

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hollis-labs/go-envelopes"
	"github.com/hollis-labs/go-envelopes/codegen"
)

func TestReleasedEnvelopeCatalogAndTypeScriptAreDeterministic(t *testing.T) {
	registry, err := envelopes.LoadCore(context.Background())
	if err != nil {
		t.Fatalf("LoadCore: %v", err)
	}
	first, err := registry.ExportCatalog()
	if err != nil {
		t.Fatalf("first ExportCatalog: %v", err)
	}
	second, err := registry.ExportCatalog()
	if err != nil {
		t.Fatalf("second ExportCatalog: %v", err)
	}
	if len(first.Types) == 0 || len(first.Schemas) == 0 {
		t.Fatalf("catalog is empty: %d types, %d schemas", len(first.Types), len(first.Schemas))
	}
	if first.Source.Module != envelopes.ModulePath || first.Source.ModuleVersion == "" {
		t.Fatalf("catalog source = %s@%s, want identified %s module", first.Source.Module, first.Source.ModuleVersion, envelopes.ModulePath)
	}
	if !strings.HasPrefix(first.Source.ManifestDigest, "sha256:") {
		t.Fatalf("catalog manifest digest = %q, want sha256 identity", first.Source.ManifestDigest)
	}

	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("marshal first catalog: %v", err)
	}
	secondJSON, err := json.Marshal(second)
	if err != nil {
		t.Fatalf("marshal second catalog: %v", err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatal("unchanged registry produced non-deterministic catalogs")
	}

	firstTypeScript, err := codegen.TypeScript(first, codegen.TypeScriptOptions{})
	if err != nil {
		t.Fatalf("first TypeScript generation: %v", err)
	}
	secondTypeScript, err := codegen.TypeScript(second, codegen.TypeScriptOptions{})
	if err != nil {
		t.Fatalf("second TypeScript generation: %v", err)
	}
	if len(firstTypeScript) == 0 || !bytes.Equal(firstTypeScript, secondTypeScript) {
		t.Fatal("unchanged catalog produced empty or non-deterministic TypeScript")
	}
}

// TestEnvelopeConsumptionHasNoEmbeddedSchemaCoupling guards Nanite's own
// side of the split: the GUI, its generators, and their generated output now
// live in the separate `flux` repo (Nanite is headless), so this only checks
// what remains here — the go.mod pin and internal/envelope's use of the
// public go-envelopes API.
func TestEnvelopeConsumptionHasNoEmbeddedSchemaCoupling(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test path")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	goMod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	if !strings.Contains(string(goMod), "\tgithub.com/hollis-labs/go-envelopes v0.4.0\n") {
		t.Fatal("go.mod does not pin the reviewed go-envelopes v0.4.0 release")
	}
	if strings.Contains(string(goMod), "replace github.com/hollis-labs/go-envelopes") {
		t.Fatal("go.mod replaces go-envelopes instead of consuming the released module")
	}

	productionFiles, err := filepath.Glob(filepath.Join(root, "internal", "envelope", "*.go"))
	if err != nil {
		t.Fatalf("glob envelope package: %v", err)
	}
	checked := 0
	for _, path := range productionFiles {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		checked++
		// #nosec G304 -- path is constrained by the internal/envelope/*.go glob above.
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if strings.Contains(string(data), "EmbeddedFS(") {
			t.Errorf("%s reopens go-envelopes embedded files instead of using its public catalog/schema APIs", path)
		}
	}
	if checked == 0 {
		t.Fatal("drift guard examined no production envelope files")
	}
}
