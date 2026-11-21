package anthropic

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/provider/providertest"
	"github.com/hollis-labs/nanite/internal/provider/station"
	"github.com/hollis-labs/nanite/internal/provider/toolchoice"
	"github.com/hollis-labs/nanite/internal/warden"
)

// recordedModel replaces the gateway's model alias in committed fixtures.
const recordedModel = "recorded-model"

// TestRecordFixtures records the Anthropic Messages fixtures in testdata
// from a live, Warden-authenticated gateway. It is skipped unless
// NANITE_RECORD_FIXTURES=1; normal runs and CI only replay. It needs:
//
//	NANITE_RECORD_WARDEN_URL, NANITE_RECORD_CREDENTIAL (the parent
//	credential file; one process at a time, since it rotates on each mint),
//	NANITE_RECORD_PRINCIPAL, NANITE_RECORD_GATEWAY_URL (the Anthropic wire
//	base, without /v1) and NANITE_RECORD_MODEL. NANITE_RECORD_ONLY, a
//	comma-separated list of scenario names, records just those: the cache
//	pair must be recorded together, on a cold cache.
//
// No endpoint, alias or credential is written to testdata: request headers
// are never kept, and the model alias is replaced with recordedModel.
func TestRecordFixtures(t *testing.T) {
	if os.Getenv("NANITE_RECORD_FIXTURES") != "1" {
		t.Skip("set NANITE_RECORD_FIXTURES=1 to record live fixtures")
	}
	env := func(key string) string {
		v := os.Getenv(key)
		if v == "" {
			t.Fatalf("%s is required", key)
		}
		return v
	}
	model := env("NANITE_RECORD_MODEL")
	src, err := warden.New(warden.Config{URL: env("NANITE_RECORD_WARDEN_URL"), CredentialFile: env("NANITE_RECORD_CREDENTIAL"),
		Principal: env("NANITE_RECORD_PRINCIPAL"), Purpose: "Record Nanite provider test fixtures", FailFastGrants: true})
	if err != nil {
		t.Fatal(err)
	}
	rec := &providertest.Recorder{Scrub: func(s string) string { return strings.ReplaceAll(s, model, recordedModel) }}
	c := New()
	c.httpClient = &http.Client{Transport: rec, Timeout: 2 * time.Minute}
	c.SetAPIKey("unused")
	c.SetGateway(station.Settings{BaseURL: env("NANITE_RECORD_GATEWAY_URL"), Auth: "bearer",
		Bearer: src.Bearer("llm-gateway", []string{"llm-gateway.chat"})})

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	record := func(name string, ctx context.Context, req llmtypes.ChatRequest) {
		t.Helper()
		req.Model = model
		stream, err := c.StreamChat(ctx, req)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for ev := range stream {
			if ev.Type == llmtypes.EventError {
				t.Fatalf("%s: %s", name, ev.Error)
			}
		}
		taken := rec.Take()
		if len(taken) != 1 {
			t.Fatalf("%s: %d exchanges", name, len(taken))
		}
		providertest.CheckClean(t, name, taken[0])
		if err := providertest.Save(filepath.Join("testdata", "replay", name+".json"), taken[0]); err != nil {
			t.Fatal(err)
		}
	}
	only := map[string]bool{}
	for _, name := range strings.Split(os.Getenv("NANITE_RECORD_ONLY"), ",") {
		if name != "" {
			only[name] = true
		}
	}
	for _, s := range anthropicScenarios() {
		if len(only) > 0 && !only[s.name] {
			continue
		}
		rctx := ctx
		if s.noTools {
			rctx = toolchoice.WithNone(ctx)
		}
		record(s.name, rctx, s.request)
	}
}

type scenario struct {
	name    string
	request llmtypes.ChatRequest
	noTools bool // send tool_choice none, as the closing reply does
}

// lookupTool is the one tool the scenarios offer.
var lookupTool = llmtypes.ToolDefinition{Name: "lookup", Description: "Look up a fact by its key.",
	InputSchema: map[string]any{"type": "object", "properties": map[string]any{"key": map[string]any{"type": "string"}}, "required": []any{"key"}}}

// cachedSystem is long enough (over 2,048 tokens) for every model's prompt
// cache minimum, and stable, so the second request reads what the first
// wrote.
var cachedSystem = "You are a terse assistant used to record test fixtures. " + strings.Repeat("Answer in as few words as possible. Never add commentary. ", 300)

func anthropicScenarios() []scenario {
	user := func(s string) []llmtypes.ChatMessage { return []llmtypes.ChatMessage{{Role: "user", Content: s}} }
	cached := llmtypes.ChatRequest{MaxTokens: 16, SystemPrompt: cachedSystem, Tools: []llmtypes.ToolDefinition{lookupTool},
		Messages:   user("Say: cached"),
		CacheHints: []llmtypes.CacheHint{{Position: "system", TTL: "1h"}, {Position: "tools", TTL: "1h"}, {Position: "recent_message", Index: 0}}}
	return []scenario{
		{name: "text", request: llmtypes.ChatRequest{MaxTokens: 32, Messages: user("Reply with exactly: pong")}},
		{name: "tool_use", request: llmtypes.ChatRequest{MaxTokens: 128, Tools: []llmtypes.ToolDefinition{lookupTool},
			Messages: user("Call the lookup tool with key \"capital-of-france\". Do not answer yourself.")}},
		{name: "tool_use_max_tokens", request: llmtypes.ChatRequest{MaxTokens: 40, Tools: []llmtypes.ToolDefinition{lookupTool},
			Messages: user("Call the lookup tool once. Its key must be the English alphabet written out three times, separated by spaces.")}},
		{name: "cache_write", request: cached},
		{name: "cache_read", request: cached},
		{name: "closing_tool_choice_none", noTools: true, request: llmtypes.ChatRequest{MaxTokens: 48, Tools: []llmtypes.ToolDefinition{lookupTool},
			Messages: user("The turn reached its limit. Reply in one short sentence. Do not call tools.")}},
	}
}
