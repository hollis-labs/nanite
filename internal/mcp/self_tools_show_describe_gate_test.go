package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/learnings"
	"github.com/hollis-labs/nanite/internal/memory"
)

// fixedLearningStore is a hermetic learnings.LearningStore that returns a
// fixed list of memories on Recall. Store is a no-op — the describe gate
// doesn't write through this path.
//
// Using the real *learnings.Recaller against this stub keeps the test on
// the production code path (RecallByToolName + tag filtering) while
// staying hermetic. Mirrors the pattern in internal/learnings/learnings_test.go's stubStore.
type fixedLearningStore struct {
	mems []fixedMemory
}

// fixedMemory is the bag of fields the test pins; it converts to
// memory.Memory in Recall below.
type fixedMemory struct {
	Summary    string
	Tags       []string
	Confidence float64
	MemoryKey  string
	Namespace  string
}

func (s *fixedLearningStore) Store(_ context.Context, _ memory.Memory) error {
	return nil
}

func (s *fixedLearningStore) Recall(_ context.Context, _ memory.RecallOpts) ([]memory.Memory, error) {
	out := make([]memory.Memory, 0, len(s.mems))
	for _, m := range s.mems {
		out = append(out, memory.Memory{
			Namespace:  m.Namespace,
			MemoryKey:  m.MemoryKey,
			Summary:    m.Summary,
			Tags:       m.Tags,
			Confidence: m.Confidence,
		})
	}
	return out, nil
}

// enableDescribeGate flips the env toggle ON for the current test. The
// global newSelfTools helper turns it OFF by default; gate tests opt back
// in here so production behavior (gate ON) is exercised.
func enableDescribeGate(t *testing.T) {
	t.Helper()
	t.Setenv("NANITE_REQUIRE_DESCRIBE_FOR_SHOW_CARD", "1")
}

// TestCallShowCard_DescribeGate_RejectsWhenMissingDescribeAndNoLearning is
// the c112 regression: env on, no nanite_tool_describe in the turn-tool
// names, no learning → describe_required JSON in the error result text.
func TestCallShowCard_DescribeGate_RejectsWhenMissingDescribeAndNoLearning(t *testing.T) {
	st := newSelfTools(t)
	enableDescribeGate(t) // overrides newSelfTools' default-OFF Setenv

	// ctx carries a turn-tool-names set that does NOT include
	// nanite_tool_describe — only data tools the agent ran before
	// jumping to show_card.
	ctx := WithTurnToolNames(context.Background(), []string{"clockwork_task_list", "context_search"})

	args := map[string]any{
		"type":    "report-card",
		"data":    cloneMap(validShowCardPayloads["report-card"]),
		"sources": validSources,
	}
	res, err := st.callShowCard(ctx, args)
	if err != nil {
		t.Fatalf("callShowCard returned transport error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected describe_required rejection, got success: %s", readToolText(t, res))
	}
	body := readToolText(t, res)

	// The error text must be the JSON envelope, parseable.
	var parsed map[string]any
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("error body should be JSON, got %q (parse err %v)", body, err)
	}
	if got := parsed["error"]; got != "describe_required" {
		t.Errorf("expected error=describe_required, got %v (body=%s)", got, body)
	}
	if got := parsed["type"]; got != "report-card" {
		t.Errorf("expected type=report-card, got %v", got)
	}
	hint, _ := parsed["hint"].(string)
	if !strings.Contains(hint, "nanite_tool_describe") {
		t.Errorf("hint should reference nanite_tool_describe: %q", hint)
	}
	if !strings.Contains(hint, "report-card") {
		t.Errorf("hint should reference the requested type 'report-card': %q", hint)
	}
	dca, ok := parsed["describe_call_args"].(map[string]any)
	if !ok {
		t.Fatalf("describe_call_args missing or not an object: %v", parsed)
	}
	if dca["name"] != "nanite_show_card" {
		t.Errorf("describe_call_args.name should be nanite_show_card, got %v", dca["name"])
	}
}

// TestCallShowCard_DescribeGate_AcceptsWhenDescribeCalledThisTurn pins the
// happy path: ctx carries nanite_tool_describe in the turn-tool-names set
// → gate passes, the call proceeds to per-type validation and emits an
// envelope.
func TestCallShowCard_DescribeGate_AcceptsWhenDescribeCalledThisTurn(t *testing.T) {
	st := newSelfTools(t)
	enableDescribeGate(t) // overrides newSelfTools' default-OFF Setenv

	ctx := WithTurnToolNames(context.Background(), []string{"nanite_tool_describe", "clockwork_task_list"})

	args := map[string]any{
		"type":    "report-card",
		"data":    cloneMap(validShowCardPayloads["report-card"]),
		"sources": validSources,
	}
	res, err := st.callShowCard(ctx, args)
	if err != nil {
		t.Fatalf("callShowCard returned transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success when nanite_tool_describe was in turn names, got error: %s", readToolText(t, res))
	}
	env := extractEnvelopeJSON(t, readToolText(t, res))
	if got := env["type"]; got != "report-card" {
		t.Fatalf("envelope type: want report-card got %v", got)
	}
}

