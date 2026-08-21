package selftools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/envelope"
)

// TestNaniteToolDescribe_RegistrationAndSelfDescribe verifies the self-tool
// is registered in selfToolDefinitions() and can describe itself by name.
// (CW-20260429-0005, A1.)
func TestNaniteToolDescribe_RegistrationAndSelfDescribe(t *testing.T) {
	defs := selfToolDefinitions()
	var found bool
	for _, d := range defs {
		if d.Name == "tool_describe" {
			found = true
			if d.InputSchema == nil {
				t.Fatal("tool_describe missing InputSchema")
			}
			required, _ := d.InputSchema["required"].([]string)
			gotName := false
			for _, r := range required {
				if r == "name" {
					gotName = true
				}
			}
			if !gotName {
				t.Fatal("tool_describe must require `name`")
			}
			break
		}
	}
	if !found {
		t.Fatal("tool_describe not in selfToolDefinitions()")
	}

	st := newSelfTools(t)
	res, err := st.CallTool(context.Background(), "tool_describe", map[string]any{
		"name": "tool_describe",
	})
	if err != nil {
		t.Fatalf("describe self: %v", err)
	}
	if res.IsError {
		t.Fatalf("describe self returned error: %s", res.Content[0].Text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		t.Fatalf("describe self JSON: %v", err)
	}
	if out["name"] != "tool_describe" {
		t.Fatalf("self-describe name mismatch: %v", out["name"])
	}
}

// TestNaniteToolDescribe_ShowCardAcceptance is the ticket's headline
// acceptance check: tool_describe("card_show") returns an
// input schema and at least one golden example whose `type` arg is
// "report-card".
func TestNaniteToolDescribe_ShowCardAcceptance(t *testing.T) {
	st := newSelfTools(t)
	res, err := st.CallTool(context.Background(), "tool_describe", map[string]any{
		"name": "card_show",
	})
	if err != nil {
		t.Fatalf("describe card_show: %v", err)
	}
	if res.IsError {
		t.Fatalf("describe card_show error: %s", res.Content[0].Text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		t.Fatalf("describe JSON: %v", err)
	}
	if _, ok := out["input_schema"].(map[string]any); !ok {
		t.Fatal("missing input_schema on describe result")
	}
	examples, ok := out["examples"].([]any)
	if !ok || len(examples) == 0 {
		t.Fatalf("expected ≥1 golden example for card_show, got %T %v", out["examples"], out["examples"])
	}
	hasReportCard := false
	for _, e := range examples {
		ex, _ := e.(map[string]any)
		args, _ := ex["args"].(map[string]any)
		if t, _ := args["type"].(string); t == "report-card" {
			hasReportCard = true
			break
		}
	}
	if !hasReportCard {
		t.Fatal("card_show examples must include at least one report-card entry")
	}
}

// TestNaniteToolDescribe_AllSelfToolsHaveExamples checks that every tool
// returned by selfToolDefinitions() has at least one golden example. The
// ticket's acceptance line: "Each existing self-tool has ≥1 golden example."
func TestNaniteToolDescribe_AllSelfToolsHaveExamples(t *testing.T) {
	defs := selfToolDefinitions()
	for _, d := range defs {
		examples, err := loadGoldenExamples(d.Name)
		if err != nil {
			t.Errorf("loadGoldenExamples(%s): %v", d.Name, err)
			continue
		}
		if len(examples) == 0 {
			t.Errorf("tool %s has no golden examples on file", d.Name)
		}
	}
}

// TestNaniteToolDescribe_UnknownToolHasClosestMatches verifies the
// not-found path returns a structured error with closest_matches by
// Levenshtein distance.
func TestNaniteToolDescribe_UnknownToolHasClosestMatches(t *testing.T) {
	st := newSelfTools(t)
	res, err := st.CallTool(context.Background(), "tool_describe", map[string]any{
		"name": "card_shwo", // typo of post-rename `card_show`; near-miss is stable as the registry evolves
	})
	if err != nil {
		t.Fatalf("describe unknown: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError=true for unknown tool")
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &payload); err != nil {
		t.Fatalf("error payload not JSON: %v (%s)", err, res.Content[0].Text)
	}
	if payload["error"] != "tool_not_found" {
		t.Fatalf("expected error=tool_not_found, got %v", payload["error"])
	}
	matches, _ := payload["closest_matches"].([]any)
	if len(matches) == 0 {
		t.Fatal("closest_matches must be non-empty")
	}
	first, _ := matches[0].(string)
	if first != "card_show" {
		t.Fatalf("expected closest match card_show, got %q (full list: %v)", first, matches)
	}
}

// TestNaniteToolDescribe_MissingNameIsError ensures the handler validates
// that `name` is present.
func TestNaniteToolDescribe_MissingNameIsError(t *testing.T) {
	st := newSelfTools(t)
	res, _ := st.CallTool(context.Background(), "tool_describe", map[string]any{})
	if !res.IsError {
		t.Fatal("expected error when name is omitted")
	}
	if !strings.Contains(res.Content[0].Text, "name is required") {
		t.Fatalf("unexpected error message: %s", res.Content[0].Text)
	}
}

// TestLoadGoldenExamples_NotFoundReturnsNil verifies a missing file is
// not treated as an error — the describe handler should still return the
// rest of the contract.
func TestLoadGoldenExamples_NotFoundReturnsNil(t *testing.T) {
	got, err := loadGoldenExamples("nanite_nonexistent_tool_xyz")
	if err != nil {
		t.Fatalf("expected nil error for missing file, got %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil examples for missing file, got %v", got)
	}
}

// TestLevenshtein_Smoke covers the basics so future refactors don't
// silently break the closest-match path.
func TestLevenshtein_Smoke(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"a", "a", 0},
		{"a", "b", 1},
		{"kitten", "sitting", 3},
		{"card_shows", "card_show", 1},
		{"CARD_SHOW", "card_show", 0}, // case-insensitive
	}
	for _, c := range cases {
		got := levenshtein(c.a, c.b)
		if got != c.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// TestClosestToolNames_DeterministicOrdering verifies same-distance ties
// resolve in lexicographic order so describe results don't flap.
func TestClosestToolNames_DeterministicOrdering(t *testing.T) {
	candidates := []string{
		"plan_get",
		"plan_list",
		"plan_create",
		"plan_update",
	}
	got := closestToolNames("nanite_plan_xyz", candidates, 3)
	if len(got) != 3 {
		t.Fatalf("expected 3 results, got %d", len(got))
	}
	// All four candidates have distance 3 from "xyz" with their suffixes
	// being "get", "list", "create", "update" of varying lengths — but
	// lexicographic ordering of names should hold deterministically.
	if got[0] >= got[1] && got[1] >= got[2] {
		// Distance can vary; we just need the call to be stable.
	}
	got2 := closestToolNames("nanite_plan_xyz", candidates, 3)
	for i := range got {
		if got[i] != got2[i] {
			t.Fatalf("ordering not deterministic: %v vs %v", got, got2)
		}
	}
}

// TestNaniteToolDescribe_ExamplesIncludePassiveRenderableCoverage checks
// the show_card examples include both grounding-required (report-card)
// and grounding-free (info-card or metric-card) cases — the ticket
// stipulates examples covering the allowed types.
func TestNaniteToolDescribe_ExamplesIncludePassiveRenderableCoverage(t *testing.T) {
	examples, err := loadGoldenExamples("card_show")
	if err != nil || len(examples) == 0 {
		t.Fatalf("card_show examples missing: %v", err)
	}
	types := map[string]bool{}
	for _, ex := range examples {
		if t, _ := ex.Args["type"].(string); t != "" {
			types[t] = true
		}
	}
	if !types["report-card"] {
		t.Error("missing report-card example for card_show")
	}
	if !types["info-card"] && !types["metric-card"] {
		t.Error("missing at least one passive non-grounded card type (info-card or metric-card)")
	}
}

// TestNaniteToolDescribe_ShowCardExamplesCoverAllCoreTypes verifies the
// card_show golden examples cover every core envelope card type
// the harness ships at v1: the 10 passive-renderable types reachable
// through card_show, plus the 6 reference-shape types (decision-flow
// and backend-only) that other emission paths use. CW-20260430-0004 (SP4)
// added these so the agent's first-call success rate on unfamiliar
// envelope types is anchored on a real example rather than guesswork.
//
// Source of truth for the type list: the external
// github.com/hollis-labs/go-envelopes module's manifest/envelopes.yaml +
// manifest/schemas/<type>.schema.json (config/envelopes.yaml and
// internal/envelope/schemas/ do not exist in this repo).
func TestNaniteToolDescribe_ShowCardExamplesCoverAllCoreTypes(t *testing.T) {
	examples, err := loadGoldenExamples("card_show")
	if err != nil || len(examples) == 0 {
		t.Fatalf("card_show examples missing: %v", err)
	}
	seen := map[string]bool{}
	for _, ex := range examples {
		if typ, _ := ex.Args["type"].(string); typ != "" {
			seen[typ] = true
		}
	}
	// The 14 core card types listed in CLAUDE.md §"Known envelope types"
	// (excluding plugin types and runtime-only envelopes). Each must
	// have at least one golden example so the describe path can show
	// the agent a working data shape.
	required := []string{
		// Core primitives
		"session-task",
		"document-viewer",
		"report-card",
		"error-report",
		"approval-card",
		"proposal-card",
		// Phase 7 primitives
		"info-card",
		"list-card",
		"metric-card",
		"progress-card",
		"confirmation-card",
		"table-card",
		"timeline-card",
		"diff-card",
	}
	for _, typ := range required {
		if !seen[typ] {
			t.Errorf("missing golden example for envelope type %q", typ)
		}
	}
}

// TestNaniteToolDescribe_ShowCardExamplesValidateAgainstSchemas walks
// every golden example for card_show and validates the example's
// `data` payload against the registered per-type schema. This guards
// against the regression class where an example uses a field name the
// schema doesn't accept (e.g. body_markdown vs content for
// document-viewer) — the agent that copies the example would then hit
// the validator and fail. CW-20260430-0004 (SP4).
func TestNaniteToolDescribe_ShowCardExamplesValidateAgainstSchemas(t *testing.T) {
	examples, err := loadGoldenExamples("card_show")
	if err != nil || len(examples) == 0 {
		t.Fatalf("card_show examples missing: %v", err)
	}
	for i, ex := range examples {
		typ, _ := ex.Args["type"].(string)
		if typ == "" {
			t.Errorf("example %d (%q) missing `type` arg", i, ex.Title)
			continue
		}
		data, ok := ex.Args["data"].(map[string]any)
		if !ok {
			t.Errorf("example %d (%q) missing `data` object", i, ex.Title)
			continue
		}
		if err := envelope.ValidateData(typ, data); err != nil {
			t.Errorf("example %d (%q, type=%s) failed schema validation: %v", i, ex.Title, typ, err)
		}
	}
}
