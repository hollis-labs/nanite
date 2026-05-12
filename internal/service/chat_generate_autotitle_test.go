package service

import (
	"strings"
	"testing"
)

// TestSanitizeAutoTitle_TruncatesLongOutput is the load-bearing assertion for
// CW-20260512-0004: a 1.2 KB refusal blob must not survive as a session title.
func TestSanitizeAutoTitle_TruncatesLongOutput(t *testing.T) {
	long := strings.Repeat("a", 1200)
	got := sanitizeAutoTitle(long)
	if len(got) > autoTitleMaxLen {
		t.Fatalf("title exceeded cap: len=%d want<=%d", len(got), autoTitleMaxLen)
	}
	if got == "" {
		t.Fatalf("expected non-empty truncated title")
	}
}

func TestSanitizeAutoTitle_StripsNewlines(t *testing.T) {
	cases := []string{
		"Refactor\nthe broker",
		"Refactor\r\nthe broker",
		"Refactor\tthe\tbroker",
		"  Refactor\n\n  the broker  ",
	}
	for _, raw := range cases {
		got := sanitizeAutoTitle(raw)
		if strings.ContainsAny(got, "\n\r\t") {
			t.Errorf("sanitizeAutoTitle(%q) returned whitespace-bearing title %q", raw, got)
		}
		if got != "Refactor the broker" {
			t.Errorf("sanitizeAutoTitle(%q) = %q, want %q", raw, got, "Refactor the broker")
		}
	}
}

