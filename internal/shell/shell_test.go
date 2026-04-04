package shell

import (
	"context"
	"testing"
	"time"
)

func TestParseCommand(t *testing.T) {
	tests := []struct {
		input   string
		wantCmd string
		wantOK  bool
	}{
		{"!ls", "ls", true},
		{"!git status", "git status", true},
		{"! echo hello", "echo hello", true},
		{"  !pwd  ", "pwd", true},
		{"!", "", false},
		{"!  ", "", false},
		{"hello", "", false},
		{"/slash", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		cmd, ok := ParseCommand(tt.input)
		if cmd != tt.wantCmd || ok != tt.wantOK {
			t.Errorf("ParseCommand(%q) = (%q, %v), want (%q, %v)", tt.input, cmd, ok, tt.wantCmd, tt.wantOK)
		}
	}
}

func TestValidMode(t *testing.T) {
	for _, m := range []string{"ask", "session", "yolo"} {
		if !ValidMode(m) {
			t.Errorf("ValidMode(%q) = false, want true", m)
		}
	}
	for _, m := range []string{"", "invalid", "YOLO", "Ask"} {
		if ValidMode(m) {
			t.Errorf("ValidMode(%q) = true, want false", m)
		}
	}
}

func TestDenylistCheck(t *testing.T) {
	dl := NewDenylist()

	// Blocked commands.
	blocked := []string{
		"rm -rf /",
		"rm -rf /*",
		"sudo rm -rf /",
		"mkfs.ext4 /dev/sda1",
		"dd if=/dev/zero of=/dev/sda",
		"shutdown -h now",
		":(){ :|:& };:",
	}
	for _, cmd := range blocked {
		if reason := dl.Check(cmd); reason == "" {
			t.Errorf("Denylist.Check(%q) = empty, want blocked", cmd)
		}
	}

	// Allowed commands.
	allowed := []string{
		"ls -la",
		"git status",
		"echo hello",
		"rm -rf ./build",
		"cat /etc/hosts",
		"go build ./...",
	}
	for _, cmd := range allowed {
		if reason := dl.Check(cmd); reason != "" {
			t.Errorf("Denylist.Check(%q) = %q, want empty (allowed)", cmd, reason)
		}
	}

	// Disabled denylist allows everything.
	dl.SetEnabled(false)
	if reason := dl.Check("rm -rf /"); reason != "" {
		t.Errorf("disabled denylist still blocked: %q", reason)
	}
}

func TestExec(t *testing.T) {
	ctx := context.Background()
	result := Exec(ctx, "echo hello", ExecOpts{})
	if result.ExitCode != 0 {
		t.Fatalf("echo hello: exit code %d, output: %s", result.ExitCode, result.Output)
	}
	if result.Output != "hello\n" && result.Output != "hello" {
		t.Errorf("echo hello: unexpected output %q", result.Output)
	}
	if result.DurationMs < 0 {
		t.Errorf("negative duration: %d", result.DurationMs)
	}
}

func TestExecNonZeroExit(t *testing.T) {
	ctx := context.Background()
	result := Exec(ctx, "exit 42", ExecOpts{})
	if result.ExitCode != 42 {
		t.Errorf("exit 42: got exit code %d, want 42", result.ExitCode)
	}
}

func TestExecTimeout(t *testing.T) {
	ctx := context.Background()
	result := Exec(ctx, "sleep 10", ExecOpts{Timeout: 100 * time.Millisecond})
	if result.ExitCode != 124 {
		t.Errorf("timeout: got exit code %d, want 124", result.ExitCode)
	}
}

func TestExecWorkDir(t *testing.T) {
	ctx := context.Background()
	result := Exec(ctx, "pwd", ExecOpts{WorkDir: "/tmp"})
	if result.ExitCode != 0 {
		t.Fatalf("pwd in /tmp: exit code %d", result.ExitCode)
	}
	// macOS may return /private/tmp.
	if result.Output != "/tmp\n" && result.Output != "/private/tmp\n" {
		t.Errorf("pwd in /tmp: unexpected output %q", result.Output)
	}
}
