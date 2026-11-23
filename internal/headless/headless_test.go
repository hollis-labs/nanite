package headless

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/engine"
	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/provider/fake"
	"github.com/hollis-labs/nanite/internal/skills"
	"github.com/hollis-labs/nanite/internal/slots"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/tools"
)

type conversation struct {
	*engine.Engine
	id string
}

func (c conversation) Send(ctx context.Context, input engine.Input) (*engine.Run, error) {
	return c.Engine.Send(ctx, c.id, input)
}

// newConversation is an engine over a fixture agent and a scripted model.
func newConversation(t *testing.T, responses ...fake.Response) conversation {
	t.Helper()
	ctx := t.Context()
	dir := t.TempDir()
	files := map[string]string{
		"agent.md": "---\nschema_version: \"2\"\ndefinition_id: def:headless-fixture\nrevision: \"1\"\nname: headless-fixture\n" +
			"description: Headless fixture.\nbehavior:\n  purpose: Exercise headless mode.\nrequirements: {}\n" +
			"harness_profile:\n  permissions:\n    profile: default\ncontinuity:\n  mode: ephemeral\n---\nAnswer briefly.\n",
		"host.yaml": "kind: embedded\nlifetime: application\napplication_id: headless\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	db, err := store.Open(ctx, filepath.Join(dir, ".nanite"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	core, err := engine.New(engine.Config{Dir: dir, GlobalSkillsDir: "-", Store: db, Provider: fake.New(responses...),
		Slots: slots.NewStore(), Tools: &tools.Groups{}, Skills: &skills.Groups{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(core.Close)
	now := time.Now().UTC()
	id := store.NewID(now)
	if err := db.CreateConversation(ctx, store.Conversation{ID: id, Role: "chat", Status: "active", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	return conversation{core, id}
}

func askUser(prompt string) fake.Response {
	return fake.Response{Events: []llmtypes.StreamEvent{
		{Type: llmtypes.EventToolUse, ToolUse: &llmtypes.ToolUseBlock{ID: "toolu_1", Name: "ask_user", Input: map[string]any{"prompt": prompt, "schema": map[string]any{"type": "boolean"}}}},
		{Type: llmtypes.EventDone},
	}}
}

type msg struct {
	Type, Subtype, SessionID string
	raw                      map[string]any
}

func decode(t *testing.T, line string) msg {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("not JSON: %q", line)
	}
	s := func(k string) string { v, _ := m[k].(string); return v }
	return msg{Type: s("type"), Subtype: s("subtype"), SessionID: s("session_id"), raw: m}
}

func TestOneShotFormats(t *testing.T) {
	c := newConversation(t, fake.Text("plain"), fake.Text("as json"), fake.Text("streamed"))
	var out bytes.Buffer
	if err := Run(t.Context(), c, Options{Prompt: "hi"}, nil, &out); err != nil || out.String() != "plain\n" {
		t.Fatalf("text: %q %v", out.String(), err)
	}
	out.Reset()
	if err := Run(t.Context(), c, Options{Prompt: "hi", OutputFormat: JSON, SessionID: "s1"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	r := decode(t, out.String())
	if r.Type != "result" || r.Subtype != "success" || r.raw["result"] != "as json" || r.SessionID != "s1" || r.raw["is_error"] != false {
		t.Fatalf("json: %s", out.String())
	}
	out.Reset()
	if err := Run(t.Context(), c, Options{Prompt: "hi", OutputFormat: StreamJSON, SessionID: "s1", Model: "m-1"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	var kinds []string
	for _, l := range lines {
		m := decode(t, l)
		kinds = append(kinds, m.Type+"/"+m.Subtype)
	}
	if strings.Join(kinds, " ") != "system/init assistant/ result/success" {
		t.Fatalf("stream: %v\n%s", kinds, out.String())
	}
	if init := decode(t, lines[0]); init.SessionID != "s1" || init.raw["model"] != "m-1" {
		t.Fatalf("init: %s", lines[0])
	}
	for _, bad := range []Options{{Prompt: "x", OutputFormat: "xml"}, {Prompt: "x", InputFormat: "yaml"}, {InputFormat: StreamJSON}, {}} {
		if err := Run(t.Context(), c, bad, strings.NewReader(""), io.Discard); err == nil {
			t.Errorf("%+v ran", bad)
		}
	}
}

func TestStreamJSONQueuesInterruptsAndAnswers(t *testing.T) {
	c := newConversation(t,
		fake.Text("hello back"),
		fake.Response{Stall: true}, // interrupted
		askUser("Ready?"), fake.Text("got it"),
	)
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	var asked []string
	done := make(chan error, 1)
	go func() {
		done <- Run(t.Context(), c, Options{InputFormat: StreamJSON, OutputFormat: StreamJSON, SessionID: "s1", Model: "m",
			Answer: func(_ context.Context, q events.Question) json.RawMessage {
				asked = append(asked, q.Prompt)
				return json.RawMessage("true")
			}}, inR, outW)
		_ = outW.Close()
	}()
	lines := make(chan msg, 64)
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			lines <- decode(t, sc.Text())
		}
		close(lines)
	}()
	next := func(want string) msg {
		t.Helper()
		for {
			select {
			case m, ok := <-lines:
				if !ok {
					t.Fatalf("output ended before %s", want)
				}
				if m.Type+"/"+m.Subtype == want || m.Type == want {
					return m
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("timed out waiting for %s", want)
			}
		}
	}
	write := func(s string) {
		t.Helper()
		if _, err := io.WriteString(inW, s+"\n"); err != nil {
			t.Fatal(err)
		}
	}
	user := func(text string) string {
		b, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": text}}}})
		return string(b)
	}

	next("system/init")
	write(user("hi"))
	if r := next("result/success"); r.raw["result"] != "hello back" || r.raw["queued_turn_count"] != float64(0) {
		t.Fatalf("first: %v", r.raw)
	}
	// The second turn stalls; the third arrives meanwhile and waits.
	write(user("take your time"))
	write(`{"type":"user","message":{"role":"user","content":"are you ready?"}}`)
	write("not json") // ignored
	time.Sleep(200 * time.Millisecond)
	write(`{"type":"control_request","request_id":"int-1","request":{"subtype":"interrupt"}}`)
	if cr := next("control_response"); cr.raw["response"].(map[string]any)["request_id"] != "int-1" {
		t.Fatalf("control: %v", cr.raw)
	}
	r := next("result")
	if r.Subtype != "error_during_execution" || r.raw["is_error"] != false || r.raw["stop_reason"] != "interrupted" || r.raw["queued_turn_count"] != float64(1) {
		t.Fatalf("interrupted: %v", r.raw)
	}
	// The queued message runs: ask_user is a tool call answered by Answer, then the reply.
	tu := next("assistant")
	block := tu.raw["message"].(map[string]any)["content"].([]any)[0].(map[string]any)
	if block["type"] != "tool_use" || block["name"] != "ask_user" || block["id"] != "toolu_1" {
		t.Fatalf("tool_use: %v", tu.raw)
	}
	tr := next("user")
	res := tr.raw["message"].(map[string]any)["content"].([]any)[0].(map[string]any)
	if res["type"] != "tool_result" || res["tool_use_id"] != "toolu_1" {
		t.Fatalf("tool_result: %v", tr.raw)
	}
	if r := next("result/success"); r.raw["result"] != "got it" {
		t.Fatalf("third: %v", r.raw)
	}
	if len(asked) != 1 || asked[0] != "Ready?" {
		t.Fatalf("asked %v", asked)
	}
	// Unknown control requests are refused; stdin closing ends the run.
	write(`{"type":"control_request","request_id":"x","request":{"subtype":"rewind"}}`)
	if cr := next("control_response"); cr.raw["response"].(map[string]any)["subtype"] != "error" {
		t.Fatalf("unsupported: %v", cr.raw)
	}
	_ = inW.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("did not end on EOF")
	}
}

func TestInterruptsChannelCancelsAOneShot(t *testing.T) {
	c := newConversation(t, fake.Response{Stall: true})
	ints := make(chan struct{}, 1)
	var out bytes.Buffer
	go func() { time.Sleep(200 * time.Millisecond); ints <- struct{}{} }()
	err := Run(t.Context(), c, Options{Prompt: "hi", OutputFormat: JSON, Interrupts: ints}, nil, &out)
	if err == nil || !strings.Contains(out.String(), `"stop_reason":"interrupted"`) {
		t.Fatalf("%v %s", err, out.String())
	}
}
