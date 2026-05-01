package truncate

import (
	"os"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/pkg/models"
)

func TestOutput_SmallResult(t *testing.T) {
	r := Output("hello world", "test_tool")
	if r.Truncated {
		t.Error("expected not truncated")
	}
	if r.Content != "hello world" {
		t.Errorf("expected original content, got %q", r.Content)
	}
	if r.OutputPath != "" {
		t.Errorf("expected no output path, got %q", r.OutputPath)
	}
}

func TestOutput_LargeResult_Truncates(t *testing.T) {
	// Generate content larger than MaxChars.
	big := strings.Repeat("line of text here\n", 500) // ~9000 chars, 500 lines
	r := Output(big, "test_tool")

	if !r.Truncated {
		t.Error("expected truncated")
	}
	if r.OutputPath == "" {
		t.Error("expected output path to be set")
	}
	if r.OriginalLen != len(big) {
		t.Errorf("expected original len %d, got %d", len(big), r.OriginalLen)
	}
	if len(r.Content) >= len(big) {
		t.Error("expected content to be shorter than original")
	}
	// CW-20260419-0014: the LLM-visible content must NOT include the
	// on-disk file path — the LLM was mis-using it as a fetch_tool_result
	// id. The path is still on r.OutputPath for operator debugging.
	if strings.Contains(r.Content, r.OutputPath) {
		t.Errorf("LLM-visible content should not include the on-disk path %q", r.OutputPath)
	}
	if strings.Contains(r.Content, "Full output saved to:") {
		t.Error("stale file-pointer hint leaked into LLM-visible content")
	}
	if !strings.Contains(r.Content, "more lines") {
		t.Error("expected truncation info in content")
	}

	// Verify file exists and has full content.
	data, err := os.ReadFile(r.OutputPath)
	if err != nil {
		t.Fatalf("failed to read saved output: %v", err)
	}
	if string(data) != big {
		t.Error("saved file content doesn't match original")
	}

	// Cleanup.
	os.Remove(r.OutputPath)
}

func TestOutput_ManyLines_Truncates(t *testing.T) {
	// 300 short lines — exceeds MaxLines but not MaxChars.
	var sb strings.Builder
	for i := 0; i < 300; i++ {
		sb.WriteString("x\n")
	}
	text := sb.String()
	r := Output(text, "test_tool")

	if !r.Truncated {
		t.Error("expected truncated by line count")
	}
	if r.OutputPath == "" {
		t.Error("expected output path")
	}

	// The truncation indicator should be present.
	if !strings.Contains(r.Content, "more lines") {
		t.Error("expected truncation info")
	}

	os.Remove(r.OutputPath)
}

func TestOutput_ExactlyAtLimit(t *testing.T) {
	// Exactly MaxChars, 1 line — should NOT truncate.
	text := strings.Repeat("a", MaxChars)
	r := Output(text, "test_tool")

	if r.Truncated {
		t.Error("expected not truncated at exact limit")
	}
}

// CW-20260430-0008 (P2 pilot) — OutputForModel should size MaxChars from the
// model's context window when one is configured.

// TestOutputForModel_UnknownModel_FallsBackToFloor pins the empty-modelID and
// catalog-miss path: behavior must be identical to Output (i.e. respect the
// static MaxChars floor). This is the "no model in scope, no breakage" guard
// that protects existing call sites passing modelID="" and existing tests that
// don't wire a catalog.
func TestOutputForModel_UnknownModel_FallsBackToFloor(t *testing.T) {
	// Single line of exactly MaxChars — must not truncate.
	atLimit := strings.Repeat("a", MaxChars)
	r := OutputForModel(atLimit, "test_tool", "")
	if r.Truncated {
		t.Errorf("expected no truncation at MaxChars with empty modelID, got truncated=true")
	}

	// Just over MaxChars on a single long line — must truncate.
	overLimit := strings.Repeat("a", MaxChars+1) + "\n"
	r = OutputForModel(overLimit, "test_tool", "")
	if !r.Truncated {
		t.Errorf("expected truncation just over MaxChars with empty modelID, got truncated=false")
	}
	// Cleanup any saved file.
	if r.OutputPath != "" {
		os.Remove(r.OutputPath)
	}

	// Catalog-miss path: a model ID that isn't in the registry must behave
	// identically to empty.
	r = OutputForModel(atLimit, "test_tool", "definitely-not-a-real-model-zzz")
	if r.Truncated {
		t.Errorf("expected no truncation at MaxChars with unknown modelID, got truncated=true")
	}
}

