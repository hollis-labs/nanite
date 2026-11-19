package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateSQLiteModes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".nanite")
	s, err := Open(t.Context(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for name, want := range map[string]os.FileMode{".": 0700, "nanite.db": 0600, "nanite.db-wal": 0600, "nanite.db-shm": 0600} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s mode %04o, want %04o", name, got, want)
		}
	}
}
