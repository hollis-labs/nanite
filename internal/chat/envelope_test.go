package chat

import "testing"

func TestParseEnvelopes(t *testing.T) {
	input := "Here is my response.\n\n" +
		"```volon-envelope\n" +
		`{"kind":"action","version":1,"type":"standard","proposals":[{"type":"create_task","payload":{"title":"Do something"}}]}` +
		"\n```\n\nMore text."

	envelopes, clean := ParseEnvelopes(input)

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

func TestParseEnvelopesMentatTag(t *testing.T) {
	input := "```mentat-envelope\n" +
		`{"kind":"question","version":1,"type":"mentat","questions":[{"prompt":"Choose one","type":"select","options":["a","b"],"required":true}]}` +
		"\n```"

	envelopes, _ := ParseEnvelopes(input)

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

	envelopes, clean := ParseEnvelopes(input)

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

	envelopes, clean := ParseEnvelopes(input)

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
