package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// envelopeRunner is a runner whose child emitted a structured envelope.
// ResultJSON carries the success-path shape ChatRunner.Run produces: the
// child's terminal envelope JSON verbatim ({"kind":"envelope",...}).
type envelopeRunner struct{ resultJSON string }

func (r envelopeRunner) Run(_ context.Context, _ *Run) (*Result, error) {
	return &Result{Summary: "did the work", ResultJSON: r.resultJSON}, nil
}

const planReviewEnvelope = `{"kind":"envelope","version":1,"type":"plan-review",` +
	`"data":{"plan_id":"e2d2685a","status":"proposed","steps":["a","b","c","d","e"]}}`

// TestSpawn_LiftsChildEnvelopeToParent is the CW-20260519-0066 fix: a
// sync subagent that produced a plan-review card has that envelope
// re-emitted onto the PARENT session via the ApprovalEmitter, so the
// operator sees the card in the parent transcript.
func TestSpawn_LiftsChildEnvelopeToParent(t *testing.T) {
	db, _ := newTestDB(t)
	emitter := &stubEmitter{}
	svc := NewService(db, envelopeRunner{resultJSON: planReviewEnvelope},
		&stubPoster{}, emitter, stubSettings{})

	_, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "parent-sess",
		ParentAgentID:   "primary-agent",
		Role:            "file-backend",
		Prompt:          "draft a plan",
		Mode:            ModeSync,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	if emitter.Count() != 1 {
		t.Fatalf("emitter calls = %d, want 1 (the lifted plan-review card)", emitter.Count())
	}
	last := emitter.Last()
	if last.sessionID != "parent-sess" {
		t.Errorf("lifted envelope session = %q, want parent-sess", last.sessionID)
	}
	if last.typ != "plan-review" {
		t.Errorf("lifted envelope type = %q, want plan-review", last.typ)
	}
	// The emitted payload is the envelope's `data` blob — Emit rebuilds
	// the kind/version/type frame around it.
	var data struct {
		PlanID string   `json:"plan_id"`
		Status string   `json:"status"`
		Steps  []string `json:"steps"`
	}
	if err := json.Unmarshal(last.payload, &data); err != nil {
		t.Fatalf("lifted payload not valid JSON (%q): %v", last.payload, err)
	}
	if data.PlanID != "e2d2685a" || data.Status != "proposed" || len(data.Steps) != 5 {
		t.Errorf("lifted payload = %+v, want the child's plan-review data", data)
	}
}

// TestSpawn_NoEnvelope_NothingLifted verifies the zero-envelope case is a
// silent no-op — a subagent whose child emitted no structured envelope
// (ResultJSON == "{}") triggers no Emit call.
func TestSpawn_NoEnvelope_NothingLifted(t *testing.T) {
	db, _ := newTestDB(t)
	emitter := &stubEmitter{}
	svc := NewService(db, emptyEnvelopeRunner{summary: "plain text result"},
		&stubPoster{}, emitter, stubSettings{})

	_, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "parent-sess",
		ParentAgentID:   "primary-agent",
		Role:            "file-backend",
		Prompt:          "just talk",
		Mode:            ModeSync,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if emitter.Count() != 0 {
		t.Errorf("emitter calls = %d, want 0 (no envelope to lift)", emitter.Count())
	}
}

// TestSpawn_PartialResultEnvelopeLifted verifies a subagent cut mid-task
// (CW-20260519-0071 partial-capture path) still has any review/approval
// card it managed to emit lifted to the parent. The partial-capture
// ResultJSON nests the child's envelope under the `envelope` key.
func TestSpawn_PartialResultEnvelopeLifted(t *testing.T) {
	db, _ := newTestDB(t)
	emitter := &stubEmitter{}
	partialJSON := `{"partial":true,"summary":"cut mid-task",` +
		`"envelope":` + planReviewEnvelope + `,` +
		`"tools":{"calls":3,"results_success":3,"results_error":0}}`
	svc := NewService(db, partialEnvelopeRunner{resultJSON: partialJSON},
		&stubPoster{}, emitter, stubSettings{})

	_, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "parent-sess",
		ParentAgentID:   "primary-agent",
		Role:            "file-backend",
		Prompt:          "draft a plan",
		Mode:            ModeSync,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if emitter.Count() != 1 {
		t.Fatalf("emitter calls = %d, want 1 (lifted from partial result)", emitter.Count())
	}
	if got := emitter.Last().typ; got != "plan-review" {
		t.Errorf("lifted envelope type = %q, want plan-review", got)
	}
}

// partialEnvelopeRunner returns a partial-capture Result (the
// CW-20260519-0071 shape) ALONGSIDE an error, mirroring ChatRunner.Run's
// deadline path. The nested envelope must still be lifted.
type partialEnvelopeRunner struct{ resultJSON string }

func (r partialEnvelopeRunner) Run(_ context.Context, _ *Run) (*Result, error) {
	return &Result{Summary: "cut mid-task", ResultJSON: r.resultJSON},
		errors.New("deadline cut the run")
}

// TestExtractLiftableEnvelopes covers the pure extraction logic across
// the recognized and rejected ResultJSON shapes.
func TestExtractLiftableEnvelopes(t *testing.T) {
	cases := []struct {
		name      string
		result    *Result
		wantCount int
		wantType  string
	}{
		{"nil result", nil, 0, ""},
		{"empty json", &Result{ResultJSON: ""}, 0, ""},
		{"bare empty object", &Result{ResultJSON: "{}"}, 0, ""},
		{"summary wrapper, not an envelope",
			&Result{ResultJSON: `{"summary":"did stuff"}`}, 0, ""},
		{"malformed json", &Result{ResultJSON: `{not json`}, 0, ""},
		{"success-path plan-review",
			&Result{ResultJSON: planReviewEnvelope}, 1, "plan-review"},
		{"partial-capture nested envelope",
			&Result{ResultJSON: `{"partial":true,"envelope":` + planReviewEnvelope + `}`},
			1, "plan-review"},
		{"partial-capture with no envelope",
			&Result{ResultJSON: `{"partial":true,"envelope":{},"summary":"x"}`}, 0, ""},
		{"envelope with no data field still lifts",
			&Result{ResultJSON: `{"kind":"envelope","version":1,"type":"approval-card"}`},
			1, "approval-card"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractLiftableEnvelopes(tc.result)
			if len(got) != tc.wantCount {
				t.Fatalf("count = %d, want %d", len(got), tc.wantCount)
			}
			if tc.wantCount == 1 && got[0].Type != tc.wantType {
				t.Errorf("type = %q, want %q", got[0].Type, tc.wantType)
			}
			if tc.wantCount == 1 && !json.Valid(got[0].Data) {
				t.Errorf("extracted data is not valid JSON: %q", got[0].Data)
			}
		})
	}
}

// TestSpawn_NilApprover_LiftIsNoOp verifies a nil approver (minimal
// wiring) does not panic — the lift is simply skipped.
func TestSpawn_NilApprover_LiftIsNoOp(t *testing.T) {
	db, _ := newTestDB(t)
	svc := NewService(db, envelopeRunner{resultJSON: planReviewEnvelope},
		&stubPoster{}, nil, stubSettings{})

	if _, err := svc.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: "parent-sess",
		ParentAgentID:   "primary-agent",
		Role:            "file-backend",
		Prompt:          "draft a plan",
		Mode:            ModeSync,
	}); err != nil {
		t.Fatalf("Spawn with nil approver: %v", err)
	}
}
