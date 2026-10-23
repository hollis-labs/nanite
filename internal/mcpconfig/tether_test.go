package mcpconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestTetherMessagingConfig_ProjectsToAStdioRowWithoutSecrets(t *testing.T) {
	data, err := TetherMessagingConfig("/home/agent/.nanite/tether.token")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	rows := ToStoreConfigs(cfg)
	if len(rows) != 1 || rows[0].Name != TetherMessagingServerName || rows[0].Command != "tether" || rows[0].TransportType != store.TransportStdio {
		t.Fatalf("rows = %+v", rows)
	}
	want := `["mcp","--no-discover","--token-file","/home/agent/.nanite/tether.token"]`
	if rows[0].Args != want {
		t.Fatalf("args = %s, want %s", rows[0].Args, want)
	}
	if rows[0].Env != `["TETHER_MCP_SCOPES=message.write"]` {
		t.Fatalf("env = %s", rows[0].Env)
	}
	if strings.Contains(string(data), "tth_") || strings.Contains(rows[0].Env, "TOKEN=") {
		t.Fatal("config must carry a token file path, never a token")
	}
	for _, a := range []string{"--proxy", "--servers"} {
		if strings.Contains(rows[0].Args, a) {
			t.Fatalf("messaging gateway must not reach upstream servers: %s", rows[0].Args)
		}
	}
}

func TestTetherMessagingConfig_RejectsMissingOrRelativeTokenFile(t *testing.T) {
	for _, p := range []string{"", "tether.token", "./tether.token"} {
		if _, err := TetherMessagingConfig(p); err == nil {
			t.Fatalf("token file %q accepted", p)
		}
	}
}

func TestTetherMessagingTools_AreTheMessagingSet(t *testing.T) {
	want := []string{"tether_message_send", "tether_message_reply", "tether_message_inbox", "tether_message_list", "tether_message_get", "tether_message_mark_read"}
	if !reflect.DeepEqual(TetherMessagingTools, want) {
		t.Fatalf("tools = %v", TetherMessagingTools)
	}
}

func TestCheckTetherTokenFile(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.token")
	if err := os.WriteFile(good, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckTetherTokenFile(good); err != nil {
		t.Fatalf("0600 file refused: %v", err)
	}
	loose := filepath.Join(dir, "loose.token")
	if err := os.WriteFile(loose, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(loose, 0o644); err != nil { // #nosec G302 -- deliberately loose fixture
		t.Fatal(err)
	}
	if err := CheckTetherTokenFile(loose); err == nil || !strings.Contains(err.Error(), "0600") {
		t.Fatalf("0644 file: %v", err)
	}
	if err := CheckTetherTokenFile(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing file accepted")
	}
	if err := CheckTetherTokenFile(dir); err == nil {
		t.Fatal("directory accepted")
	}
}

func TestCheckTetherSingleActor(t *testing.T) {
	existing := &store.MCPServerConfig{Name: TetherMessagingServerName, Args: `["mcp","--no-discover","--token-file","/a/tok"]`}
	if err := CheckTetherSingleActor(nil, "/a/tok"); err != nil {
		t.Fatalf("no entry: %v", err)
	}
	if err := CheckTetherSingleActor(existing, "/a/tok"); err != nil {
		t.Fatalf("same file refused: %v", err)
	}
	if err := CheckTetherSingleActor(existing, "/b/tok"); err == nil || !strings.Contains(err.Error(), "one Tether actor per host") {
		t.Fatalf("second actor: %v", err)
	}
	if err := CheckTetherSingleActor(&store.MCPServerConfig{Args: `not json`}, "/a/tok"); err == nil {
		t.Fatal("unreadable existing args must refuse, not pass")
	}
}
