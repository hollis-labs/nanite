package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/provider/fake"
	"github.com/hollis-labs/nanite/internal/tools"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func waitQuestion(t *testing.T, run *Run) events.Question {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	for {
		select {
		case event, ok := <-run.Events():
			if !ok {
				t.Fatal("run ended before any question")
			}
			if q, ok := event.(events.Question); ok {
				return q
			}
			if result, ok := event.(events.ToolCallFinished); ok && result.IsError {
				t.Fatalf("tool failed before question: %s", result.Result.Text)
			}
		case <-ctx.Done():
			t.Fatal("timed out awaiting question")
		}
	}
}

func TestAskUserValidatesAndUnblocks(t *testing.T) {
	response := toolResponse("ask_user")
	response.Events[0].ToolUse.Input = map[string]any{"prompt": "Ready?", "schema": map[string]any{"type": "boolean"}}
	e, db, id := setupEngine(t, fake.New(response, fake.Text("done"), response), "")
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	q := waitQuestion(t, run)
	if q.Prompt != "Ready?" {
		t.Fatalf("prompt %q", q.Prompt)
	}
	if err := run.Answer(q.ID, events.Answer{Value: json.RawMessage(`"yes"`)}); err == nil {
		t.Fatal("accepted a string for a boolean question")
	}
	if err := run.Answer(q.ID, events.Answer{Value: json.RawMessage(`true`)}); err != nil {
		t.Fatal(err)
	}
	if err := run.Answer(q.ID, events.Answer{Value: json.RawMessage(`false`)}); err == nil {
		t.Fatal("accepted a second answer")
	}
	if result, err := run.Wait(); err != nil || result.Text != "done" {
		t.Fatalf("result %+v, %v", result, err)
	}
	history, err := db.History(t.Context(), id)
	if err != nil || history[2].Blocks[0].Content != "true" {
		t.Fatalf("answer was not persisted: %+v, %v", history, err)
	}

	run, err = e.Send(t.Context(), id, Input{Text: "cancel"})
	if err != nil {
		t.Fatal(err)
	}
	q = waitQuestion(t, run)
	run.Cancel()
	if _, err := run.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	if err := run.Answer(q.ID, events.Answer{Value: json.RawMessage(`true`)}); err == nil {
		t.Fatal("cancelled question is still pending")
	}
}

func TestMCPElicitationUsesActiveRun(t *testing.T) {
	server := sdk.NewServer(&sdk.Implementation{Name: "elicitation-fixture", Version: "1"}, nil)
	sdk.AddTool(server, &sdk.Tool{Name: "question", Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true}},
		func(ctx context.Context, req *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, any, error) {
			answer, err := req.Session.Elicit(ctx, &sdk.ElicitParams{Mode: "form", Message: "Pick a label",
				RequestedSchema: json.RawMessage(`{"type":"object","properties":{"label":{"type":"string"}},"required":["label"]}`)})
			if err != nil {
				return nil, nil, err
			}
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: fmt.Sprint(answer.Content["label"])}}}, nil, nil
		})
	httpServer := httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil))
	t.Cleanup(httpServer.Close)
	e, db, id := setupEngine(t, fake.New(toolResponse("mcp__fixture__question"), fake.Text("done")), "")
	config := fmt.Sprintf(`{"mcpServers":{"fixture":{"type":"http","url":%q}}}`, httpServer.URL)
	if err := os.WriteFile(filepath.Join(e.cfg.Dir, ".mcp.json"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	run, err := e.Send(t.Context(), id, Input{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	defer run.Cancel()
	q := waitQuestion(t, run)
	if !strings.Contains(q.Prompt, "Pick a label") {
		t.Fatalf("prompt %q", q.Prompt)
	}
	if err := run.Answer(q.ID, events.Answer{Value: json.RawMessage(`{"label":"chosen"}`)}); err != nil {
		t.Fatal(err)
	}
	if result, err := run.Wait(); err != nil || result.Text != "done" {
		t.Fatalf("result %+v, %v", result, err)
	}
	h, err := db.History(t.Context(), id)
	if err != nil || h[2].Blocks[0].Content != "chosen" {
		t.Fatalf("MCP answer was not returned: %+v, %v", h, err)
	}
}

func TestNoticesWakeHostAndAppearOnce(t *testing.T) {
	p := &watchedProvider{Provider: fake.New(fake.Text("first"), fake.Text("second"))}
	e, db, id := setupEngine(t, p, "")
	if err := e.PostNotice(t.Context(), id, "job", "build finished"); err != nil {
		t.Fatal(err)
	}
	select {
	case n := <-e.Notices():
		if n.ConversationID != id || n.Body != "build finished" {
			t.Fatalf("notice %+v", n)
		}
	default:
		t.Fatal("idle host did not receive notice")
	}
	for i := range 2 {
		run, err := e.Send(t.Context(), id, Input{Text: "go"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := run.Wait(); err != nil {
			t.Fatal(err)
		}
		present := false
		for _, block := range p.requests[i].SlotBlocks {
			present = present || block.Name == "session" && strings.Contains(block.Content, "build finished")
		}
		if present != (i == 0) {
			t.Fatalf("turn %d notice present=%v", i, present)
		}
	}
	pending, err := db.PendingNotices(t.Context(), id)
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending %+v, %v", pending, err)
	}
}

func TestSchemaRepairBeforeAuthorization(t *testing.T) {
	for _, tc := range []struct {
		name, enabled, repair string
		want                  bool
	}{
		{"valid", "true", `{"repaired_args":{"name":"fixture"},"missing_required":[]}`, true},
		{"disabled", "false", "", false},
		{"invented", "true", `{"repaired_args":{"name":"invented"},"missing_required":[]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("NANITE_AUTO_REPAIR", tc.enabled)
			response := toolResponse("schema_fixture")
			response.Events[0].ToolUse.Input = map[string]any{"alias": "fixture"}
			script := []fake.Response{response}
			if tc.enabled == "true" {
				script = append(script, fake.Text(tc.repair))
			}
			script = append(script, fake.Text("done"))
			e, db, id := setupEngine(t, fake.New(script...), "")
			executed := false
			if err := e.cfg.Tools.SetGroup("fixture", tools.Tool{Name: "schema_fixture",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"],"additionalProperties":false}`),
				Handler: func(_ context.Context, call tools.Call) (tools.Result, error) {
					executed = true
					return tools.Result{Text: string(call.Input)}, nil
				},
			}); err != nil {
				t.Fatal(err)
			}
			run, err := e.Send(t.Context(), id, Input{Text: "go"})
			if err != nil {
				t.Fatal(err)
			}
			if result, err := run.Wait(); err != nil || result.Text != "done" {
				t.Fatalf("result %+v, %v", result, err)
			}
			h, err := db.History(t.Context(), id)
			if err != nil || executed != tc.want || h[2].Blocks[0].IsError == tc.want {
				t.Fatalf("executed %v, history %+v, %v", executed, h, err)
			}
			if tc.want && h[2].Blocks[0].Content != `{"name":"fixture"}` {
				t.Fatalf("unexpected applied args: %q", h[2].Blocks[0].Content)
			}
		})
	}
}
