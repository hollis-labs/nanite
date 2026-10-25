package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// policyHome is a fake real home holding every denied directory and ~/dev.
func policyHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, d := range append(slices.Clone(CLIRootDenyHomeDirs), "dev/proj", "dev/other", "elsewhere") {
		if err := os.MkdirAll(filepath.Join(home, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func TestCLIRootDenyList_IsTheDocumentedSet(t *testing.T) {
	want := []string{".tether", ".cerberus", ".nanite", ".ssh", ".gnupg", ".aws", ".kube", ".docker", ".gemini", ".agents", ".config"}
	if !slices.Equal(CLIRootDenyHomeDirs, want) {
		t.Fatalf("deny list = %v, want %v", CLIRootDenyHomeDirs, want)
	}
}

func TestCLIRootDenyReason(t *testing.T) {
	home := policyHome(t)
	link := filepath.Join(home, "elsewhere", "innocent")
	if err := os.Symlink(filepath.Join(home, ".tether"), link); err != nil {
		t.Fatal(err)
	}
	homeLink := filepath.Join(t.TempDir(), "homelink")
	if err := os.Symlink(home, homeLink); err != nil {
		t.Fatal(err)
	}
	denied := []string{
		"/", home, filepath.Dir(home), homeLink, link,
		filepath.Join(home, ".tether"), filepath.Join(home, ".tether", "sub"),
		filepath.Join(home, ".ssh"), filepath.Join(home, ".config", "systemd"),
		filepath.Join(home, ".nanite", "workspaces"), filepath.Join(home, ".cerberus"),
		filepath.Join(home, ".aws"), filepath.Join(home, ".kube"), filepath.Join(home, ".docker"),
		filepath.Join(home, ".gnupg"), filepath.Join(home, ".gemini"), filepath.Join(home, ".agents"),
		filepath.Join(home, ".ssh", "not-yet-created"), "relative/path", "",
	}
	for _, d := range denied {
		if reason := CLIRootDenyReason(d, home); reason == "" {
			t.Errorf("%q must be denied", d)
		}
	}
	allowed := []string{
		filepath.Join(home, "dev"), filepath.Join(home, "dev", "proj"), filepath.Join(home, "elsewhere"),
		filepath.Join(home, ".local", "share", "nanite", "agent-workspaces", "x"),
		filepath.Join(home, ".tetherish"), filepath.Join(home, ".configs"),
	}
	for _, a := range allowed {
		if reason := CLIRootDenyReason(a, home); reason != "" {
			t.Errorf("%q must be allowed, got %q", a, reason)
		}
	}
}

func TestEffectiveCLIWritableRoots_Sources(t *testing.T) {
	home := policyHome(t)
	work := filepath.Join(home, "dev", "proj")
	hostDir := filepath.Join(home, "dev", "other")
	cfgRoot := filepath.Join(home, "elsewhere")

	// Without cli_writable_roots nothing but the work root and host dirs.
	got := effectiveCLIWritableRoots(&Dependencies{}, "", work, hostDir)
	if want := []string{work, hostDir}; !slices.Equal(got, want) {
		t.Fatalf("no cli_writable_roots: %v, want %v", got, want)
	}
	// With them they follow.
	got = effectiveCLIWritableRoots(&Dependencies{CLIWritableRoots: []string{cfgRoot}}, "", work, hostDir)
	if want := []string{work, hostDir, cfgRoot}; !slices.Equal(got, want) {
		t.Fatalf("with cli_writable_roots: %v, want %v", got, want)
	}
}

// Explicit configuration, a host's directories and the work root are all
// subject to the deny-list.
func TestEffectiveCLIWritableRoots_DenyListBeatsEverySource(t *testing.T) {
	home := policyHome(t)
	ok := filepath.Join(home, "dev", "proj")
	secretLink := filepath.Join(home, "elsewhere", "l")
	if err := os.Symlink(filepath.Join(home, ".ssh"), secretLink); err != nil {
		t.Fatal(err)
	}
	deps := &Dependencies{CLIWritableRoots: []string{
		filepath.Join(home, ".tether"), home, "/", secretLink, filepath.Join(home, ".config"), ok,
	}}
	got := effectiveCLIWritableRoots(deps, "", filepath.Join(home, ".nanite"),
		filepath.Join(home, ".ssh"), home, filepath.Join(home, "dev", "other"))
	want := []string{filepath.Join(home, "dev", "other"), ok}
	if !slices.Equal(got, want) {
		t.Fatalf("roots = %v, want %v", got, want)
	}
}

// The CLI roots must not include the dev tools list, so the composed
// SetupParams carry only the work root and host directories by default, and
// the planted claude settings, codex config and agy rules agree.
func TestCLIRootsGolden_DenyListAndNoDevToolsRoots(t *testing.T) {
	home := policyHome(t)
	work := filepath.Join(home, "dev", "proj")
	hostDir := filepath.Join(home, "dev", "other")
	devToolsOnly := filepath.Join(home, "elsewhere") // would come from dev_tools_allowed_paths
	hostDirsJSON, _ := json.Marshal([]string{hostDir, filepath.Join(home, ".ssh"), home, filepath.Join(home, ".tether")})

	for _, provider := range []string{"claude", "codex", "agy"} {
		t.Run(provider, func(t *testing.T) {
			profile := &store.AgentProfile{Name: "w", Slug: "w", ID: "agt1", SystemPrompt: "p", Directories: string(hostDirsJSON)}
			layout, params := composeBootdirParams(&Dependencies{},
				Options{Provider: provider, SessionID: "s-" + provider, Workdir: work}, profile, "s-"+provider)
			if want := []string{work, hostDir}; !slices.Equal(params.CLIWritableRoots, want) {
				t.Fatalf("CLIWritableRoots = %v, want %v", params.CLIWritableRoots, want)
			}
			boot, err := layout.Setup(params)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(boot) })

			var planted string
			switch provider {
			case "codex":
				planted = readTestFile(t, filepath.Join(boot, "config.toml"))
				if !strings.Contains(planted, `writable_roots = ["`+work+`", "`+hostDir+`"]`) {
					t.Fatalf("codex writable_roots wrong:\n%s", planted)
				}
			case "claude":
				planted = readTestFile(t, filepath.Join(boot, ".claude", "settings.json"))
				var s struct {
					Permissions struct {
						AdditionalDirectories []string `json:"additionalDirectories"`
					} `json:"permissions"`
				}
				if err := json.Unmarshal([]byte(planted), &s); err != nil {
					t.Fatal(err)
				}
				if want := []string{work, hostDir}; !slices.Equal(s.Permissions.AdditionalDirectories, want) {
					t.Fatalf("additionalDirectories = %v, want %v", s.Permissions.AdditionalDirectories, want)
				}
			case "agy":
				allow := allowList(readAgySettings(t, agyPrivateGeminiDir(boot, params)))
				for _, w := range []string{"read_file(" + work + ")", "write_file(" + hostDir + ")"} {
					if !slices.Contains(allow, w) {
						t.Errorf("agy rules lack %q: %q", w, allow)
					}
				}
				planted = strings.Join(allow, "\n")
			}
			for _, bad := range []string{home, filepath.Join(home, ".ssh"), filepath.Join(home, ".tether"), devToolsOnly} {
				if strings.Contains(planted, `"`+bad+`"`) || strings.Contains(planted, "("+bad+")") {
					t.Errorf("planted %s config lists %s\n%s", provider, bad, planted)
				}
			}
		})
	}
}

func TestCLIRootsFromDevToolsEnabled(t *testing.T) {
	t.Setenv(CLIRootsFromDevToolsEnv, "")
	if CLIRootsFromDevToolsEnabled() {
		t.Fatal("off by default")
	}
	t.Setenv(CLIRootsFromDevToolsEnv, "1")
	if !CLIRootsFromDevToolsEnabled() {
		t.Fatal("=1 turns it on")
	}
}
