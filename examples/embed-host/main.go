// Command embed-host demonstrates the Nanite embedding API without Substrate.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/agent"
	"github.com/hollis-labs/nanite/internal/provider/fake"
)

//go:embed agentdir/agent.md agentdir/host.yaml
var exampleFiles embed.FS

func main() {
	offline := flag.Bool("offline", false, "Use a scripted provider")
	dir := flag.String("dir", "", "Use an existing agent directory")
	flag.Parse()
	if err := demo(context.Background(), os.Stdout, *dir, *offline); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func demo(ctx context.Context, w io.Writer, dir string, offline bool) error {
	if dir == "" {
		var err error
		dir, err = os.MkdirTemp("", "nanite-embed-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		for _, name := range []string{"agent.md", "host.yaml"} {
			data, err := exampleFiles.ReadFile("agentdir/" + name)
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
				return err
			}
		}
	}
	opts := agent.Options{Dir: dir, GlobalSkillsDir: "-", Gateway: agent.Gateway{
		BaseURL: os.Getenv("NANITE_GATEWAY_URL"), KeyRef: os.Getenv("NANITE_GATEWAY_KEY_REF"),
		Auth: os.Getenv("NANITE_GATEWAY_AUTH"),
	}}
	if offline {
		opts.Provider = fake.New(call("1", "notes_search"), fake.Text("The notes mention a planning meeting."),
			call("2", "files_list"), fake.Text("The workspace contains agenda.md."), fake.Text("The workspace context is clear."))
	}
	a, err := agent.Open(ctx, opts)
	if err != nil {
		return err
	}
	defer a.Close()
	conversation, err := a.NewConversation(ctx)
	if err != nil {
		return err
	}
	writer := a.Slots().Owner("demo")
	turns := []struct {
		workspace, context, prompt string
		tool                       agent.Tool
	}{
		{"Notes", "Notebook: Planning", "What do my planning notes say?",
			lookup("notes_search", "Search the planning notes.", "Planning meeting on Friday.")},
		{"Files", "Directory: Project", "What files are in the project?",
			lookup("files_list", "List the project files.", "agenda.md")},
		{prompt: "Which workspace is currently selected?"},
	}
	for i, turn := range turns {
		if turn.workspace == "" {
			writer.Clear()
			a.Tools().RemoveGroup("demo")
		} else {
			if err := writer.Replace(agent.SlotWorkspace, agent.SlotEntry{Key: "view", Content: turn.workspace}); err != nil {
				return err
			}
			if err := writer.Replace(agent.SlotContext, agent.SlotEntry{Key: "selection", Content: turn.context}); err != nil {
				return err
			}
			if err := a.Tools().SetGroup("demo", turn.tool); err != nil {
				return err
			}
		}
		run, err := conversation.Send(ctx, agent.Input{Text: turn.prompt})
		if err != nil {
			return err
		}
		for event := range run.Events() {
			switch e := event.(type) {
			case agent.SlotsAssembled:
				if _, err := fmt.Fprintf(w, "Turn %d slots: %s, %s\n", i+1, slotStatus(e.Report, agent.SlotWorkspace), slotStatus(e.Report, agent.SlotContext)); err != nil {
					run.Cancel()
					_, _ = run.Wait()
					return err
				}
			case agent.ToolCallStarted:
				if _, err := fmt.Fprintf(w, "Tool: %s\n", e.Name); err != nil {
					run.Cancel()
					_, _ = run.Wait()
					return err
				}
			case agent.Question:
				_ = run.Answer(e.ID, agent.Answer{Value: json.RawMessage("null")})
			}
		}
		result, err := run.Wait()
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w, result.Text); err != nil {
			return err
		}
	}
	return nil
}

func slotStatus(report agent.SlotReport, name agent.SlotName) string {
	slot := report[name]
	if slot.Tokens == 0 && slot.Changed {
		return string(name) + " cleared"
	}
	if slot.Changed {
		return string(name) + " changed"
	}
	return string(name) + " unchanged"
}

func lookup(name, description, text string) agent.Tool {
	return agent.Tool{Name: name, Description: description, ReadOnly: true, InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(context.Context, agent.ToolCall) (agent.ToolResult, error) {
			return agent.ToolResult{Text: text}, nil
		}}
}

func call(id, name string) fake.Response {
	return fake.Response{Events: []llmtypes.StreamEvent{
		{Type: llmtypes.EventToolUse, ToolUse: &llmtypes.ToolUseBlock{ID: id, Name: name}},
		{Type: llmtypes.EventDone},
	}}
}
