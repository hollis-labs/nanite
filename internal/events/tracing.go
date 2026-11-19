package events

import (
	"context"
	"github.com/hollis-labs/libs/agent/trace"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
	"os"

	hotel "github.com/hollis-labs/libs/core/util/otel"
	"github.com/hollis-labs/libs/core/util/otel/genai"
)

func TurnSpan(ctx context.Context) (context.Context, func()) {
	if os.Getenv("NANITE_OTEL_DISABLED") == "1" {
		return ctx, func() {}
	}
	ctx, span := hotel.StartSpan(ctx, "nanite.turn")
	return ctx, func() { span.End() }
}

func ModelSpan(ctx context.Context, model string) (context.Context, func()) {
	if os.Getenv("NANITE_OTEL_DISABLED") == "1" {
		return ctx, func() {}
	}
	ctx, span := genai.ModelCallSpan(ctx, model, "chat")
	return ctx, func() { span.End() }
}

func ToolSpan(ctx context.Context, tool string) (context.Context, func()) {
	if os.Getenv("NANITE_OTEL_DISABLED") == "1" {
		return ctx, func() {}
	}
	ctx, span := hotel.ToolCallSpan(ctx, tool)
	return ctx, func() { span.End() }
}

// SetTurnAttributes records a turn's trace summary on the span in ctx.
func SetTurnAttributes(ctx context.Context, attrs []trace.Attribute) {
	span := oteltrace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}
	kv := make([]attribute.KeyValue, 0, len(attrs))
	for _, a := range attrs {
		switch v := a.Value.(type) {
		case string:
			kv = append(kv, attribute.String(a.Key, v))
		case int:
			kv = append(kv, attribute.Int(a.Key, v))
		case []string:
			kv = append(kv, attribute.StringSlice(a.Key, v))
		}
	}
	span.SetAttributes(kv...)
}
