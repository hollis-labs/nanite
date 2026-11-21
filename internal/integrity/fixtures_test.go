package integrity_test

import (
	"encoding/json"
	"strings"
	"time"
)

// fixture builds a transcript in the shapes internal/store writes: sideband
// records with a payload, and messages whose body carries nanite_turn_id.
// Store-written records are checked against the real store in
// internal/store/integrity_test.go.
type fixture struct {
	conv  string
	lines []string
	tail  string // bytes after the last complete line (a torn tail)
	n     int
}

const fixtureConversation = "conv-1"

func newFixture() *fixture {
	f := &fixture{conv: fixtureConversation}
	f.side("nanite.conversation", f.conv, map[string]any{"ID": f.conv, "Role": "", "Title": "", "Status": "active"})
	return f
}

func (f *fixture) at() string {
	f.n++
	return time.Date(2026, 11, 21, 8, 0, f.n, 0, time.UTC).Format(time.RFC3339Nano)
}

func (f *fixture) raw(v any) *fixture {
	line, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	f.lines = append(f.lines, string(line))
	return f
}

func (f *fixture) side(subtype, uuid string, payload any) *fixture {
	return f.raw(map[string]any{"type": "system", "subtype": subtype, "uuid": uuid, "parentUuid": nil,
		"sessionId": f.conv, "timestamp": f.at(), "payload": payload})
}

func (f *fixture) start(turn string) *fixture {
	return f.side("nanite.turn.start", turn, map[string]any{"ID": turn, "UserMessageID": "u-" + turn})
}

func (f *fixture) finish(turn, status string) *fixture {
	return f.side("nanite.turn.finish", turn, map[string]any{"ID": turn, "UserMessageID": "u-" + turn, "Status": status})
}

func (f *fixture) interrupt(turn string) *fixture {
	return f.side("nanite.turn.interrupt", turn, map[string]any{"ID": turn})
}

func (f *fixture) resume(turn string) *fixture {
	return f.side("nanite.turn.resume", turn, map[string]any{"ID": turn, "UserMessageID": "u-" + turn})
}

func (f *fixture) compaction(id, turn, through string) *fixture {
	return f.side("nanite.compaction", id, map[string]any{"TurnID": turn, "ThroughMessageID": through, "Summary": "summary", "Before": 10, "After": 3})
}

func (f *fixture) message(role, id, turn string, blocks ...map[string]any) *fixture {
	if blocks == nil {
		blocks = []map[string]any{}
	}
	return f.raw(map[string]any{"type": role, "uuid": id, "parentUuid": nil, "sessionId": f.conv, "timestamp": f.at(),
		"message": map[string]any{"role": role, "content": blocks, "nanite_turn_id": turn}})
}

// owner writes the turn's opening user message, u-<turn>.
func (f *fixture) owner(turn string) *fixture {
	return f.message("user", "u-"+turn, turn, text("hello"))
}

func (f *fixture) say(id, turn string) *fixture {
	return f.message("assistant", id, turn, text("done"))
}

func (f *fixture) calls(id, turn string, ids ...string) *fixture {
	var blocks []map[string]any
	for _, call := range ids {
		blocks = append(blocks, map[string]any{"type": "tool_use", "id": call, "name": "read", "input": map[string]any{}})
	}
	return f.message("assistant", id, turn, blocks...)
}

func (f *fixture) results(id, turn string, ids ...string) *fixture {
	var blocks []map[string]any
	for _, call := range ids {
		blocks = append(blocks, map[string]any{"type": "tool_result", "tool_use_id": call, "content": "ok"})
	}
	return f.message("user", id, turn, blocks...)
}

func (f *fixture) synthetic(id, turn string, ids ...string) *fixture {
	var blocks []map[string]any
	for _, call := range ids {
		blocks = append(blocks, map[string]any{"type": "tool_result", "tool_use_id": call,
			"content": "interrupted before completion; not retried", "is_error": true})
	}
	return f.message("user", id, turn, blocks...)
}

func (f *fixture) closing(id, turn string) *fixture {
	return f.message("user", id, turn, map[string]any{"type": "text", "name": "nanite_closing_reply:max_steps",
		"text": "The turn reached its limit."})
}

func (f *fixture) torn(partial string) *fixture { f.tail = partial; return f }

