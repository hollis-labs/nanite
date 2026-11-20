package authored

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseIdentity(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "agent.cred")
	ok := "principal: agent:research-lead\nwarden:\n  url: https://warden.example.test\n  credential_file: " + outside + "\nmcp:\n  gateway:\n    audience: mcp-gateway\n    scopes: [mcp.profile.standard]\n    correlation: true\n"
	id, err := ParseIdentity([]byte(ok), dir)
	if err != nil {
		t.Fatal(err)
	}
	if id.Principal != "agent:research-lead" || id.Warden.CredentialFile != outside || !id.MCP["gateway"].Correlation {
		t.Fatalf("parsed %+v", id)
	}
	for name, bad := range map[string]string{
		"unknown field":       "principal: agent:a\nextra: 1\n",
		"principal kind":      "principal: user:a\n",
		"relative credential": "principal: agent:a\nwarden:\n  url: https://warden.example.test\n  credential_file: agent.cred\n",
		"credential inside":   "principal: agent:a\nwarden:\n  url: https://warden.example.test\n  credential_file: " + filepath.Join(dir, "secrets", "agent.cred") + "\n",
		"plain http":          "principal: agent:a\nwarden:\n  url: http://warden.example.test\n  credential_file: " + outside + "\n",
		"mcp without warden":  "principal: agent:a\nmcp:\n  gateway:\n    audience: mcp-gateway\n",
		"scopes no audience":  "principal: agent:a\nmcp:\n  gateway:\n    scopes: [x]\n",
	} {
		if _, err := ParseIdentity([]byte(bad), dir); err == nil {
			t.Errorf("%s: accepted", name)
		} else if !strings.HasPrefix(err.Error(), "identity.yaml: ") {
			t.Errorf("%s: error %q lacks the file name", name, err)
		}
	}
	// Correlation alone needs no Warden.
	if _, err := ParseIdentity([]byte("principal: agent:a\nmcp:\n  gateway:\n    correlation: true\n"), dir); err != nil {
		t.Fatal(err)
	}
}

func TestLoadIdentityMissingFile(t *testing.T) {
	dir := t.TempDir()
	if _, ok, err := LoadIdentity(dir); ok || err != nil {
		t.Fatalf("LoadIdentity = %t, %v", ok, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "identity.yaml"), []byte("principal: agent:a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if id, ok, err := LoadIdentity(dir); !ok || err != nil || id.Principal != "agent:a" {
		t.Fatalf("LoadIdentity = %+v, %t, %v", id, ok, err)
	}
}
