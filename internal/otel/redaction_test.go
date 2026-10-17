package otel

import (
	"context"
	"strings"
	"testing"

	spanredaction "github.com/hollis-labs/libs/core/util/otel/redaction"
	"github.com/hollis-labs/nanite/internal/redaction"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestExportRedactsCredentialsAndPIIWithoutChangingSource(t *testing.T) {
	t.Setenv("HOLLIS_OTEL_REDACT_PROMPTS", "true")
	const known = "opaque-fixture-for-export-92837"
	redaction.Sink{}.Add(known)
	input := "diagnostic " + known + " alice@example.com sk-" + strings.Repeat("x", 40)
	source := tracetest.SpanStub{
		Name: input,
		Attributes: []attribute.KeyValue{
			attribute.String("detail", input),
			attribute.StringSlice("details", []string{input, "ordinary text"}),
			attribute.String("api_key", "opaque"),
			attribute.String("gen_ai.content.prompt", "private prompt"),
			attribute.Int("max_tokens", 1024),
		},
		Events: []sdktrace.Event{{Name: input, Attributes: []attribute.KeyValue{attribute.String("detail", input)}},
			{Name: "gen_ai.content.completion", Attributes: []attribute.KeyValue{attribute.String("detail", "private completion")}}},
		Links:  []sdktrace.Link{{Attributes: []attribute.KeyValue{attribute.String("detail", input)}}},
		Status: sdktrace.Status{Code: codes.Error, Description: input},
	}.Snapshot()
	next := tracetest.NewInMemoryExporter()
	exporter := spanredaction.Exporter(next, spanRedactionOptions()...)
	if err := exporter.ExportSpans(context.Background(), []sdktrace.ReadOnlySpan{source}); err != nil {
		t.Fatal(err)
	}
	got := next.GetSpans()[0]
	assertClean := func(value string) {
		t.Helper()
		for _, secret := range []string{known, "alice@example.com", "sk-" + strings.Repeat("x", 40)} {
			if strings.Contains(value, secret) {
				t.Fatal("export retained sensitive text")
			}
		}
		if !strings.Contains(value, "diagnostic") {
			t.Fatal("export discarded non-sensitive diagnostic text")
		}
	}
	assertClean(got.Name)
	assertClean(got.Status.Description)
	assertClean(got.Events[0].Name)
	assertClean(got.Events[0].Attributes[0].Value.AsString())
	assertClean(got.Links[0].Attributes[0].Value.AsString())
	for _, attr := range got.Attributes {
		switch attr.Key {
		case "api_key", "gen_ai.content.prompt":
			t.Fatalf("sensitive attribute retained: %s", attr.Key)
		case "detail":
			assertClean(attr.Value.AsString())
		case "details":
			assertClean(attr.Value.AsStringSlice()[0])
			if attr.Value.AsStringSlice()[1] != "ordinary text" {
				t.Fatal("ordinary text changed")
			}
		case "max_tokens":
			if attr.Value.AsInt64() != 1024 {
				t.Fatal("token count changed")
			}
		}
	}
	if len(got.Events[1].Attributes) != 0 {
		t.Fatal("prompt policy did not drop completion event attributes")
	}
	if source.Name() != input || source.Attributes()[0].Value.AsString() != input || source.Events()[0].Name != input {
		t.Fatal("export mutated the original span")
	}
}

func TestExportPromptOptInStillRedactsSensitiveValues(t *testing.T) {
	t.Setenv("HOLLIS_OTEL_REDACT_PROMPTS", "false")
	next := tracetest.NewInMemoryExporter()
	exporter := spanredaction.Exporter(next, spanRedactionOptions()...)
	source := tracetest.SpanStub{Attributes: []attribute.KeyValue{
		attribute.String("gen_ai.content.prompt", "contact alice@example.com"),
		attribute.String("gen_ai.content.completion", "ordinary response"),
	}}.Snapshot()
	if err := exporter.ExportSpans(context.Background(), []sdktrace.ReadOnlySpan{source}); err != nil {
		t.Fatal(err)
	}
	attrs := next.GetSpans()[0].Attributes
	if len(attrs) != 2 || attrs[0].Key != "gen_ai.content.prompt" || attrs[0].Value.AsString() != "contact [REDACTED]" || attrs[1].Value.AsString() != "ordinary response" {
		t.Fatalf("prompt opt-in lost or PII retained: %v", attrs)
	}
}
