package selftools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// validateResp mirrors the shape callValidate emits so tests can assert
// against it without re-marshaling the raw text payload by hand.
type validateResp struct {
	Valid  bool                  `json:"valid"`
	Errors []validateRespErrItem `json:"errors"`
}

type validateRespErrItem struct {
	Path       string `json:"path"`
	Reason     string `json:"reason"`
	Suggestion string `json:"suggestion,omitempty"`
}

// callValidateForTest is a small helper that invokes tool_validate via
// the public CallTool dispatch path and unmarshals the result.
func callValidateForTest(t *testing.T, st *SelfToolsTransport, args map[string]any) validateResp {
	t.Helper()
	res, err := st.CallTool(context.Background(), "tool_validate", args)
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if res == nil {
		t.Fatalf("nil ToolResult from CallTool")
	}
	if res.IsError {
		t.Fatalf("expected IsError=false, got error result: %+v", res)
	}
	if len(res.Content) == 0 || res.Content[0].Type != "text" {
		t.Fatalf("expected one text content block, got: %+v", res.Content)
	}
	var out validateResp
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		t.Fatalf("unmarshal validateResp: %v\nbody: %s", err, res.Content[0].Text)
	}
	return out
}

// TestValidate_ShowCard_ValidReportCard is the ticket's positive
// acceptance check: a well-formed report-card payload validates clean.
func TestValidate_ShowCard_ValidReportCard(t *testing.T) {
	st := newSelfTools(t)
	resp := callValidateForTest(t, st, map[string]any{
		"tool_name": "card_show",
		"args": map[string]any{
			"type": "report-card",
			"data": map[string]any{
				"title":   "X",
				"metrics": []any{map[string]any{"label": "Y", "value": "Z"}},
			},
		},
	})
	if !resp.Valid {
		t.Fatalf("expected valid=true, got valid=false errors=%+v", resp.Errors)
	}
	if len(resp.Errors) != 0 {
		t.Errorf("expected no errors, got: %+v", resp.Errors)
	}
}

// TestValidate_ShowCard_AdditionalPropsRejected pins the ticket's
// negative acceptance check: passing `data: {sections: [...]}` produces
// a structured error rooted at /data with a fix suggestion.
func TestValidate_ShowCard_AdditionalPropsRejected(t *testing.T) {
	st := newSelfTools(t)
	resp := callValidateForTest(t, st, map[string]any{
		"tool_name": "card_show",
		"args": map[string]any{
			"type": "report-card",
			"data": map[string]any{
				"sections": []any{},
			},
		},
	})
	if resp.Valid {
		t.Fatalf("expected valid=false, got valid=true")
	}
	if len(resp.Errors) == 0 {
		t.Fatalf("expected at least one error")
	}
	// At least one error rooted at /data (the deep-validation pass), and
	// at least one mentioning additionalProperties with a suggestion.
	gotData := false
	gotAddl := false
	for _, e := range resp.Errors {
		if strings.HasPrefix(e.Path, "/data") {
			gotData = true
		}
		if strings.Contains(e.Reason, "additional propert") && e.Suggestion != "" {
			gotAddl = true
			for _, key := range []string{"title", "metrics"} {
				if !strings.Contains(e.Suggestion, key) {
					t.Errorf("suggestion %q missing allowed key %q", e.Suggestion, key)
				}
			}
		}
	}
	if !gotData {
		t.Errorf("expected at least one error rooted at /data, got: %+v", resp.Errors)
	}
	if !gotAddl {
		t.Errorf("expected an additionalProperties error with a suggestion, got: %+v", resp.Errors)
	}
}

