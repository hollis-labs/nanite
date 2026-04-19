package truncate

import (
	"os"
	"strings"
	"testing"
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
