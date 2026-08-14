package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	condmcp "github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/store"
)

// newTestServer constructs a Server backed by an on-disk test store.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	tmp := t.TempDir()
	dbPath := tmp + "/test.db"
	s, err := store.New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		s.Close()
		os.Remove(dbPath)
	})
	return New(s, "test-session", nil, "", "", nil)
}

// TestBuildMCPTool_HappyPath verifies a valid schema passes through cleanly
// and the resulting tool has Name, Description, and a non-nil InputSchema.
func TestBuildMCPTool_HappyPath(t *testing.T) {
	in := condmcp.Tool{
		Name:        "demo",
		Description: "demo tool",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{"type": "string"},
			},
			"required": []any{"name"},
		},
	}
	tool := buildMCPTool(in)
	if tool.Name != "demo" {
		t.Errorf("Name = %q, want %q", tool.Name, "demo")
	}
	if tool.Description != "demo tool" {
		t.Errorf("Description = %q, want %q", tool.Description, "demo tool")
	}
	if tool.InputSchema == nil {
		t.Fatal("InputSchema is nil, want non-nil")
	}
}

// TestBuildMCPTool_NilSchema verifies that a nil input schema still yields a
// non-nil tool.InputSchema (defensive contract: never register a tool with
// nil schema).
func TestBuildMCPTool_NilSchema(t *testing.T) {
	in := condmcp.Tool{
		Name:        "demo",
		Description: "demo",
		InputSchema: nil,
	}
	tool := buildMCPTool(in)
	if tool.InputSchema == nil {
		t.Fatal("InputSchema is nil, want non-nil fallback")
	}
}

// TestBuildMCPTool_InvalidSchema feeds a schema whose "type" is not a string
// (invalid per jsonschema spec). The first unmarshal should fail; the
// fallback should kick in and still yield a non-nil InputSchema.
func TestBuildMCPTool_InvalidSchema(t *testing.T) {
	in := condmcp.Tool{
		Name:        "bad",
		Description: "bad schema",
		InputSchema: map[string]any{
			// "type" must be string or []string; a number forces the
			// jsonschema unmarshal to error, exercising the fallback path.
			"type": 42,
		},
	}
	tool := buildMCPTool(in)
	if tool.InputSchema == nil {
		t.Fatal("InputSchema is nil after fallback, want non-nil")
	}
}

// sentinelErr is used to verify the error chain is preserved by
// newErrorResult / SetError / GetError round-trip.
type sentinelErr struct{ msg string }

func (e *sentinelErr) Error() string { return e.msg }

// TestNewErrorResult_PreservesError verifies that the wire shape matches the
// mark3labs semantics (1 TextContent block with err.Error(), IsError=true)
// and that the original error type is recoverable via GetError().
func TestNewErrorResult_PreservesError(t *testing.T) {
	sentinel := &sentinelErr{msg: "kaboom"}
	r := newErrorResult(sentinel)

	if !r.IsError {
		t.Error("IsError = false, want true")
	}
	if len(r.Content) != 1 {
		t.Fatalf("len(Content) = %d, want 1", len(r.Content))
	}
	tc, ok := r.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("Content[0] type = %T, want *mcp.TextContent", r.Content[0])
	}
	if tc.Text != "kaboom" {
		t.Errorf("Text = %q, want %q", tc.Text, "kaboom")
	}

	got := r.GetError()
	if got == nil {
		t.Fatal("GetError() = nil, want non-nil")
	}
	var target *sentinelErr
	if !errors.As(got, &target) {
		t.Errorf("errors.As(GetError, *sentinelErr) = false; error chain not preserved")
	}
}

// TestNewErrorResult_WrappedError verifies that wrapped errors preserve their
// unwrap chain so callers can inspect the root cause.
func TestNewErrorResult_WrappedError(t *testing.T) {
	root := errors.New("root cause")
	wrapped := errors.Join(root, errors.New("context"))
	r := newErrorResult(wrapped)

	got := r.GetError()
	if !errors.Is(got, root) {
		t.Error("errors.Is(GetError, root) = false; unwrap chain not preserved")
	}
}

