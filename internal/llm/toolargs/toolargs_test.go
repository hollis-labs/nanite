package toolargs

import "testing"

func TestParseObject_MalformedToolArgumentsFallbackRaw(t *testing.T) {
	raw := `{"pattern":`
	got := ParseObject(raw)
	if got["_raw"] != raw {
		t.Fatalf("ParseObject(%q) = %#v, want _raw fallback", raw, got)
	}
}

func TestParseObject_ValidObject(t *testing.T) {
	got := ParseObject(`{"pattern":"*.go"}`)
	if got["pattern"] != "*.go" {
		t.Fatalf("ParseObject valid object = %#v, want pattern", got)
	}
}

func TestParseObject_EmptyObject(t *testing.T) {
	got := ParseObject("")
	if len(got) != 0 {
		t.Fatalf("ParseObject empty = %#v, want empty map", got)
	}
}
