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
var retiredCoreTypeDeclaration = regexp.MustCompile("(?m)# `([a-z][a-z0-9-]+)` was retired as a standalone type")

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
			if len(parsed) != 1 || parsed[0].Kind != "envelope" || parsed[0].Version != envelopes.ProtocolVersion {
				t.Errorf("%s example %d parsed as %+v, want one canonical kind=envelope versioned envelope", path, i+1, parsed)
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

func TestEmbeddedAndExtractedFrameworkDoNotInvokeRetiredCoreTypes(t *testing.T) {
	retiredTypes := releasedRetiredCoreTypes(t)
	set, err := loadManifestSet()
	if err != nil {
		t.Fatalf("load embedded framework manifests: %v", err)
	}
	allowedHistory := make(map[string]bool, len(set.current.LegacyAllowPaths))
	for _, path := range set.current.LegacyAllowPaths {
		allowedHistory[filepath.ToSlash(path)] = true
	}

	var embeddedViolations []string
	err = fs.WalkDir(frameworkAssets, "framework", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel := strings.TrimPrefix(path, "framework/")
		if entry.IsDir() || allowedHistory[rel] {
			return nil
		}
		data, readErr := frameworkAssets.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, retiredType := range retiredTypes {
			if regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(retiredType) + `\b`).Match(data) {
				embeddedViolations = append(embeddedViolations, rel+": "+retiredType)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan embedded framework: %v", err)
	}
	if len(embeddedViolations) > 0 {
		t.Fatalf("executable embedded framework prose invokes retired core envelope types:\n%s", strings.Join(embeddedViolations, "\n"))
	}

	extractedDir := t.TempDir()
	if _, extractErr := ExtractTo(extractedDir, ExtractOptions{}); extractErr != nil {
		t.Fatalf("extract framework for drift scan: %v", extractErr)
	}
	extractedRoot, err := os.OpenRoot(extractedDir)
	if err != nil {
		t.Fatalf("open extracted framework root: %v", err)
	}
	t.Cleanup(func() { _ = extractedRoot.Close() })
	var extractedViolations []string
	err = filepath.WalkDir(extractedDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(extractedDir, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if allowedHistory[rel] || strings.HasPrefix(rel, ".framework-conflicts/") {
			return nil
		}
		data, readErr := extractedRoot.ReadFile(filepath.FromSlash(rel))
		if readErr != nil {
			return readErr
		}
		for _, retiredType := range retiredTypes {
			if regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(retiredType) + `\b`).Match(data) {
				extractedViolations = append(extractedViolations, rel+": "+retiredType)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan extracted framework: %v", err)
	}
	if len(extractedViolations) > 0 {
		t.Fatalf("freshly extracted framework prose invokes retired core envelope types:\n%s", strings.Join(extractedViolations, "\n"))
	}
}

func TestRepositorySourcesClassifyRetiredTypesAndNeverInvokeThem(t *testing.T) {
	retiredTypes := releasedRetiredCoreTypes(t)
	repoRoot := filepath.Join("..", "..")
	repository, err := os.OpenRoot(repoRoot)
	if err != nil {
		t.Fatalf("open repository root: %v", err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	var violations []string
	err = filepath.WalkDir(repoRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(repoRoot, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			if rel == ".git" || rel == "node_modules" || strings.Contains(rel, "/node_modules") ||
				strings.HasPrefix(rel, "internal/assets/framework") {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(rel))
		if ext != ".go" && ext != ".ts" && ext != ".tsx" && ext != ".js" && ext != ".mjs" &&
			ext != ".json" && ext != ".yaml" && ext != ".yml" && ext != ".md" {
			return nil
		}
		data, readErr := repository.ReadFile(filepath.FromSlash(rel))
		if readErr != nil {
			return readErr
		}
		generated := strings.HasPrefix(rel, "ui/src/generated/")
		for _, retiredType := range retiredTypes {
			plain := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(retiredType) + `\b`)
			if generated && plain.Match(data) {
				violations = append(violations, rel+": generated output names "+retiredType)
				continue
			}
			invocations := []*regexp.Regexp{
				regexp.MustCompile(`(?i)["']type["']\s*:\s*["']` + regexp.QuoteMeta(retiredType) + `["']`),
				regexp.MustCompile(`(?i)\btype\s*(?:==|!=|=|:)\s*["']` + regexp.QuoteMeta(retiredType) + `["']`),
			}
			for _, invocation := range invocations {
				if invocation.Match(data) {
					violations = append(violations, rel+": executable type invocation "+retiredType)
					break
				}
			}
			for _, match := range plain.FindAllIndex(data, -1) {
				start := match[0] - 240
				if start < 0 {
					start = 0
				}
				end := match[1] + 240
				if end > len(data) {
					end = len(data)
				}
				context := strings.ToLower(string(data[start:end]))
				normalizedContext := regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(context, " ")
				if !containsAny(context, "retir", "replac", "legacy", "migration", "histor", "composition", "not a separate") &&
					!strings.Contains(normalizedContext, "old standalone") {
					violations = append(violations, rel+": unclassified reference to "+retiredType)
					break
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan repository prose and source: %v", err)
	}
	if len(violations) > 0 {
		t.Fatalf("retired core envelope drift found outside explicit migration/history context:\n%s", strings.Join(violations, "\n"))
	}
}

func TestPlannerEnvelopeCompositionMatchesLiveContract(t *testing.T) {
	data, err := File("docs/nanite-planner.md")
	if err != nil {
		t.Fatalf("read embedded planner guide: %v", err)
	}
	text := string(data)
	for _, required := range []string{
		"automatically emits a `list-card`",
		"Do not emit another envelope manually",
		"`list-card` + `confirmation-card`",
		"share the same `plan_id`",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("planner guide does not state live work-envelope contract %q", required)
		}
	}

	registry, err := envelopes.LoadCore(context.Background())
	if err != nil {
		t.Fatalf("load production core envelope registry: %v", err)
	}
	chat.InitCoreTypes(registry.Names())
	matches := frameworkEnvelopeFence.FindAllString(text, -1)
	if len(matches) != 2 {
		t.Fatalf("planner guide contains %d envelope examples, want list-card + confirmation-card", len(matches))
	}
	parsed, _, validationErrors := chat.ParseEnvelopes(strings.Join(matches, "\n"))
	if len(validationErrors) != 0 || len(parsed) != 2 {
		t.Fatalf("parse planner envelope composition: parsed=%+v errors=%+v", parsed, validationErrors)
	}
	byType := make(map[string]chat.Envelope, len(parsed))
	for _, envelope := range parsed {
		if envelope.Kind != "envelope" || envelope.Version != envelopes.ProtocolVersion {
			t.Fatalf("planner example uses non-canonical wire header: %+v", envelope)
		}
		if err := registry.ValidateEnvelope(&envelopes.Envelope{
			V: envelope.Version, ID: "planner-contract", Type: envelope.Type, Data: envelope.Data,
		}); err != nil {
			t.Fatalf("planner %s example fails released schema: %v", envelope.Type, err)
		}
		byType[envelope.Type] = envelope
	}
	listSource := envelopeDataSource(t, byType["list-card"])
	confirmationSource := envelopeDataSource(t, byType["confirmation-card"])
	if listSource["kind"] != "plans" || confirmationSource["kind"] != "plan_approval" {
		t.Fatalf("planner composition data sources = list:%+v confirmation:%+v", listSource, confirmationSource)
	}
	if listSource["plan_id"] == "" || listSource["plan_id"] != confirmationSource["plan_id"] {
		t.Fatalf("planner composition must share a non-empty plan_id: list:%+v confirmation:%+v", listSource, confirmationSource)
	}
}

func releasedRetiredCoreTypes(t *testing.T) []string {
	t.Helper()
	manifest, err := fs.ReadFile(envelopes.EmbeddedFS(), "manifest/envelopes.yaml")
	if err != nil {
		t.Fatalf("read released core manifest: %v", err)
	}
	matches := retiredCoreTypeDeclaration.FindAllSubmatch(manifest, -1)
	if len(matches) == 0 {
		t.Fatal("released core manifest does not classify any retired standalone work-envelope types")
	}
	types := make([]string, 0, len(matches))
	for _, match := range matches {
		types = append(types, string(match[1]))
	}
	return types
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

func envelopeDataSource(t *testing.T, envelope chat.Envelope) map[string]any {
	t.Helper()
	if envelope.Type == "" {
		t.Fatal("required planner envelope type is missing")
	}
	source, ok := envelope.Data["data_source"].(map[string]any)
	if !ok {
		t.Fatalf("%s data_source = %#v, want object", envelope.Type, envelope.Data["data_source"])
	}
	return source
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
