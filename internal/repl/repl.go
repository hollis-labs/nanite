package repl

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
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
	go func() {
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
	}()
	return Terminal{lines: lines, Output: output}
}

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
	if _, err := fmt.Fprintf(t.Output, "Allow %s (%s)? [y/N/always] ", request.Tool, request.Reason); err != nil {
		return permission.PermissionDecision{}, err
	}
	value, err := t.read(ctx)
	if err != nil {
		return permission.PermissionDecision{}, err
	}
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "y", "yes":
		return permission.PermissionDecision{Allow: true, Reason: "approved at terminal"}, nil
	case "always":
		return permission.PermissionDecision{Allow: true, Scope: permission.ScopeConversation, Reason: "approved for conversation"}, nil
	default:
		return permission.PermissionDecision{Reason: "denied at terminal"}, nil
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
		if err == io.EOF && line == "" {
			return nil
		}
		if err != nil && err != io.EOF {
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
				for event := range run.Events() {
					switch v := event.(type) {
					case events.TextDelta:
						_, _ = fmt.Fprint(t.Output, v.Text)
					case events.ToolCallStarted:
						_, _ = fmt.Fprintf(t.Output, "\n[%s]\n", v.Name)
					case events.SlotsAssembled:
						report = v.Report
					}
				}
				_, err = run.Wait()
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
	if format == "stream-json" {
		enc := json.NewEncoder(out)
		for event := range run.Events() {
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

// Sender is the part of an authored conversation used by the terminal.
type Sender interface {
	Send(context.Context, engine.Input) (*engine.Run, error)
}
