// Package toolchoice carries per-request tool selection to Nanite's adapters.
package toolchoice

import "context"

type noneKey struct{}

// WithNone retains tool schemas for history and caching but disables new calls.
func WithNone(ctx context.Context) context.Context { return context.WithValue(ctx, noneKey{}, true) }

// IsNone reports whether this request must prohibit new tool calls.
func IsNone(ctx context.Context) bool { none, _ := ctx.Value(noneKey{}).(bool); return none }
