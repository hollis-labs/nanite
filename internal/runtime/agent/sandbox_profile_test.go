package agent

import (
	"testing"

	"github.com/hollis-labs/go-sandbox/sandbox"
)

// TestSandboxProfile_LongLived enforces AllowLoopback=true and that
// workdir, workspace, and boot dir land in the FS.Write allowlist.
func TestSandboxProfile_LongLived(t *testing.T) {
	got := buildSandboxProfile(sandbox.Profile{ID: "base"}, Options{
		Mode:    ModeLongLived,
		Workdir: "/proj",
	}, "/ws/sess1", "/tmp/boot-1")

	if !got.AllowLoopback {
		t.Errorf("AllowLoopback = false, want true (MCP loopback subprocess needs it)")
	}
	if got.ID != "base" {
		t.Errorf("base profile ID = %q, want preserved", got.ID)
	}
	wantWrites := map[string]bool{"/proj": false, "/ws/sess1": false, "/tmp/boot-1": false}
	for _, w := range got.FS.Write {
		if _, ok := wantWrites[w]; ok {
			wantWrites[w] = true
		}
	}
	for path, present := range wantWrites {
		if !present {
			t.Errorf("FS.Write missing %q (got %v)", path, got.FS.Write)
		}
	}
}

// TestSandboxProfile_BackgroundFullGate confirms ModeBackground without
// WideOpen still applies enforcement.
func TestSandboxProfile_BackgroundFullGate(t *testing.T) {
	got := buildSandboxProfile(sandbox.Profile{ID: "base"}, Options{
		Mode:     ModeBackground,
		Workdir:  "/proj",
		WideOpen: false,
	}, "/ws/sess2", "/tmp/boot-2")

	if !got.AllowLoopback {
		t.Errorf("ModeBackground (gated) should set AllowLoopback")
	}
	if got.ID != "base" {
		t.Errorf("base profile not preserved: %q", got.ID)
	}
}

// TestSandboxProfile_BackgroundWideOpen returns the zero-value Profile
// (no enforcement) — the legacy privileged primitive.
func TestSandboxProfile_BackgroundWideOpen(t *testing.T) {
	got := buildSandboxProfile(sandbox.Profile{ID: "base"}, Options{
		Mode:     ModeBackground,
		Workdir:  "/proj",
		WideOpen: true,
	}, "/ws/sess3", "/tmp/boot-3")

	if got.ID != "" || got.AllowLoopback || len(got.FS.Write) != 0 || len(got.FS.Read) != 0 || len(got.FS.Deny) != 0 {
		t.Errorf("WideOpen profile should be zero-value, got %+v", got)
	}
}

// TestSandboxProfile_DedupesPaths confirms a base profile's existing
// FS.Write entries don't get re-added.
func TestSandboxProfile_DedupesPaths(t *testing.T) {
	base := sandbox.Profile{
		FS: sandbox.FSSpec{Write: []string{"/proj"}},
	}
	got := buildSandboxProfile(base, Options{Workdir: "/proj"}, "/ws", "/tmp/boot")

	count := 0
	for _, w := range got.FS.Write {
		if w == "/proj" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("/proj appeared %d times in FS.Write, want 1 (got %v)", count, got.FS.Write)
	}
}

// TestSandboxProfile_SkipsEmptyPaths drops zero-value path inputs.
func TestSandboxProfile_SkipsEmptyPaths(t *testing.T) {
	got := buildSandboxProfile(sandbox.Profile{}, Options{
		Mode:    ModeLongLived,
		Workdir: "", // empty
	}, "", "/tmp/boot")

	for _, w := range got.FS.Write {
		if w == "" {
			t.Errorf("empty path leaked into FS.Write: %v", got.FS.Write)
		}
	}
}
