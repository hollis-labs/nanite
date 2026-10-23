package mcpconfig

import (
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
