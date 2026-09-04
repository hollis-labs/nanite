package assets

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/fsutil"
)

var retiredAssetPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bvanta\b`),
	regexp.MustCompile(`(?i)\bconduit\b`),
	regexp.MustCompile(`(?i)contextd`),
	regexp.MustCompile(`(?i)mcp__vanta__`),
	regexp.MustCompile(`(?i)(?:context_head|context_history|memory_get|memory_history|knowledge_get|knowledge_history|tesseract_lookup)`),
	regexp.MustCompile(`(?i)context_(?:rag_query|search|typed_(?:write|view)|status_(?:promote|deprecate))`),
	regexp.MustCompile(`(?i)memory_(?:recall|get_revision|deprecate)`),
	regexp.MustCompile(`(?i)context_broker_(?:plan|fetch)`),
	regexp.MustCompile(`(?i)(?:views_evaluate|context_packet|context_(?:bulk|chunked)_ingest|context_(?:types|views|namespaces)_list|context_namespace_show|context_promote_(?:request|approve|apply|list)|context_audit|context_session_snapshot)`),
}

func staleAssetViolations(value string) []string {
	var matches []string
	for _, pattern := range retiredAssetPatterns {
		if pattern.MatchString(value) {
			matches = append(matches, pattern.String())
		}
	}
	return matches
}

func TestVersion(t *testing.T) {
	v := Version()
	if v == "" {
		t.Fatal("Version() returned empty string")
	}
	if !strings.HasPrefix(v, "2.") {
		t.Errorf("Version() = %q, want prefix 2.", v)
	}
}

func TestFile(t *testing.T) {
	data, err := File("VERSION")
	if err != nil {
		t.Fatalf("File(VERSION): %v", err)
	}
	if len(data) == 0 {
		t.Fatal("File(VERSION) returned empty bytes")
	}
	if _, err := File("does/not/exist.txt"); err == nil {
		t.Error("File(nonexistent) should have returned error")
	}
}

func TestFrameworkAssetsUseCurrentTesseractContract(t *testing.T) {
	set, err := loadManifestSet()
	if err != nil {
		t.Fatalf("load manifests: %v", err)
	}
	allowedHistory := make(map[string]bool, len(set.current.LegacyAllowPaths))
	for _, path := range set.current.LegacyAllowPaths {
		allowedHistory["framework/"+path] = true
	}
	var violations []string
	err = fs.WalkDir(frameworkAssets, "framework", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || allowedHistory[path] {
			return nil
		}
		data, readErr := frameworkAssets.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if patterns := staleAssetViolations(string(data)); len(patterns) > 0 {
			violations = append(violations, strings.TrimPrefix(path, "framework/")+": "+strings.Join(patterns, ", "))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan framework assets: %v", err)
	}
	sort.Strings(violations)
	if len(violations) > 0 {
		t.Fatalf("executable framework assets contain retired identity or tool contracts:\n%s", strings.Join(violations, "\n"))
	}
}

func TestCurrentManifestMatchesEmbeddedFramework(t *testing.T) {
	set, err := loadManifestSet()
	if err != nil {
		t.Fatalf("load manifests: %v", err)
	}
	if set.current.Source != "github.com/hollis-labs/tesseract@v0.9.0" {
		t.Fatalf("manifest source = %q, want released Tesseract v0.9.0", set.current.Source)
	}
	seen := make(map[string]bool)
	err = fs.WalkDir(frameworkAssets, "framework", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(path, "framework/")
		data, readErr := frameworkAssets.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if got, want := contentDigest(data), set.current.Files[rel]; got != want {
			t.Errorf("manifest digest for %s = %q, want %q", rel, want, got)
		}
		seen[rel] = true
		return nil
	})
	if err != nil {
		t.Fatalf("walk embedded framework: %v", err)
	}
	for rel := range set.current.Files {
		if !seen[rel] {
			t.Errorf("manifest contains missing embedded asset %s", rel)
		}
	}
}

func TestFrameworkAssetsPinTesseractV09Semantics(t *testing.T) {
	required := map[string][]string{
		"agent-boot.md": {
			"Tesseract v0.9",
			"mcp__tesseract__tesseract_recall",
			"{results, facets, manifest}",
			"withheld, not empty",
		},
		"commands/doc-search.md": {
			"mcp__tesseract__tesseract_recall",
			"payload_mode",
			"manifest.next_cursor",
			"mcp__tesseract__tesseract_get_revision",
		},
		"skills/doc-note.md": {
			"mcp__tesseract__knowledge_write",
			`"pointer_scheme": "nil"`,
			`"author_agent_id"`,
			`"session_id"`,
		},
		"skills/doc-search.md": {
			`"namespaces": "[\"user/{USER}/knowledge/{PROJECT}\"]"`,
			`"domains": "[\"knowledge\"]"`,
			"Summary/keys pages cap at 500; full pages cap at 100",
			"opaque and query-bound",
		},
		"docs/nanite-setup-guide.md": {
			"nanite-agent init --refresh",
			"tesseract mcp",
			"~/.local/share/tesseract",
			"~/.local/state/tesseract",
			"~/.cache/tesseract",
			"~/.config/tesseract",
		},
		"docs/tesseract-v0.9-contract.md": {
			"github.com/hollis-labs/tesseract@v0.9.0",
			"There are no compatibility aliases",
		},
	}
	for path, fragments := range required {
		data, err := File(path)
		if err != nil {
			t.Errorf("read %s: %v", path, err)
			continue
		}
		for _, fragment := range fragments {
			if !bytes.Contains(data, []byte(fragment)) {
				t.Errorf("%s is missing required v0.9 contract fragment %q", path, fragment)
			}
		}
	}
	if _, err := File("docs/ref-conduit-plugin.md"); err == nil {
		t.Error("retired plugin reference is still embedded")
	}
	if _, err := File("docs/ref-nanite-plugin.md"); err != nil {
		t.Errorf("current Nanite plugin reference missing: %v", err)
	}
}

func TestStaleAssetViolations_PositiveControl(t *testing.T) {
	if got := staleAssetViolations("call mcp__vanta__memory_recall through Conduit"); len(got) < 3 {
		t.Fatalf("positive control found %d patterns, want at least 3: %v", len(got), got)
	}
	if got := staleAssetViolations("call mcp__tesseract__tesseract_recall with payload_mode summary"); len(got) != 0 {
		t.Fatalf("current contract rejected: %v", got)
	}
}

func TestExtractTo_EmptyTarget(t *testing.T) {
	dir := t.TempDir()
	report, err := ExtractTo(dir, ExtractOptions{})
	if err != nil {
		t.Fatalf("ExtractTo: %v", err)
	}
	if report.Created == 0 {
		t.Error("expected Created > 0")
	}
	if report.Skipped != 0 {
		t.Errorf("Skipped = %d, want 0 (empty target)", report.Skipped)
	}

	// Spot-check: VERSION file should exist and match embedded.
	extracted, err := os.ReadFile(filepath.Join(dir, "VERSION"))
	if err != nil {
		t.Fatalf("read extracted VERSION: %v", err)
	}
	embedded, _ := File("VERSION")
	if string(extracted) != string(embedded) {
		t.Errorf("VERSION mismatch: extracted=%q embedded=%q", extracted, embedded)
	}
}

func TestExtractTo_SkipsModified(t *testing.T) {
	dir := t.TempDir()
	// First extract populates.
	if _, err := ExtractTo(dir, ExtractOptions{}); err != nil {
		t.Fatalf("first ExtractTo: %v", err)
	}
	// User modifies one file.
	modPath := filepath.Join(dir, "VERSION")
	if err := os.WriteFile(modPath, []byte("99.0.0-custom\n"), 0o644); err != nil {
		t.Fatalf("write mod: %v", err)
	}
	// Second extract should skip VERSION.
	report, err := ExtractTo(dir, ExtractOptions{})
	if err != nil {
		t.Fatalf("second ExtractTo: %v", err)
	}
	if report.Skipped == 0 {
		t.Error("expected Skipped > 0 after user modification")
	}
	// Verify the modified file is unchanged.
	after, _ := os.ReadFile(modPath)
	if string(after) != "99.0.0-custom\n" {
		t.Errorf("modified VERSION was overwritten: %q", after)
	}
}

func TestExtractTo_UpgradesKnownStockAndPreservesCustomization(t *testing.T) {
	dir := t.TempDir()
	if _, err := ExtractTo(dir, ExtractOptions{}); err != nil {
		t.Fatalf("fresh ExtractTo: %v", err)
	}

	legacy, err := os.ReadFile("testdata/legacy-2.3.0/commands/doc-note.md") // #nosec G304 -- fixed repository test fixture.
	if err != nil {
		t.Fatalf("read legacy fixture: %v", err)
	}
	// The released asset had no final newline; keep the fixture readable while
	// exercising its exact historical digest.
	legacy = bytes.TrimSuffix(legacy, []byte("\n"))
	stockPath := filepath.Join(dir, "commands", "doc-note.md")
	if writeErr := fsutil.AtomicWriteFile(stockPath, legacy, 0o644); writeErr != nil {
		t.Fatalf("write legacy stock file: %v", writeErr)
	}

	customPath := filepath.Join(dir, "commands", "doc-search.md")
	custom := []byte("user-owned search instructions\n")
	if writeErr := fsutil.AtomicWriteFile(customPath, custom, 0o644); writeErr != nil {
		t.Fatalf("write custom file: %v", writeErr)
	}

	report, err := ExtractTo(dir, ExtractOptions{})
	if err != nil {
		t.Fatalf("upgrade ExtractTo: %v", err)
	}
	if report.Updated != 1 {
		t.Fatalf("Updated = %d, want 1", report.Updated)
	}
	if report.Skipped != 1 {
		t.Fatalf("Skipped = %d, want 1", report.Skipped)
	}
	currentStock, _ := File("commands/doc-note.md")
	gotStock, _ := os.ReadFile(stockPath) // #nosec G304 -- exact path beneath t.TempDir.
	if !bytes.Equal(gotStock, currentStock) {
		t.Fatal("historical stock asset was not upgraded")
	}
	gotCustom, _ := os.ReadFile(customPath) // #nosec G304 -- exact path beneath t.TempDir.
	if !bytes.Equal(gotCustom, custom) {
		t.Fatal("user customization was overwritten")
	}
	if len(report.ConflictFiles) != 2 {
		t.Fatalf("ConflictFiles = %v, want existing and new snapshots", report.ConflictFiles)
	}
	for _, rel := range report.ConflictFiles {
		_, statErr := os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))
		if statErr != nil {
			t.Errorf("conflict file %s missing: %v", rel, statErr)
		}
	}

	second, err := ExtractTo(dir, ExtractOptions{})
	if err != nil {
		t.Fatalf("idempotent ExtractTo: %v", err)
	}
	if second.Updated != 0 || second.Skipped != 1 || len(second.ConflictFiles) != 2 {
		t.Fatalf("unexpected second report: %+v", second)
	}
	gotCustom, _ = os.ReadFile(customPath) // #nosec G304 -- exact path beneath t.TempDir.
	if !bytes.Equal(gotCustom, custom) {
		t.Fatal("idempotent refresh changed customization")
	}
}

func TestExtractTo_ForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	if _, err := ExtractTo(dir, ExtractOptions{}); err != nil {
		t.Fatal(err)
	}
	modPath := filepath.Join(dir, "VERSION")
	if err := os.WriteFile(modPath, []byte("99.0.0-custom\n"), 0o644); err != nil {
		t.Fatalf("write mod: %v", err)
	}

	if _, err := ExtractTo(dir, ExtractOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(modPath)
	embedded, _ := File("VERSION")
	if string(after) != string(embedded) {
		t.Errorf("Force=true did not overwrite: got %q want %q", after, embedded)
	}
}

func TestRetirePath_RemovesStockAndPreservesCustomized(t *testing.T) {
	dir := t.TempDir()
	stock := []byte("released legacy asset")
	set := manifestSet{historical: []contentManifest{{Files: map[string]string{"retired.md": contentDigest(stock)}}}}
	target := filepath.Join(dir, "retired.md")
	if writeErr := fsutil.AtomicWriteFile(target, stock, 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}
	report := &ExtractReport{}
	if err := retirePath(dir, "retired.md", set, ExtractOptions{}, report); err != nil {
		t.Fatalf("retire stock path: %v", err)
	}
	if report.Removed != 1 || len(report.RemovedFiles) != 1 {
		t.Fatalf("unexpected stock retirement report: %+v", report)
	}
	if _, err := os.Stat(target); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("stock retired path still exists: %v", err)
	}

	custom := []byte("user customization")
	if writeErr := fsutil.AtomicWriteFile(target, custom, 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}
	report = &ExtractReport{}
	if err := retirePath(dir, "retired.md", set, ExtractOptions{}, report); err != nil {
		t.Fatalf("retire customized path: %v", err)
	}
	if report.Skipped != 1 || len(report.ConflictFiles) != 2 {
		t.Fatalf("unexpected customized retirement report: %+v", report)
	}
	got, err := os.ReadFile(target) // #nosec G304 -- exact path beneath t.TempDir.
	if err != nil || !bytes.Equal(got, custom) {
		t.Fatalf("custom retired path was not preserved: got %q, err %v", got, err)
	}
}
