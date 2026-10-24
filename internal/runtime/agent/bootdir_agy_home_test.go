package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// agyFakeHome points HOME (which naniteHomeDir and the farm read) at a temp dir
// that looks like an operator's home, and returns it. testhome already
// isolates the package; this one has content to assert against.
func agyFakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, d := range []string{".ssh", "dev", ".nanite", ".gemini/antigravity-cli", ".agents/plugins/pulse"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for f, body := range map[string]string{
		".gitconfig": "[user]", ".DS_Store": "x", ".CFUserTextEncoding": "0:0",
		".gemini/oauth_creds.json": "REAL-AUTH", ".gemini/installation_id": "id",
		".gemini/antigravity-cli/antigravity-oauth-token": "tok", ".gemini/settings.json": `{"real":true}`,
	} {
		if err := os.WriteFile(filepath.Join(home, f), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func agySetupParams() SetupParams {
	return SetupParams{
		SessionID:    "agy-home",
		Mode:         ModeLongLived,
		AgentProfile: &store.AgentProfile{Name: "AGY", Slug: "agy-agent", ID: "agt1"},
		MCPConfig:    MCPConfig{BinaryPath: "/bin/nanite", DBPath: "/tmp/x.db"},
		HostMCP: &HostMCPLaunch{
			Servers: []HostMCPServer{{Name: "tether", Command: "tether"}},
			Tools:   []string{"agent_list", "tether_message_send"},
		},
	}
}

func agySetup(t *testing.T, params SetupParams) string {
	t.Helper()
	boot, err := bootdirLayoutFor("agy").Setup(params)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(boot) })
	return boot
}

func readAgySettings(t *testing.T, gem string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(gem, agySettingsRel))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("settings.json: %v\n%s", err, b)
	}
	return m
}

func allowList(m map[string]any) []string {
	var out []string
	perms, _ := m["permissions"].(map[string]any)
	allow, _ := perms["allow"].([]any)
	for _, a := range allow {
		out = append(out, a.(string))
	}
	return out
}

func TestAgyHomeFarmAndPrivateGemini(t *testing.T) {
	home := agyFakeHome(t)
	boot := agySetup(t, agySetupParams())
	farm := filepath.Join(boot, "agy-home")

	// Links to the real home, minus the global agent config and noise.
	for _, name := range []string{".ssh", "dev", ".nanite", ".gitconfig"} {
		got, err := os.Readlink(filepath.Join(farm, name))
		if err != nil || got != filepath.Join(home, name) {
			t.Errorf("farm/%s -> %q, %v", name, got, err)
		}
	}
	for _, name := range []string{".agents", ".DS_Store", ".CFUserTextEncoding", ".Trash"} {
		if _, err := os.Lstat(filepath.Join(farm, name)); err == nil {
			t.Errorf("farm links %s; it must not", name)
		}
	}
	// .gemini is the agent's persistent private dir under ~/.nanite, not the real one.
	gem := filepath.Join(home, ".nanite", "agy-gemini", "agy-agent")
	if got, err := os.Readlink(filepath.Join(farm, ".gemini")); err != nil || got != gem {
		t.Fatalf("farm/.gemini -> %q, %v; want %s", got, err, gem)
	}
	// Auth shared by link, only for files that exist; no copy of the credential.
	for rel, want := range map[string]bool{"oauth_creds.json": true, "installation_id": true,
		"antigravity-cli/antigravity-oauth-token": true, "google_accounts.json": false, "jetski-standalone-oauth-token": false} {
		got, err := os.Readlink(filepath.Join(gem, rel))
		if want && (err != nil || got != filepath.Join(home, ".gemini", rel)) {
			t.Errorf("%s -> %q, %v", rel, got, err)
		}
		if !want && err == nil {
			t.Errorf("%s linked though the real file does not exist", rel)
		}
	}
	// The operator's real settings are not the private ones.
	if _, err := os.Lstat(filepath.Join(gem, "settings.json")); err == nil {
		t.Error("the real top-level ~/.gemini/settings.json must not be carried over")
	}
}

