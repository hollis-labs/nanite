package store

import (
	"testing"
)

func TestSessionOverrides_Roundtrip(t *testing.T) {
	s := newTestStore(t)

	want := `{"model":"claude-opus-4","temperature":0.5}`
	if err := s.SetSessionOverrides("session-abc", want); err != nil {
		t.Fatalf("SetSessionOverrides: %v", err)
	}

	got, err := s.GetSessionOverrides("session-abc")
	if err != nil {
		t.Fatalf("GetSessionOverrides: %v", err)
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSessionOverrides_DefaultsToEmptyObject(t *testing.T) {
	s := newTestStore(t)

	got, err := s.GetSessionOverrides("nonexistent-session")
	if err != nil {
		t.Fatalf("GetSessionOverrides: %v", err)
	}
	if got != "{}" {
		t.Errorf("got %q, want \"{}\"", got)
	}
}

func TestSessionOverrides_Upsert(t *testing.T) {
	s := newTestStore(t)

	first := `{"model":"claude-haiku-3"}`
	if err := s.SetSessionOverrides("session-xyz", first); err != nil {
		t.Fatalf("SetSessionOverrides first: %v", err)
	}

	second := `{"model":"claude-opus-4","max_tokens":4096}`
	if err := s.SetSessionOverrides("session-xyz", second); err != nil {
		t.Fatalf("SetSessionOverrides second: %v", err)
	}

	got, err := s.GetSessionOverrides("session-xyz")
	if err != nil {
		t.Fatalf("GetSessionOverrides: %v", err)
	}
	if got != second {
		t.Errorf("got %q, want %q", got, second)
	}
}
