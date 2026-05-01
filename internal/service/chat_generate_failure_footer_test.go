package service

import (
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/chat"
)

// CW-20260429-0026: tests for maybeAppendFailureFooter. The helper is a pure
// string transform over a ToolCallRef slice + env var, so we test it directly
// instead of spinning up a full chat_generate path.

func TestMaybeAppendFailureFooter_NoErrors_TextUnchanged(t *testing.T) {
	t.Setenv(failureFooterEnvVar, "")

	refs := []chat.ToolCallRef{
		{ID: "t1", Name: "nanite_show_card", Status: "success", HasEnvelope: true},
		{ID: "t2", Name: "nanite_validate", Status: "success"},
	}
	text := "Perfect! I've created a demo report card."
	got := maybeAppendFailureFooter(text, refs)
	if got != text {
		t.Fatalf("expected text unchanged when no errors, got:\n%s", got)
	}
}

func TestMaybeAppendFailureFooter_ErrorsButTextAcknowledges_NoFooter(t *testing.T) {
	t.Setenv(failureFooterEnvVar, "")

	refs := []chat.ToolCallRef{
		{ID: "t1", Name: "nanite_show_card", Status: "error"},
		{ID: "t2", Name: "nanite_show_card", Status: "success", HasEnvelope: true},
	}
	// Marker: "first attempt" (case-insensitive).
	text := "On the First Attempt the call rejected, so I retried with a corrected schema and it worked."
	got := maybeAppendFailureFooter(text, refs)
	if got != text {
		t.Fatalf("expected text unchanged when honesty marker present, got:\n%s", got)
	}

	// Sanity: each marker individually should suppress the footer.
	for _, marker := range honestyMarkers {
		t.Run("marker_"+marker, func(t *testing.T) {
			body := "All good. trigger=" + strings.ToUpper(marker)
			out := maybeAppendFailureFooter(body, refs)
			if out != body {
				t.Fatalf("marker %q failed to suppress footer, got:\n%s", marker, out)
			}
		})
	}
}

func TestMaybeAppendFailureFooter_ErrorsAndCleanText_FooterAppended(t *testing.T) {
	t.Setenv(failureFooterEnvVar, "")

	refs := []chat.ToolCallRef{
		{ID: "t1", Name: "nanite_show_card", Status: "error", ErrorReason: "schema validation: additionalProperties"},
		{ID: "t2", Name: "nanite_validate", Status: "success"},
		{ID: "t3", Name: "nanite_show_card", Status: "success", HasEnvelope: true},
	}
	text := "Perfect! I've created a demo report card showing Q1 2024 performance metrics."
	got := maybeAppendFailureFooter(text, refs)
	if got == text {
		t.Fatalf("expected footer to be appended, got unchanged text:\n%s", got)
	}
	if !strings.HasPrefix(got, text) {
		t.Fatalf("footer must be appended after original text; got:\n%s", got)
	}
	want := "_(harness note: 1 tool call this turn returned an error: `nanite_show_card` — \"schema validation: additionalProperties\". See the tool_calls array for the full list.)_"
	if !strings.Contains(got, want) {
		t.Fatalf("footer text mismatch.\nwant substring: %s\nfull output:\n%s", want, got)
	}
	// Footer must be separated from prior text by a blank line.
	if !strings.Contains(got, "\n\n_(harness note:") {
		t.Fatalf("footer must be separated by blank line; got:\n%s", got)
	}
}

// CW-20260501-0013: missing ErrorReason should still produce a sane footer
// (collapsed entry, no inlined reason). Protects older paths that don't
// populate ErrorReason and the inspector test fixture where Status alone is
// the load-bearing signal.
func TestMaybeAppendFailureFooter_NoReason_BareToolName(t *testing.T) {
	t.Setenv(failureFooterEnvVar, "")

	refs := []chat.ToolCallRef{
		{ID: "t1", Name: "nanite_show_card", Status: "error"},
		{ID: "t2", Name: "nanite_show_card", Status: "success", HasEnvelope: true},
	}
	got := maybeAppendFailureFooter("Done.", refs)
	want := "_(harness note: 1 tool call this turn returned an error: `nanite_show_card`. See the tool_calls array for the full list.)_"
	if !strings.Contains(got, want) {
		t.Fatalf("bare-name footer mismatch.\nwant substring: %s\nfull output:\n%s", want, got)
	}
}

