package selftools

import (
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/go-envelopes"
	"github.com/hollis-labs/nanite/internal/chat"
)

func TestWorkToolDescriptionsUseReleasedEnvelopeComposition(t *testing.T) {
	descriptions := make(map[string]string)
	for _, definition := range SelfToolProviderDefinitions() {
		descriptions[definition.Name] = definition.Description
	}

	todoDescription := descriptions["todo_list"]
	for _, required := range []string{"`list-card`", "live todos data source", "Do NOT emit a nanite-envelope block manually"} {
		if !strings.Contains(todoDescription, required) {
			t.Errorf("todo_list description does not state %q", required)
		}
	}

	planDescription := descriptions["plan_create"]
	for _, required := range []string{"emit TWO envelopes", "`list-card`", "`confirmation-card`", "Both cards share the same `plan_id`"} {
		if !strings.Contains(planDescription, required) {
			t.Errorf("plan_create description does not state %q", required)
		}
	}

	registry, err := envelopes.LoadCore(context.Background())
	if err != nil {
		t.Fatalf("load released core envelopes: %v", err)
	}
	chat.InitCoreTypes(registry.Names())
	parsed, _, validationErrors := chat.ParseEnvelopes(planDescription)
	if len(validationErrors) != 0 || len(parsed) != 2 {
		t.Fatalf("parse plan_create envelope instructions: parsed=%+v errors=%+v", parsed, validationErrors)
	}
	byType := make(map[string]chat.Envelope, len(parsed))
	for _, envelope := range parsed {
		if envelope.Kind != "envelope" || envelope.Version != envelopes.ProtocolVersion {
			t.Fatalf("plan_create description uses non-canonical wire header: %+v", envelope)
		}
		if err := registry.ValidateEnvelope(&envelopes.Envelope{
			V: envelope.Version, ID: "self-tool-contract", Type: envelope.Type, Data: envelope.Data,
		}); err != nil {
			t.Fatalf("plan_create %s example fails released schema: %v", envelope.Type, err)
		}
		byType[envelope.Type] = envelope
	}
	listSource := workEnvelopeDataSource(t, byType["list-card"])
	confirmationSource := workEnvelopeDataSource(t, byType["confirmation-card"])
	if listSource["kind"] != "plans" || confirmationSource["kind"] != "plan_approval" {
		t.Fatalf("plan composition data sources = list:%+v confirmation:%+v", listSource, confirmationSource)
	}
	if listSource["plan_id"] == "" || listSource["plan_id"] != confirmationSource["plan_id"] {
		t.Fatalf("plan composition must share a non-empty plan_id: list:%+v confirmation:%+v", listSource, confirmationSource)
	}
}

func workEnvelopeDataSource(t *testing.T, envelope chat.Envelope) map[string]any {
	t.Helper()
	if envelope.Type == "" {
		t.Fatal("required work envelope type is missing")
	}
	source, ok := envelope.Data["data_source"].(map[string]any)
	if !ok {
		t.Fatalf("%s data_source = %#v, want object", envelope.Type, envelope.Data["data_source"])
	}
	return source
}
