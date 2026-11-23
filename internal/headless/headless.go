// Package headless runs a conversation the way `claude -p` does for a program driving it:
// turns come from a prompt or from stream-json lines on stdin, and the output is text, one JSON
// result, or stream-json messages in Claude Code's shapes (system/init, assistant and user
// content blocks, result).
//
// Input (--input-format stream-json), one JSON object per line:
//
//	{"type":"user","message":{"role":"user","content":"text" | [{"type":"text","text":"..."}]}}
//	{"type":"control_request","request_id":"r1","request":{"subtype":"interrupt"}}
//
// A user message that arrives while a turn runs is queued and sent when the turn ends; each user
// message gets its own result. An interrupt cancels the running turn and is answered with a
// control_response. The process ends when stdin closes and every queued message is done.
package headless

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hollis-labs/nanite/internal/engine"
	"github.com/hollis-labs/nanite/internal/events"
)

// Formats.
const (
	Text       = "text"
	JSON       = "json"
	StreamJSON = "stream-json"
)

// Sender is the part of a conversation a headless run uses.
type Sender interface {
	Send(context.Context, engine.Input) (*engine.Run, error)
}

// Options says what the run reports about itself and how it reads and writes.
type Options struct {
	// SessionID is the conversation id, reported as session_id.
	SessionID string
	Model     string
	Cwd       string
	// Tools, MCPServers and PermissionMode go into system/init.
	Tools          []string
	MCPServers     []string
	PermissionMode string
	InputFormat    string // text (default) or stream-json
	OutputFormat   string // text (default), json or stream-json
	// Prompt is the first user message, if any.
	Prompt string
	// Version is reported in system/init as claude_code_version's counterpart.
	Version string
	// Interrupts, if set, cancels the running turn on each receive (the CLI feeds it SIGINT);
	// with stream-json input and no turn running, it ends the session.
	Interrupts <-chan struct{}
	// Answer, if set, answers a Question (an ask_user call or an MCP elicitation); nil answers
	// null (declined).
	Answer func(ctx context.Context, q events.Question) json.RawMessage
}

// Run drives the conversation until the prompt (text input) or stdin (stream-json input) is done.
// It returns the last turn's error for text and json output, so the exit status says whether it
// worked; with stream-json output the errors are in the result messages.
func Run(ctx context.Context, conv Sender, o Options, in io.Reader, out io.Writer) error {
	if o.InputFormat == "" {
		o.InputFormat = Text
	}
	if o.OutputFormat == "" {
		o.OutputFormat = Text
	}
	switch o.InputFormat {
	case Text, StreamJSON:
	default:
		return fmt.Errorf("unsupported --input-format %q (text or stream-json)", o.InputFormat)
	}
	switch o.OutputFormat {
	case Text, JSON, StreamJSON:
	default:
		return fmt.Errorf("unsupported --output-format %q (text, json or stream-json)", o.OutputFormat)
	}
	if o.InputFormat == StreamJSON && o.OutputFormat != StreamJSON {
		return errors.New("--input-format stream-json needs --output-format stream-json")
	}
	h := &runner{conv: conv, o: o, w: &writer{enc: json.NewEncoder(out), out: out}}
	if o.InputFormat == Text {
		if strings.TrimSpace(o.Prompt) == "" {
			return errors.New("no prompt: give -p \"...\" or use --input-format stream-json")
		}
		h.init()
		stop := make(chan struct{})
		defer close(stop)
		go func() {
			for {
				select {
				case <-stop:
					return
				case <-o.Interrupts:
					h.interrupt()
				}
			}
		}()
		return h.turn(ctx, o.Prompt)
	}
	return h.stream(ctx, in)
}

// writer serializes output lines.
type writer struct {
	mu  sync.Mutex
	enc *json.Encoder
	out io.Writer
}

func (w *writer) json(v any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.enc.Encode(v)
}

func (w *writer) text(s string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, _ = fmt.Fprintln(w.out, s)
}

