package assets

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/hollis-labs/go-envelopes"
	"github.com/hollis-labs/nanite/internal/chat"
)

var frameworkEnvelopeFence = regexp.MustCompile("(?s)```nanite-envelope\\s*\\n(.*?)```")

func TestEmbeddedFrameworkEnvelopeExamplesUseProductionWireContract(t *testing.T) {
	registry, err := envelopes.LoadCore(context.Background())
	if err != nil {
		t.Fatalf("load production core envelope registry: %v", err)
	}
	chat.InitCoreTypes(registry.Names())
	found := 0
	err = fs.WalkDir(frameworkAssets, "framework", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		data, readErr := frameworkAssets.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for i, match := range frameworkEnvelopeFence.FindAllSubmatch(data, -1) {
			found++
			var raw struct {
				Type string `json:"type"`
			}
			if decodeErr := json.Unmarshal(match[1], &raw); decodeErr != nil {
				t.Errorf("%s example %d is not JSON: %v", path, i+1, decodeErr)
				continue
			}
			parsed, _, validationErrors := chat.ParseEnvelopes(string(match[0]))
			if len(validationErrors) != 0 {
				t.Errorf("%s example %d fails production validation: %+v", path, i+1, validationErrors)
			}
			if len(parsed) != 1 || parsed[0].Kind == "" || parsed[0].Version != envelopes.ProtocolVersion {
				t.Errorf("%s example %d parsed as %+v, want one versioned envelope", path, i+1, parsed)
				continue
			}
			if validationErr := registry.ValidateEnvelope(&envelopes.Envelope{
				V:    parsed[0].Version,
				ID:   "embedded-doc-example",
				Type: parsed[0].Type,
				Data: parsed[0].Data,
			}); validationErr != nil {
				t.Errorf("%s example %d fails released core type/schema validation: %v", path, i+1, validationErr)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan embedded framework examples: %v", err)
	}
	if found == 0 {
		t.Fatal("no shipped nanite-envelope examples were validated")
	}
}

func TestEmbeddedEnvelopeReferencesDescribeCurrentWorkflow(t *testing.T) {
	for _, modulePath := range []string{
		"manifest/envelopes.yaml",
		"manifest/envelopes.schema.json",
	} {
		if _, err := fs.ReadFile(envelopes.EmbeddedFS(), modulePath); err != nil {
			t.Fatalf("documented go-envelopes path %s is not shipped by the pinned module: %v", modulePath, err)
		}
	}
	if _, err := envelopes.LoadCore(context.Background()); err != nil {
		t.Fatalf("load documented module-owned core envelope manifest: %v", err)
	}
	localPaths := []string{
		"scripts/generate-plugin-imports.mjs",
		"scripts/generate-envelope-types.mjs",
		"ui/src/generated/plugin-envelopes.ts",
	}
	for _, localPath := range localPaths {
		if _, err := os.Stat(filepath.Join("..", "..", filepath.FromSlash(localPath))); err != nil {
			t.Fatalf("documented local envelope path %s does not exist: %v", localPath, err)
		}
	}
	for _, docPath := range []string{
		"docs/ref-envelope-component.md",
		"docs/ref-nanite-plugin.md",
	} {
		data, err := File(docPath)
		if err != nil {
			t.Fatalf("read embedded framework doc %s: %v", docPath, err)
		}
		text := string(data)
		if strings.Contains(text, "config/envelopes.yaml") {
			t.Errorf("%s points to nonexistent config/envelopes.yaml", docPath)
		}
		for _, required := range append([]string{
			"github.com/hollis-labs/go-envelopes",
			"manifest/envelopes.yaml",
		}, localPaths...) {
			if !strings.Contains(text, required) {
				t.Errorf("%s does not document current envelope workflow path %q", docPath, required)
			}
		}
	}
}