// TestMakeHandler_MalformedArguments verifies that invalid JSON in the
// request arguments produces an error result (not a transport-level error).
func TestMakeHandler_MalformedArguments(t *testing.T) {
	srv := newTestServer(t)
	// Use any valid tool name; argument parsing happens before dispatch.
	h := srv.makeHandler("skill_list")

	req := &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Arguments: json.RawMessage(`{not valid json`),
		},
	}
	result, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("handler returned transport error: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatal("expected IsError result for malformed JSON")
	}
	if len(result.Content) == 0 {
		t.Fatal("expected error content, got none")
	}
}

// TestMakeHandler_EmptyArguments verifies that an empty arguments payload
// (len==0) is forwarded as an empty map to CallTool, and the happy path
// returns a TextContent result.
func TestMakeHandler_EmptyArguments(t *testing.T) {
	srv := newTestServer(t)
	// skill_list accepts empty args (listing all skills).
	h := srv.makeHandler("skill_list")

	req := &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Arguments: nil, // len==0 triggers the "skip unmarshal" branch
		},
	}
	result, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("handler returned transport error: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
	if result.IsError {
		t.Errorf("IsError = true, want false (empty list is a valid result)")
	}
	if len(result.Content) != 1 {
		t.Fatalf("len(Content) = %d, want 1", len(result.Content))
	}
	if _, ok := result.Content[0].(*mcp.TextContent); !ok {
		t.Errorf("Content[0] type = %T, want *mcp.TextContent", result.Content[0])
	}
}

// TestMakeHandler_PropagatesIsError verifies that a self-tool handler's
// errorResult (IsError=true on the internal condmcp.ToolResult, with a
// nil Go error return — the convention every self-tool handler uses)
// propagates onto the wire CallToolResult.IsError. Found missing via
// CW-20260813-0011's end-to-end verification: a real MCP client reading
// CallToolResult.IsError (rather than sniffing the text body) could never
// see a handler-reported failure, which defeats tools like
// workflow_verify_step whose entire contract is pass/fail via that flag.
func TestMakeHandler_PropagatesIsError(t *testing.T) {
	srv := newTestServer(t)
	// workflow_execute_llm_step's handler returns errorResult(...) when
	// no WorkflowExecutor is wired — deterministic and needs no other
	// service wiring, unlike most other self-tool error paths.
	h := srv.makeHandler("workflow_execute_llm_step")

	req := &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Arguments: json.RawMessage(`{"provider":"anthropic","model":"claude-sonnet-5","messages":[{"role":"user","content":"hi"}]}`),
		},
	}
	result, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("handler returned transport error: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("expected IsError=true on the wire result, got %+v", result)
	}
}

// TestMakeHandler_EnvelopeConversion verifies that envelope markers in the
// underlying tool's text output are converted to nanite-envelope fenced
// blocks before being returned to the SDK caller.
func TestMakeHandler_EnvelopeConversion(t *testing.T) {
	srv := newTestServer(t)
	// card_show emits an envelope marker in its text output. Using
	// info-card here because it has no grounding/sources requirement, so
	// the smoke check stays focused on the envelope-conversion path.
	h := srv.makeHandler("card_show")

	args, _ := json.Marshal(map[string]any{
		"type": "info-card",
		"data": map[string]any{
			"title":   "Smoke Test",
			"body":    "envelope conversion check",
			"variant": "info",
		},
	})
	req := &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Arguments: args},
	}
	result, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("handler returned transport error: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
	if len(result.Content) != 1 {
		t.Fatalf("len(Content) = %d, want 1", len(result.Content))
	}
	tc, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("Content[0] type = %T, want *mcp.TextContent", result.Content[0])
	}
	// If the underlying tool emitted an envelope marker, it should have been
	// converted. If not, this test degrades to a smoke check of the happy path.
	if strings.Contains(tc.Text, "<!--ENVELOPE_DATA:") {
		t.Errorf("raw envelope marker leaked into output: %q", tc.Text)
	}
}