func (f *fixture) bytes() []byte {
	var b strings.Builder
	for _, line := range f.lines {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteString(f.tail)
	return []byte(b.String())
}

func text(s string) map[string]any { return map[string]any{"type": "text", "text": s} }

// toolTurn is one clean turn with a tool step.
func (f *fixture) toolTurn(turn string) *fixture {
	return f.start(turn).owner(turn).
		calls("a1-"+turn, turn, "call-"+turn).
		results("r1-"+turn, turn, "call-"+turn).
		say("a2-"+turn, turn).
		finish(turn, "completed")
}

// fixtures is the table every fixture test reads. Each one is written to
// testdata/<name>.jsonl; its expected findings are testdata/<name>.golden.json.
var fixtures = []struct {
	name  string
	build func() *fixture
}{
	{"clean", func() *fixture {
		return newFixture().toolTurn("t1").start("t2").owner("t2").say("a1-t2", "t2").finish("t2", "completed")
	}},
	{"clean_closing_note", func() *fixture {
		return newFixture().start("t1").owner("t1").
			calls("a1", "t1", "c1").results("r1", "t1", "c1").
			closing("n1", "t1").
			// A provider that ignores the tool-less request gets its calls
			// cancelled, which keeps them paired.
			calls("a2", "t1", "c2").synthetic("r2", "t1", "c2").
			finish("t1", "completed")
	}},
	{"clean_compaction_kept_tool_step", func() *fixture {
		// Compaction inside a long turn summarizes through a tool result and
		// keeps the latest step whole.
		return newFixture().toolTurn("t1").start("t2").owner("t2").
			calls("a1-t2", "t2", "c1").results("r1-t2", "t2", "c1").
			compaction("k1", "t2", "r1-t2").
			calls("a2-t2", "t2", "c2").results("r2-t2", "t2", "c2").
			say("a3-t2", "t2").finish("t2", "completed")
	}},
	{"clean_discarded", func() *fixture {
		return newFixture().start("t1").owner("t1").calls("a1", "t1", "c1").
			interrupt("t1").synthetic("r1", "t1", "c1").finish("t1", "cancelled")
	}},
	{"clean_resumed", func() *fixture {
		return newFixture().start("t1").owner("t1").calls("a1", "t1", "c1").
			interrupt("t1").resume("t1").synthetic("r1", "t1", "c1").say("a2", "t1").finish("t1", "completed")
	}},
	{"clean_notice_and_unknown_sideband", func() *fixture {
		// A notice turn opens with its own user message; a sideband from a
		// newer version is skipped.
		return newFixture().side("nanite.permission", "p1", map[string]any{"tool": "read"}).
			start("t1").owner("t1").say("a1", "t1").finish("t1", "completed").
			side("nanite.future_kind", "x1", map[string]any{"v": 2})
	}},
	{"interrupted_running", func() *fixture {
		return newFixture().toolTurn("t1").start("t2").owner("t2").calls("a1", "t2", "c1", "c2").results("r1", "t2", "c1")
	}},
	{"interrupted_marked", func() *fixture {
		return newFixture().start("t1").owner("t1").calls("a1", "t1", "c1").interrupt("t1")
	}},
	{"interrupted_before_user_message", func() *fixture {
		return newFixture().start("t1")
	}},
	{"torn_tail", func() *fixture {
		return newFixture().start("t1").owner("t1").torn(`{"type":"assistant","uuid":"a1","mess`)
	}},
	{"tool_use_without_result_finished", func() *fixture {
		return newFixture().start("t1").owner("t1").calls("a1", "t1", "c1", "c2").results("r1", "t1", "c1").finish("t1", "completed")
	}},
	{"tool_use_superseded", func() *fixture {
		return newFixture().start("t1").owner("t1").calls("a1", "t1", "c1").say("a2", "t1").finish("t1", "completed")
	}},
	{"orphan_tool_result", func() *fixture {
		return newFixture().start("t1").owner("t1").results("r1", "t1", "nope").say("a1", "t1").finish("t1", "completed")
	}},
	{"orphan_tool_result_cross_turn", func() *fixture {
		return newFixture().toolTurn("t1").start("t2").owner("t2").results("r1-t2", "t2", "call-t1").say("a1-t2", "t2").finish("t2", "completed")
	}},
	{"duplicate_tool_result", func() *fixture {
		return newFixture().start("t1").owner("t1").calls("a1", "t1", "c1").results("r1", "t1", "c1").results("r2", "t1", "c1").
			say("a2", "t1").finish("t1", "completed")
	}},
	{"duplicate_turn_outcome", func() *fixture {
		return newFixture().start("t1").owner("t1").say("a1", "t1").finish("t1", "completed").finish("t1", "completed")
	}},
	{"conflicting_turn_outcome", func() *fixture {
		return newFixture().start("t1").owner("t1").say("a1", "t1").finish("t1", "completed").finish("t1", "failed")
	}},
	{"illegal_second_owner_message", func() *fixture {
		return newFixture().start("t1").owner("t1").message("user", "u2", "t1", text("again")).say("a1", "t1").finish("t1", "completed")
	}},
	{"illegal_closing_while_pending", func() *fixture {
		return newFixture().start("t1").owner("t1").calls("a1", "t1", "c1").closing("n1", "t1").results("r1", "t1", "c1").
			say("a2", "t1").finish("t1", "completed")
	}},
	{"illegal_message_while_interrupted", func() *fixture {
		return newFixture().start("t1").owner("t1").interrupt("t1").say("a1", "t1")
	}},
	{"message_after_outcome", func() *fixture {
		return newFixture().start("t1").owner("t1").say("a1", "t1").finish("t1", "completed").say("a2", "t1")
	}},
	{"message_unknown_turn", func() *fixture {
		return newFixture().say("a1", "ghost").message("assistant", "a2", "", text("x"))
	}},
	{"overlapping_turns", func() *fixture {
		return newFixture().start("t1").owner("t1").start("t2").owner("t2").say("a1", "t2").finish("t2", "completed")
	}},
	{"turn_opens_without_user_message", func() *fixture {
		return newFixture().start("t1").say("a1", "t1").finish("t1", "completed")
	}},
	{"illegal_turn_transitions", func() *fixture {
		return newFixture().start("t1").owner("t1").resume("t1").say("a1", "t1").finish("t1", "completed").
			interrupt("t1").finish("ghost", "completed").start("t1")
	}},
	{"unknown_record_type", func() *fixture {
		return newFixture().raw(map[string]any{"type": "attachment", "uuid": "x1", "sessionId": fixtureConversation})
	}},
	{"compaction_unknown_message", func() *fixture {
		return newFixture().toolTurn("t1").compaction("k1", "t1", "missing")
	}},
	{"compaction_splits_tool_step", func() *fixture {
		return newFixture().start("t1").owner("t1").calls("a1", "t1", "c1").compaction("k1", "t1", "a1").
			results("r1", "t1", "c1").say("a2", "t1").finish("t1", "completed")
	}},
	{"session_mismatch", func() *fixture {
		f := newFixture()
		f.conv = "other"
		f.start("t9")
		return f
	}},
	{"duplicate_message_uuid", func() *fixture {
		return newFixture().start("t1").owner("t1").say("a1", "t1").say("a1", "t1").finish("t1", "completed")
	}},
	{"invalid_message_content", func() *fixture {
		f := newFixture().start("t1").owner("t1")
		return f.raw(map[string]any{"type": "assistant", "uuid": "a1", "sessionId": f.conv, "timestamp": f.at(),
			"message": map[string]any{"role": "assistant", "content": "plain string", "nanite_turn_id": "t1"}}).finish("t1", "completed")
	}},
	{"missing_conversation_record", func() *fixture {
		f := &fixture{conv: fixtureConversation}
		return f.start("t1").owner("t1").say("a1", "t1").finish("t1", "completed")
	}},
	{"clean_reused_tool_ids", func() *fixture {
		// Some OpenAI-compatible servers number calls per response: the same
		// ID in a later step and a later turn still pairs step by step.
		return newFixture().start("t1").owner("t1").
			calls("a1", "t1", "call_0").results("r1", "t1", "call_0").
			calls("a2", "t1", "call_0").results("r2", "t1", "call_0").
			say("a3", "t1").finish("t1", "completed").
			start("t2").owner("t2").calls("a1-t2", "t2", "call_0").results("r1-t2", "t2", "call_0").
			say("a2-t2", "t2").finish("t2", "completed")
	}},
	{"duplicate_call_id_in_step", func() *fixture {
		return newFixture().start("t1").owner("t1").calls("a1", "t1", "c1", "c1").results("r1", "t1", "c1").
			say("a2", "t1").finish("t1", "completed")
	}},
	{"compaction_unknown_turn", func() *fixture {
		// compactions.turn_id references turns: the store cannot project it.
		return newFixture().toolTurn("t1").compaction("k1", "", "u-t1").compaction("k2", "ghost", "u-t1")
	}},
	{"payload_wrong_type", func() *fixture {
		// The store decodes into its own Turn type and fails on this field.
		return newFixture().side("nanite.turn.start", "t1", map[string]any{"ID": "t1", "UserMessageID": "u-t1", "Headers": 5})
	}},
	{"bad_timestamp", func() *fixture {
		f := newFixture()
		return f.raw(map[string]any{"type": "system", "subtype": "nanite.permission", "uuid": "p1", "sessionId": f.conv,
			"timestamp": "yesterday", "payload": map[string]any{}})
	}},
	{"unreadable_block", func() *fixture {
		// History decodes blocks into llmtypes.ContentBlock, whose content is
		// a string: an array here makes the conversation unreadable.
		return newFixture().start("t1").owner("t1").calls("a1", "t1", "c1").
			message("user", "r1", "t1", map[string]any{"type": "tool_result", "tool_use_id": "c1", "content": []any{"x"}}).
			say("a2", "t1").finish("t1", "completed")
	}},
	{"interrupted_reused_tool_id", func() *fixture {
		// ResumePlan pairs by ID across the turn: it would take this call as
		// answered and never write its result.
		return newFixture().start("t1").owner("t1").
			calls("a1", "t1", "call_0").results("r1", "t1", "call_0").
			calls("a2", "t1", "call_0").interrupt("t1")
	}},
	{"mixed", func() *fixture {
		return newFixture().toolTurn("t1").
			start("t2").owner("t2").calls("a1-t2", "t2", "c1", "c2").results("r1-t2", "t2", "c1", "ghost").
			say("a2-t2", "t2").finish("t2", "completed").finish("t2", "failed").
			side("nanite.future_kind", "x1", map[string]any{}).
			start("t3").owner("t3").calls("a1-t3", "t3", "c3").
			torn(`{"type":"user"`)
	}},
}
