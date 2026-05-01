package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExpandArchiveBase(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"no tilde", "/tmp/foo", "/tmp/foo"},
		{"tilde root", "~", home},
		{"tilde slash", "~/Projects-apps/.archived", filepath.Join(home, "Projects-apps/.archived")},
		// "~user" (other-user home) is not supported — returned as-is.
		{"tilde user unsupported", "~otheruser/foo", "~otheruser/foo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ExpandArchiveBase(tc.in)
			if err != nil {
				t.Fatalf("ExpandArchiveBase(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestResolveArchiveDir_EmptyBasename(t *testing.T) {
	ts := time.Date(2026, 4, 9, 14, 30, 22, 0, time.UTC)
	if _, err := ResolveArchiveDir(t.TempDir(), "", ts); err == nil {
		t.Error("expected error for empty basename")
	}
}

func TestResolveArchiveDir_NoCollision(t *testing.T) {
	base := t.TempDir()
	ts := time.Date(2026, 4, 9, 14, 30, 22, 0, time.UTC)
	got, err := ResolveArchiveDir(base, "hadron", ts)
	if err != nil {
		t.Fatalf("ResolveArchiveDir: %v", err)
	}
	want := filepath.Join(base, "hadron-2026-04-09")
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestResolveArchiveDir_Collision(t *testing.T) {
	base := t.TempDir()
	ts := time.Date(2026, 4, 9, 14, 30, 22, 0, time.UTC)
	if err := os.MkdirAll(filepath.Join(base, "hadron-2026-04-09"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveArchiveDir(base, "hadron", ts)
	if err != nil {
		t.Fatalf("ResolveArchiveDir: %v", err)
	}
	want := filepath.Join(base, "hadron-2026-04-09-143022")
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestResolveArchiveDir_DoubleCollision(t *testing.T) {
	base := t.TempDir()
	ts := time.Date(2026, 4, 9, 14, 30, 22, 0, time.UTC)
	os.MkdirAll(filepath.Join(base, "hadron-2026-04-09"), 0o755)
	os.MkdirAll(filepath.Join(base, "hadron-2026-04-09-143022"), 0o755)
	got, err := ResolveArchiveDir(base, "hadron", ts)
	if err != nil {
		t.Fatalf("ResolveArchiveDir: %v", err)
	}
	if !strings.HasPrefix(filepath.Base(got), "hadron-2026-04-09-143022-") {
		t.Errorf("expected sequence suffix, got %q", got)
	}
}