// TestValidate_ShowCard_AllPassiveRenderables walks the v1 allow-list
// and confirms each type validates clean against a known-good payload.
// Detects drift between the allow-list and the schema set in one pass.
//
// `artifact-mini` is intentionally absent: it is on
// envelope.PassiveRenderableTypes (the IsPassiveRenderable Go gate) but
// is NOT listed in the card_show top-level input-schema enum at
// internal/mcp/self_tools.go. That asymmetry is a pre-existing
// inconsistency outside this ticket's scope (B1 — CW-20260429-0006);
// surfacing here as a documented mismatch rather than masking it. The
// envelope-only path is exercised by
// TestValidateEnvelopeData_AllPassiveRenderables_HappyPaths in the
// envelope package, which covers artifact-mini.
func TestValidate_ShowCard_AllPassiveRenderables(t *testing.T) {
	st := newSelfTools(t)
	cases := map[string]map[string]any{
		"document-viewer": {"title": "doc", "content": "hello"},
		"report-card":     {"title": "x", "metrics": []any{map[string]any{"label": "y", "value": "z"}}},
		"info-card":       {"title": "x", "body": "y"},
		"list-card":       {"items": []any{map[string]any{"label": "x"}}},
		"metric-card":     {"label": "x", "value": "y"},
		"progress-card":   {"title": "x", "progress": 50},
		"table-card":      {"columns": []any{map[string]any{"key": "k", "label": "L"}}, "rows": []any{map[string]any{"k": "v"}}},
		"timeline-card":   {"events": []any{map[string]any{"label": "e", "timestamp": "2026-04-29T00:00:00Z"}}},
		"diff-card":       {"before": map[string]any{"label": "a", "content": "x"}, "after": map[string]any{"label": "b", "content": "y"}},
	}
	for envType, data := range cases {
		envType, data := envType, data
		t.Run(envType, func(t *testing.T) {
			resp := callValidateForTest(t, st, map[string]any{
				"tool_name": "card_show",
				"args": map[string]any{
					"type": envType,
					"data": data,
				},
			})
			if !resp.Valid {
				t.Fatalf("expected valid=true for %q, got errors: %+v", envType, resp.Errors)
			}
		})
	}
}

// TestValidate_ShowCard_MissingRequiredHints checks that a payload with
// missing required fields produces a "missing property" error and a
// suggestion naming the field, rooted at /data.
func TestValidate_ShowCard_MissingRequiredHints(t *testing.T) {
	st := newSelfTools(t)
	resp := callValidateForTest(t, st, map[string]any{
		"tool_name": "card_show",
		"args": map[string]any{
			"type": "report-card",
			"data": map[string]any{"title": "X"}, // missing metrics
		},
	})
	if resp.Valid {
		t.Fatalf("expected valid=false")
	}
	hit := false
	for _, e := range resp.Errors {
		if strings.HasPrefix(e.Path, "/data") && strings.Contains(e.Reason, "missing") && strings.Contains(e.Reason, "metrics") {
			hit = true
			if !strings.Contains(e.Suggestion, "metrics") {
				t.Errorf("suggestion should hint at 'metrics', got: %q", e.Suggestion)
			}
		}
	}
	if !hit {
		t.Fatalf("expected /data missing-required error mentioning 'metrics', got: %+v", resp.Errors)
	}
}

// TestValidate_ShowCard_UnknownEnvelopeType returns a structured error
// pointing at /type rather than collapsing into a schema-load failure.
func TestValidate_ShowCard_UnknownEnvelopeType(t *testing.T) {
	st := newSelfTools(t)
	resp := callValidateForTest(t, st, map[string]any{
		"tool_name": "card_show",
		"args": map[string]any{
			"type": "approval-card", // not on the passive-renderable allow-list
			"data": map[string]any{"x": 1},
		},
	})
	if resp.Valid {
		t.Fatalf("expected valid=false for non-passive-renderable type")
	}
	hit := false
	for _, e := range resp.Errors {
		if e.Path == "/type" || strings.Contains(e.Reason, "passive-renderable") {
			hit = true
		}
	}
	if !hit {
		// May also be caught by the top-level enum check on `type`.
		// Both are acceptable; we just need *some* /type error.
		for _, e := range resp.Errors {
			if strings.HasPrefix(e.Path, "/type") || (strings.Contains(e.Reason, "value must be") && e.Path == "/type") {
				hit = true
			}
		}
	}
	if !hit {
		t.Fatalf("expected an error pointing at /type, got: %+v", resp.Errors)
	}
}

