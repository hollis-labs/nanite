package mcp

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// capOutput is model-facing; a cap that lands inside a rune must not emit a
// broken sequence, and the marker must report the bytes actually shown.
func TestCapOutput_UTF8Boundary(t *testing.T) {
	s := strings.Repeat("世", 100) // 300 bytes
	for _, cap := range []int{1, 2, 4, 100, 299} {
		got := capOutput(s, cap)
		body := got[:strings.Index(got, "\n[truncated")]
		if !utf8.ValidString(body) || len(body) > cap {
			t.Errorf("cap %d: body len %d valid=%v", cap, len(body), utf8.ValidString(body))
		}
		if want := "bytes shown]"; !strings.Contains(got, want) {
			t.Errorf("cap %d: marker missing: %q", cap, got)
		}
	}
	if capOutput("short", 100) != "short" {
		t.Error("under-cap output changed")
	}
}
