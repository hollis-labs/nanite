package repl

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/hollis-labs/libs/core/panics"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/hollis-labs/nanite/internal/engine"
	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/nanite/internal/slots"
)

type line struct {
	text string
	err  error
}

type Terminal struct {
	lines  <-chan line
	Output io.Writer
}

func NewTerminal(ctx context.Context, input io.Reader, output io.Writer) Terminal {
	lines := make(chan line)
	inputGuard.Go("read terminal input", func() {
		defer close(lines)
		reader := bufio.NewReader(input)
		for ctx.Err() == nil {
			text, err := reader.ReadString('\n')
			select {
			case lines <- line{text, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}, nil)
	return Terminal{lines: lines, Output: output}
}

// inputGuard contains a panic in the input reader. A closed input ends the
// session as end of input does. The value is withheld.
var inputGuard = panics.New(panics.Config{})

func (t Terminal) read(ctx context.Context) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case line, ok := <-t.lines:
		if !ok {
			return "", io.EOF
		}
		return line.text, line.err
	}
}

func (t Terminal) Decide(ctx context.Context, request permission.PermissionRequest) (permission.PermissionDecision, error) {
	if err := ctx.Err(); err != nil {
		return permission.PermissionDecision{}, err
	}
	if request.Tool == "bash" || request.Tool == "background_start" {
		var args struct{ Command string }
		if err := json.Unmarshal(request.Input, &args); err != nil {
			return permission.PermissionDecision{}, err
		}
		if _, err := fmt.Fprintf(t.Output, "Bash command: %q\n", args.Command); err != nil {
			return permission.PermissionDecision{}, err
		}
	}
	if o := request.Subtask; o != nil {
		if _, err := fmt.Fprintf(t.Output, "Subtask %q (%s, depth %d) asks; \"always\" covers this subtask and its own subtasks only:\n", o.Goal, o.Variant, o.Depth); err != nil {
			return permission.PermissionDecision{}, err
		}
	}
	if _, err := fmt.Fprintf(t.Output, "Allow %s (%s)? [y/N/always] ", request.Tool, request.Reason); err != nil {
		return permission.PermissionDecision{}, err
	}
	value, err := t.read(ctx)
	if err != nil {
		return permission.PermissionDecision{}, err
	}
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "y", "yes":
		return permission.PermissionDecision{Allow: true, Reason: "approved at terminal", Human: true}, nil
	case "always":
		return permission.PermissionDecision{Allow: true, Scope: permission.ScopeConversation, Reason: "approved for conversation", Human: true}, nil
	default:
		return permission.PermissionDecision{Reason: "denied at terminal", Human: true}, nil
	}
}

func (t Terminal) Chat(ctx context.Context, fresh func(context.Context) (Sender, error), conversation Sender) error {
	var report slots.Report
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := fmt.Fprint(t.Output, "\n> "); err != nil {
			return err
		}
		line, err := t.read(ctx)
		if errors.Is(err, io.EOF) && line == "" {
			return nil
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		line = strings.TrimSpace(line)
		switch line {
		case "":
			continue
		case "/quit":
			return nil
		case "/new":
			conversation, err = fresh(ctx)
			if err != nil {
				return err
			}
			report = nil
		case "/slots":
			if err := json.NewEncoder(t.Output).Encode(report); err != nil {
				return err
			}
		default:
			turnCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
			run, err := conversation.Send(turnCtx, engine.Input{Text: line})
			if err == nil {
				err = t.render(turnCtx, run, &report)
			}
			stop()
			if err != nil {
				if _, writeErr := fmt.Fprintln(t.Output, "\n"+err.Error()); writeErr != nil {
					return writeErr
				}
			}
		}
	}
}

func (t Terminal) render(ctx context.Context, run *engine.Run, report *slots.Report) error {
	streamed := false
	for event := range run.Events() {
		var err error
		switch v := event.(type) {
		case events.TextDelta:
			streamed = true
			_, err = fmt.Fprint(t.Output, v.Text)
		case events.ToolCallStarted:
			_, err = fmt.Fprintf(t.Output, "\n[%s]\n", v.Name)
		case events.Question:
			err = t.answer(ctx, run, v)
		case events.SlotsAssembled:
			*report = v.Report
		}
		if err != nil {
			run.Cancel()
			_, _ = run.Wait()
			return err
		}
	}
	result, err := run.Wait()
	if err == nil && !streamed && result.Text != "" {
		_, err = fmt.Fprint(t.Output, result.Text)
	}
	return err
}