// TestValidate_NonEnvelopeSelfTool exercises the non-envelope path: a
// plain self-tool's input schema is checked against the args.
//
// agent_create has required {name, slug, system_prompt}; an empty args
// object should yield three missing-property errors.
//
// TASKS/skills/01: this used to exercise skill_create (also required
// {name, slug, description}) — skill_create is deleted in full per
// docs/engineering/architecture/20-skills.md's "Scope: skills are authored
// packages only" section, so this generic-validation-path coverage moved to
// agent_create, another still-live non-envelope self-tool with the same
// three-required-fields shape.
func TestValidate_NonEnvelopeSelfTool(t *testing.T) {
	st := newSelfTools(t)
	resp := callValidateForTest(t, st, map[string]any{
		"tool_name": "agent_create",
		"args":      map[string]any{},
	})
	if resp.Valid {
		t.Fatalf("expected valid=false for empty args")
	}
	missing := map[string]bool{"name": false, "slug": false, "system_prompt": false}
	for _, e := range resp.Errors {
		for k := range missing {
			if strings.Contains(e.Reason, k) {
				missing[k] = true
			}
		}
	}
	for k, found := range missing {
		if !found {
			t.Errorf("expected error mentioning required field %q in %+v", k, resp.Errors)
		}
	}
}

// TestValidate_NonEnvelopeSelfTool_HappyPath supplies all required
// fields and asserts a valid:true response.
func TestValidate_NonEnvelopeSelfTool_HappyPath(t *testing.T) {
	st := newSelfTools(t)
	resp := callValidateForTest(t, st, map[string]any{
		"tool_name": "agent_create",
		"args": map[string]any{
			"name":          "X",
			"slug":          "x",
			"system_prompt": "y",
		},
	})
	if !resp.Valid {
		t.Fatalf("expected valid=true, got errors: %+v", resp.Errors)
	}
}

// TestValidate_TodoCreate covers todo_create — another
// non-envelope tool with the required field "title" — to confirm the
// path generalises beyond the skill/agent set.
func TestValidate_TodoCreate(t *testing.T) {
	st := newSelfTools(t)
	// missing required title
	resp := callValidateForTest(t, st, map[string]any{
		"tool_name": "todo_create",
		"args":      map[string]any{},
	})
	if resp.Valid {
		t.Fatalf("expected valid=false for empty args")
	}
	hit := false
	for _, e := range resp.Errors {
		if strings.Contains(e.Reason, "title") {
			hit = true
		}
	}
	if !hit {
		t.Errorf("expected error mentioning 'title', got: %+v", resp.Errors)
	}

	// happy path
	resp = callValidateForTest(t, st, map[string]any{
		"tool_name": "todo_create",
		"args":      map[string]any{"title": "test"},
	})
	if !resp.Valid {
		t.Errorf("expected valid=true, got: %+v", resp.Errors)
	}
}

// TestValidate_UnknownTool returns a structured error rather than a
// hard error so the agent gets uniform feedback.
func TestValidate_UnknownTool(t *testing.T) {
	st := newSelfTools(t)
	resp := callValidateForTest(t, st, map[string]any{
		"tool_name": "definitely_not_a_real_tool_name_xyz",
		"args":      map[string]any{},
	})
	if resp.Valid {
		t.Fatalf("expected valid=false for unknown tool")
	}
	if len(resp.Errors) == 0 {
		t.Fatalf("expected at least one error")
	}
	if !strings.Contains(resp.Errors[0].Reason, "unknown tool") {
		t.Errorf("expected 'unknown tool' reason, got: %q", resp.Errors[0].Reason)
	}
}

// TestValidate_MissingArgsField: when `args` is omitted or wrong type,
// the validator surfaces it as a structured error rather than crashing.
func TestValidate_MissingArgsField(t *testing.T) {
	st := newSelfTools(t)
	res, err := st.CallTool(context.Background(), "tool_validate", map[string]any{
		"tool_name": "card_show",
		// args field omitted entirely
	})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected non-error result, got: %+v", res)
	}
	var out validateResp
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Valid {
		t.Fatalf("expected valid=false")
	}
	if len(out.Errors) == 0 || out.Errors[0].Path != "/args" {
		t.Errorf("expected an /args-rooted error, got: %+v", out.Errors)
	}
}

