package mcp

import (
	"context"
	//nolint:gosec // G501: md5 is a user-selectable algorithm in the hash
	// tool contract; test validates that round-trip, not any security claim.
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newGeneralTools() *GeneralToolsTransport {
	return NewGeneralToolsTransport()
}

// --- web_fetch ---

func TestWebFetch_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		fmt.Fprint(w, "response body")
	}))
	defer srv.Close()

	// httptest binds to 127.0.0.1; flip the opt-in flag so the SSRF guard
	// allows loopback for this positive-path test.
	gt := newGeneralTools()
	gt.AllowLocalhost = true
	result, err := gt.CallTool(context.Background(), "web_fetch", map[string]any{
		"url": srv.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "200") {
		t.Errorf("expected status 200, got: %s", text)
	}
	if !strings.Contains(text, "response body") {
		t.Errorf("expected body, got: %s", text)
	}
}

func TestWebFetch_MissingURL(t *testing.T) {
	gt := newGeneralTools()
	result, _ := gt.CallTool(context.Background(), "web_fetch", map[string]any{})
	if !result.IsError {
		t.Fatal("expected error for missing url")
	}
}

// --- json_parse ---

func TestJSONParse_NestedPath(t *testing.T) {
	gt := newGeneralTools()
	jsonStr := `{"data":{"items":[{"name":"first"},{"name":"second"}]}}`

	result, err := gt.CallTool(context.Background(), "json_parse", map[string]any{
		"json": jsonStr,
		"path": ".data.items[1].name",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	if result.Content[0].Text != "second" {
		t.Errorf("expected 'second', got: %s", result.Content[0].Text)
	}
}

func TestJSONParse_TopLevelArray(t *testing.T) {
	gt := newGeneralTools()
	result, err := gt.CallTool(context.Background(), "json_parse", map[string]any{
		"json": `[10, 20, 30]`,
		"path": ".[1]",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	if result.Content[0].Text != "20" {
		t.Errorf("expected '20', got: %s", result.Content[0].Text)
	}
}

func TestJSONParse_InvalidJSON(t *testing.T) {
	gt := newGeneralTools()
	result, _ := gt.CallTool(context.Background(), "json_parse", map[string]any{
		"json": `{invalid`,
		"path": ".key",
	})
	if !result.IsError {
		t.Fatal("expected error for invalid JSON")
	}
}

// --- datetime ---

func TestDatetime_CurrentTime(t *testing.T) {
	gt := newGeneralTools()
	result, err := gt.CallTool(context.Background(), "datetime", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	// Should contain a valid RFC3339 timestamp.
	if !strings.Contains(result.Content[0].Text, "T") {
		t.Errorf("expected RFC3339 format, got: %s", result.Content[0].Text)
	}
}

func TestDatetime_DateMath(t *testing.T) {
	gt := newGeneralTools()
	result, err := gt.CallTool(context.Background(), "datetime", map[string]any{
		"operation": "+3d",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, "T") {
		t.Errorf("expected RFC3339 format, got: %s", result.Content[0].Text)
	}
}

func TestDatetime_InvalidOp(t *testing.T) {
	gt := newGeneralTools()
	result, _ := gt.CallTool(context.Background(), "datetime", map[string]any{
		"operation": "xyz",
	})
	if !result.IsError {
		t.Fatal("expected error for invalid operation")
	}
}

// --- base64 ---

func TestBase64_RoundTrip(t *testing.T) {
	gt := newGeneralTools()
	original := "Hello, World! 🌍"

	encResult, err := gt.CallTool(context.Background(), "base64_encode", map[string]any{
		"input": original,
	})
	if err != nil {
		t.Fatal(err)
	}
	if encResult.IsError {
		t.Fatalf("encode error: %s", encResult.Content[0].Text)
	}

	encoded := encResult.Content[0].Text
	expected := base64.StdEncoding.EncodeToString([]byte(original))
	if encoded != expected {
		t.Errorf("expected %s, got %s", expected, encoded)
	}

	decResult, err := gt.CallTool(context.Background(), "base64_decode", map[string]any{
		"input": encoded,
	})
	if err != nil {
		t.Fatal(err)
	}
	if decResult.IsError {
		t.Fatalf("decode error: %s", decResult.Content[0].Text)
	}
	if decResult.Content[0].Text != original {
		t.Errorf("expected %s, got %s", original, decResult.Content[0].Text)
	}
}

func TestBase64_InvalidDecode(t *testing.T) {
	gt := newGeneralTools()
	result, _ := gt.CallTool(context.Background(), "base64_decode", map[string]any{
		"input": "not-valid-base64!!!",
	})
	if !result.IsError {
		t.Fatal("expected error for invalid base64")
	}
}

// --- url encode/decode ---

func TestURLEncode_SpecialChars(t *testing.T) {
	gt := newGeneralTools()
	result, err := gt.CallTool(context.Background(), "url_encode", map[string]any{
		"input": "hello world&foo=bar",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	encoded := result.Content[0].Text
	if !strings.Contains(encoded, "%26") || !strings.Contains(encoded, "%3D") {
		t.Errorf("expected encoded special chars, got: %s", encoded)
	}
}

func TestURLDecode_RoundTrip(t *testing.T) {
	gt := newGeneralTools()
	original := "hello world&foo=bar"

	encResult, _ := gt.CallTool(context.Background(), "url_encode", map[string]any{
		"input": original,
	})
	decResult, _ := gt.CallTool(context.Background(), "url_decode", map[string]any{
		"input": encResult.Content[0].Text,
	})
	if decResult.Content[0].Text != original {
		t.Errorf("expected %s, got %s", original, decResult.Content[0].Text)
	}
}

// --- hash ---

func TestHash_SHA256(t *testing.T) {
	gt := newGeneralTools()
	result, err := gt.CallTool(context.Background(), "hash", map[string]any{
		"input": "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	expected := sha256.Sum256([]byte("test"))
	if result.Content[0].Text != hex.EncodeToString(expected[:]) {
		t.Errorf("sha256 mismatch: got %s", result.Content[0].Text)
	}
}

func TestHash_MD5(t *testing.T) {
	gt := newGeneralTools()
	result, err := gt.CallTool(context.Background(), "hash", map[string]any{
		"input":     "test",
		"algorithm": "md5",
	})
	if err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // G401: test fixture for user-selectable md5 algorithm.
	expected := md5.Sum([]byte("test"))
	if result.Content[0].Text != hex.EncodeToString(expected[:]) {
		t.Errorf("md5 mismatch: got %s", result.Content[0].Text)
	}
}

func TestHash_UnsupportedAlgorithm(t *testing.T) {
	gt := newGeneralTools()
	result, _ := gt.CallTool(context.Background(), "hash", map[string]any{
		"input":     "test",
		"algorithm": "sha512",
	})
	if !result.IsError {
		t.Fatal("expected error for unsupported algorithm")
	}
}

// --- math_eval ---

func TestMathEval_BasicArithmetic(t *testing.T) {
	tests := []struct {
		expr     string
		expected string
	}{
		{"2 + 3", "5"},
		{"10 - 4", "6"},
		{"3 * 7", "21"},
		{"15 / 3", "5"},
		{"2 ^ 10", "1024"},
		{"(2 + 3) * 4", "20"},
		{"2 + 3 * 4", "14"},
		{"10 / 3", "3.3333333333333335"},
		{"-5 + 3", "-2"},
		{"2.5 * 4", "10"},
	}

	gt := newGeneralTools()
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			result, err := gt.CallTool(context.Background(), "math_eval", map[string]any{
				"expression": tt.expr,
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.IsError {
				t.Fatalf("unexpected error: %s", result.Content[0].Text)
			}
			if result.Content[0].Text != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, result.Content[0].Text)
			}
		})
	}
}

func TestMathEval_DivisionByZero(t *testing.T) {
	gt := newGeneralTools()
	result, _ := gt.CallTool(context.Background(), "math_eval", map[string]any{
		"expression": "1 / 0",
	})
	if !result.IsError {
		t.Fatal("expected error for division by zero")
	}
}

func TestMathEval_InvalidExpression(t *testing.T) {
	gt := newGeneralTools()
	result, _ := gt.CallTool(context.Background(), "math_eval", map[string]any{
		"expression": "abc",
	})
	if !result.IsError {
		t.Fatal("expected error for invalid expression")
	}
}

// --- ListTools ---

func TestDevToolsListTools(t *testing.T) {
	dt, _ := tempDevTools(t)
	tools, err := dt.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	names := make(map[string]bool)
	for _, tool := range tools {
		names[tool.Name] = true
	}
	expected := []string{"dev_read", "dev_grep", "dev_write", "dev_bash", "dev_glob", "dev_edit"}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("missing tool: %s", name)
		}
	}
}

func TestGeneralToolsListTools(t *testing.T) {
	gt := newGeneralTools()
	tools, err := gt.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	names := make(map[string]bool)
	for _, tool := range tools {
		names[tool.Name] = true
	}
	expected := []string{"web_fetch", "json_parse", "datetime", "base64_encode", "base64_decode", "url_encode", "url_decode", "hash", "math_eval"}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("missing tool: %s", name)
		}
	}
}
