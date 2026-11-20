package authored

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadProviders(t *testing.T) {
	dir := t.TempDir()
	write := func(text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "providers.yaml"), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if p, err := LoadProviders(dir); err != nil || p.Named != nil || p.Use != nil {
		t.Fatalf("missing file: %+v %v", p, err)
	}
	write(`providers:
  local:
    wire: openai-chat
    base_url: http://127.0.0.1:8080/v1
    auth: none
  station:
    wire: anthropic
    base_url: https://gateway.example/api/llm-gateway
    key_ref: keychain://nanite/provider/station
    auth: bearer
    headers: {X-Team: nanite}
use:
  chat: {provider: local, model: llama-3.1-8b}
  summarizer: {provider: anthropic, model: claude-haiku-5-5}
  subtask: {provider: station, model: claude-sonnet-5-5}
  desk: {provider: local, model: llama-3.2-1b}
`)
	p, err := LoadProviders(dir)
	if err != nil {
		t.Fatal(err)
	}
	specs, use := p.Named, p.Use
	if specs["local"].Name != "local" || specs["station"].Headers["X-Team"] != "nanite" || use["chat"].Model != "llama-3.1-8b" || use["summarizer"].Provider != "anthropic" || use["desk"].Model != "llama-3.2-1b" {
		t.Fatalf("specs %+v use %+v", specs, use)
	}

	for text, want := range map[string]string{ // #nosec G101 -- Fixture YAML with placeholder references, not credentials.
		"providers:\n  anthropic: {wire: anthropic, base_url: http://x}\n":                             "built in",
		"providers:\n  Bad: {wire: openai-chat, base_url: http://x}\n":                                 "lowercase",
		"providers:\n  p: {wire: grpc, base_url: http://x}\n":                                          "wire",
		"providers:\n  p: {wire: openai-chat, base_url: not-a-url}\n":                                  "base_url",
		"providers:\n  p: {wire: openai-chat, base_url: http://x, auth: bearer}\n":                     "anthropic wire",
		"providers:\n  p: {wire: openai-chat, base_url: http://x, auth: none, key_ref: k}\n":           "no key_ref",
		"providers:\n  p: {wire: openai-chat, base_url: http://x, headers: {Authorization: secret}}\n": "header",
		"providers:\n  p: {wire: openai-chat, base_url: http://x, headers: {api-key: secret}}\n":       "looks like a credential",
		"providers:\n  p: {wire: openai-chat, base_url: http://x, extra: 1}\n":                         "field extra not found",
		"use:\n  chat: {provider: anthropic}\n":                                                        "needs provider and model",
		"use:\n  review: {provider: anthropic, model: m}\n":                                            "one of chat, summarizer, subtask, desk",
		"use:\n  subagent: {provider: anthropic, model: m}\n":                                          "one of chat, summarizer, subtask, desk",
	} {
		write(text)
		if _, err := LoadProviders(dir); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: error %v, want %q", text, err, want)
		}
		// The bytes are judged the same way before they are written.
		if _, err := ParseProviders([]byte(text)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseProviders %q: error %v, want %q", text, err, want)
		}
	}
}
