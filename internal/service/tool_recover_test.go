package service

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/envelope"
)

// TestClassifyAndFormatToolError_SchemaValidation_EmitsAgentEnvelope is the
// C1 acceptance check at the harness boundary: a bad nanite_show_card
// payload comes back with IsError=true and a JSON envelope carrying
// kind=schema_validation, the missing-field reason, a suggestion that
// names 'metrics', and the stable mem://...report-card schema URI.
func TestClassifyAndFormatToolError_SchemaValidation_EmitsAgentEnvelope(t *testing.T) {
	// Real validator path so we test against the production error type.
	err := envelope.ValidateData("report-card", map[string]any{"title": "x"})
	if err == nil {
		t.Fatalf("expected validation error for report-card without metrics")
	}

	args := map[string]any{"type": "report-card", "data": map[string]any{"title": "x"}}
	result := classifyAndFormatToolError(err, "nanite_show_card", args)
	if result == nil {
		t.Fatalf("classifyAndFormatToolError returned nil")
	}
	if !result.IsError {
		t.Errorf("expected IsError=true on the recoverable path")
	}

	var env map[string]any
	if jerr := json.Unmarshal([]byte(result.Output), &env); jerr != nil {
		t.Fatalf("expected JSON envelope, got plain prose: %q (decode err %v)", result.Output, jerr)
	}
	if env["recoverable_error"] != true {
		t.Errorf("envelope missing recoverable_error=true: %+v", env)
	}
	if env["kind"] != "schema_validation" {
		t.Errorf("envelope kind = %v, want schema_validation", env["kind"])
	}
	if env["tool"] != "nanite_show_card" {
		t.Errorf("envelope tool = %v, want nanite_show_card", env["tool"])
	}
	if uri, _ := env["schema_uri"].(string); !strings.HasSuffix(uri, "report-card.schema.json") || !strings.HasPrefix(uri, "mem://") {
		t.Errorf("schema_uri must be the stable mem:// form, got %q", uri)
	}
	if reason, _ := env["reason"].(string); reason == "" {
		t.Errorf("expected a reason in the envelope: %+v", env)
	}
	if suggestion, _ := env["suggestion"].(string); !strings.Contains(suggestion, "metrics") {
		t.Errorf("expected suggestion to mention 'metrics', got %q", suggestion)
	}
}

// TestClassifyAndFormatToolError_PermissionDenied_PreservesLegacyShape is
// the negative half of the acceptance gate: an auth-class failure must
// flow through with the byte-stable legacy `Error: <prose>` output so
// existing agents (and tests) are unaffected by the classification layer.
func TestClassifyAndFormatToolError_PermissionDenied_PreservesLegacyShape(t *testing.T) {
	err := errors.New(`permission denied: tool "shell_exec" not permitted for agent "chat-1"`)
	result := classifyAndFormatToolError(err, "shell_exec", nil)
	if !result.IsError {
		t.Errorf("expected IsError=true on permission denied")
	}
	if !strings.HasPrefix(result.Output, "Error: ") {
		t.Errorf("permission-denied path must keep the `Error: ...` prefix, got %q", result.Output)
	}
	if strings.Contains(result.Output, "recoverable_error") {
		t.Errorf("permission-denied must NOT be classified as recoverable, got %q", result.Output)
	}
}

// TestClassifyAndFormatToolError_ServiceUnavailable_PreservesLegacyShape
// covers the audit's G6 cluster: service-not-configured failures look
// like recoverable shape errors but no schema fix retries them. Must
// pass through unchanged.
func TestClassifyAndFormatToolError_ServiceUnavailable_PreservesLegacyShape(t *testing.T) {
	err := errors.New("tool error: todo service not available")
	result := classifyAndFormatToolError(err, "nanite_todo_create", nil)
	if !result.IsError {
		t.Errorf("expected IsError=true")
	}
	if strings.Contains(result.Output, "recoverable_error") {
		t.Errorf("service-not-available must NOT be classified as recoverable, got %q", result.Output)
	}
}

// TestClassifyAndFormatToolError_WrongCardType emits the right kind for
// the audit's `nanite_show_card` "not addressable" path so C2 can pick
// the type-remap repair strategy without LLM-budget spend on auth-class
// false positives.
func TestClassifyAndFormatToolError_WrongCardType(t *testing.T) {
	err := errors.New(`tool error: envelope type "approval-card" is not addressable through nanite_show_card. Allow-list (v1): info-card, list-card`)
	result := classifyAndFormatToolError(err, "nanite_show_card", map[string]any{"type": "approval-card"})
	if !result.IsError {
		t.Errorf("expected IsError=true")
	}
	var env map[string]any
	if jerr := json.Unmarshal([]byte(result.Output), &env); jerr != nil {
		t.Fatalf("expected JSON envelope, got %q", result.Output)
	}
	if env["kind"] != "wrong_card_type" {
		t.Errorf("expected kind=wrong_card_type, got %v", env["kind"])
	}
}
