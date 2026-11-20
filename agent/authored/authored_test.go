package authored_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/agent/authored"
)

// TestPublicRulesAreTheEngines: the exported functions apply the engine's
// rules (tested in internal/authored); this checks they are wired through.
func TestPublicRulesAreTheEngines(t *testing.T) {
	if _, err := authored.ParseVariant("worker", []byte("Body")); err == nil || !strings.Contains(err.Error(), "built-in") {
		t.Errorf("ParseVariant built-in name: %v", err)
	}
	if v, err := authored.ParseVariant("lead", []byte("---\nlast_n: 3\n---\nLead.")); err != nil || v.LastN != 3 || v.Prompt != "Lead." {
		t.Errorf("ParseVariant: %+v %v", v, err)
	}
	if _, err := authored.ParseProviders([]byte("use:\n  review: {provider: anthropic, model: m}\n")); err == nil {
		t.Error("ParseProviders accepted an unknown use")
	}
	p, err := authored.ParseProviders([]byte("providers:\n  local: {wire: openai-chat, base_url: http://127.0.0.1:8080/v1, auth: none}\nuse:\n  chat: {provider: local, model: m}\n"))
	if err != nil || p.Named["local"].Wire != authored.WireOpenAIChat || p.Use[authored.UseChat].Model != "m" {
		t.Errorf("ParseProviders: %+v %v", p, err)
	}
	if err := authored.CheckVariantName("ok-name"); err != nil {
		t.Error(err)
	}
	if err := authored.CheckVariantName("Bad"); err == nil {
		t.Error("CheckVariantName accepted Bad")
	}
	if !slices.Equal(authored.BuiltinVariants(), []string{"code", "planner", "research", "worker"}) ||
		!slices.Equal(authored.Uses(), []string{"chat", "summarizer", "subtask", "desk"}) ||
		!slices.Equal(authored.BuiltinProviders(), []string{"anthropic", "openai"}) {
		t.Errorf("lists: %v %v %v", authored.BuiltinVariants(), authored.Uses(), authored.BuiltinProviders())
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "variants"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"variants/good.md": "Good.", "variants/bad.md": "---\nlast_n: 99\n---\nBad.", "providers.yaml": "use:\n  chat: {provider: local, model: m}\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if v, err := authored.LoadVariants(dir); err == nil || len(v) != 1 || v["good"].Prompt != "Good." {
		t.Errorf("LoadVariants: %v %v", v, err)
	}
	if p, err := authored.LoadProviders(dir); err != nil || p.Use[authored.UseChat].Provider != "local" {
		t.Errorf("LoadProviders: %+v %v", p, err)
	}
}

// TestIdentityIsTheEngines: identity.yaml is parsed and loaded with the
// engine's rules.
func TestIdentityIsTheEngines(t *testing.T) {
	dir := t.TempDir()
	if _, ok, err := authored.LoadIdentity(dir); ok || err != nil {
		t.Fatalf("LoadIdentity without the file: %v %v", ok, err)
	}
	if _, err := authored.ParseIdentity([]byte("principal: agent:a\nwarden:\n  url: https://warden.example\n  credential_file: "+filepath.Join(dir, "agent.cred")+"\n"), dir); err == nil {
		t.Error("ParseIdentity accepted a credential file inside the agent directory")
	}
	raw := "principal: agent:a\nwarden:\n  url: https://warden.example\n  credential_file: /var/empty/agent.cred\n"
	if err := os.WriteFile(filepath.Join(dir, "identity.yaml"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	id, ok, err := authored.LoadIdentity(dir)
	if err != nil || !ok || id.Principal != "agent:a" || id.Warden == nil || id.Warden.URL != "https://warden.example" {
		t.Fatalf("LoadIdentity = %+v %v %v", id, ok, err)
	}
}
