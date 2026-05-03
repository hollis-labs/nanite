package service

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestTruncateForHandoff_UTF8Safe pins the A1/A3 fix for the at-compaction
// fallback. truncateForHandoff slices a byte budget to keep the rendered
// next_step_anchor inside the handoff payload cap. Without rune-boundary
// awareness, a multi-byte rune (emoji, CJK, Cyrillic) split mid-byte
// produces a string that is no longer valid UTF-8 — the agent then sees
// garbled state on the post-compaction read.
func TestTruncateForHandoff_UTF8Safe(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		maxChars int
	}{
		// "こんにちは" is 5 runes × 3 bytes = 15 bytes. Cutting at 4 falls
		// inside the second rune.
		{"japanese-mid-rune", strings.Repeat("こんにちは世界", 4), 10},
		// Cyrillic 2-byte runes — cut inside a rune.
		{"cyrillic-mid-rune", strings.Repeat("Привет, мир! ", 6), 17},
		// 4-byte emoji.
		{"emoji-mid-rune", strings.Repeat("👋🌍 hello ", 4), 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := truncateForHandoff(tc.input, tc.maxChars)
			if !utf8.ValidString(out) {
				t.Errorf("truncateForHandoff produced invalid UTF-8: %q", out)
			}
			// Truncation should still trim down (output strictly shorter
			// than input when input exceeded budget).
			if len(out) >= len(tc.input) {
				t.Errorf("expected truncation: input %d bytes, output %d bytes", len(tc.input), len(out))
			}
		})
	}
}

// TestTruncateForHandoff_ShortInputUntouched: inputs already within the
// budget pass through unchanged (no ellipsis, no rune walk).
func TestTruncateForHandoff_ShortInputUntouched(t *testing.T) {
	in := "hello world"
	out := truncateForHandoff(in, 100)
	if out != in {
		t.Errorf("short input should be returned verbatim; got %q want %q", out, in)
	}
}