func TestMaybeAppendFailureFooter_MultipleErrors_UniqueToolNames(t *testing.T) {
	t.Setenv(failureFooterEnvVar, "")

	// nanite_show_card errors twice — should appear once in the name list,
	// but the count should reflect all error-status calls (here: 3).
	// First-encounter ErrorReason wins for the de-duped tool.
	refs := []chat.ToolCallRef{
		{ID: "t1", Name: "nanite_show_card", Status: "error", ErrorReason: "schema validation"},
		{ID: "t2", Name: "nanite_validate", Status: "error", ErrorReason: "query is required"},
		{ID: "t3", Name: "nanite_show_card", Status: "error", ErrorReason: "different reason — should be ignored"},
		{ID: "t4", Name: "nanite_show_card", Status: "success", HasEnvelope: true},
	}
	text := "All set."
	got := maybeAppendFailureFooter(text, refs)
	want := "_(harness note: 3 tool calls this turn returned errors: `nanite_show_card` — \"schema validation\", `nanite_validate` — \"query is required\". See the tool_calls array for the full list.)_"
	if !strings.Contains(got, want) {
		t.Fatalf("multi-error footer mismatch.\nwant substring: %s\nfull output:\n%s", want, got)
	}
	// `nanite_show_card` should appear exactly once in the name list portion
	// (the de-dup contract).
	footerStart := strings.Index(got, "_(harness note:")
	if footerStart < 0 {
		t.Fatalf("footer not found in output:\n%s", got)
	}
	footer := got[footerStart:]
	occurrences := strings.Count(footer, "`nanite_show_card`")
	if occurrences != 1 {
		t.Fatalf("expected nanite_show_card to appear exactly once in footer, got %d.\nfooter: %s", occurrences, footer)
	}
	// Subsequent encounters of the same tool with a different reason must NOT
	// leak into the footer (first-encounter-wins contract).
	if strings.Contains(footer, "different reason") {
		t.Fatalf("second-encounter reason leaked into footer: %s", footer)
	}
}

func TestMaybeAppendFailureFooter_EnvOff_NoFooter(t *testing.T) {
	refs := []chat.ToolCallRef{
		{ID: "t1", Name: "nanite_show_card", Status: "error"},
		{ID: "t2", Name: "nanite_show_card", Status: "success"},
	}
	text := "Perfect! Done."

	for _, off := range []string{"0", "false", "FALSE", "off", "Off", "no", "NO"} {
		t.Run("off="+off, func(t *testing.T) {
			t.Setenv(failureFooterEnvVar, off)
			got := maybeAppendFailureFooter(text, refs)
			if got != text {
				t.Fatalf("expected text unchanged when env=%q, got:\n%s", off, got)
			}
		})
	}
}

func TestMaybeAppendFailureFooter_EnvOn_ExplicitTruthy(t *testing.T) {
	refs := []chat.ToolCallRef{
		{ID: "t1", Name: "nanite_show_card", Status: "error"},
		{ID: "t2", Name: "nanite_show_card", Status: "success"},
	}
	text := "Perfect! Done."

	for _, on := range []string{"1", "true", "yes", "on", ""} {
		t.Run("on="+on, func(t *testing.T) {
			t.Setenv(failureFooterEnvVar, on)
			got := maybeAppendFailureFooter(text, refs)
			if got == text {
				t.Fatalf("expected footer appended when env=%q, got unchanged:\n%s", on, got)
			}
			if !strings.Contains(got, "_(harness note:") {
				t.Fatalf("expected footer marker in output when env=%q, got:\n%s", on, got)
			}
		})
	}
}

func TestMaybeAppendFailureFooter_EmptyText_FooterStandalone(t *testing.T) {
	t.Setenv(failureFooterEnvVar, "")

	refs := []chat.ToolCallRef{
		{ID: "t1", Name: "nanite_show_card", Status: "error"},
	}
	got := maybeAppendFailureFooter("", refs)
	if !strings.HasPrefix(got, "_(harness note:") {
		t.Fatalf("footer should stand alone when text is empty; got:\n%s", got)
	}
	if strings.HasPrefix(got, "\n") {
		t.Fatalf("footer should not be prefixed with newline when text is empty; got:\n%q", got)
	}
}

// CW-20260501-0013: c121-shaped reproducer. The exact failure that produced
// the "I don't have access to a memory recall tool" hallucination — a single
// tool error from nanite_memory_recall with reason "memory service not
// configured" — must surface that reason verbatim in the next-turn footer.
func TestMaybeAppendFailureFooter_C121Reproducer_VerbatimReasonInlined(t *testing.T) {
	t.Setenv(failureFooterEnvVar, "")

	refs := []chat.ToolCallRef{
		{ID: "t1", Name: "nanite_memory_recall", Status: "error", ErrorReason: "memory service not configured"},
	}
	// Use a body that does NOT contain any honesty marker so the footer fires.
	got := maybeAppendFailureFooter("I'll get right on that.", refs)
	if !strings.Contains(got, `"memory service not configured"`) {
		t.Fatalf("c121 reproducer: verbatim reason missing from footer.\noutput:\n%s", got)
	}
	if !strings.Contains(got, "`nanite_memory_recall`") {
		t.Fatalf("c121 reproducer: tool name missing from footer.\noutput:\n%s", got)
	}
	// Sanity: the inlined slot should be `tool` — "reason".
	wantSubstr := "`nanite_memory_recall` — \"memory service not configured\""
	if !strings.Contains(got, wantSubstr) {
		t.Fatalf("c121 reproducer: expected substring %q in footer.\noutput:\n%s", wantSubstr, got)
	}
}

