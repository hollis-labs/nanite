package agent

import (
	"testing"

	"github.com/hollis-labs/go-agent-wrapper/adapters"
	"github.com/hollis-labs/go-providers/provider"
)

func TestSelectNativeAdapter_PreservesNaniteLaunchPolicy(t *testing.T) {
	cases := []struct {
		name      string
		provider  string
		cli       provider.CLIAdapter
		protocol  adapters.Protocol
		transport adapters.Transport
	}{
		{"claude streaming", "claude", provider.NewClaudeAdapterStreamingStdio(), adapters.ProtocolClaudeStreamJSON, adapters.TransportStdio},
		{"codex per turn", "codex", provider.NewCodexAdapter(), "", ""},
		{"opencode per turn", "opencode", provider.NewOpencodeAdapter(), "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			selected, err := selectNativeAdapter(tc.provider, ModeLongLived, tc.cli)
			if err != nil {
				t.Fatalf("selectNativeAdapter: %v", err)
			}
			desc := selected.Describe()
			if desc.Protocol != tc.protocol || desc.Transport != tc.transport {
				t.Fatalf("descriptor = protocol %q transport %q, want %q/%q", desc.Protocol, desc.Transport, tc.protocol, tc.transport)
			}
			got := selected.CLIAdapter()
			if got.Name() != tc.cli.Name() {
				t.Fatalf("CLIAdapter name = %q, want %q", got.Name(), tc.cli.Name())
			}
			if gotArgs, wantArgs := got.BuildArgs("prompt", "system", "session"), tc.cli.BuildArgs("prompt", "system", "session"); !equalStrings(gotArgs, wantArgs) {
				t.Fatalf("CLIAdapter args = %#v, want configured host args %#v", gotArgs, wantArgs)
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSelectNativeAdapter_RejectsShapeMismatch(t *testing.T) {
	if _, err := selectNativeAdapter("codex", ModeLongLived, provider.NewCodexAdapterAppServer()); err == nil {
		t.Fatal("app-server adapter accepted for Nanite's subprocess-per-turn launch policy")
	}
}
