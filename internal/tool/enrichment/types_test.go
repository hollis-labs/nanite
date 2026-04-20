package enrichment

import (
	"reflect"
	"testing"
)

func TestHints_MarshalRoundTrip(t *testing.T) {
	h := Hints{
		Preconditions: []string{"call list/search first"},
		AntiPatterns:  []string{"IDs are ULIDs, not file paths"},
		ChainsWith:    []string{"dev_glob before dev_read"},
		OutputShape:   "Array of {id, name, status}",
	}
	s, err := MarshalHints(h)
	if err != nil {
		t.Fatalf("MarshalHints: %v", err)
	}
	got, err := UnmarshalHints(s)
	if err != nil {
		t.Fatalf("UnmarshalHints: %v", err)
	}
	if !reflect.DeepEqual(got, h) {
		t.Errorf("round-trip mismatch:\n got: %#v\nwant: %#v", got, h)
	}
}

func TestHints_UnmarshalEmpty(t *testing.T) {
	got, err := UnmarshalHints("{}")
	if err != nil {
		t.Fatalf("unmarshal empty: %v", err)
	}
	if got.OutputShape != "" || len(got.Preconditions) != 0 {
		t.Errorf("expected zero-value Hints, got %#v", got)
	}
}

func TestHints_UnmarshalMalformed(t *testing.T) {
	_, err := UnmarshalHints("not json")
	if err == nil {
		t.Error("expected error on malformed JSON")
	}
}

func TestHints_IsEmpty(t *testing.T) {
	cases := []struct {
		name string
		h    Hints
		want bool
	}{
		{"zero", Hints{}, true},
		{"only shape", Hints{OutputShape: "x"}, false},
		{"only precondition", Hints{Preconditions: []string{"a"}}, false},
		{"empty slices", Hints{Preconditions: []string{}, AntiPatterns: []string{}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.h.IsEmpty(); got != c.want {
				t.Errorf("IsEmpty = %v, want %v", got, c.want)
			}
		})
	}
}
