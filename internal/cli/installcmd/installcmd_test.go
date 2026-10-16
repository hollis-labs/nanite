package installcmd

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Run calls os.Exit, so exit-path tests re-execute the test binary with a
// throwaway HOME/XDG tree and inspect the child's exit code and output.
const childEnv = "INSTALLCMD_TEST_CHILD"

func TestMain(m *testing.M) {
	if os.Getenv(childEnv) == "1" {
		Run("test-init", strings.Fields(os.Getenv("INSTALLCMD_TEST_ARGS")))
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runChild(t *testing.T, home, cwd, args string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Dir = cwd
	cmd.Env = []string{
		childEnv + "=1", "INSTALLCMD_TEST_ARGS=" + args,
		"HOME=" + home,
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
		"XDG_DATA_HOME=" + filepath.Join(home, ".local", "share"),
		"XDG_CACHE_HOME=" + filepath.Join(home, ".cache"),
	}
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return o.String(), e.String(), code
}

func TestRunExitPaths(t *testing.T) {
	tests := []struct {
		name     string
		args     string
		wantCode int
		wantErr  string
	}{
		{"adapter flags conflict", "--adapters claude --no-adapters", 1,
			"test-init: flag conflict: --adapters and --no-adapters are mutually exclusive"},
		{"print-diff unimplemented", "--project . --print-diff", 2,
			"test-init: --print-diff not yet implemented"},
		{"unknown flag", "--bogus", 2, "flag provided but not defined: -bogus"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home, cwd := t.TempDir(), t.TempDir()
			stdout, stderr, code := runChild(t, home, cwd, tc.args)
			if code != tc.wantCode {
				t.Errorf("exit code = %d, want %d (stderr %q)", code, tc.wantCode, stderr)
			}
			if !strings.Contains(stderr, tc.wantErr) {
				t.Errorf("stderr = %q, want substring %q", stderr, tc.wantErr)
			}
			if stdout != "" {
				t.Errorf("unexpected stdout %q", stdout)
			}
			if entries, _ := os.ReadDir(home); len(entries) != 0 {
				t.Errorf("failed run wrote into HOME: %v", entries)
			}
		})
	}
}

func TestPrintConflictFiles(t *testing.T) {
	capture := func(paths []string) string {
		r, w, _ := os.Pipe()
		old := os.Stdout
		os.Stdout = w
		printConflictFiles(paths)
		os.Stdout = old
		w.Close()
		var b bytes.Buffer
		b.ReadFrom(r)
		return b.String()
	}
	if got := capture(nil); got != "" {
		t.Errorf("no conflicts should print nothing, got %q", got)
	}
	want := "  comparison copies (merge manually; originals were preserved):\n    a/b.new\n    c.new\n"
	if got := capture([]string{"a/b.new", "c.new"}); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveProjectDir(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	wd, _ := os.Getwd() // symlink-resolved form, as Getwd reports it
	for _, tc := range []struct{ in, want string }{
		{"", wd},
		{".", wd},
		{"sub", filepath.Join(wd, "sub")},
		{"/abs/proj", "/abs/proj"},
	} {
		got, err := resolveProjectDir(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("resolveProjectDir(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
}

func TestDefaultGlobalHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got, want := defaultGlobalHome(), filepath.Join(home, ".nanite"); got != want {
		t.Errorf("defaultGlobalHome() = %q, want %q", got, want)
	}
}
