package events

import (
	"context"
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
