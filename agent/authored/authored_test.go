package authored_test

import (
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
	if v, err := authored.LoadVariants(t.TempDir()); err != nil || len(v) != 0 {
		t.Errorf("LoadVariants on an empty dir: %v %v", v, err)
	}
	if p, err := authored.LoadProviders(t.TempDir()); err != nil || p.Named != nil {
		t.Errorf("LoadProviders on an empty dir: %+v %v", p, err)
	}
}