type runner struct {
	conv Sender
	o    Options
	w    *writer

	mu       sync.Mutex
	current  *engine.Run                     // the running turn, nil between turns
	stopAsk  context.CancelFunc              // ends the running turn's waits for answers
	answers  map[string]chan json.RawMessage // control_request id -> where its answer goes
	answerN  int
	stdinEOF bool
	inited   bool
	msgN     int
	queued   atomic.Int64 // user messages waiting behind the running turn (stream-json input)
}

// init writes system/init once (stream-json output only).
func (h *runner) init() {
	if h.o.OutputFormat != StreamJSON || h.inited {
		return
	}
	h.inited = true
	tools, servers := h.o.Tools, []map[string]string{}
	if tools == nil {
		tools = []string{}
	}
	for _, s := range h.o.MCPServers {
		servers = append(servers, map[string]string{"name": s, "status": "connected"})
	}
	h.w.json(map[string]any{
		"type": "system", "subtype": "init", "session_id": h.o.SessionID, "model": h.o.Model, "cwd": h.o.Cwd,
		"tools": tools, "mcp_servers": servers, "permissionMode": firstNonEmpty(h.o.PermissionMode, "default"),
		"apiKeySource": "none", "output_style": "default", "nanite_version": h.o.Version,
	})
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func (h *runner) messageID() string {
	h.msgN++
	return fmt.Sprintf("msg_%s_%d", h.o.SessionID, h.msgN)
}

// turn sends one user message and reports it until it ends; the result says how many messages
// wait behind it then (stream-json input).
func (h *runner) turn(ctx context.Context, text string) error {
	start := time.Now()
	run, err := h.conv.Send(ctx, engine.Input{Text: text})
	if err != nil {
		h.result(start, events.Result{}, err, nil)
		return err
	}
	askCtx, stopAsk := context.WithCancel(ctx)
	defer stopAsk()
	h.mu.Lock()
	h.current, h.stopAsk = run, stopAsk
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		h.current, h.stopAsk = nil, nil
		h.mu.Unlock()
	}()

	var reply, thinking strings.Builder
	var denials []map[string]any
	asked := map[string]events.PermissionAsked{}
	stream := h.o.OutputFormat == StreamJSON
	assistant := func(content ...map[string]any) {
		if !stream {
			return
		}
		h.w.json(map[string]any{"type": "assistant", "session_id": h.o.SessionID, "parent_tool_use_id": nil,
			"message": map[string]any{"id": h.messageID(), "type": "message", "role": "assistant", "model": h.o.Model, "content": content}})
	}
	flush := func() {
		if t := thinking.String(); t != "" {
			assistant(map[string]any{"type": "thinking", "thinking": t})
			thinking.Reset()
		}
		if t := reply.String(); strings.TrimSpace(t) != "" {
			assistant(map[string]any{"type": "text", "text": t})
		}
		reply.Reset()
	}
	for event := range run.Events() {
		switch v := event.(type) {
		case events.TextDelta:
			reply.WriteString(v.Text)
		case events.Thinking:
			thinking.WriteString(v.Text)
		case events.ToolCallStarted:
			flush()
			input := v.Input
			if len(input) == 0 || !json.Valid(input) {
				input = json.RawMessage(`{}`)
			}
			assistant(map[string]any{"type": "tool_use", "id": v.CallID, "name": v.Name, "input": input})
		case events.ToolCallFinished:
			flush()
			if stream {
				h.w.json(map[string]any{"type": "user", "session_id": h.o.SessionID, "parent_tool_use_id": nil,
					"message": map[string]any{"role": "user", "content": []map[string]any{{
						"type": "tool_result", "tool_use_id": v.CallID, "content": v.Result.Text, "is_error": v.IsError || v.Result.IsError,
					}}}})
			}
		case events.PermissionAsked:
			asked[v.Request.ID] = v
		case events.PermissionResolved:
			if a, ok := asked[v.ID]; ok && !v.Decision.Allow {
				input := a.Request.Input
				if len(input) == 0 {
					input = json.RawMessage(`{}`)
				}
				denials = append(denials, map[string]any{"tool_name": a.Request.Tool, "tool_input": input})
			}
		case events.Question:
			answer := json.RawMessage("null")
			switch {
			case h.o.Answer != nil:
				if a := h.o.Answer(askCtx, v); len(a) > 0 {
					answer = a
				}
			case h.o.InputFormat == StreamJSON:
				answer = h.ask(askCtx, v)
			}
			_ = run.Answer(v.ID, events.Answer{Value: answer})
		}
	}
	flush()
	result, err := run.Wait()
	h.result(start, result, err, denials)
	return err
}

