package toolclient

import (
	"context"

	llmtypes "github.com/hollis-labs/substrate/llm-core/llmtypes"
)

// NativeToolGate decides, call by call, whether a turn of a Nanite-hosted native
// view may run a tool. It stands in for the fabric-actor grant check in
// ToolClient.CallTool, and only for a call whose context carries one. A native
// view is not a fabric actor, so GetAgentForActor / ListAgentToolNames cannot
// answer for it. The gate is installed only by internal/service, from a live
// execution capability (TestNativeToolGateIsInstalledOnlyByTheChatService pins
// that); every other caller still goes through the fabric check.
type NativeToolGate interface {
	AuthorizeNativeTool(ctx context.Context, toolName string) (allowed bool, reason string)
}

type nativeToolGateKey struct{}

// WithNativeToolGate returns a context whose tool calls are authorized by g.
func WithNativeToolGate(ctx context.Context, g NativeToolGate) context.Context {
	return context.WithValue(ctx, nativeToolGateKey{}, g)
}

func nativeToolGateFrom(ctx context.Context) (NativeToolGate, bool) {
	g, ok := ctx.Value(nativeToolGateKey{}).(NativeToolGate)
	return g, ok && g != nil
}

// OmitUnavailableDevTools drops dev_* tools when developer mode is off, the
// same selection-time filter ordinary agents get, so a native turn is not
// offered a tool that CallTool would refuse.
func (tb *ToolClient) OmitUnavailableDevTools(tools []llmtypes.ToolDefinition) []llmtypes.ToolDefinition {
	if tb.developerModeEnabled() {
		return tools
	}
	out := make([]llmtypes.ToolDefinition, 0, len(tools))
	for _, t := range tools {
		if !isDevTool(t.Name) {
			out = append(out, t)
		}
	}
	return out
}