// TestValidate_TopLevelInputSchema verifies the top-level input-schema
// pass on card_show flags missing `type` (the schema requires it)
// independently of the per-data validation.
func TestValidate_TopLevelInputSchema(t *testing.T) {
	st := newSelfTools(t)
	resp := callValidateForTest(t, st, map[string]any{
		"tool_name": "card_show",
		"args": map[string]any{
			"data": map[string]any{"title": "X", "metrics": []any{}},
			// `type` omitted — top-level schema requires it.
		},
	})
	if resp.Valid {
		t.Fatalf("expected valid=false when 'type' missing")
	}
	hit := false
	for _, e := range resp.Errors {
		if strings.Contains(e.Reason, "type") && strings.Contains(e.Reason, "missing") {
			hit = true
		}
	}
	if !hit {
		t.Errorf("expected missing-required error for 'type', got: %+v", resp.Errors)
	}
}

// TestValidate_SelfIntrospection — tool_validate can validate calls
// to itself. The schema requires {tool_name, args}; an empty args here
// should flag both as missing.
func TestValidate_SelfIntrospection(t *testing.T) {
	st := newSelfTools(t)
	resp := callValidateForTest(t, st, map[string]any{
		"tool_name": "tool_validate",
		"args":      map[string]any{},
	})
	if resp.Valid {
		t.Fatalf("expected valid=false for empty args")
	}
	for _, k := range []string{"tool_name", "args"} {
		hit := false
		for _, e := range resp.Errors {
			if strings.Contains(e.Reason, k) {
				hit = true
			}
		}
		if !hit {
			t.Errorf("expected error mentioning %q, got: %+v", k, resp.Errors)
		}
	}
}

// stubSchemaLookup lets us prove the SchemaLookup path is exercised when
// the manager is wired in. Returns a fixed schema for one tool name.
type stubSchemaLookup struct {
	name   string
	schema map[string]any
}

func (s *stubSchemaLookup) LookupToolInputSchema(uniformName string) (map[string]any, bool) {
	if uniformName == s.name {
		return s.schema, true
	}
	return nil, false
}

// TestValidate_NonSelfToolViaSchemaLookup confirms a tool published by
// (e.g.) an MCP server flows through the SchemaLookup path. We stub a
// minimal "memory_write" schema and check the validator picks it up.
func TestValidate_NonSelfToolViaSchemaLookup(t *testing.T) {
	st := newSelfTools(t)
	st.SchemaLookup = &stubSchemaLookup{
		name: "memory_write",
		schema: map[string]any{
			"type":     "object",
			"required": []any{"namespace", "key", "value"},
			"properties": map[string]any{
				"namespace": map[string]any{"type": "string"},
				"key":       map[string]any{"type": "string"},
				"value":     map[string]any{"type": "string"},
			},
			"additionalProperties": false,
		},
	}
	// missing required
	resp := callValidateForTest(t, st, map[string]any{
		"tool_name": "memory_write",
		"args":      map[string]any{"namespace": "x"},
	})
	if resp.Valid {
		t.Fatalf("expected valid=false")
	}
	for _, k := range []string{"key", "value"} {
		hit := false
		for _, e := range resp.Errors {
			if strings.Contains(e.Reason, k) {
				hit = true
			}
		}
		if !hit {
			t.Errorf("expected error mentioning %q, got: %+v", k, resp.Errors)
		}
	}
	// happy path
	resp = callValidateForTest(t, st, map[string]any{
		"tool_name": "memory_write",
		"args": map[string]any{
			"namespace": "x",
			"key":       "k",
			"value":     "v",
		},
	})
	if !resp.Valid {
		t.Errorf("expected valid=true, got: %+v", resp.Errors)
	}
}
