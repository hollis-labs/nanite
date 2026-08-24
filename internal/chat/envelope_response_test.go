package chat

import (
	"encoding/json"
	"testing"
)

func TestResponseV1_RoundTrip(t *testing.T) {
	accepted := true
	orig := ResponseV1{
		V:      1,
		Kind:   "collect_feedback",
		ID:     "01J000000000000000000000AA",
		Status: StatusSubmitted,
		Data:   map[string]any{"freeform": "hi"},
		Answers: []Answer{
			{QuestionID: "q1", Value: "yes", AcceptedSuggestion: &accepted, Note: "n"},
		},
		Decisions: []Decision{
			{ItemID: "i1", Action: "accept", Meta: map[string]any{"score": 0.9}},
		},
	}

	raw, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	got, err := UnmarshalResponseV1(raw)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.V != orig.V || got.Kind != orig.Kind || got.ID != orig.ID || got.Status != orig.Status {
		t.Fatalf("scalars diverged: got %+v want %+v", got, orig)
	}
	if len(got.Answers) != 1 || got.Answers[0].QuestionID != "q1" {
		t.Fatalf("answers not preserved: %+v", got.Answers)
	}
	if len(got.Decisions) != 1 || got.Decisions[0].ItemID != "i1" {
		t.Fatalf("decisions not preserved: %+v", got.Decisions)
	}
	if got.Data["freeform"] != "hi" {
		t.Fatalf("data not preserved: %+v", got.Data)
	}
}

func TestResponseV1_VersionGuard(t *testing.T) {
	_, err := UnmarshalResponseV1([]byte(`{"v":2,"kind":"x","id":"i","status":"submitted"}`))
	if err == nil {
		t.Fatal("expected version guard to reject v=2")
	}
}

func TestResponseV1_StatusEnum(t *testing.T) {
	cases := []struct {
		name   string
		status string
		ok     bool
	}{
		{"submitted", "submitted", true},
		{"canceled", "canceled", true},
		{"partial", "partial", true},
		{"unknown", "accepted", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(`{"v":1,"kind":"k","id":"i","status":"` + tc.status + `"}`)
			_, err := UnmarshalResponseV1(raw)
			if tc.ok && err != nil {
				t.Fatalf("expected ok, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestResponseV1_RequiredFields(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"missing_kind", `{"v":1,"id":"i","status":"submitted"}`},
		{"missing_id", `{"v":1,"kind":"k","status":"submitted"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := UnmarshalResponseV1([]byte(tc.raw)); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