// CW-20260501-0013: per-error reason cap (200 chars). Reasons longer than
// perErrorReasonCap must be truncated with an ellipsis.
func TestMaybeAppendFailureFooter_PerErrorCap_TruncatesWithEllipsis(t *testing.T) {
	t.Setenv(failureFooterEnvVar, "")

	longReason := strings.Repeat("a", 500) // > perErrorReasonCap
	refs := []chat.ToolCallRef{
		{ID: "t1", Name: "nanite_long", Status: "error", ErrorReason: longReason},
	}
	got := maybeAppendFailureFooter("ok", refs)

	// The full long reason must NOT appear verbatim.
	if strings.Contains(got, longReason) {
		t.Fatalf("expected long reason to be truncated, but full string appears in footer:\n%s", got)
	}
	// An ellipsis must mark the truncation.
	if !strings.Contains(got, "…") {
		t.Fatalf("expected ellipsis after per-error truncation, got:\n%s", got)
	}
	// Inlined slot should still contain the start of the reason.
	if !strings.Contains(got, "`nanite_long` — \"aaa") {
		t.Fatalf("expected truncated reason to begin with tool-name + opening quote + reason prefix; got:\n%s", got)
	}
	// Total inlined-reason rune count for this entry must be <= perErrorReasonCap.
	footerStart := strings.Index(got, "`nanite_long` — \"")
	if footerStart < 0 {
		t.Fatalf("entry not found:\n%s", got)
	}
	closing := strings.Index(got[footerStart:], `".`)
	if closing < 0 {
		t.Fatalf("closing quote not found:\n%s", got)
	}
	// runes between the opening quote and closing quote
	openQuote := strings.Index(got[footerStart:], `"`)
	reasonSlice := got[footerStart+openQuote+1 : footerStart+closing]
	if rc := len([]rune(reasonSlice)); rc > perErrorReasonCap {
		t.Fatalf("inlined reason exceeded per-error cap: %d > %d (slice=%q)", rc, perErrorReasonCap, reasonSlice)
	}
}

// CW-20260501-0013: total-footer cap (~600 chars). Many errors with long
// reasons must keep the footer bounded; surplus collapses to "+N more".
func TestMaybeAppendFailureFooter_TotalCap_CollapsesSurplus(t *testing.T) {
	t.Setenv(failureFooterEnvVar, "")

	// Eight unique tools, each with a 150-char reason. After 3-4 entries plus
	// prefix/suffix we are over totalFooterCap; remaining must collapse.
	bigReason := strings.Repeat("x", 150)
	var refs []chat.ToolCallRef
	for i := 0; i < 8; i++ {
		refs = append(refs, chat.ToolCallRef{
			ID:          "t" + string(rune('0'+i)),
			Name:        "tool_" + string(rune('a'+i)),
			Status:      "error",
			ErrorReason: bigReason,
		})
	}
	got := maybeAppendFailureFooter("done", refs)

	footerStart := strings.Index(got, "_(harness note:")
	if footerStart < 0 {
		t.Fatalf("footer not found:\n%s", got)
	}
	footer := got[footerStart:]
	if len([]rune(footer)) > totalFooterCap+50 {
		t.Fatalf("footer exceeded total cap (with slack): %d > %d\nfooter: %s",
			len([]rune(footer)), totalFooterCap+50, footer)
	}
	// Surplus must be summarized, not silently dropped.
	if !strings.Contains(footer, "+") || !strings.Contains(footer, "more") {
		t.Fatalf("expected '+N more' summary tail; got:\n%s", footer)
	}
	// The visible count of inlined entries must be < total entries (some collapsed).
	listed := strings.Count(footer, "` — \"")
	if listed >= 8 {
		t.Fatalf("expected total cap to collapse some entries; listed=%d (out of 8)", listed)
	}
	if listed < 1 {
		t.Fatalf("expected at least one entry to be inlined; listed=%d", listed)
	}
}

// CW-20260501-0013: multiline / whitespace-laden reasons must collapse to a
// single line so the footer doesn't blow up vertically.
func TestMaybeAppendFailureFooter_MultilineReason_Collapsed(t *testing.T) {
	t.Setenv(failureFooterEnvVar, "")

	refs := []chat.ToolCallRef{
		{
			ID: "t1", Name: "nanite_show_card", Status: "error",
			ErrorReason: "schema validation:\n  field foo:\n    required\n",
		},
	}
	got := maybeAppendFailureFooter("ok", refs)
	if strings.Contains(got, "\n  field") || strings.Contains(got, "\n    required") {
		t.Fatalf("multiline reason should be collapsed to single line; got:\n%s", got)
	}
	want := `"schema validation: field foo: required"`
	if !strings.Contains(got, want) {
		t.Fatalf("expected collapsed reason %q in footer; got:\n%s", want, got)
	}
}
