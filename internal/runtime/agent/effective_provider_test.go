package agent

import (
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/nanite/internal/store"
)

// TestExpandUserHome pins the helper's per-shape contract directly,
// without going through Boot's filesystem-touching path. Mirrors the
// rule set in expandUserHome's doc comment so a future tweak to the
// rules updates this table in lock-step.
func TestExpandUserHome(t *testing.T) {
	t.Setenv("HOME", "/fake/home")

	tests := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"/abs/path", "/abs/path"},
		{"relative/path", "relative/path"},
		{"~", "/fake/home"},
		{"~/", "/fake/home"},
		{"~/subdir", "/fake/home/subdir"},
		{"~/Projects-apps/nanite", "/fake/home/Projects-apps/nanite"},
		// "~user" form: out of scope, preserved verbatim.
		{"~someoneelse", "~someoneelse"},
		{"~someoneelse/path", "~someoneelse/path"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := expandUserHome(tt.in)
			if err != nil {
				t.Fatalf("expandUserHome(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("expandUserHome(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestEffectiveProvider_Precedence pins the c197 regression
// (CW-20260514-0053). The helper's contract:
//
//	opts.Provider WHEN non-empty  →  opts.Provider
//	profile.DefaultProvider only  →  profile.DefaultProvider
//	both set, opts wins           →  opts.Provider
//	both empty / nil profile      →  ""
//
// The function is small but every dispatch site downstream
// (bootdirLayoutFor, deps.ProviderAdapter, RuntimeRow.Provider,
// Session.Provider, wrapper adapter selection) depends on this contract
// silently, so a future refactor that flips precedence would have
// downstream consequences this test localizes.
func TestEffectiveProvider_Precedence(t *testing.T) {
	tests := []struct {
		name    string
		opts    Options
		profile *store.AgentProfile
		want    string
	}{
		{
			name:    "opts.Provider wins over profile.DefaultProvider",
			opts:    Options{Provider: "claude"},
			profile: &store.AgentProfile{DefaultProvider: "anthropic"},
			want:    "claude",
		},
		{
			name:    "empty opts falls back to profile",
			opts:    Options{},
			profile: &store.AgentProfile{DefaultProvider: "anthropic"},
			want:    "anthropic",
		},
		{
			name:    "c197 reproducer: spec.Provider via opts when profile is empty",
			opts:    Options{Provider: "claude"},
			profile: &store.AgentProfile{DefaultProvider: ""},
			want:    "claude",
		},
		{
			name:    "both empty stays empty",
			opts:    Options{},
			profile: &store.AgentProfile{DefaultProvider: ""},
			want:    "",
		},
		{
			name:    "nil profile + opts override → opts",
			opts:    Options{Provider: "codex"},
			profile: nil,
			want:    "codex",
		},
		{
			name:    "nil profile + empty opts → empty",
			opts:    Options{},
			profile: nil,
			want:    "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := effectiveProvider(tt.opts, tt.profile); got != tt.want {
				t.Errorf("effectiveProvider = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestComposeBootdirParams_OptionsProviderRoutesToClaude is the
// c197 end-to-end pin. With Options.Provider="claude" and a profile
// carrying an empty DefaultProvider (the file-default shape), the
// dispatch must land on claudeLayout — not unsupportedLayout{name:""}
// which is what shipped before this fix.
func TestComposeBootdirParams_OptionsProviderRoutesToClaude(t *testing.T) {
	profile := &store.AgentProfile{DefaultProvider: ""} // file-default shape
	opts := Options{Provider: "claude", SessionID: "sess-c197"}

	layout, _ := composeBootdirParams(nil, opts, profile, "sess-c197")
	if _, ok := layout.(claudeLayout); !ok {
		t.Fatalf("composeBootdirParams returned %T, want claudeLayout (c197 regression — Options.Provider must override empty profile.DefaultProvider)", layout)
	}
}

// TestComposeBootdirParams_LegacyProfileProviderStillWorks confirms
// the no-regression case: when Options.Provider is empty, the legacy
// profile.DefaultProvider path keeps dispatching as it did before.
func TestComposeBootdirParams_LegacyProfileProviderStillWorks(t *testing.T) {
	profile := &store.AgentProfile{DefaultProvider: "codex"}
	opts := Options{SessionID: "sess-legacy"}

	layout, _ := composeBootdirParams(nil, opts, profile, "sess-legacy")
	if _, ok := layout.(codexLayout); !ok {
		t.Fatalf("composeBootdirParams returned %T, want codexLayout (legacy profile-driven dispatch must keep working)", layout)
	}
}

func TestComposeBootdirParams_IncludesSessionPathGrantsInCLIWritableRoots(t *testing.T) {
	sessionID := "sess-grants"
	projectRoot := t.TempDir()
	explicitDir := t.TempDir()
	explicitFile := filepath.Join(explicitDir, "notes.md")
	grants := permission.NewPathGrants()
	grants.RegisterFromUserMessage(sessionID, explicitFile)

	layout, params := composeBootdirParams(&Dependencies{
		CLIWritableRoots: []string{projectRoot},
		PathGrants:       grants,
	}, Options{SessionID: sessionID}, &store.AgentProfile{DefaultProvider: "codex"}, sessionID)

	if _, ok := layout.(codexLayout); !ok {
		t.Fatalf("composeBootdirParams returned %T, want codexLayout", layout)
	}

	want := map[string]bool{
		projectRoot: true,
		explicitDir: true,
	}
	if len(params.CLIWritableRoots) != len(want) {
		t.Fatalf("CLIWritableRoots = %v, want %v", params.CLIWritableRoots, keys(want))
	}
	for _, root := range params.CLIWritableRoots {
		if !want[root] {
			t.Fatalf("unexpected CLIWritableRoot %q (roots=%v)", root, params.CLIWritableRoots)
		}
		delete(want, root)
	}
	if len(want) != 0 {
		t.Fatalf("missing CLIWritableRoots %v (roots=%v)", keys(want), params.CLIWritableRoots)
	}
}

func TestComposeBootdirParams_IncludesLineagePathGrantsInCLIWritableRoots(t *testing.T) {
	parentID := "sess-parent"
	childID := "sess-child"
	parentDir := t.TempDir()
	childDir := t.TempDir()
	grants := permission.NewPathGrants()
	grants.RegisterFromUserMessage(parentID, filepath.Join(parentDir, "design.md"))
	grants.RegisterFromUserMessage(childID, filepath.Join(childDir, "todo.md"))
	grants.RegisterLineage(childID, parentID)

	_, params := composeBootdirParams(&Dependencies{
		PathGrants: grants,
	}, Options{SessionID: childID}, &store.AgentProfile{DefaultProvider: "claude"}, childID)

	want := map[string]bool{
		parentDir: true,
		childDir:  true,
	}
	if len(params.CLIWritableRoots) != len(want) {
		t.Fatalf("CLIWritableRoots = %v, want %v", params.CLIWritableRoots, keys(want))
	}
	for _, root := range params.CLIWritableRoots {
		if !want[root] {
			t.Fatalf("unexpected CLIWritableRoot %q (roots=%v)", root, params.CLIWritableRoots)
		}
		delete(want, root)
	}
	if len(want) != 0 {
		t.Fatalf("missing CLIWritableRoots %v (roots=%v)", keys(want), params.CLIWritableRoots)
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
