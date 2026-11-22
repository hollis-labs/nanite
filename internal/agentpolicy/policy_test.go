package agentpolicy

import (
	"context"
	"errors"
	"strings"
	"testing"

	shared "github.com/hollis-labs/substrate/agent/reflexes"
	"github.com/hollis-labs/substrate/mesh/agentdef"
)

func TestNativeNegotiationAndExplicitZero(t *testing.T) {
	data := []byte(`---
schema_version: "2"
definition_id: def:policy-test
revision: "1"
name: policy-test
description: Test native behavior
behavior:
  purpose: Exercise native behavior
requirements: {}
harness_profile:
  context: {}
  permissions:
    profile: default
continuity:
  mode: ephemeral
extensions:
  com.hollislabs.nanite/native-policy:
    version: "1"
    area: harness_profile
    mandatory: true
    data:
      class: process
      auto_recall:
        enabled: false
        min_confidence: 0
---
Perform the requested work.
`)
	if _, err := agentdef.Parse(data); err == nil {
		t.Fatal("unnegotiated mandatory extension admitted")
	}
	d, err := agentdef.Parse(data, Option())
	if err != nil {
		t.Fatal(err)
	}
	p, err := DecodeNative(d.Extensions[NativeNamespace])
	if err != nil {
		t.Fatal(err)
	}
	p = p.Defaults()
	if p.Class != "process" || *p.AutoRecall.Enabled || *p.AutoRecall.MinConfidence != 0 || p.WriteClaimGuard != "deny" || p.SubagentCompletionPolicy != "render_and_wait" {
		t.Fatalf("incorrect resolved behavior: %+v", p)
	}
	for _, change := range [][2]string{{`version: "1"`, `version: "2"`}, {`class: process`, `model: unauthorized`}, {`enabled: false`, `enabled: null`}, {`min_confidence: 0`, `min_confidence: 2`}, {`class: process`, `class: harness`}, {`area: harness_profile`, `area: behavior`}} {
		if _, err := agentdef.Parse([]byte(strings.Replace(string(data), change[0], change[1], 1)), Option()); err == nil {
			t.Fatalf("unsupported semantics admitted: %v", change)
		}
	}
}

func TestPinnedReflexBehaviorAndRefusals(t *testing.T) {
	data := []byte(`{"version":"1","rules":[{"id":"remind","name":"Reminder","priority":10,"trigger":{"kind":"event","spec":{"name":"tool_failed"}},"action":{"kind":"inject_reminder","body":"Inspect the tool failure"}},{"id":"stop","name":"Stop","priority":1,"trigger":{"kind":"predicate","spec":{"kind":"AND","clauses":[{"kind":"tool_calls_window","window":2,"op":"=","value":0},{"kind":"identical_output_window","window":2}]}},"action":{"kind":"halt_session","reason":"Repeated output without progress"}}]}`)
	pin := agentdef.Ref{URI: "resource:reflex.json", Digest: agentdef.ArtifactDigest(data)}
	b, err := ParseReflexBundle(data, pin)
	if err != nil {
		t.Fatal(err)
	}
	state := shared.State{Events: []shared.EventSignal{{EventType: "tool_failed"}}, Messages: []shared.MessageSignal{{Content: "same"}, {Content: "same"}}}
	fired, err := b.Evaluate(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if len(fired) != 1 || fired[0].ID != "stop" {
		t.Fatalf("deny override failed: %+v", fired)
	}
	state.Messages = []shared.MessageSignal{{Content: "healthy"}}
	fired, err = b.Evaluate(context.Background(), state)
	if err != nil || len(fired) != 1 || fired[0].ID != "remind" {
		t.Fatalf("cold predicate or reminder behavior: %+v %v", fired, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = b.Evaluate(ctx, state); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	if _, err = ParseReflexBundle(append(data, ' '), pin); err == nil {
		t.Fatal("changed bundle accepted under old content pin")
	}
	for _, changed := range []string{
		strings.Replace(string(data), `"inject_reminder"`, `"send_message"`, 1),
		strings.Replace(string(data), `"name":"tool_failed"`, `"name":"tool_failed","command":"unapproved"`, 1),
		strings.Replace(string(data), `"window":2`, `"window":2.5`, 1),
		strings.Replace(string(data), `"version":"1"`, `"version":"1","version":"2"`, 1),
		strings.Replace(string(data), `"kind":"AND"`, `"kind":"AND","clauses":null`, 1),
	} {
		candidate := []byte(changed)
		pin.Digest = agentdef.ArtifactDigest(candidate)
		if _, err = ParseReflexBundle(candidate, pin); err == nil {
			t.Fatalf("unsupported reflex admitted: %s", changed)
		}
	}
}
