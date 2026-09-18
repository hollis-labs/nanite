package mcpserver

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/envelope"
	condmcp "github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/storetest"
)

// newTestServer constructs a Server backed by an on-disk test store.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	tmp := t.TempDir()
	dbPath := tmp + "/test.db"
	s, err := storetest.New(t, context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		s.Close(context.Background())
		os.Remove(dbPath)
	})
	return New(s, "test-session", nil, "", "", nil)
}

// TestBuildTool_HappyPath verifies a schema passes through cleanly and the
// resulting tool has Name, Description, and a non-nil InputSchema.
func TestBuildTool_HappyPath(t *testing.T) {
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
	tool := buildTool(in, func(context.Context, map[string]any) (any, error) { return "", nil })
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

// TestBuildTool_NilSchema verifies that a nil input schema still yields a
// non-nil tool.InputSchema (defensive contract: never register a tool with
// nil schema).
func TestBuildTool_NilSchema(t *testing.T) {
	in := condmcp.Tool{Name: "demo", Description: "demo", InputSchema: nil}
	tool := buildTool(in, func(context.Context, map[string]any) (any, error) { return "", nil })
	if tool.InputSchema == nil {
		t.Fatal("InputSchema is nil, want non-nil fallback")
	}
}

// TestBuildTool_UnauditedNameDefaultsToDangerous verifies annotationsFor's
// fail-closed default: a tool name not in the annotations.go table must
// come back looking mutating and non-idempotent, not read-only.
func TestBuildTool_UnauditedNameDefaultsToDangerous(t *testing.T) {
	in := condmcp.Tool{Name: "definitely_not_a_real_tool_name"}
	tool := buildTool(in, func(context.Context, map[string]any) (any, error) { return "", nil })
	if tool.ReadOnlyHint || !tool.DestructiveHint {
		t.Errorf("unaudited tool annotations = %+v, want the safe-default (destructive, not read-only)", tool)
	}
}

// TestMakeHandler_EmptyArguments verifies an empty arguments map is
// forwarded to CallTool and the happy path returns a plain string result.
func TestMakeHandler_EmptyArguments(t *testing.T) {
	srv := newTestServer(t)
	// skill_list accepts empty args (listing all skills).
	h := srv.makeHandler("skill_list")

	result, err := h(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("handler returned unexpected error: %v", err)
	}
	if _, ok := result.(string); !ok {
		t.Fatalf("result type = %T, want string", result)
	}
}

// TestMakeHandler_PropagatesIsError verifies that a self-tool handler's
// errorResult (IsError=true on the internal condmcp.ToolResult, with a nil
// Go error return — the convention every self-tool handler uses) surfaces
// as a returned Go error here, which go-mcp/server's own ToolHandler
// contract then reports as CallToolResult.IsError=true on the wire. Found
// missing via CW-20260813-0011's end-to-end verification: a real MCP
// client reading CallToolResult.IsError (rather than sniffing the text
// body) could never see a handler-reported failure, which defeats tools
// like workflow_verify_step whose entire contract is pass/fail via that
// flag.
func TestMakeHandler_PropagatesIsError(t *testing.T) {
	srv := newTestServer(t)
	// workflow_execute_llm_step's handler returns errorResult(...) when
	// no WorkflowExecutor is wired — deterministic and needs no other
	// service wiring, unlike most other self-tool error paths.
	h := srv.makeHandler("workflow_execute_llm_step")

	args := map[string]any{
		"provider": "anthropic",
		"model":    "claude-sonnet-5",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	}
	result, err := h(context.Background(), args)
	if err == nil {
		t.Fatalf("expected a returned error (wire IsError=true), got result %+v, nil error", result)
	}
}

// TestMakeHandler_EnvelopeConversion verifies that envelope markers in the
// underlying tool's text output are converted to nanite-envelope fenced
// blocks before being returned to the SDK caller.
func TestMakeHandler_EnvelopeConversion(t *testing.T) {
	srv := newTestServer(t)
	// card_show validates its payload against the shared go-envelopes
	// registry, which this package has no TestMain to install (unlike
	// internal/mcp and internal/envelope's own suites) -- without this,
	// the call fails before ever reaching the marker-conversion code this
	// test exists to exercise, and go-mcp/server's contract now correctly
	// surfaces that as a returned error (see makeTransportHandler's doc
	// comment) rather than silently degrading to a smoke check the way a
	// handler-level error never returned as `err` used to.
	envelope.SetupForTesting()

	// card_show emits an envelope marker in its text output. Using
	// info-card here because it has no grounding/sources requirement, so
	// the smoke check stays focused on the envelope-conversion path.
	h := srv.makeHandler("card_show")

	args := map[string]any{
		"type": "info-card",
		"data": map[string]any{
			"title":   "Smoke Test",
			"body":    "envelope conversion check",
			"variant": "info",
		},
	}
	result, err := h(context.Background(), args)
	if err != nil {
		t.Fatalf("handler returned unexpected error: %v", err)
	}
	text, ok := result.(string)
	if !ok {
		t.Fatalf("result type = %T, want string", result)
	}
	// If the underlying tool emitted an envelope marker, it should have been
	// converted. If not, this test degrades to a smoke check of the happy path.
	if strings.Contains(text, "<!--ENVELOPE_DATA:") {
		t.Errorf("raw envelope marker leaked into output: %q", text)
	}
}
