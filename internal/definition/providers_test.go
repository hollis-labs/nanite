package definition

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
	if specs, use, err := loadProviders(dir); err != nil || specs != nil || use != nil {
		t.Fatalf("missing file: %v %v %v", specs, use, err)
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
`)
	specs, use, err := loadProviders(dir)
	if err != nil {
		t.Fatal(err)
	}
	if specs["local"].Name != "local" || specs["station"].Headers["X-Team"] != "nanite" || use["chat"].Model != "llama-3.1-8b" || use["summarizer"].Provider != "anthropic" {
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
		"use:\n  review: {provider: anthropic, model: m}\n":                                            "chat, summarizer or subtask",
		"use:\n  subagent: {provider: anthropic, model: m}\n":                                          "chat, summarizer or subtask",
	} {
		write(text)
		if _, _, err := loadProviders(dir); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: error %v, want %q", text, err, want)
		}
	}
}
