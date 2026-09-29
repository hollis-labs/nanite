package truncate

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestUTF8Cut(t *testing.T) {
	s := "aé世😀z" // 1 + 2 + 3 + 4 + 1 bytes
	for n := -1; n <= len(s)+2; n++ {
		got := UTF8Cut(s, n)
		if got > n && n >= 0 {
			t.Errorf("n=%d: cut %d exceeds the cap", n, got)
		}
		if !utf8.ValidString(s[:got]) {
			t.Errorf("n=%d: cut %d splits a rune", n, got)
		}
		// Largest such cut: extending by one byte would exceed n or split.
		if got < len(s) && got < n && utf8.ValidString(s[:got+1]) {
			t.Errorf("n=%d: cut %d is not the largest boundary", n, got)
		}
	}
	for n, want := range map[int]int{0: 0, 1: 1, 2: 1, 3: 3, 4: 3, 5: 3, 6: 6, 9: 6, 10: 10, 99: 11} {
		if got := UTF8Cut(s, n); got != want {
			t.Errorf("UTF8Cut(%d) = %d, want %d", n, got, want)
		}
	}
}

func TestUTF8Cut_BytesAndHead(t *testing.T) {
	b := []byte("日本語")
	if got := UTF8Cut(b, 4); got != 3 {
		t.Errorf("bytes cut = %d, want 3", got)
	}
	if got := UTF8Head("日本語", 7); got != "日本" {
		t.Errorf("UTF8Head = %q", got)
	}
	if got := UTF8Head("abc", 10); got != "abc" {
		t.Errorf("under cap changed: %q", got)
	}
	if got := UTF8Head(strings.Repeat("é", 1000), 501); !utf8.ValidString(got) || len(got) != 500 {
		t.Errorf("len %d valid=%v", len(got), utf8.ValidString(got))
	}
	// Invalid input never loops or panics; the cut lands on some start byte.
	_ = UTF8Cut("\x80\x80\x80\x80", 2)
}
