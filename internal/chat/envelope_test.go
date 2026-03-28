package chat

import "testing"

func TestParseEnvelopes(t *testing.T) {
	input := "Here is my response.\n\n" +
		"```volon-envelope\n" +
		`{"kind":"action","version":1,"type":"standard","proposals":[{"type":"create_task","payload":{"title":"Do something"}}]}` +
		"\n```\n\nMore text."

	envelopes, clean, _ := ParseEnvelopes(input)

	if len(envelopes) != 1 {
		t.Fatalf("expected 1 envelope, got %d", len(envelopes))
	}

	env := envelopes[0]
	if env.Kind != "action" {
		t.Errorf("expected kind 'action', got %q", env.Kind)
	}
	if env.Version != 1 {
		t.Errorf("expected version 1, got %d", env.Version)
	}
	if env.Type != "standard" {
		t.Errorf("expected type 'standard', got %q", env.Type)
	}
	if len(env.Proposals) != 1 {
		t.Fatalf("expected 1 proposal, got %d", len(env.Proposals))
	}
	if env.Proposals[0].Type != "create_task" {
		t.Errorf("expected proposal type 'create_task', got %q", env.Proposals[0].Type)
	}

	// Verify the envelope block was removed from the clean text.
	if containsStr(clean, "volon-envelope") {
		t.Error("clean text should not contain envelope block")
	}
	if !containsStr(clean, "Here is my response.") {
		t.Error("clean text should contain surrounding text")
	}
	if !containsStr(clean, "More text.") {
		t.Error("clean text should contain trailing text")
	}
}

func TestParseEnvelopesConduitTag(t *testing.T) {
	input := "```conduit-envelope\n" +
		`{"kind":"question","version":1,"type":"conduit","questions":[{"prompt":"Choose one","type":"select","options":["a","b"],"required":true}]}` +
		"\n```"

	envelopes, _, _ := ParseEnvelopes(input)

	if len(envelopes) != 1 {
		t.Fatalf("expected 1 envelope, got %d", len(envelopes))
	}
	if envelopes[0].Kind != "question" {
		t.Errorf("expected kind 'question', got %q", envelopes[0].Kind)
	}
	if len(envelopes[0].Questions) != 1 {
		t.Fatalf("expected 1 question, got %d", len(envelopes[0].Questions))
	}
}

func TestParseEnvelopesNoMatch(t *testing.T) {
	input := "Just some plain text without any envelope blocks."

	envelopes, clean, _ := ParseEnvelopes(input)

	if envelopes != nil {
		t.Errorf("expected nil envelopes, got %v", envelopes)
	}
	if clean != input {
		t.Errorf("expected clean text to equal input, got %q", clean)
	}
}

func TestParseEnvelopesMultiple(t *testing.T) {
	input := "Text before.\n\n" +
		"```volon-envelope\n" +
		`{"kind":"action","version":1,"type":"standard"}` +
		"\n```\n\nMiddle text.\n\n" +
		"```volon-envelope\n" +
		`{"kind":"status","version":1,"type":"standard","status":{"phase":"running","progress":0.5}}` +
		"\n```\n\nEnd."

	envelopes, clean, _ := ParseEnvelopes(input)

	if len(envelopes) != 2 {
		t.Fatalf("expected 2 envelopes, got %d", len(envelopes))
	}
	if envelopes[0].Kind != "action" {
		t.Errorf("first envelope kind: got %q, want 'action'", envelopes[0].Kind)
	}
	if envelopes[1].Kind != "status" {
		t.Errorf("second envelope kind: got %q, want 'status'", envelopes[1].Kind)
	}
	if !containsStr(clean, "Middle text.") {
		t.Error("clean text should preserve middle text")
	}
}

func TestParseEnvelopes_InvalidJSON(t *testing.T) {
	input := "```conduit-envelope\n{not valid json}\n```"
	envelopes, _, errors := ParseEnvelopes(input)

	if len(envelopes) != 0 {
		t.Errorf("expected 0 envelopes, got %d", len(envelopes))
	}
	if len(errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errors))
	}
	if errors[0].Reason != "invalid_json" {
		t.Errorf("expected reason=invalid_json, got %s", errors[0].Reason)
	}
}

func TestParseEnvelopes_MissingKind(t *testing.T) {
	input := "```conduit-envelope\n" +
		`{"version":1,"type":"conduit"}` +
		"\n```"
	envelopes, _, errors := ParseEnvelopes(input)

	// Envelope still included (parsed OK) but validation error reported.
	if len(envelopes) != 1 {
		t.Errorf("expected 1 envelope, got %d", len(envelopes))
	}
	if len(errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errors))
	}
	if errors[0].Reason != "missing_kind" {
		t.Errorf("expected reason=missing_kind, got %s", errors[0].Reason)
	}
}

func TestParseEnvelopes_MissingVersion(t *testing.T) {
	input := "```conduit-envelope\n" +
		`{"kind":"action","type":"conduit"}` +
		"\n```"
	_, _, errors := ParseEnvelopes(input)

	if len(errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errors))
	}
	if errors[0].Reason != "missing_version" {
		t.Errorf("expected reason=missing_version, got %s", errors[0].Reason)
	}
}

func TestParseEnvelopes_UnregisteredType(t *testing.T) {
	input := "```conduit-envelope\n" +
		`{"kind":"action","version":1,"type":"invented-type"}` +
		"\n```"
	envelopes, _, errors := ParseEnvelopes(input)

	// Envelope still included — unregistered is a warning, not fatal.
	if len(envelopes) != 1 {
		t.Errorf("expected 1 envelope, got %d", len(envelopes))
	}
	if len(errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errors))
	}
	if errors[0].Reason != "unregistered_type" {
		t.Errorf("expected reason=unregistered_type, got %s", errors[0].Reason)
	}
}

func TestParseEnvelopes_ValidNoErrors(t *testing.T) {
	input := "```conduit-envelope\n" +
		`{"kind":"action","version":1,"type":"kb-result","data":{"query":"test"}}` +
		"\n```"
	envelopes, _, errors := ParseEnvelopes(input)

	if len(envelopes) != 1 {
		t.Errorf("expected 1 envelope, got %d", len(envelopes))
	}
	if len(errors) != 0 {
		t.Errorf("expected 0 errors, got %d", len(errors))
	}
}

func TestValidateEnvelope(t *testing.T) {
	tests := []struct {
		name   string
		env    Envelope
		reason string // empty means valid
	}{
		{"valid", Envelope{Kind: "action", Version: 1, Type: "kb-result"}, ""},
		{"valid_standard", Envelope{Kind: "question", Version: 1, Type: "standard"}, "unregistered_type"},
		{"missing_kind", Envelope{Version: 1, Type: "conduit"}, "missing_kind"},
		{"missing_version", Envelope{Kind: "action", Type: "conduit"}, "missing_version"},
		{"unregistered", Envelope{Kind: "action", Version: 1, Type: "nonexistent"}, "unregistered_type"},
		{"no_type_ok", Envelope{Kind: "action", Version: 1}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateEnvelope(tt.env, "raw")
			if tt.reason == "" {
				if err != nil {
					t.Errorf("expected valid, got error: %s", err.Reason)
				}
			} else {
				if err == nil {
					t.Fatalf("expected error %q, got nil", tt.reason)
				}
				if err.Reason != tt.reason {
					t.Errorf("expected reason=%s, got %s", tt.reason, err.Reason)
				}
			}
		})
	}
}
