package install

import (
	"errors"
	"os/exec"
	"testing"
)

// TestCheckBwrap_Missing verifies the Linux preflight returns
// ErrBwrapMissing when bwrap is not on PATH. Uses the lookPath hook so the
// test runs regardless of the host OS — the production Preflight() still
// gates the check on runtime.GOOS == "linux".
func TestCheckBwrap_Missing(t *testing.T) {
	orig := lookPath
	defer func() { lookPath = orig }()
	lookPath = func(string) (string, error) {
		return "", exec.ErrNotFound
	}

	err := checkBwrap()
	if err == nil {
		t.Fatal("checkBwrap returned nil, want ErrBwrapMissing")
	}
	if !errors.Is(err, ErrBwrapMissing) {
		t.Fatalf("err = %v, want ErrBwrapMissing", err)
	}
}

// TestCheckBwrap_LookPathPermissionError verifies that non-ErrNotFound
// errors from lookPath (e.g. an EACCES on a PATH directory) are wrapped
// and returned rather than being masked as ErrBwrapMissing. Masking such
// errors misleads operators into running install commands that cannot
// fix the underlying permission problem.
func TestCheckBwrap_LookPathPermissionError(t *testing.T) {
	orig := lookPath
	defer func() { lookPath = orig }()
	permErr := errors.New("permission denied")
	lookPath = func(string) (string, error) {
		return "", permErr
	}

	err := checkBwrap()
	if err == nil {
		t.Fatal("checkBwrap returned nil, want wrapped permission error")
	}
	if errors.Is(err, ErrBwrapMissing) {
		t.Fatalf("err = %v, want non-ErrBwrapMissing wrapped permission error", err)
	}
	if !errors.Is(err, permErr) {
		t.Fatalf("err = %v, should wrap underlying lookPath error", err)
	}
}

// TestCheckBwrap_Present verifies the preflight succeeds when bwrap
// resolves on PATH.
func TestCheckBwrap_Present(t *testing.T) {
	orig := lookPath
	defer func() { lookPath = orig }()
	lookPath = func(string) (string, error) {
		return "/usr/bin/bwrap", nil
	}

	if err := checkBwrap(); err != nil {
		t.Fatalf("checkBwrap: unexpected error: %v", err)
	}
}

// TestErrBwrapMissing_Message verifies the error message includes distro
// install hints so beta users can fix it without a round trip to docs.
func TestErrBwrapMissing_Message(t *testing.T) {
	msg := ErrBwrapMissing.Error()
	for _, want := range []string{"bwrap", "bubblewrap", "apt install", "dnf install", "pacman"} {
		if !contains(msg, want) {
			t.Errorf("ErrBwrapMissing missing %q: %s", want, msg)
		}
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	n, m := len(s), len(sub)
	if m == 0 {
		return 0
	}
	for i := 0; i+m <= n; i++ {
		if s[i:i+m] == sub {
			return i
		}
	}
	return -1
}
