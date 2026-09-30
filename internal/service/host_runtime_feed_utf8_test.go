package service

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBoundedRuntimeString_UTF8Boundary(t *testing.T) {
	s := strings.Repeat("😀", 50)
	for _, n := range []int{1, 3, 5, 7, 41} {
		got := boundedRuntimeString(s, n)
		if !utf8.ValidString(got) {
			t.Errorf("max %d: invalid UTF-8 %q", n, got)
		}
	}
}
