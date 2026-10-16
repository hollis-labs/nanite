package toolclient

import "context"

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
