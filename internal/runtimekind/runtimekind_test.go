package runtimekind

import (
	"testing"

	"github.com/hollis-labs/substrate/llm-core/contracts/runtimes"
)

// The tokens rows were written with before agentkit v0.12.0 must still read
// as their current kind (CW-20260930-0113).
func TestParse_LegacyTokensReadAsCurrentKinds(t *testing.T) {
	for raw, want := range map[string]Kind{
		"subprocess":       SubprocessPerTurn,
		"serve-http":       HTTPSSE,
		"app-server":       JSONRPCStdio,
		"pty-debug":        PTY,
		"api":              API,
		"Subprocess ":      SubprocessPerTurn,
		"serve_http":       HTTPSSE,
		"exec":             SubprocessPerTurn,
		"codex-app-server": JSONRPCStdio,
		"":                 Unknown,
		"unknown":          Unknown,
		"bogus":            Unknown,
	} {
		if got := Parse(raw); got != want {
			t.Errorf("Parse(%q) = %q, want %q", raw, got, want)
		}
	}
}

// Every runtime mode Nanite names is the leaf spelling, and parses to
// itself.
func TestParse_CurrentSpellingsRoundTrip(t *testing.T) {
	for _, k := range []Kind{StreamingStdio, SubprocessPerTurn, JSONRPCStdio, HTTPSSE, PTY, ACPStdio, ACPTCP} {
		if !runtimes.Mode(k).Valid() {
			t.Errorf("%q is not a runtimes.Mode", k)
		}
		if got := Parse(string(k)); got != k {
			t.Errorf("Parse(%q) = %q, want it unchanged", k, got)
		}
	}
	if got := Parse(string(API)); got != API {
		t.Errorf("Parse(%q) = %q", API, got)
	}
}

func TestIsManagedAutomation(t *testing.T) {
	for k, want := range map[Kind]bool{
		API: true, StreamingStdio: true, SubprocessPerTurn: true, JSONRPCStdio: true, HTTPSSE: true,
		// ACP agent_runtime rows record their mode (CW-20261001-0139); that
		// does not make ACP managed automation, which it was not as Unknown.
		PTY: false, Unknown: false, ACPStdio: false, ACPTCP: false,
	} {
		if got := IsManagedAutomation(k); got != want {
			t.Errorf("IsManagedAutomation(%q) = %v, want %v", k, got, want)
		}
	}
}
