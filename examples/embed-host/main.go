// Command embed-host demonstrates the Nanite embedding API without Substrate.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hollis-labs/libs/llm/core/llmcontracts"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/agent"
	"github.com/hollis-labs/nanite/internal/definition"
	"github.com/hollis-labs/nanite/internal/provider"
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

func demo(ctx context.Context, w io.Writer, dir string, offline bool) (retErr error) {
	if dir == "" {
		var err error
		dir, err = os.MkdirTemp("", "nanite-embed-")
		if err != nil {
			return err
		}
		defer func() { retErr = errors.Join(retErr, os.RemoveAll(dir)) }()
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
	// An application seat does not need a shell or the ability to write files:
	// name the built-in groups it gets, and the rest are never shown to the model.
	builtins := []agent.BuiltinGroup{agent.BuiltinFilesRead, agent.BuiltinSkills, agent.BuiltinNotes, agent.BuiltinSubtasks, agent.BuiltinQuestions}
	opts := agent.Options{Dir: dir, GlobalSkillsDir: "-", Builtins: builtins, Gateway: agent.Gateway{
		BaseURL: os.Getenv("NANITE_GATEWAY_URL"), KeyRef: os.Getenv("NANITE_GATEWAY_KEY_REF"),
		Auth: os.Getenv("NANITE_GATEWAY_AUTH"),
	}}
	model := &observedProvider{}
	if offline {
		model.Provider = fake.New(call("1", "notes_search"), fake.Text("The notes mention a planning meeting."),
			call("2", "files_list"), fake.Text("The workspace contains agenda.md."), fake.Text("The workspace context is clear."))
	} else {
		def, err := definition.Load(dir, "-")
		if err != nil {
			return err
		}
		model.Provider, err = provider.New(provider.Config{Provider: def.Runtime.Provider, Gateway: opts.Gateway})
		if err != nil {
			return err
		}
	}
	opts.Provider = model
	a, err := agent.Open(ctx, opts)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, a.Close()) }()
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
		var report agent.SlotReport
		var calls []string
		for event := range run.Events() {
			switch e := event.(type) {
			case agent.SlotsAssembled:
				report = e.Report
			case agent.ToolCallStarted:
				calls = append(calls, e.Name)
			case agent.Question:
				_ = run.Answer(e.ID, agent.Answer{Value: json.RawMessage("null")})
			}
		}
		result, err := run.Wait()
		if err != nil {
			return err
		}
		if _, err := fmt.Fprint(w, turnReport(i+1, report, model.offered, calls, result.Text)); err != nil {
			return err
		}
	}
	return nil
}

// observedProvider records the tool names from the actual provider requests.
type observedProvider struct {
	llmcontracts.Provider
	offered []string
}

func (p *observedProvider) StreamChat(ctx context.Context, req llmtypes.ChatRequest) (<-chan llmtypes.StreamEvent, error) {
	p.offered = p.offered[:0]
	for _, tool := range req.Tools {
		p.offered = append(p.offered, tool.Name)
	}
	slices.Sort(p.offered)
	return p.Provider.StreamChat(ctx, req)
}

func turnReport(turn int, report agent.SlotReport, offered, calls []string, response string) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Turn %d slots:\n", turn)
	for _, name := range agent.SlotNames() {
		slot := report[name]
		if slot.Tokens == 0 && !slot.Changed {
			continue
		}
		changed := "no"
		if slot.Changed {
			changed = "yes"
		}
		fmt.Fprintf(&out, "  %s: %d tokens, changed %s\n", name, slot.Tokens, changed)
		for _, entry := range slot.Decisions {
			var disposition string
			switch entry.Disposition {
			case agent.SlotShip:
				disposition = "ship"
			case agent.SlotPointer:
				disposition = "pointer"
			case agent.SlotSkip:
				disposition = "skip"
			case agent.SlotTrim:
				disposition = "trim"
			}
			fmt.Fprintf(&out, "    %s/%s: %s\n", entry.Owner, entry.Key, disposition)
		}
	}
	fmt.Fprintf(&out, "Tools offered: %s\n", strings.Join(offered, ", "))
	for _, name := range calls {
		fmt.Fprintf(&out, "Tool called: %s\n", name)
	}
	fmt.Fprintln(&out, response)
	return out.String()
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