// Choices for an interrupted turn found on startup.
const (
	ResumeTurn  = "resume"
	DiscardTurn = "discard"
	LeaveTurn   = "leave"
)

// ConfirmResume asks what to do with an interrupted turn found on startup.
// Anything but a clear yes or discard leaves the turn as it is.
func (t Terminal) ConfirmResume(ctx context.Context, conversation string) (string, error) {
	if _, err := fmt.Fprintf(t.Output, "Conversation %s has an interrupted turn. Resume it? [y/N/discard] ", conversation); err != nil {
		return LeaveTurn, err
	}
	response, err := t.read(ctx)
	if err != nil && !errors.Is(err, io.EOF) {
		return LeaveTurn, err
	}
	switch strings.ToLower(strings.TrimSpace(response)) {
	case "y", "yes":
		return ResumeTurn, nil
	case "d", "discard":
		return DiscardTurn, nil
	default:
		return LeaveTurn, nil
	}
}

func (t Terminal) Resume(ctx context.Context, start func(context.Context) (*engine.Run, error)) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	run, err := start(ctx)
	if err != nil {
		return err
	}
	var report slots.Report
	if err := t.render(ctx, run, &report); err != nil {
		return err
	}
	_, err = fmt.Fprintln(t.Output)
	return err
}

func One(ctx context.Context, c Sender, prompt, format string, out io.Writer) error {
	switch format {
	case "text", "json", "stream-json":
	default:
		return fmt.Errorf("unsupported --output %q", format)
	}
	run, err := c.Send(ctx, engine.Input{Text: prompt})
	if err != nil {
		return err
	}
	return Output(run, format, out)
}

func Output(run *engine.Run, format string, out io.Writer) error {
	if format == "stream-json" {
		enc := json.NewEncoder(out)
		for event := range run.Events() {
			if question, ok := event.(events.Question); ok {
				_ = run.Answer(question.ID, events.Answer{Value: json.RawMessage("null")})
			}

			var payload any = event
			if failed, ok := event.(events.RunFailed); ok {
				payload = struct {
					events.EventMeta
					Error string
				}{failed.EventMeta, failed.Err.Error()}
			}
			if err := enc.Encode(struct {
				Type  string `json:"type"`
				Event any    `json:"event"`
			}{strings.TrimPrefix(fmt.Sprintf("%T", event), "events."), payload}); err != nil {
				run.Cancel()
				_, _ = run.Wait()
				return err
			}
		}
	}
	result, err := run.Wait()
	if err != nil {
		return err
	}
	switch format {
	case "json":
		return json.NewEncoder(out).Encode(result)
	case "text":
		_, err = fmt.Fprintln(out, result.Text)
	}
	return err
}

func (t Terminal) answer(ctx context.Context, run *engine.Run, q events.Question) error {
	var schema struct{ Type string }
	_ = json.Unmarshal(q.Schema, &schema)
	if o := q.Subtask; o != nil {
		if _, err := fmt.Fprintf(t.Output, "\nSubtask %q (%s, depth %d) asks:", o.Goal, o.Variant, o.Depth); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(t.Output, "\n%s\n", q.Prompt); err != nil {
		return err
	}
	if schema.Type != "string" {
		if _, err := fmt.Fprintf(t.Output, "Answer as JSON matching %s. Enter /decline to skip.\n", q.Schema); err != nil {
			return err
		}
	}
	for {
		if _, err := fmt.Fprint(t.Output, "Answer: "); err != nil {
			return err
		}
		line, err := t.read(ctx)
		if ctx.Err() != nil {
			return nil //nolint:nilerr // Let render drain the canceled run and return its terminal error.
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		input := strings.TrimSpace(line)
		var value json.RawMessage
		switch {
		case input == "/decline" || (input == "" && errors.Is(err, io.EOF)):
			value = json.RawMessage("null")
		case schema.Type == "string":
			value, _ = json.Marshal(input)
		default:
			value = json.RawMessage(input)
		}
		if err := run.Answer(q.ID, events.Answer{Value: value}); err != nil {
			if ctx.Err() != nil {
				return nil //nolint:nilerr // Let render report the run cancellation.
			}
			if _, writeErr := fmt.Fprintln(t.Output, err); writeErr != nil {
				return writeErr
			}
			continue
		}
		return nil
	}
}

// Sender is the part of an authored conversation used by the terminal.
type Sender interface {
	Send(context.Context, engine.Input) (*engine.Run, error)
}
