package adapterclaude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TASKS/phase-0/16's TestDiscover_Noop lived here and asserted that a real
// .claude/agents/*.md file was NOT imported. CW-20260910-0012 deliberately
// reverses that for this adapter alone -- it is the one format shipped across
// the re-armed Import seam, so the mechanism is proven rather than asserted.
// The affirmative coverage lives in import_test.go. What phase-0/16 actually
// forbade is unchanged and still enforced elsewhere: nothing reads these
// files at boot. internal/agent/discovery.go has no adapter tier at all now.

func TestSyncProjectRoot_EmptyAgentsWritesPlaceholder(t *testing.T) {
	dir := t.TempDir()
	a := New().Adapter()
	if err := a.SyncProjectRoot(dir, nil); err != nil {
		t.Fatalf("SyncProjectRoot: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("CLAUDE.md should exist: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "<!-- nanite:start -->") {
		t.Errorf("missing start marker: %q", got)
	}
	if !strings.Contains(got, "<!-- nanite:end -->") {
		t.Errorf("missing end marker: %q", got)
	}
	if !strings.Contains(got, "No agents configured") {
		t.Errorf("missing placeholder text: %q", got)
	}
	if !strings.Contains(got, "NANITE.md") {
		t.Errorf("missing NANITE.md pointer: %q", got)
	}
}
