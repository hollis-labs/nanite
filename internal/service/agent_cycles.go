package service

import "context"

type agentCycleKindContextKey struct{}

// WithAgentCycleKindForAPI annotates API-originated turns with an optional
// durable-agent cycle kind. Nanite's current runtime does not yet consume the
// value, but the control-plane contract preserves it for future use.
func WithAgentCycleKindForAPI(ctx context.Context, kind string) context.Context {
	if kind == "" {
		return ctx
	}
	return context.WithValue(ctx, agentCycleKindContextKey{}, kind)
}
