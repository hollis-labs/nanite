package mcp

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func TestCodeExecute_Shell(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	transport := NewCodeExecTransport("test-shell")
	result, err := transport.CallTool(context.Background(), "nanite_code_execute", map[string]any{
		"code":     "echo hello",
		"language": "shell",
	})
	if err != nil {
		t.Fatalf("CallTool() error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, "hello") {
		t.Errorf("stdout = %q, want it to contain 'hello'", result.Content[0].Text)
	}
}

func TestCodeExecute_ShellDefault(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	transport := NewCodeExecTransport("test-shell-default")
	result, err := transport.CallTool(context.Background(), "nanite_code_execute", map[string]any{
		"code": "echo default_language",
	})
	if err != nil {
		t.Fatalf("CallTool() error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, "default_language") {
		t.Errorf("stdout = %q, want it to contain 'default_language'", result.Content[0].Text)
	}
}

func TestCodeExecute_Timeout(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	transport := NewCodeExecTransport("test-timeout")
	result, err := transport.CallTool(context.Background(), "nanite_code_execute", map[string]any{
		"code":     "sleep 60",
		"language": "shell",
		"timeout":  float64(2),
	})
	if err != nil {
		t.Fatalf("CallTool() error: %v", err)
	}
	if !result.IsError {
		t.Log("result text:", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, "timed out") {
		t.Errorf("result = %q, want it to contain 'timed out'", result.Content[0].Text)
	}
}

func TestCodeExecute_Python(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	transport := NewCodeExecTransport("test-python")
	result, err := transport.CallTool(context.Background(), "nanite_code_execute", map[string]any{
		"code":     "print('hello from python')",
		"language": "python",
	})
	if err != nil {
		t.Fatalf("CallTool() error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, "hello from python") {
		t.Errorf("stdout = %q, want it to contain 'hello from python'", result.Content[0].Text)
	}
}

func TestCodeExecute_ExitCode(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	transport := NewCodeExecTransport("test-exitcode")
	result, err := transport.CallTool(context.Background(), "nanite_code_execute", map[string]any{
		"code":     "exit 1",
		"language": "shell",
	})
	if err != nil {
		t.Fatalf("CallTool() error: %v", err)
	}
	if !strings.Contains(result.Content[0].Text, "Exit code: 1") {
		t.Errorf("result = %q, want it to contain 'Exit code: 1'", result.Content[0].Text)
	}
}

func TestCodeExecute_LargeOutput(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	// Generate output that exceeds the truncation limit (>4000 chars).
	// Each iteration of seq prints a number + newline. 2000 lines of 50-char strings should do it.
	transport := NewCodeExecTransport("test-large")
	result, err := transport.CallTool(context.Background(), "nanite_code_execute", map[string]any{
		"code":     `i=0; while [ $i -lt 500 ]; do echo "line_${i}_padding_to_make_this_line_longer_than_normal_xxxxxxxxxxxxxxxx"; i=$((i+1)); done`,
		"language": "shell",
		"timeout":  float64(10),
	})
	if err != nil {
		t.Fatalf("CallTool() error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content[0].Text)
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "truncated") {
		t.Errorf("expected truncation hint in output, got %d chars without 'truncated'", len(text))
	}
}

func TestCodeExecute_UnknownTool(t *testing.T) {
	transport := NewCodeExecTransport("test")
	result, err := transport.CallTool(context.Background(), "unknown_tool", map[string]any{})
	if err != nil {
		t.Fatalf("CallTool() error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for unknown tool")
	}
}

func TestCodeExecute_InvalidLanguage(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	transport := NewCodeExecTransport("test-invalid")
	result, err := transport.CallTool(context.Background(), "nanite_code_execute", map[string]any{
		"code":     "print('hi')",
		"language": "ruby",
	})
	if err != nil {
		t.Fatalf("CallTool() error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for unsupported language")
	}
	if !strings.Contains(result.Content[0].Text, "unsupported language") {
		t.Errorf("result = %q, want 'unsupported language'", result.Content[0].Text)
	}
}

func TestCodeExecute_EmptyCode(t *testing.T) {
	transport := NewCodeExecTransport("test")
	result, err := transport.CallTool(context.Background(), "nanite_code_execute", map[string]any{
		"code": "",
	})
	if err != nil {
		t.Fatalf("CallTool() error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for empty code")
	}
}

func TestCodeExecute_ListTools(t *testing.T) {
	transport := NewCodeExecTransport("test")
	tools, err := transport.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools() error: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("len(tools) = %d, want 1", len(tools))
	}
	if tools[0].Name != "nanite_code_execute" {
		t.Errorf("tool name = %q, want %q", tools[0].Name, "nanite_code_execute")
	}
}
