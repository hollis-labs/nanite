package openai

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

// TestRecordFixtures records the OpenAI Chat Completions fixtures in
// testdata from a live, Warden-authenticated gateway. It is skipped unless
// NANITE_RECORD_FIXTURES=1; normal runs and CI only replay. It needs
// NANITE_RECORD_WARDEN_URL, NANITE_RECORD_CREDENTIAL (one process at a time:
// it rotates on each mint), NANITE_RECORD_PRINCIPAL,
// NANITE_RECORD_OPENAI_URL (the OpenAI wire base, with /v1) and
// NANITE_RECORD_OPENAI_MODEL. NANITE_RECORD_ONLY limits it to the named
// scenarios. No endpoint, alias or credential is written to testdata.
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
	model := env("NANITE_RECORD_OPENAI_MODEL")
	src, err := warden.New(warden.Config{URL: env("NANITE_RECORD_WARDEN_URL"), CredentialFile: env("NANITE_RECORD_CREDENTIAL"),
		Principal: env("NANITE_RECORD_PRINCIPAL"), Purpose: "Record Nanite provider test fixtures", FailFastGrants: true})
	if err != nil {
		t.Fatal(err)
	}
	rec := &providertest.Recorder{Scrub: func(s string) string { return strings.ReplaceAll(s, model, recordedModel) }}
	c := New("unused", &http.Client{Transport: rec, Timeout: 2 * time.Minute})
	c.SetAPI(APIChat)
	c.SetGateway(station.Settings{BaseURL: env("NANITE_RECORD_OPENAI_URL"), Bearer: src.Bearer("llm-gateway", []string{"llm-gateway.chat"})})

	only := map[string]bool{}
	for _, name := range strings.Split(os.Getenv("NANITE_RECORD_ONLY"), ",") {
		if name != "" {
			only[name] = true
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	for _, s := range openAIScenarios() {
		if len(only) > 0 && !only[s.name] {
			continue
		}
		rctx := ctx
		if s.noTools {
			rctx = toolchoice.WithNone(ctx)
		}
		req := s.request
		req.Model = model
		stream, err := c.StreamChat(rctx, req)
		if err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		for ev := range stream {
			if ev.Type == llmtypes.EventError {
				t.Fatalf("%s: %s", s.name, ev.Error)
			}
		}
		taken := rec.Take()
		if len(taken) != 1 {
			t.Fatalf("%s: %d exchanges", s.name, len(taken))
		}
		providertest.CheckClean(t, s.name, taken[0])
		if err := providertest.Save(filepath.Join("testdata", "replay", s.name+".json"), taken[0]); err != nil {
			t.Fatal(err)
		}
	}
}

type scenario struct {
	name    string
	request llmtypes.ChatRequest
	noTools bool
}

var lookupTool = llmtypes.ToolDefinition{Name: "lookup", Description: "Look up a fact by its key.",
	InputSchema: map[string]any{"type": "object", "properties": map[string]any{"key": map[string]any{"type": "string"}}, "required": []any{"key"}}}

func openAIScenarios() []scenario {
	user := func(s string) []llmtypes.ChatMessage { return []llmtypes.ChatMessage{{Role: "user", Content: s}} }
	return []scenario{
		{name: "text", request: llmtypes.ChatRequest{MaxTokens: 400, Messages: user("Reply with exactly: pong")}},
		{name: "tool_use", request: llmtypes.ChatRequest{MaxTokens: 800, Tools: []llmtypes.ToolDefinition{lookupTool},
			Messages: user("Call the lookup tool with key \"capital-of-france\". Do not answer yourself.")}},
		{name: "closing_tool_choice_none", noTools: true, request: llmtypes.ChatRequest{MaxTokens: 400, Tools: []llmtypes.ToolDefinition{lookupTool},
			Messages: user("The turn reached its limit. Reply in one short sentence. Do not call tools.")}},
	}
}
