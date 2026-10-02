package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/nanite/internal/plugin/subprocess"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
	"github.com/hollis-labs/plugin-sdk/manifest"
	sdkprocess "github.com/hollis-labs/plugin-sdk/subprocess"
)

type manifestToolCaller interface {
	CallTool(context.Context, *sdkprocess.MCPCallRequest) (*sdkprocess.MCPCallResult, error)
}

// manifestPluginTransport discovers only accepted declarations. The child
// cannot add a tool, change its schema, or override its reviewed effect.
type manifestPluginTransport struct {
	caller       manifestToolCaller
	declarations []Tool
	names        map[string]bool
	consumer     subprocess.EnvelopeConsumer
}

func newManifestPluginTransport(caller manifestToolCaller, declarations []manifest.Tool, consumer subprocess.EnvelopeConsumer) (*manifestPluginTransport, error) {
	if caller == nil {
		return nil, fmt.Errorf("plugin tools require a subprocess caller")
	}
	if err := pluginapi.ValidateAgentTools(declarations); err != nil {
		return nil, err
	}
	transport := &manifestPluginTransport{caller: caller, names: make(map[string]bool), consumer: consumer}
	for _, declaration := range declarations {
		if transport.names[declaration.Name] {
			return nil, fmt.Errorf("duplicate declared tool %q", declaration.Name)
		}
		read, destructive, err := pluginapi.ToolEffectHints(declaration.Effect)
		if err != nil {
			return nil, err
		}
		var schema map[string]any
		if schemaErr := json.Unmarshal(declaration.InputSchema, &schema); schemaErr != nil || schema["type"] != "object" {
			return nil, fmt.Errorf("tool %q requires an object input schema", declaration.Name)
		}
		tool := Tool{Name: declaration.Name, Description: declaration.Description, InputSchema: schema, Annotations: map[string]any{"readOnlyHint": read, "destructiveHint": destructive}}
		if problems := ValidateToolMeta(TierPluginStdio, tool); len(problems) != 0 {
			return nil, fmt.Errorf("tool %q has invalid metadata: %v", declaration.Name, problems)
		}
		transport.names[declaration.Name] = true
		transport.declarations = append(transport.declarations, tool)
	}
	return transport, nil
}

func (p *manifestPluginTransport) ListTools(ctx context.Context) ([]Tool, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Do not return mutable maps belonging to the accepted declaration.
	raw, err := json.Marshal(p.declarations)
	if err != nil {
		return nil, err
	}
	var snapshot []Tool
	if decodeErr := json.Unmarshal(raw, &snapshot); decodeErr != nil {
		return nil, decodeErr
	}
	return snapshot, nil
}

func (p *manifestPluginTransport) CallTool(ctx context.Context, name string, args map[string]any) (*ToolResult, error) {
	if !p.names[name] {
		return nil, fmt.Errorf("undeclared plugin tool %q", name)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sessionID := SessionIDFromContext(ctx)
	result, err := p.caller.CallTool(ctx, &sdkprocess.MCPCallRequest{ToolName: name, Arguments: args, SessionID: sessionID})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("plugin tool returned no result")
	}
	if len(result.Content) > LimitsFor(TierPluginStdio).MaxResultBytes {
		return nil, fmt.Errorf("plugin tool result exceeds response limit")
	}
	out := &ToolResult{IsError: result.IsError}
	// SDK output may be text, structured JSON or an MCP content array. Preserve
	// structured output as one text block for Nanite's existing tool processor.
	if len(result.Content) != 0 && string(result.Content) != "null" {
		var blocks []json.RawMessage
		if json.Unmarshal(result.Content, &blocks) == nil && blocks != nil {
			for _, raw := range blocks {
				var block ToolContent
				if json.Unmarshal(raw, &block) == nil && block.Type == "text" {
					out.Content = append(out.Content, block)
				} else {
					out.Content = append(out.Content, ToolContent{Type: "text", Text: string(raw)})
				}
			}
		} else {
			var text string
			if json.Unmarshal(result.Content, &text) != nil {
				text = string(result.Content)
			}
			out.Content = []ToolContent{{Type: "text", Text: text}}
		}
	}
	if sessionID != "" && p.consumer != nil && len(result.Envelopes) != 0 {
		p.consumer.Deliver(sessionID, result.Envelopes)
	}
	return out, nil
}