func TestSanitizeAutoTitle_TrimsWrappingPunctuation(t *testing.T) {
	cases := map[string]string{
		`"Refactor the broker"`: "Refactor the broker",
		`'Refactor the broker'`: "Refactor the broker",
		"Refactor the broker.":  "Refactor the broker",
		"Refactor the broker!":  "Refactor the broker",
		"`Refactor the broker`": "Refactor the broker",
	}
	for in, want := range cases {
		if got := sanitizeAutoTitle(in); got != want {
			t.Errorf("sanitizeAutoTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitizeAutoTitle_EmptyInputReturnsEmpty(t *testing.T) {
	for _, in := range []string{"", "   ", "\n\n\t"} {
		if got := sanitizeAutoTitle(in); got != "" {
			t.Errorf("sanitizeAutoTitle(%q) = %q, want empty", in, got)
		}
	}
}

func TestSanitizeAutoTitle_TruncationRespectsUTF8(t *testing.T) {
	// Build a string that puts a multi-byte rune straddling the cap.
	// "é" is 2 bytes; pack the cap so the boundary falls inside it.
	prefix := strings.Repeat("a", autoTitleMaxLen-1)
	in := prefix + "é" + "more"
	got := sanitizeAutoTitle(in)
	if len(got) > autoTitleMaxLen {
		t.Fatalf("truncation exceeded cap: len=%d", len(got))
	}
	// Must be a valid UTF-8 string (no half-rune at the end).
	if !validUTF8(got) {
		t.Fatalf("truncation produced invalid UTF-8: %q", got)
	}
}

// TestSanitizeAutoTitle_HappyPath ensures normal short label responses are
// returned unchanged — no regression on the common case.
func TestSanitizeAutoTitle_HappyPath(t *testing.T) {
	cases := []string{
		"Refactor the broker",
		"Go module rename",
		"Debug session",
		"Title",
	}
	for _, in := range cases {
		if got := sanitizeAutoTitle(in); got != in {
			t.Errorf("sanitizeAutoTitle(%q) = %q, want unchanged", in, got)
		}
	}
}

func TestLooksLikeRefusal_DetectsC160Shape(t *testing.T) {
	// Verbatim shape of the c160 refusal that motivated this ticket.
	refusal := "I appreciate the detailed context, but I need to be straightforward about what I can and cannot do: this request asks for something I'm not able to provide."
	if !looksLikeRefusal(refusal) {
		t.Fatalf("expected c160-shaped refusal to be detected")
	}
}

func TestLooksLikeRefusal_DetectsCommonRefusalPhrases(t *testing.T) {
	cases := []string{
		"I cannot help with that request",
		"I can't generate a title for this",
		"I don't have access to that information",
		"I do not have access to the conversation",
		"I'm sorry, but I cannot",
		"I am sorry, I won't",
		"I need to be clear about my limitations here",
	}
	for _, in := range cases {
		if !looksLikeRefusal(in) {
			t.Errorf("looksLikeRefusal(%q) = false, want true", in)
		}
	}
}

func TestLooksLikeRefusal_LongOutputAlwaysRefusal(t *testing.T) {
	// Any 120+ char response from a title model is wrong-shaped — label
	// outputs are short by definition. This catches refusals that don't
	// start with "I".
	long := strings.Repeat("word ", 40)
	if !looksLikeRefusal(long) {
		t.Fatalf("expected long output to be flagged as refusal-shaped")
	}
}

func TestLooksLikeRefusal_NormalTitlesNotFlagged(t *testing.T) {
	cases := []string{
		"Refactor the broker",
		"Go module rename",
		"Debug session",
		"Investigating chat regression",
		// Starts with "I" but no refusal marker — must not flag.
		"Idea capture flow",
		"Image upload pipeline",
		"",
	}
	for _, in := range cases {
		if looksLikeRefusal(in) {
			t.Errorf("looksLikeRefusal(%q) = true, want false", in)
		}
	}
}

func TestFallbackTitleFromUser_Truncates(t *testing.T) {
	long := strings.Repeat("user content ", 20)
	got := fallbackTitleFromUser(long)
	if got == "" {
		t.Fatalf("expected non-empty fallback")
	}
	if len(got) > 40 {
		t.Fatalf("fallback exceeded 40 chars: len=%d", len(got))
	}
}

func TestFallbackTitleFromUser_StripsWhitespace(t *testing.T) {
	got := fallbackTitleFromUser("  hello\n\nworld  ")
	if got != "hello world" {
		t.Errorf("fallbackTitleFromUser whitespace handling: got %q, want %q", got, "hello world")
	}
}

func TestFallbackTitleFromUser_EmptyReturnsEmpty(t *testing.T) {
	for _, in := range []string{"", "   ", "\n\n\t"} {
		if got := fallbackTitleFromUser(in); got != "" {
			t.Errorf("fallbackTitleFromUser(%q) = %q, want empty", in, got)
		}
	}
}

// TestAutoTitleLenInvariant exercises the full sanitize pipeline against a
// fuzzy mix of inputs and asserts the len(title) <= autoTitleMaxLen invariant
// from the ticket acceptance criteria.
func TestAutoTitleLenInvariant(t *testing.T) {
	inputs := []string{
		"",
		"short",
		"a normal looking title",
		strings.Repeat("x", 60),
		strings.Repeat("x", 61),
		strings.Repeat("x", 600),
		"I appreciate the detailed context, but I need to be straightforward",
		"\n\n\n  spaced out  \n\n",
		"\"quoted with newlines\nin the middle\"",
	}
	for _, in := range inputs {
		got := sanitizeAutoTitle(in)
		if len(got) > autoTitleMaxLen {
			t.Errorf("invariant violated for input %q: len=%d", in, len(got))
		}
	}
}

// TestPickAutoTitle_BothRefusalsFallsBackToUserContent is the load-bearing
// regression test for the Copilot review fix (PR #136). Previously, when
// both the first response and the retry were refusal-shaped, the sanitized
// first response (a truncated refusal snippet) leaked through because the
// `if title == ""` fallback gate never fired. With pickAutoTitle, both
// refusals get rejected and the deterministic user-content fallback wins.
func TestPickAutoTitle_BothRefusalsFallsBackToUserContent(t *testing.T) {
	rawFirst := "I appreciate the context, but I cannot generate a title for this request."
	rawRetry := "I'm sorry, I can't help with that."
	user := "Refactor the broker dispatch path"

	got := pickAutoTitle(rawFirst, rawRetry, user)
	want := fallbackTitleFromUser(user)
	if got != want {
		t.Fatalf("pickAutoTitle both-refusal: got %q, want %q (the user-content fallback)", got, want)
	}
	// Belt-and-suspenders: the result must not contain the canonical refusal
	// markers from either raw response.
	for _, marker := range []string{"appreciate", "cannot", "sorry", "can't"} {
		if strings.Contains(strings.ToLower(got), marker) {
			t.Errorf("pickAutoTitle leaked refusal marker %q into title %q", marker, got)
		}
	}
}

func TestPickAutoTitle_RetrySucceedsAfterFirstRefusal(t *testing.T) {
	got := pickAutoTitle(
		"I cannot help with that",
		"Refactor the broker",
		"the user message",
	)
	if got != "Refactor the broker" {
		t.Errorf("expected retry label to win, got %q", got)
	}
}

func TestPickAutoTitle_FirstResponseUsable(t *testing.T) {
	// retryRaw == "" mirrors the "no retry attempted" path.
	got := pickAutoTitle("Refactor the broker", "", "user msg")
	if got != "Refactor the broker" {
		t.Errorf("expected first-response label to win, got %q", got)
	}
}

func TestPickAutoTitle_BothEmptyFallsBackToUserContent(t *testing.T) {
	got := pickAutoTitle("", "", "the user wrote something")
	if got != fallbackTitleFromUser("the user wrote something") {
		t.Errorf("expected user-content fallback for both-empty, got %q", got)
	}
}

// validUTF8 is a tiny helper so we don't add a unicode/utf8 import just for the
// truncation test.
func validUTF8(s string) bool {
	for i := 0; i < len(s); {
		b := s[i]
		switch {
		case b < 0x80:
			i++
		case b < 0xC0:
			return false
		case b < 0xE0:
			if i+1 >= len(s) || s[i+1]&0xC0 != 0x80 {
				return false
			}
			i += 2
		case b < 0xF0:
			if i+2 >= len(s) || s[i+1]&0xC0 != 0x80 || s[i+2]&0xC0 != 0x80 {
				return false
			}
			i += 3
		case b < 0xF8:
			if i+3 >= len(s) || s[i+1]&0xC0 != 0x80 || s[i+2]&0xC0 != 0x80 || s[i+3]&0xC0 != 0x80 {
				return false
			}
			i += 4
		default:
			return false
		}
	}
	return true
}
