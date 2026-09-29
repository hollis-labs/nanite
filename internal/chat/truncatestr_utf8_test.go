package chat

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateStr_UTF8Boundary(t *testing.T) {
	got := TruncateStr(strings.Repeat("é", 20), 5)
	if !utf8.ValidString(got) || !strings.HasSuffix(got, "...") {
		t.Errorf("got %q", got)
	}
	if TruncateStr("ok", 5) != "ok" {
		t.Error("under-cap string changed")
	}
}
