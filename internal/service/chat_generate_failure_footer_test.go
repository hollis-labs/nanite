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
		{ID: "t1", Name: "nanite_show_card", Status: "error"},
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
	want := "_(harness note: 1 tool call this turn returned an error before success — `nanite_show_card`. See the tool_calls array for the full list.)_"
	if !strings.Contains(got, want) {
		t.Fatalf("footer text mismatch.\nwant substring: %s\nfull output:\n%s", want, got)
	}
	// Footer must be separated from prior text by a blank line.
	if !strings.Contains(got, "\n\n_(harness note:") {
		t.Fatalf("footer must be separated by blank line; got:\n%s", got)
	}
}

func TestMaybeAppendFailureFooter_MultipleErrors_UniqueToolNames(t *testing.T) {
	t.Setenv(failureFooterEnvVar, "")

	// nanite_show_card errors twice — should appear once in the name list,
	// but the count should reflect all error-status calls (here: 3).
	refs := []chat.ToolCallRef{
		{ID: "t1", Name: "nanite_show_card", Status: "error"},
		{ID: "t2", Name: "nanite_validate", Status: "error"},
		{ID: "t3", Name: "nanite_show_card", Status: "error"},
		{ID: "t4", Name: "nanite_show_card", Status: "success", HasEnvelope: true},
	}
	text := "All set."
	got := maybeAppendFailureFooter(text, refs)
	want := "_(harness note: 3 tool calls this turn returned errors before success — `nanite_show_card`, `nanite_validate`. See the tool_calls array for the full list.)_"
	if !strings.Contains(got, want) {
		t.Fatalf("multi-error footer mismatch.\nwant substring: %s\nfull output:\n%s", want, got)
	}
	// `nanite_show_card` should appear exactly once in the name list portion
	// (the de-dup contract). It will appear in the footer ONCE since the
	// surrounding text doesn't reference it.
	footerStart := strings.Index(got, "_(harness note:")
	if footerStart < 0 {
		t.Fatalf("footer not found in output:\n%s", got)
	}
	footer := got[footerStart:]
	occurrences := strings.Count(footer, "`nanite_show_card`")
	if occurrences != 1 {
		t.Fatalf("expected nanite_show_card to appear exactly once in footer, got %d.\nfooter: %s", occurrences, footer)
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