func TestAgyHomeSettingsFromGrants(t *testing.T) {
	agyFakeHome(t)
	boot := agySetup(t, agySetupParams())
	gem, _ := os.Readlink(filepath.Join(boot, "agy-home", ".gemini"))
	got := allowList(readAgySettings(t, gem))
	want := []string{"mcp(tether_nanite/*)", "mcp(tether_tether/agent_list)", "mcp(tether_tether/tether_message_send)"}
	if !slices.Equal(got, want) {
		t.Fatalf("allow = %v, want %v", got, want)
	}
	info, err := os.Stat(filepath.Join(gem, agySettingsRel))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("settings.json mode = %v, %v; want 0600", info.Mode().Perm(), err)
	}
}

func TestAgyHomeSettingsNoGrantMeansNoHostRule(t *testing.T) {
	agyFakeHome(t)
	params := agySetupParams()
	params.HostMCP.Tools = nil
	boot := agySetup(t, params)
	gem, _ := os.Readlink(filepath.Join(boot, "agy-home", ".gemini"))
	if got := allowList(readAgySettings(t, gem)); !slices.Equal(got, []string{"mcp(tether_nanite/*)"}) {
		t.Fatalf("allow = %v; an ungranted host server must have no rule", got)
	}
	// No MCP planting, no host: no rules at all.
	params.MCPConfig, params.HostMCP = MCPConfig{}, nil
	boot = agySetup(t, params)
	if got := allowList(readAgySettings(t, gem)); len(got) != 0 {
		t.Fatalf("allow = %v, want none", got)
	}
	_ = boot
}