// TestOutputForModel_LargeWindow_Expands pins the dynamic-cap behavior. With a
// 1M-token window stubbed via the catalog overlay, OutputForModel should let
// content larger than the static MaxChars through (up to the computed budget).
// Verifies the pilot's whole purpose: large-window models get more headroom.
func TestOutputForModel_LargeWindow_Expands(t *testing.T) {
	const fakeModel = "test-pilot-large-window-model"

	// Stub the model in the catalog overlay with a 1M-token window.
	// computeDynamicMaxChars: 1_000_000 tokens × 4 bytes/tok × 0.004 = 16_000
	// bytes (under MaxCharsCeiling, above MaxChars).
	models.SyncFromCatalog(models.CatalogInput{
		ContextWindows: map[string]int{fakeModel: 1_000_000},
	})
	defer models.SyncFromCatalog(models.CatalogInput{}) // clear overlay

	// Build content sized to exceed the static MaxChars but fit under the
	// dynamic cap. 8 KB of single-line content does not exceed MaxLines.
	content := strings.Repeat("z", 8000)

	// Static path truncates this — sanity check.
	staticResult := Output(content, "test_tool")
	if !staticResult.Truncated {
		t.Fatalf("test setup invariant: 8000-byte content should truncate at static MaxChars=4000, got truncated=false")
	}
	if staticResult.OutputPath != "" {
		os.Remove(staticResult.OutputPath)
	}

	// Dynamic path with 1M window must NOT truncate — content (8000) fits
	// under the computed cap (16000).
	dynResult := OutputForModel(content, "test_tool", fakeModel)
	if dynResult.Truncated {
		t.Errorf("expected no truncation with 1M-window model and 8000-byte content, got truncated=true (cap was %d)", len(dynResult.Content))
	}
	if dynResult.Content != content {
		t.Errorf("expected verbatim content from dynamic path, got len(content)=%d, want %d", len(dynResult.Content), len(content))
	}
	if dynResult.OutputPath != "" {
		os.Remove(dynResult.OutputPath)
	}
}

// TestComputeDynamicMaxChars_FloorAndCeiling pins the clamp behavior.
func TestComputeDynamicMaxChars_FloorAndCeiling(t *testing.T) {
	const smallModel = "test-pilot-small-window-model"
	const hugeModel = "test-pilot-huge-window-model"

	models.SyncFromCatalog(models.CatalogInput{
		ContextWindows: map[string]int{
			smallModel: 200_000,    // 800 KB × 0.004 = 3.2 KB → floor (4000)
			hugeModel:  10_000_000, // 40 MB × 0.004 = 160 KB → ceiling (32000)
		},
	})
	defer models.SyncFromCatalog(models.CatalogInput{})

	if got, _ := computeDynamicMaxChars(""); got != MaxChars {
		t.Errorf("empty modelID: got %d, want %d", got, MaxChars)
	}
	if got, _ := computeDynamicMaxChars(smallModel); got != MaxChars {
		t.Errorf("small window: expected floor %d, got %d", MaxChars, got)
	}
	if got, _ := computeDynamicMaxChars(hugeModel); got != MaxCharsCeiling {
		t.Errorf("huge window: expected ceiling %d, got %d", MaxCharsCeiling, got)
	}
}
