package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/hollis-labs/nanite/internal/store"
)

// cmdChat is the entry point for `nanite chat` — a CLI client of Nanite's
// own harness, driven over the existing GUI-agnostic /api/harness/v1
// control plane (internal/api/harness_v1.go). It talks to an already-running
// `nanite serve` process; it does not start its own server or touch the
// database directly.
//
// This is additive to the existing CLI-subprocess agent launching (`nanite
// launch`, agentkit-based agent.Boot) — a second, independent surface on the
// same harness and turn loop the browser UI drives, not a replacement.
// CW-20260812-0001.
func cmdChat(args []string) {
	fs := flag.NewFlagSet("chat", flag.ExitOnError)
	url := fs.String("url", "", "harness API base URL (default: "+apiBaseURL()+"; also honors NANITE_API_URL/NANITE_PORT)")
	workspace := fs.String("workspace", "", "workspace_id for a new session (required unless --session is given)")
	project := fs.String("project", "", "project_id for a new session")
	agentID := fs.String("agent", "", "agent_id for a new session")
	sessionID := fs.String("session", "", "resume an existing session id instead of creating a new one")
	title := fs.String("title", "", "title for a new session")
	fs.Parse(args)

	if *sessionID == "" && *workspace == "" {
		fmt.Fprintln(os.Stderr, "chat: --workspace is required when creating a new session (or pass --session to resume one)")
		os.Exit(1)
	}

	client := newHarnessClient(*url)
	ctx := context.Background()

	sess, err := resolveChatSession(ctx, client, *sessionID, harnessCreateSessionRequest{
		WorkspaceID: *workspace,
		ProjectID:   *project,
		AgentID:     *agentID,
		Title:       *title,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "chat: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("session %s (%s) — type a message, Ctrl-D or /quit to exit\n", sess.ID, client.baseURL)

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			fmt.Println()
			return
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == "/quit" || line == "/exit" {
			return
		}
		if err := runChatTurn(ctx, client, sess.ID, line); err != nil {
			fmt.Fprintf(os.Stderr, "chat: %v\n", err)
		}
	}
}

func resolveChatSession(ctx context.Context, client *harnessClient, sessionID string, createReq harnessCreateSessionRequest) (*store.Session, error) {
	if sessionID != "" {
		return client.GetSession(ctx, sessionID)
	}
	return client.CreateSession(ctx, createReq)
}

// runChatTurn sends one message and renders its SSE stream as plain text:
// assistant deltas print inline, tool activity and approval requests print
// as one-line markers. Rich envelope rendering (report cards, etc.) is a
// GUI-only concern — the CLI surfaces plugin_envelope events only as a
// terse marker, not a rendered card.
func runChatTurn(ctx context.Context, client *harnessClient, sessionID, content string) error {
	turn, err := client.SendTurn(ctx, sessionID, content)
	if err != nil {
		return err
	}
	events, err := client.StreamEvents(ctx, turn.StreamURL)
	if err != nil {
		return err
	}
	for evt := range events {
		switch evt.Type {
		case "delta":
			fmt.Print(evt.Content)
		case "tool_call":
			label := evt.Tool
			if evt.Detail != "" {
				label += ": " + evt.Detail
			}
			fmt.Printf("\n[tool] %s\n", label)
		case "tool_result":
			status := "ok"
			if evt.IsError {
				status = "error"
			}
			fmt.Printf("[tool %s] %s\n", status, evt.Summary)
		case "approval_request":
			// Data is a JSON payload {request_id, tool, input, reason} —
			// see internal/service/chat_tool_executor.go's approvalData.
			var payload struct {
				RequestID string `json:"request_id"`
				Tool      string `json:"tool"`
				Reason    string `json:"reason"`
			}
			if jsonErr := json.Unmarshal([]byte(evt.Data), &payload); jsonErr != nil || payload.RequestID == "" {
				fmt.Println("\n[approval requested] respond via the GUI — could not parse the request id from the event payload")
				continue
			}
			fmt.Printf("\n[approval requested] tool=%s reason=%q — respond via the GUI, or POST /api/harness/v1/sessions/%s/approvals/%s\n",
				payload.Tool, payload.Reason, sessionID, payload.RequestID)
		case "plugin_envelope":
			fmt.Printf("\n[envelope: %s]\n", evt.PluginID)
		case "error":
			fmt.Printf("\n[error] %s\n", evt.Error)
		case "stream_end":
			fmt.Println()
		}
	}
	return nil
}