// TestCallShowCard_DescribeGate_AcceptsWhenLearningMentionsType pins the
// second escape hatch: a Vanta-recalled learning whose Summary mentions
// the requested envelope type lets the call through even without
// describe-this-turn. Uses a stub LearningStore so the gate exercises the
// real RecallByToolName code path.
func TestCallShowCard_DescribeGate_AcceptsWhenLearningMentionsType(t *testing.T) {
	st := newSelfTools(t)
	enableDescribeGate(t) // overrides newSelfTools' default-OFF Setenv

	// Inject a learning that mentions "report-card" in its summary.
	st.LearningRecaller = learnings.NewRecaller(&fixedLearningStore{
		mems: []fixedMemory{
			{
				Summary:    "When emitting report-card, populate metrics with label/value pairs.",
				Tags:       []string{"learning", "tool:nanite_show_card"},
				Confidence: 0.9,
				MemoryKey:  "test-memory-1",
				Namespace:  "user/default/tool_use/nanite_show_card",
			},
		},
	})

	// No describe in the turn names — the learning has to carry the
	// gate by itself.
	ctx := WithTurnToolNames(context.Background(), []string{"clockwork_task_list"})

	args := map[string]any{
		"type":    "report-card",
		"data":    cloneMap(validShowCardPayloads["report-card"]),
		"sources": validSources,
	}
	res, err := st.callShowCard(ctx, args)
	if err != nil {
		t.Fatalf("callShowCard returned transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success with learning mentioning report-card, got: %s", readToolText(t, res))
	}
}

// TestCallShowCard_DescribeGate_RejectsWhenLearningMissesType is the
// inverse: a learning is present but the Summary doesn't mention the
// requested type → gate still fires.
func TestCallShowCard_DescribeGate_RejectsWhenLearningMissesType(t *testing.T) {
	st := newSelfTools(t)
	enableDescribeGate(t) // overrides newSelfTools' default-OFF Setenv

	st.LearningRecaller = learnings.NewRecaller(&fixedLearningStore{
		mems: []fixedMemory{
			{
				Summary:    "When emitting metric-card, prefer an absolute value plus optional trend.",
				Tags:       []string{"learning", "tool:nanite_show_card"},
				Confidence: 0.9,
				MemoryKey:  "test-memory-2",
				Namespace:  "user/default/tool_use/nanite_show_card",
			},
		},
	})

	// No describe in the turn names; learning mentions metric-card,
	// but the call is for report-card → gate fires.
	ctx := WithTurnToolNames(context.Background(), []string{"clockwork_task_list"})

	args := map[string]any{
		"type":    "report-card",
		"data":    cloneMap(validShowCardPayloads["report-card"]),
		"sources": validSources,
	}
	res, err := st.callShowCard(ctx, args)
	if err != nil {
		t.Fatalf("callShowCard returned transport error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected describe_required when learning doesn't mention type, got success: %s", readToolText(t, res))
	}
	if !strings.Contains(readToolText(t, res), "describe_required") {
		t.Errorf("error body should be the describe_required JSON: %s", readToolText(t, res))
	}
}

// TestCallShowCard_DescribeGate_DisabledByEnv_AcceptsRegardless asserts
// the env toggle is honored. With NANITE_REQUIRE_DESCRIBE_FOR_SHOW_CARD=0
// the gate is off — even with no describe, no learning, the call
// proceeds. This is the test/CI escape hatch.
func TestCallShowCard_DescribeGate_DisabledByEnv_AcceptsRegardless(t *testing.T) {
	// newSelfTools() already sets the env to "0", but be explicit.
	t.Setenv("NANITE_REQUIRE_DESCRIBE_FOR_SHOW_CARD", "0")
	st := newSelfTools(t)

	// Stamp a turn-names set that does NOT contain describe — the
	// gate would fire if it were on.
	ctx := WithTurnToolNames(context.Background(), []string{"clockwork_task_list"})

	args := map[string]any{
		"type":    "report-card",
		"data":    cloneMap(validShowCardPayloads["report-card"]),
		"sources": validSources,
	}
	res, err := st.callShowCard(ctx, args)
	if err != nil {
		t.Fatalf("callShowCard returned transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success with gate disabled, got error: %s", readToolText(t, res))
	}
}

// TestRequireDescribeForShowCardEnabled_EnvParsing pins the parser for
// the env toggle: default ON, off-values case-insensitive.
func TestRequireDescribeForShowCardEnabled_EnvParsing(t *testing.T) {
	cases := []struct {
		val  string
		want bool
	}{
		{"", true},      // unset → default ON
		{"1", true},     // anything other than off-values is ON
		{"true", true},  // ditto
		{"on", true},    // ditto
		{"yes", true},   // ditto
		{"0", false},    // off-value
		{"false", false},
		{"FALSE", false},
		{"Off", false},
		{"NO", false},
		{"  off  ", false}, // trimmed
	}
	for _, c := range cases {
		t.Run(c.val, func(t *testing.T) {
			t.Setenv("NANITE_REQUIRE_DESCRIBE_FOR_SHOW_CARD", c.val)
			if got := requireDescribeForShowCardEnabled(); got != c.want {
				t.Errorf("env=%q: got %v, want %v", c.val, got, c.want)
			}
		})
	}
}
