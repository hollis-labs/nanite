package anthropic

import (
	"os"
	"strings"
	"testing"
)

func TestDumpRequestIsOffByDefaultAndOwnerOnly(t *testing.T) {
	t.Setenv(requestDumpEnv, "")
	if got := dumpRequest("m", []byte(`{}`)); got != "" {
		t.Fatalf("dumped with no directory configured: %q", got)
	}
	dir := t.TempDir() + "/dump"
	t.Setenv(requestDumpEnv, dir)
	path := dumpRequest("claude-x", []byte(`{"a":1}`))
	if path == "" || !strings.HasPrefix(path, dir) {
		t.Fatalf("path = %q", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}
	if b, _ := os.ReadFile(path); string(b) != `{"a":1}` {
		t.Fatalf("body = %q", b)
	}
}
