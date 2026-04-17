package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestCLIDeterministicFromAgentID_Shape verifies the computed id is
// stable within a process, matches the documented convention, and
// reflects the current hostname + pid + process-start-unix.
func TestCLIDeterministicFromAgentID_Shape(t *testing.T) {
	origHostFn := cliHostnameFn
	origStart := cliProcessStartUnix
	t.Cleanup(func() {
		cliHostnameFn = origHostFn
		cliProcessStartUnix = origStart
	})

	cliHostnameFn = func() (string, error) { return "build-host-01", nil }
	cliProcessStartUnix = 1700000000

	got := cliDeterministicFromAgentID()
	want := fmt.Sprintf("cli-build-host-01-%d-1700000000", os.Getpid())
	if got != want {
		t.Fatalf("cliDeterministicFromAgentID()=%q, want %q", got, want)
	}
}

// TestCLIDeterministicFromAgentID_StableAcrossCalls confirms the id is
// the same when called repeatedly within one process — two sends from
// the same CLI session land in one inbox.
func TestCLIDeterministicFromAgentID_StableAcrossCalls(t *testing.T) {
	origHostFn := cliHostnameFn
	origStart := cliProcessStartUnix
	t.Cleanup(func() {
		cliHostnameFn = origHostFn
		cliProcessStartUnix = origStart
	})

	cliHostnameFn = func() (string, error) { return "myhost.local", nil }
	cliProcessStartUnix = 1700000001

	first := cliDeterministicFromAgentID()
	second := cliDeterministicFromAgentID()
	if first != second {
		t.Fatalf("ids diverged: %q vs %q", first, second)
	}
}

// TestCLIDeterministicFromAgentID_HostnameFallback covers the
// os.Hostname error path — a hostname lookup failure must not abort
// the CLI; instead the id falls back to the literal "unknown" token.
func TestCLIDeterministicFromAgentID_HostnameFallback(t *testing.T) {
	origHostFn := cliHostnameFn
	origStart := cliProcessStartUnix
	t.Cleanup(func() {
		cliHostnameFn = origHostFn
		cliProcessStartUnix = origStart
	})

	cliHostnameFn = func() (string, error) { return "", errors.New("no hostname") }
	cliProcessStartUnix = 42

	got := cliDeterministicFromAgentID()
	want := fmt.Sprintf("cli-unknown-%d-42", os.Getpid())
	if got != want {
		t.Fatalf("fallback id=%q, want %q", got, want)
	}
}

// TestCLIDeterministicFromAgentID_HostnameSanitization checks that
// non-[a-z0-9-] characters are either mapped to dashes (dot, underscore)
// or stripped so the final id survives slug conventions in the
// agent_profiles table. Mixed-case input is lower-cased.
func TestCLIDeterministicFromAgentID_HostnameSanitization(t *testing.T) {
	origHostFn := cliHostnameFn
	origStart := cliProcessStartUnix
	t.Cleanup(func() {
		cliHostnameFn = origHostFn
		cliProcessStartUnix = origStart
	})

	cliHostnameFn = func() (string, error) { return "Build_Host.Corp.Example", nil }
	cliProcessStartUnix = 7

	got := cliDeterministicFromAgentID()
	if !strings.HasPrefix(got, "cli-build-host-corp-example-") {
		t.Fatalf("sanitized prefix missing: got %q", got)
	}
}

// TestCLIDeterministicFromAgentID_HostnameAllStrippedFallback covers
// the degenerate case where sanitization strips every character (e.g.
// a pathological hostname of pure punctuation). The function falls
// back to "unknown" rather than producing "cli--<pid>-<unix>".
func TestCLIDeterministicFromAgentID_HostnameAllStrippedFallback(t *testing.T) {
	origHostFn := cliHostnameFn
	origStart := cliProcessStartUnix
	t.Cleanup(func() {
		cliHostnameFn = origHostFn
		cliProcessStartUnix = origStart
	})

	cliHostnameFn = func() (string, error) { return "!!!???", nil }
	cliProcessStartUnix = 1

	got := cliDeterministicFromAgentID()
	want := fmt.Sprintf("cli-unknown-%d-1", os.Getpid())
	if got != want {
		t.Fatalf("all-stripped fallback id=%q, want %q", got, want)
	}
}