// result reports the end of a turn: a result message (stream-json and json), or the text.
func (h *runner) result(start time.Time, r events.Result, err error, denials []map[string]any) {
	if denials == nil {
		denials = []map[string]any{}
	}
	subtype, isError, text, stop := "success", false, r.Text, firstNonEmpty(r.StopReason, "end_turn")
	switch {
	case errors.Is(err, context.Canceled):
		subtype, stop, text = "error_during_execution", "interrupted", "interrupted"
	case err != nil:
		subtype, isError, text, stop = "error_during_execution", true, err.Error(), "error"
	case strings.Contains(r.StopReason, "max_steps") || strings.Contains(r.StopReason, "step"):
		subtype = "error_max_turns"
	}
	if h.o.OutputFormat == Text {
		if err == nil {
			h.w.text(r.Text)
		}
		return
	}
	h.w.json(map[string]any{
		"type": "result", "subtype": subtype, "is_error": isError, "result": text, "stop_reason": stop,
		"session_id": h.o.SessionID, "duration_ms": time.Since(start).Milliseconds(), "num_turns": r.Steps,
		"usage": map[string]int{
			"input_tokens": r.Usage.InputTokens, "output_tokens": r.Usage.OutputTokens,
			"cache_read_input_tokens": r.Usage.CacheReadTokens, "cache_creation_input_tokens": r.Usage.CacheWriteTokens,
		},
		"permission_denials": denials, "queued_turn_count": h.queued.Load(),
	})
}

// stream reads stream-json input until stdin closes, one turn at a time.
func (h *runner) stream(ctx context.Context, in io.Reader) error {
	type line struct {
		b   []byte
		err error
	}
	lines := make(chan line)
	go func() {
		sc := bufio.NewScanner(in)
		sc.Buffer(make([]byte, 64<<10), 32<<20)
		for sc.Scan() {
			b := append([]byte(nil), sc.Bytes()...)
			select {
			case lines <- line{b: b}:
			case <-ctx.Done():
				return
			}
		}
		select {
		case lines <- line{err: errors.Join(io.EOF, sc.Err())}:
		case <-ctx.Done():
		}
	}()

	h.init()
	var queue []string
	if strings.TrimSpace(h.o.Prompt) != "" {
		queue = append(queue, h.o.Prompt)
	}
	done := make(chan struct{}, 1)
	running, eof := false, false
	startNext := func() {
		if running || len(queue) == 0 {
			return
		}
		text := queue[0]
		queue = queue[1:]
		running = true
		h.queued.Store(int64(len(queue)))
		go func() {
			_ = h.turn(context.WithoutCancel(ctx), text)
			done <- struct{}{}
		}()
	}
	startNext()
	for {
		if eof && !running && len(queue) == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			h.interrupt()
			if running {
				<-done
			}
			return ctx.Err()
		case <-h.o.Interrupts:
			if !running {
				return nil // nothing to interrupt: like claude, an interrupt between turns ends the session
			}
			h.interrupt()
		case <-done:
			running = false
			startNext()
		case l := <-lines:
			if l.err != nil {
				eof = true
				h.closed()
				lines = nil // nothing more will come
				continue
			}
			if text, ok := h.handle(l.b); ok {
				queue = append(queue, text)
				h.queued.Store(int64(len(queue)))
				startNext()
			}
		}
	}
}

