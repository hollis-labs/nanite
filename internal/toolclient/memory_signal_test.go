package toolclient

import (
	"context"
	"testing"
)

func TestRecordToolPattern_NoOpWhenSequenceEmpty(t *testing.T) {
	r := &memoryRecaller{} // svc is nil, sequence is empty
	if err := r.RecordToolPattern(context.Background(), "s", "i", nil, "ok"); err != nil {
		t.Errorf("expected nil error from no-op record, got %v", err)
	}
}

func TestParseToolNames(t *testing.T) {
	cases := map[string][]string{
		"":               nil,
		"a":              {"a"},
		"a, b , c":       {"a", "b", "c"},
		"a, bad-name, c": {"a", "c"},
		"a, , c":         {"a", "c"},
	}
	for in, want := range cases {
		got := parseToolNames(in)
		if !sliceEqual(got, want) {
			t.Errorf("parseToolNames(%q) = %v, want %v", in, got, want)
		}
	}
}

func sliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