func TestAgyHomeSettingsMergeKeepsForeignAndDropsStale(t *testing.T) {
	agyFakeHome(t)
	params := agySetupParams()
	boot := agySetup(t, params)
	gem, _ := os.Readlink(filepath.Join(boot, "agy-home", ".gemini"))

	// agy / the operator add their own keys and an allow entry to the private file.
	sp := filepath.Join(gem, agySettingsRel)
	m := readAgySettings(t, gem)
	m["theme"] = "dark"
	m["permissions"].(map[string]any)["deny"] = []any{"command(rm)"}
	m["permissions"].(map[string]any)["allow"] = append(m["permissions"].(map[string]any)["allow"].([]any), "read_url(example.com)")
	b, _ := json.Marshal(m)
	if err := os.WriteFile(sp, b, 0o644); err != nil {
		t.Fatal(err)
	}

	// The grant for tether_message_send is revoked.
	params.HostMCP.Tools = []string{"agent_list"}
	agySetup(t, params)

	m = readAgySettings(t, gem)
	got := allowList(m)
	want := []string{"read_url(example.com)", "mcp(tether_nanite/*)", "mcp(tether_tether/agent_list)"}
	if !slices.Equal(got, want) {
		t.Fatalf("allow = %v, want %v (foreign kept, revoked rule gone)", got, want)
	}
	if m["theme"] != "dark" || m["permissions"].(map[string]any)["deny"] == nil {
		t.Errorf("unrelated keys clobbered: %v", m)
	}
	if info, _ := os.Stat(sp); info.Mode().Perm() != 0o600 {
		t.Errorf("rewritten settings.json mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestAgyHomeCorruptSettingsAreKeptAside(t *testing.T) {
	agyFakeHome(t)
	params := agySetupParams()
	boot := agySetup(t, params)
	gem, _ := os.Readlink(filepath.Join(boot, "agy-home", ".gemini"))
	sp := filepath.Join(gem, agySettingsRel)
	if err := os.WriteFile(sp, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	agySetup(t, params)
	if len(allowList(readAgySettings(t, gem))) != 3 {
		t.Error("settings not regenerated")
	}
	baks, _ := filepath.Glob(sp + ".bak-*")
	if len(baks) != 1 {
		t.Errorf("corrupt file not kept aside: %v", baks)
	}
}

func TestAgyHomeNeverWritesTheRealGemini(t *testing.T) {
	home := agyFakeHome(t)
	snapshot := func() map[string]string {
		out := map[string]string{}
		for _, root := range []string{".gemini", ".agents"} {
			_ = filepath.WalkDir(filepath.Join(home, root), func(p string, d os.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				info, _ := d.Info()
				b, _ := os.ReadFile(p)
				out[p] = info.Mode().String() + info.ModTime().String() + string(b)
				return nil
			})
		}
		return out
	}
	before := snapshot()
	params := agySetupParams()
	boot := agySetup(t, params)
	agySetup(t, params)
	if after := snapshot(); len(after) != len(before) {
		t.Fatalf("real .gemini/.agents entries changed: %d -> %d", len(before), len(after))
	} else {
		for k, v := range before {
			if after[k] != v {
				t.Errorf("%s changed", k)
			}
		}
	}
	// Writing through the private dir's settings never reaches the real file.
	if b, _ := os.ReadFile(filepath.Join(home, ".gemini", "settings.json")); string(b) != `{"real":true}` {
		t.Errorf("real settings.json = %q", b)
	}
	if strings.Contains(boot, home+"/.gemini") {
		t.Errorf("boot dir %q is under the real .gemini", boot)
	}
}

func TestAgyHomeWithoutUsableSlugIsPerBoot(t *testing.T) {
	agyFakeHome(t)
	params := agySetupParams()
	params.AgentProfile = &store.AgentProfile{Name: "x", Slug: "../evil", ID: ""}
	boot := agySetup(t, params)
	if got, _ := os.Readlink(filepath.Join(boot, "agy-home", ".gemini")); got != filepath.Join(boot, "agy-gemini") {
		t.Errorf("gemini dir = %q, want the per-boot fallback", got)
	}
}

func TestAgyAmendEnvKeepsTheRestOfTheEnv(t *testing.T) {
	base := map[string]string{"HOME": "/real", "PATH": "/bin", "NANITE_X": "1"}
	got := agyLayout{}.AmendEnv(base, "/boot")
	if got["HOME"] != "/boot/agy-home" || got["PATH"] != "/bin" || got["NANITE_X"] != "1" || base["HOME"] != "/real" {
		t.Errorf("AmendEnv = %v (base %v)", got, base)
	}
	if got := (agyLayout{}).AmendEnv(base, ""); got["HOME"] != "/real" {
		t.Errorf("empty bootDir must leave env alone: %v", got)
	}
}

func TestAgyHostMCPRefusalsAndHooks(t *testing.T) {
	src := &fakeHostMCP{rows: map[string]*store.MCPServerConfig{"tether": tetherRow()}, granted: []string{"agent_list", "bad)tool", "a/b"}}
	got, err := resolveHostMCP(context.Background(), src, hostProfile("tether"), "antigravity", "nanite")
	if err != nil || got == nil || len(got.Servers) != 1 {
		t.Fatalf("antigravity must be allowed: %+v %v", got, err)
	}
	rules := agyMCPAllowRules(SetupParams{HostMCP: got})
	if !slices.Equal(rules, []string{"mcp(tether_tether/agent_list)"}) {
		t.Errorf("rules = %v; tool names that would break a rule get none", rules)
	}
	for _, p := range []string{"codex", "opencode"} {
		if g, err := resolveHostMCP(context.Background(), src, hostProfile("tether"), p, "nanite"); err == nil || g != nil {
			t.Errorf("%s must stay refused", p)
		}
	}
	if _, err := agyPlantSpec(SetupParams{AgentProfile: &store.AgentProfile{}, Hooks: []BootDirHook{{}}}); err == nil {
		t.Error("agy must still refuse hooks")
	}
}
