package store

import (
	"strings"
	"testing"
)

func TestTitleFrom(t *testing.T) {
	if got := titleFrom("  plan the\n\trelease  "); got != "plan the release" {
		t.Fatalf("got %q", got)
	}
	long := titleFrom(strings.Repeat("é", 100))
	if n := len([]rune(long)); n != 60 || !strings.HasSuffix(long, "…") {
		t.Fatalf("got %d runes: %q", n, long)
	}
	if titleFrom(" \n") != "" {
		t.Fatal("blank text should give no title")
	}
}
