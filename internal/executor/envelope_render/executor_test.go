package envelope_render

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/dispatch"
	"github.com/hollis-labs/nanite/internal/envelope"
)

// TestMain wires the go-envelopes registry once for the whole test
// binary. ValidateData refuses to run without it.
func TestMain(m *testing.M) {
	envelope.SetupForTesting()
	os.Exit(m.Run())
}

// TestExecute_HappyPath_ReportCard is the canonical c117/c119-shaped
// scenario. The dispatching caller (the future B2 wiring) supplies a
// pre-resolved data payload and source citations; the executor
// validates, stamps generated_at + sources + render_target, and
// returns the envelope.
func TestExecute_HappyPath_ReportCard(t *testing.T) {
	exec := New()
	resp, err := exec.Execute(context.Background(), dispatch.ExecutorRequest{
		Intent:             IntentRenderEnvelope,
		TargetEnvelopeType: "report-card",
		UserRequest:        "Let's do some testing again. Please create a demo report, use the report card to show it to me.",
		Data: map[string]any{
			"title": "Q1 Demo Metrics",
			"metrics": []any{
				map[string]any{"label": "Active sprints", "value": "3"},
				map[string]any{"label": "Tickets closed", "value": "47"},
			},
			"summary": "Q1 demo data — synthesized for illustration.",
		},
		Sources: []dispatch.ExecutorSource{
			{ToolUseID: "toolu_synth_01", ToolName: "synthesized", Note: "demo data"},
		},
		SyntheticAllowed: true,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if resp == nil {
		t.Fatal("nil response")
	}
	if resp.Failure != nil {
		t.Fatalf("unexpected failure: %+v", resp.Failure)
	}
	if resp.Envelope == nil {
		t.Fatal("expected envelope, got nil")
	}
	if resp.Envelope.Type != "report-card" {
		t.Fatalf("wrong type: %q", resp.Envelope.Type)
	}
	if resp.Envelope.Kind != "envelope" || resp.Envelope.Version != 1 {
		t.Fatalf("wrong wire shape: kind=%q version=%d", resp.Envelope.Kind, resp.Envelope.Version)
	}
	if resp.Envelope.RenderTarget != "bottom_chat_drawer" {
		t.Fatalf("expected bottom_chat_drawer render_target, got %q", resp.Envelope.RenderTarget)
	}
	if got, _ := resp.Envelope.Data["generated_at"].(string); got == "" {
		t.Error("expected generated_at to be stamped")
	}
	srcs, ok := resp.Envelope.Data["sources"].([]map[string]any)
	if !ok || len(srcs) != 1 {
		t.Fatalf("expected one source citation, got %T %v", resp.Envelope.Data["sources"], resp.Envelope.Data["sources"])
	}
	if srcs[0]["tool_name"] != "synthesized" {
		t.Errorf("source tool_name should pass through verbatim, got %v", srcs[0]["tool_name"])
	}
	// c119 judgment 1: the synthesis disclosure must reach the Chat
	// agent verbatim via the Summary so it can pass to the user.
	if !strings.Contains(strings.ToLower(resp.Summary), "synthesized") {
		t.Errorf("Summary must disclose synthesis, got: %q", resp.Summary)
	}
}

// TestExecute_HappyPath_ListCard verifies the pilot covers a non-grounded
// passive type (no Sources requirement).
func TestExecute_HappyPath_ListCard(t *testing.T) {
	exec := New()
	resp, err := exec.Execute(context.Background(), dispatch.ExecutorRequest{
		Intent:             IntentRenderEnvelope,
		TargetEnvelopeType: "list-card",
		UserRequest:        "Show me a list of demo items",
		Data: map[string]any{
			"title": "Demo list",
			"items": []any{
				map[string]any{"label": "Item 1"},
				map[string]any{"label": "Item 2"},
			},
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if resp.Failure != nil {
		t.Fatalf("unexpected failure: %+v", resp.Failure)
	}
	if resp.Envelope == nil || resp.Envelope.Type != "list-card" {
		t.Fatalf("expected list-card envelope, got %+v", resp.Envelope)
	}
	// list-card is not a grounded type — Sources should not be required.
	if _, has := resp.Envelope.Data["sources"]; has {
		t.Error("non-grounded type should not stamp sources")
	}
}

// TestExecute_AllPassiveRenderableTypes_Reachable confirms the executor
// recognizes every v1 passive-renderable type — not that every shape
// renders without further data, but that the type slug check passes
// (the failure mode for an unsupported type is unrecoverable, not
// missing_context).
//
// This is the "pilot covers all v1 passive renderable envelope types"
// acceptance gate. For each type we send minimum viable data; types that
// require fields beyond what we supply legitimately fail with
// missing_context (validation), not unrecoverable (allow-list miss).
func TestExecute_AllPassiveRenderableTypes_Reachable(t *testing.T) {
	exec := New()
	for _, envType := range envelope.PassiveRenderableTypes {
		t.Run(envType, func(t *testing.T) {
			req := dispatch.ExecutorRequest{
				Intent:             IntentRenderEnvelope,
				TargetEnvelopeType: envType,
				UserRequest:        "smoke",
				Data:               map[string]any{}, // most types will fail validation; that's OK
			}
			if envType == "report-card" || envType == "document-viewer" {
				req.Sources = []dispatch.ExecutorSource{{ToolUseID: "tu_smoke", ToolName: "smoke"}}
			}
			resp, err := exec.Execute(context.Background(), req)
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			// The unrecoverable path is the *only* one that means "this
			// type is not supported by the pilot." Anything else (happy,
			// missing_context for incomplete data) is acceptable here.
			if resp.Failure != nil && resp.Failure.Code == dispatch.ExecutorFailureUnrecoverable {
				t.Fatalf("type %q routed to unrecoverable — pilot must accept all v1 passive renderables: %s",
					envType, resp.Failure.Message)
			}
		})
	}
}

// TestExecute_Failure_UnknownType triggers the unrecoverable failure
// path — the dispatching caller asked for a type that is not on the v1
// passive-renderable allow-list. Per B1 §5 the Chat agent narrates this
// without retrying.
func TestExecute_Failure_UnknownType(t *testing.T) {
	exec := New()
	resp, err := exec.Execute(context.Background(), dispatch.ExecutorRequest{
		Intent:             IntentRenderEnvelope,
		TargetEnvelopeType: "no-such-card",
		Data:               map[string]any{"title": "x"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if resp.Failure == nil {
		t.Fatal("expected typed failure, got success")
	}
	if resp.Failure.Code != dispatch.ExecutorFailureUnrecoverable {
		t.Fatalf("expected unrecoverable, got %q", resp.Failure.Code)
	}
	if !strings.Contains(resp.Failure.Message, "no-such-card") {
		t.Errorf("failure message should reference the offending type: %q", resp.Failure.Message)
	}
	if resp.Envelope != nil {
		t.Error("expected nil envelope on unrecoverable failure")
	}
}

// TestExecute_Failure_MissingRequiredField triggers the
// missing_context path with PartialEnvelope set — the schema rejected
// the data. Per B1 §5 the Chat agent may render the partial as a
// degraded card with a banner.
func TestExecute_Failure_MissingRequiredField(t *testing.T) {
	exec := New()
	resp, err := exec.Execute(context.Background(), dispatch.ExecutorRequest{
		Intent:             IntentRenderEnvelope,
		TargetEnvelopeType: "report-card",
		Data: map[string]any{
			// missing required `title` and `metrics`
			"summary": "incomplete",
		},
		Sources: []dispatch.ExecutorSource{{ToolUseID: "tu", ToolName: "synth"}},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if resp.Failure == nil {
		t.Fatal("expected typed failure")
	}
	if resp.Failure.Code != dispatch.ExecutorFailureMissingContext {
		t.Fatalf("expected missing_context, got %q (%s)", resp.Failure.Code, resp.Failure.Message)
	}
	if resp.Failure.PartialEnvelope == nil {
		t.Error("expected PartialEnvelope set so the Chat agent can render a degraded card")
	}
	if !strings.Contains(resp.Failure.Message, "report-card") {
		t.Errorf("failure message should reference the type: %q", resp.Failure.Message)
	}
}

// TestExecute_RepairNeeded_SingleMetricCoercion exercises the repair
// pass: the caller passed data["metrics"] as a single object instead of
// an array. The executor coerces the shape and re-validates; on success
// the response carries Envelope (not Failure) and the Summary mentions
// the coercion.
func TestExecute_RepairNeeded_SingleMetricCoercion(t *testing.T) {
	exec := New()
	resp, err := exec.Execute(context.Background(), dispatch.ExecutorRequest{
		Intent:             IntentRenderEnvelope,
		TargetEnvelopeType: "report-card",
		Data: map[string]any{
			"title": "Repair Test",
			// schema requires metrics to be a list — pass an object,
			// the executor's repair pass should coerce to [{...}].
			"metrics": map[string]any{"label": "Solo metric", "value": "42"},
		},
		Sources: []dispatch.ExecutorSource{{ToolUseID: "tu", ToolName: "synth"}},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if resp.Failure != nil {
		t.Fatalf("expected repair to succeed, got failure: %+v", resp.Failure)
	}
	if resp.Envelope == nil {
		t.Fatal("expected envelope after repair")
	}
	metrics, ok := resp.Envelope.Data["metrics"].([]any)
	if !ok {
		t.Fatalf("metrics should have been coerced to []any, got %T", resp.Envelope.Data["metrics"])
	}
	if len(metrics) != 1 {
		t.Errorf("expected one metric after coercion, got %d", len(metrics))
	}
	if !strings.Contains(strings.ToLower(resp.Summary), "coerced") {
		t.Errorf("Summary should disclose the coercion, got: %q", resp.Summary)
	}
}

// TestExecute_Failure_GroundedTypeMissingSources verifies the c119
// judgment 3 enforcement: report-card and document-viewer require at
// least one source citation. Without it the executor returns
// missing_context.
func TestExecute_Failure_GroundedTypeMissingSources(t *testing.T) {
	exec := New()
	resp, err := exec.Execute(context.Background(), dispatch.ExecutorRequest{
		Intent:             IntentRenderEnvelope,
		TargetEnvelopeType: "report-card",
		Data: map[string]any{
			"title":   "Ungrounded",
			"metrics": []any{map[string]any{"label": "x", "value": "1"}},
		},
		// Sources omitted on purpose
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if resp.Failure == nil {
		t.Fatal("expected missing_context failure for grounded type without sources")
	}
	if resp.Failure.Code != dispatch.ExecutorFailureMissingContext {
		t.Fatalf("expected missing_context, got %q", resp.Failure.Code)
	}
}

// TestExecute_WrongIntent_DefensiveCheck verifies the executor's own
// intent guard fires even when DispatchExecutor is bypassed. (The
// canonical entry path filters first; this is the second line.)
func TestExecute_WrongIntent_DefensiveCheck(t *testing.T) {
	exec := New()
	resp, err := exec.Execute(context.Background(), dispatch.ExecutorRequest{
		Intent:             "knowledge_grounded_answer",
		TargetEnvelopeType: "report-card",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if resp.Failure == nil || resp.Failure.Code != dispatch.ExecutorFailureInvalidIntent {
		t.Fatalf("expected invalid_intent failure, got %+v", resp.Failure)
	}
}

// TestExecute_MissingTargetType_MissingContext verifies the executor
// reports missing_context when the dispatching caller didn't tell it
// what to render (B1: classifier may leave TargetEnvelopeType empty
// when the type is to-be-decided, but the in-process pilot does not
// pick types yet).
func TestExecute_MissingTargetType_MissingContext(t *testing.T) {
	exec := New()
	resp, err := exec.Execute(context.Background(), dispatch.ExecutorRequest{
		Intent: IntentRenderEnvelope,
		Data:   map[string]any{"title": "x"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if resp.Failure == nil || resp.Failure.Code != dispatch.ExecutorFailureMissingContext {
		t.Fatalf("expected missing_context, got %+v", resp.Failure)
	}
}

// TestPromptIsEmbeddedAndUnderBudget asserts the executor prompt is
// embedded and stays under the <300-word budget the B3 acceptance gate
// requires. Trips the test when a future edit accidentally bloats the
// prompt back toward the c107→c117 anti-pattern.
func TestPromptIsEmbeddedAndUnderBudget(t *testing.T) {
	if Prompt == "" {
		t.Fatal("Prompt is empty — //go:embed prompt.md not wired")
	}
	body := stripFrontmatter(Prompt)
	words := len(strings.Fields(body))
	if words >= 300 {
		t.Fatalf("prompt body must be <300 words (B3 acceptance), got %d", words)
	}
	// Spot-check the four c119 judgments are present so a future edit
	// doesn't silently drop one. Match on distinctive phrases that are
	// unlikely to appear unless the rule is verbatim.
	musts := []string{
		"synthesize",                  // judgment 1
		"empty for THIS user",         // judgment 2
		"semantically aligned",        // judgment 3
		"signal to pivot",             // judgment 4
		"`tool_describe`'s id",        // the right-vs-wrong example
	}
	for _, m := range musts {
		if !strings.Contains(body, m) {
			t.Errorf("prompt is missing the c119 judgment phrase %q", m)
		}
	}
}

// stripFrontmatter peels off a leading YAML frontmatter block (--- ...
// ---) from raw so the word count reflects the prompt body only.
func stripFrontmatter(raw string) string {
	s := strings.TrimSpace(raw)
	if !strings.HasPrefix(s, "---") {
		return raw
	}
	rest := s[3:]
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return raw
	}
	return strings.TrimSpace(rest[idx+4:])
}
