package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/selftools"
	"github.com/hollis-labs/nanite/internal/toolclient"
)

// Reproduce an initial surface with discovery tools but no file-read
// schema. All discovery and execution use the real service and MCP stack.
type discoveryInitialSurface struct {
	ToolService
	progressive bool
}

func (s discoveryInitialSurface) SelectForAgent(context.Context, string, string, string, string, int) (*ToolSelection, error) {
	return &ToolSelection{Tools: []llmtypes.ToolDefinition{
		toolclient.RequestToolsMetaTool(), {Name: "tool_describe", Description: "Describe a registered tool.", InputSchema: map[string]any{"type": "object"}},
	}, Progressive: s.progressive}, nil
}

func TestChatDiscovery_DescribeLoadAndExecute(t *testing.T) {
	for _, progressive := range []bool{false, true} {
		name := "ordinary"
		if progressive {
			name = "progressive"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "orientation.txt")
			const content = "portfolio orientation verified"
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			f := newCharacterizationFixture(t, []characterizationProviderStep{
				{events: toolTurnEvents(llmtypes.ToolUseBlock{ID: "describe", Name: "tool_describe", Input: map[string]any{"name": "dev_read"}})},
				{events: toolTurnEvents(llmtypes.ToolUseBlock{ID: "load", Name: "request_tools", Input: map[string]any{"tool_names": []any{"dev_read", "dev_write"}}})},
				{events: toolTurnEvents(llmtypes.ToolUseBlock{ID: "read", Name: "dev_read", Input: map[string]any{"path": path}})},
				{events: doneEvents("Read the orientation file.")},
			})
			agent := f.svc.agents.(*characterizationAgents).agent
			agent.SystemPrompt = "You are the portfolio assistant.\n" + strings.Repeat("Keep the operator's instructions available throughout this session.\n", 200)
			if err := f.st.CreateAgent(ctx, agent); err != nil {
				t.Fatal(err)
			}
			f.context.inner = NewContextService(ContextServiceConfig{Client: chat.NewContextClient(f.st), SlotStasher: &fakeArtifactStasher{}})
			mgr := mcp.NewManager()
			self := selftools.NewSelfToolsTransport(f.st)
			self.Inventory = mgr
			if err := mgr.AddServer("self", self, mcp.TierBuiltin); err != nil {
				t.Fatal(err)
			}
			if err := mgr.AddServer("dev", mcp.NewDevToolsTransport([]string{filepath.Dir(path)}), mcp.TierBuiltin); err != nil {
				t.Fatal(err)
			}
			if err := mgr.DiscoverTools(ctx); err != nil {
				t.Fatal(err)
			}
			for _, toolName := range []string{"tool_describe", "request_tools", "dev_read", "dev_write"} {
				id, err := f.st.UpsertKnownTool(ctx, toolName, "builtin", "available", "")
				if err != nil {
					t.Fatal(err)
				}
				if toolName != "dev_write" {
					if err := f.st.GrantAgentTool(ctx, agent.ID, id, "explicit"); err != nil {
						t.Fatal(err)
					}
				}
			}
			tc := toolclient.New(mgr, f.st, nil)
			tc.DeveloperModeFunc = func() bool { return true }
			f.svc.tools = discoveryInitialSurface{ToolService: NewToolService(tc, mgr, f.st), progressive: progressive}
			events := f.run(t, "discovery-response")
			readSucceeded := false
			for _, event := range events {
				if event.Type == "tool_result" && event.IsError {
					t.Fatalf("tool failed: %s: %s", event.Tool, event.Summary)
				}
				if event.Type == "tool_result" && event.Tool == "dev_read" && strings.Contains(event.Summary, content) {
					readSucceeded = true
				}
			}
			if !readSucceeded {
				t.Fatalf("loaded file-read tool did not execute successfully: %+v", events)
			}
			requests := f.provider.requestsSnapshot()
			loadedOnWire := false
			for _, request := range requests {
				if containsToolNamed(request.Tools, "dev_write") {
					t.Fatal("ungranted tool leaked into a provider request")
				}
				if containsToolNamed(request.Tools, "dev_read") {
					loadedOnWire = true
				}
				promptOnWire := false
				for _, block := range request.SlotBlocks {
					if block.Name == "agent" && strings.Contains(block.Content, agent.SystemPrompt) {
						promptOnWire = true
					}
				}
				if !promptOnWire {
					t.Fatal("provider request lost the full agent prompt")
				}
			}
			if !loadedOnWire {
				t.Fatal("request_tools reported success but never supplied the schema to the provider")
			}
		})
	}
}
