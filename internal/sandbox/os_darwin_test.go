//go:build darwin

package sandbox

import (
	"strings"
	"testing"
)

// TestSeatbeltProfile_RejectsInjection verifies that a sandboxDir containing
// bytes with meaning to the TinyScheme profile parser (quotes, parens,
// semicolons, backslashes, control chars) is rejected before a profile is
// emitted. Before the fix, `fmt.Fprintf(&b, `(subpath "%s")`, absDir)` let
// a crafted dir inject arbitrary seatbelt rules — "foo\") (allow
// file-write*)" turned the sandbox into a no-op.
func TestSeatbeltProfile_RejectsInjection(t *testing.T) {
	hostile := []string{
		`/tmp/foo") (allow file-write*) (allow network*) ;"`,
		`/tmp/a"b`,
		`/tmp/a\b`,
		`/tmp/a(b`,
		`/tmp/a)b`,
		`/tmp/a;b`,
		"/tmp/a\x00b",
		"/tmp/a\x1fb",
	}
	for _, dir := range hostile {
		t.Run(dir, func(t *testing.T) {
			_, err := seatbeltProfile(dir, nil)
			if err == nil {
				t.Fatalf("seatbeltProfile(%q) accepted hostile path", dir)
			}
		})
	}
}

// TestSeatbeltProfile_AcceptsSafe confirms well-formed paths emit a profile
// that contains the expected deny-write rule.
func TestSeatbeltProfile_AcceptsSafe(t *testing.T) {
	profile, err := seatbeltProfile("/Users/test/.nanite/sandboxes/s-1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(profile, `(subpath "/Users/test/.nanite/sandboxes/s-1")`) {
		t.Errorf("profile missing expected subpath rule:\n%s", profile)
	}
	if strings.Contains(profile, "(allow file-write*)") {
		t.Errorf("profile unexpectedly contains injected allow rule:\n%s", profile)
	}
}

// TestSeatbeltProfile_ValidatesNetworkAllow ensures entries in networkAllow
// are also validated, so a future profile change that interpolates them
// cannot be retrofitted into an injection vector.
func TestSeatbeltProfile_ValidatesNetworkAllow(t *testing.T) {
	_, err := seatbeltProfile("/tmp/ok", []string{`evil.com") (allow network*`})
	if err == nil {
		t.Fatal("seatbeltProfile accepted hostile networkAllow entry")
	}
}
