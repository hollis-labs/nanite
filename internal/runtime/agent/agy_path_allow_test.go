package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestAgyPathAllowRulesGolden(t *testing.T) {
	home := agyFakeHome(t)
	work, extra := t.TempDir(), t.TempDir()
	got := agyPathAllowRules([]string{work, extra, work}, home)
	want := []string{
		"read_file(" + work + ")", "read_file(" + extra + ")",
		"write_file(" + work + ")", "write_file(" + extra + ")",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("rules\n got %q\nwant %q", got, want)
	}
}

func TestAgyPathAllowRulesRefuseUnsafe(t *testing.T) {
	home := agyFakeHome(t)
	link := filepath.Join(t.TempDir(), "homelink")
	if err := os.Symlink(home, link); err != nil {
		t.Fatal(err)
	}
	ok := t.TempDir()
	spaced := filepath.Join(ok, "a b")
	for _, bad := range []string{
		"", "/", home, filepath.Dir(home), filepath.Dir(filepath.Dir(home)), link,
		filepath.Join(home, ".ssh"), filepath.Join(home, ".ssh", "x"), filepath.Join(home, ".gemini"), filepath.Join(home, ".nanite", "d"),
		"relative/dir", ".", "~/x", ok + "/", ok + "/../x", filepath.Join(ok, "a)b"), filepath.Join(ok, "a\nb"), filepath.Join(ok, "*"), spaced,
	} {
		if got := agyPathAllowRules([]string{bad}, home); len(got) != 0 {
			t.Errorf("root %q must get no rule, got %q", bad, got)
		}
	}
	if got := agyPathAllowRules([]string{filepath.Join(home, "dev")}, home); len(got) != 2 {
		t.Errorf("a project under the home is fine, got %q", got)
	}
	if got := agyPathAllowRules([]string{ok}, ""); len(got) != 0 {
		t.Errorf("unknown home must fail closed, got %q", got)
	}
}

func TestAgyHomeSettingsCarryPathRulesAndDropStale(t *testing.T) {
	home := agyFakeHome(t)
	work, other := t.TempDir(), t.TempDir()
	params := agySetupParams()
	params.CLIWritableRoots = []string{work, other}
	boot := agySetup(t, params)
	gem := agyPrivateGeminiDir(boot, params)
	got := allowList(readAgySettings(t, gem))
	for _, w := range []string{"read_file(" + work + ")", "write_file(" + work + ")", "read_file(" + other + ")", "write_file(" + other + ")", "mcp(tether_tether/agent_list)"} {
		if !slices.Contains(got, w) {
			t.Errorf("settings lack %q: %q", w, got)
		}
	}
	for _, g := range got {
		if g == "read_file(/)" || g == "write_file(/)" || g == "read_file("+home+")" {
			t.Errorf("unsafe rule %q", g)
		}
	}
	// A user rule added later survives; a revoked directory's rules go.
	sp := filepath.Join(gem, agySettingsRel)
	settings := readAgySettings(t, gem)
	perms := settings["permissions"].(map[string]any)
	perms["allow"] = append(perms["allow"].([]any), "command(ls)", "read_file("+other+")x")
	writeJSONForTest(t, sp, settings)

	params.CLIWritableRoots = []string{work}
	boot2 := agySetup(t, params)
	got = allowList(readAgySettings(t, agyPrivateGeminiDir(boot2, params)))
	for _, stale := range []string{"read_file(" + other + ")", "write_file(" + other + ")"} {
		if slices.Contains(got, stale) {
			t.Errorf("stale rule %q kept: %q", stale, got)
		}
	}
	for _, keep := range []string{"command(ls)", "read_file(" + other + ")x", "read_file(" + work + ")", "write_file(" + work + ")"} {
		if !slices.Contains(got, keep) {
			t.Errorf("rule %q missing: %q", keep, got)
		}
	}
	if n := countOf(got, "read_file("+work+")"); n != 1 {
		t.Errorf("work rule duplicated %d times", n)
	}
}

func TestAgyHomeNoRootsNoPathRules(t *testing.T) {
	agyFakeHome(t)
	params := agySetupParams()
	boot := agySetup(t, params)
	gem := agyPrivateGeminiDir(boot, params)
	for _, r := range allowList(readAgySettings(t, gem)) {
		if r == "read_file("+filepath.Join(gem, "antigravity-cli", "mcp")+")" {
			continue // agy's own MCP tool-descriptor directory, not a work root
		}
		if len(r) > 5 && (r[:5] == "read_" || r[:5] == "write" || r[:5] == "comma") {
			t.Errorf("unexpected non-MCP rule %q", r)
		}
	}
}

func countOf(s []string, v string) int {
	n := 0
	for _, e := range s {
		if e == v {
			n++
		}
	}
	return n
}

func writeJSONForTest(t *testing.T, path string, v any) {
	t.Helper()
	b, err := jsonMarshalIndentForTest(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func jsonMarshalIndentForTest(v any) ([]byte, error) { return json.MarshalIndent(v, "", "  ") }
