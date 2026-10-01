package truncate

import "unicode/utf8"

// Truncation policy (CW-20260929-0012 #5).
//
// Any text that is shown to a model, a user or a client, or fed into a prompt,
// is cut on a UTF-8 rune boundary, never with a raw s[:n]: a raw cut can end
// mid-sequence, which a model reads as garbage and a JSON encoder rewrites to
// U+FFFD. Use UTF8Cut / UTF8Head for a hard byte cap. Where a line boundary is
// also wanted, use the boundary-preferring cuts in internal/tool
// (go-toolresult.Preview) or internal/truncate.OutputForModel.
//
// A raw byte cut remains acceptable only where the bytes are never rendered as
// text: equality-only comparisons (reflexes head1KB), log/diagnostic lines
// (mcp truncateForLog, recover trimForLog, the panic stack cap), and slicing a
// list of items rather than text. Those sites are listed in
// docs/architecture/truncation-policy.md.

// UTF8Cut returns the largest index <= n at which s can be cut without
// splitting a multi-byte rune. n >= len(s) returns len(s); n <= 0 returns 0.
func UTF8Cut[T ~string | ~[]byte](s T, n int) int {
	if n >= len(s) {
		return len(s)
	}
	if n <= 0 {
		return 0
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return n
}

// UTF8Head returns the longest prefix of s that is at most n bytes and ends on
// a rune boundary.
func UTF8Head(s string, n int) string {
	return s[:UTF8Cut(s, n)]
}
