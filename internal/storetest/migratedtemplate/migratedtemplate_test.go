package migratedtemplate

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestMain(m *testing.M) {
	code := m.Run()
	Cleanup()
	os.Exit(code)
}

func TestCopyBuildsOnceAndGivesEachCallerItsOwnFile(t *testing.T) {
	// -count=N reruns the test in one process; start from an unbuilt template.
	once, path, buildErr = sync.Once{}, "", nil
	builds := 0
	build := func(path string) error {
		builds++
		return os.WriteFile(path, []byte("migrated"), 0o600)
	}
	a := filepath.Join(t.TempDir(), "a.db")
	b := filepath.Join(t.TempDir(), "b.db")
	Copy(t, a, build)
	Copy(t, b, build)
	if builds != 1 {
		t.Fatalf("template built %d times, want once per process", builds)
	}
	for _, p := range []string{a, b} {
		got, err := readFile(p)
		if err != nil || string(got) != "migrated" {
			t.Fatalf("copy %s = %q, %v", p, got, err)
		}
	}
	// Writing to one copy must not touch the other or the template.
	if err := os.WriteFile(a, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ := readFile(b); string(got) != "migrated" {
		t.Fatalf("copies are not independent: b = %q", got)
	}
	c := filepath.Join(t.TempDir(), "c.db")
	Copy(t, c, build)
	if got, _ := readFile(c); string(got) != "migrated" {
		t.Fatalf("template was modified through a copy: %q", got)
	}
}

func readFile(p string) ([]byte, error) {
	return os.ReadFile(p) // #nosec G304 -- p is a test-owned temp path
}
