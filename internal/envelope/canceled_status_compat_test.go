package envelope_test

import (
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/envelope"
)

// This file is the written answer to Torque CW-20260824-0027's persisted-
// envelope compatibility question: does moving session-task's status enum from
// 'cancelled' to 'canceled' break envelopes already persisted with the old
// spelling when they are read back?
//
// Answer, derived from the code rather than assumed: NO, and the reason is
// that the read path never consults the JSON Schema at all. Two independent
// mechanisms are asserted below, because the safety rests on the gap between
// them:
//
//   - The EMIT path (envelope.ValidateData, reached from
//     executor/envelope_render and selftools' transport) does enforce the enum.
//     This is where the contract change bites, and it is the intended effect.
//   - The READ path (chat.ParseEnvelopes -> chat.ValidateEnvelope, the only
//     validation applied to envelope blocks recovered from stored assistant
//     message content) checks kind, version and type-registration only. It
//     never loads a schema, so no `data` value — including a status the schema
//     no longer lists — can fail it.
//
// Because of that gap, neither a tolerant reader nor a JSON backfill of stored
// envelope blocks was added. That is a deliberate decision, not an omission:
// there is nothing on the read path to be tolerant *of*. Migration 148 does
// still backfill envelope_instances.response_status and the envelope_response
// message prefix, which are separate persisted surfaces that carry a status
// string outside any envelope `data` blob.
//
// Supporting measurement (2026-08-24, this session): the live dev database and
// the operator backup both hold zero persisted session-task envelopes and zero
// envelope blobs containing the legacy spelling, and session-task has no frontend
// component at all (ui/src/generated/plugin-envelopes.ts lists it under
// "Backend-only types (no frontend component)"), so the "re-rendered on page
// reload" path this question was framed around does not exist for this type.
// The assertions below do not depend on any of that remaining true.

// legacyStatus is the pre-migration British spelling these tests exist to
// prove is gone. It is named once, here, rather than repeated as a literal
// across every assertion: a test that verifies a value's removal has to
// contain that value somewhere, and one clearly-labeled occurrence per file
// is the smallest honest form of that. (Deliberately NOT hidden from the
// misspell gate by string concatenation or a //nolint — an evaded finding and
// a fixed one are not the same thing.)
const legacyStatus = "cancelled"

// TestSessionTaskEmitPathEnforcesUSEnglishStatus pins the intended contract
// change. Both directions are asserted: a bare rejection test would also pass
// against a schema that rejected everything, and a bare acceptance test would
// pass against a schema with no enum at all.
func TestSessionTaskEmitPathEnforcesUSEnglishStatus(t *testing.T) {
	base := func(status string) map[string]any {
		return map[string]any{"task_id": "t-1", "title": "Probe", "status": status}
	}

	if err := envelope.ValidateData("session-task", base("canceled")); err != nil {
		t.Errorf("session-task status=\"canceled\" rejected on the emit path, want accepted: %v", err)
	}

	err := envelope.ValidateData("session-task", base(legacyStatus))
	if err == nil {
		t.Errorf("session-task status=%q accepted on the emit path, want rejected — "+
			"the go-envelopes manifest enum did not move", legacyStatus)
	} else if !strings.Contains(err.Error(), "canceled") {
		t.Errorf("rejection message does not name the accepted spelling, got: %v", err)
	}

	// Control: the untouched members of the same enum still validate, proving
	// the schema is loaded and functioning rather than failing wholesale.
	for _, ok := range []string{"pending", "in_progress", "completed", "failed"} {
		if err := envelope.ValidateData("session-task", base(ok)); err != nil {
			t.Errorf("session-task status=%q rejected, want accepted: %v", ok, err)
		}
	}
}

// TestPersistedEnvelopeWithLegacyCancelledStatusStillReads is the read-path
// half. It feeds ParseEnvelopes exactly what a stored assistant message
// written before this migration looks like — a fenced nanite-envelope block
// whose data carries the old spelling — and asserts it comes back parsed and
// error-free.
func TestPersistedEnvelopeWithLegacyCancelledStatusStillReads(t *testing.T) {
	// ValidateEnvelope gates on the envelope's `type` being registered; this
	// test binary starts with an empty registry, so register the value real
	// persisted envelopes carry.
	chat.InitCoreTypes([]string{"standard"})

	const stored = "Here is the task.\n\n" +
		"```nanite-envelope\n" +
		`{"kind":"card","version":1,"type":"standard","card":"session-task",` +
		`"data":{"task_id":"t-1","title":"Probe","status":"` + legacyStatus + `"}}` +
		"\n```\n\nTrailing text."

	envelopes, clean, errs := chat.ParseEnvelopes(stored)

	if len(errs) != 0 {
		t.Fatalf("read path reported %d error(s) for a persisted legacy-spelling envelope, want 0: %+v", len(errs), errs)
	}
	if len(envelopes) != 1 {
		t.Fatalf("read path recovered %d envelope(s), want 1", len(envelopes))
	}
	if envelopes[0].Kind != "card" {
		t.Errorf("recovered envelope kind = %q, want \"card\"", envelopes[0].Kind)
	}
	if strings.Contains(clean, "nanite-envelope") {
		t.Error("envelope block was not stripped from the surrounding text")
	}

	// Positive control for this assertion's strength: the same read path DOES
	// reject a genuinely malformed block, so "0 errors" above is a real pass
	// and not a validator that never rejects anything.
	_, _, badErrs := chat.ParseEnvelopes(
		"```nanite-envelope\n" + `{"kind":"card","version":1,"type":"never-registered"}` + "\n```")
	if len(badErrs) == 0 {
		t.Fatal("read path accepted an unregistered envelope type — the clean parse above proves nothing")
	}
}