// handle processes one input line: it returns a user message's text, or answers a control request.
func (h *runner) handle(b []byte) (string, bool) {
	var m struct {
		Type      string `json:"type"`
		RequestID string `json:"request_id"`
		Request   struct {
			Subtype string `json:"subtype"`
		} `json:"request"`
		Response struct {
			Subtype   string          `json:"subtype"`
			RequestID string          `json:"request_id"`
			Response  json.RawMessage `json:"response"`
		} `json:"response"`
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(b, &m) != nil {
		return "", false // not JSON: ignored, as claude does
	}
	switch m.Type {
	case "user":
		text := contentText(m.Message.Content)
		return text, strings.TrimSpace(text) != ""
	case "control_response":
		h.answered(m.Response.RequestID, m.Response.Response, m.Response.Subtype != "success")
	case "control_request":
		switch m.Request.Subtype {
		case "interrupt":
			h.interrupt()
			h.control(m.RequestID, nil)
		case "initialize":
			h.control(m.RequestID, map[string]any{"commands": []any{}, "output_style": "default"})
		default:
			h.w.json(map[string]any{"type": "control_response", "response": map[string]any{
				"subtype": "error", "request_id": m.RequestID, "error": "unsupported control request: " + m.Request.Subtype}})
		}
	}
	return "", false
}

func (h *runner) control(id string, response any) {
	r := map[string]any{"subtype": "success", "request_id": id}
	if response != nil {
		r["response"] = response
	}
	h.w.json(map[string]any{"type": "control_response", "response": r})
}

// Interrupt cancels the running turn, if any, and its waits for answers.
func (h *runner) interrupt() {
	h.mu.Lock()
	run, stop := h.current, h.stopAsk
	h.mu.Unlock()
	if stop != nil {
		stop()
	}
	if run != nil {
		run.Cancel()
	}
}

// ask puts a Question to the client (stream-json input) as an elicitation control request and
// waits for its control_response: {"action":"accept","content":<value>} answers it; decline,
// cancel, an error, an interrupt or stdin closing answers null (declined).
//
//	{"type":"control_request","request_id":"q-1","request":{"subtype":"elicitation","message":"...","requested_schema":{...}}}
func (h *runner) ask(ctx context.Context, q events.Question) json.RawMessage {
	h.mu.Lock()
	if h.stdinEOF {
		h.mu.Unlock()
		return json.RawMessage("null")
	}
	if h.answers == nil {
		h.answers = map[string]chan json.RawMessage{}
	}
	h.answerN++
	id := fmt.Sprintf("q-%d", h.answerN)
	ch := make(chan json.RawMessage, 1)
	h.answers[id] = ch
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.answers, id)
		h.mu.Unlock()
	}()
	schema := q.Schema
	if len(schema) == 0 || !json.Valid(schema) {
		schema = json.RawMessage(`{}`)
	}
	req := map[string]any{"subtype": "elicitation", "message": q.Prompt, "requested_schema": schema, "question_id": q.ID}
	if q.Subtask != nil {
		req["subtask"] = q.Subtask
	}
	h.w.json(map[string]any{"type": "control_request", "request_id": id, "request": req})
	select {
	case a := <-ch:
		return a
	case <-ctx.Done():
		return json.RawMessage("null")
	}
}

// answered routes a control_response to the question waiting for it.
func (h *runner) answered(id string, response json.RawMessage, failed bool) {
	h.mu.Lock()
	ch := h.answers[id]
	h.mu.Unlock()
	if ch == nil {
		return
	}
	value := json.RawMessage("null")
	var r struct {
		Action  string          `json:"action"`
		Content json.RawMessage `json:"content"`
	}
	if !failed && json.Unmarshal(response, &r) == nil && r.Action == "accept" && len(r.Content) > 0 {
		value = r.Content
	}
	select {
	case ch <- value:
	default:
	}
}

// closed notes that stdin ended: no question can be answered any more.
func (h *runner) closed() {
	h.mu.Lock()
	h.stdinEOF = true
	waiting := make([]chan json.RawMessage, 0, len(h.answers))
	for _, ch := range h.answers {
		waiting = append(waiting, ch)
	}
	h.mu.Unlock()
	for _, ch := range waiting {
		select {
		case ch <- json.RawMessage("null"):
		default:
		}
	}
}

// contentText is a user message's text: a string, or its text blocks joined.
func contentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type, Text string
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}
