package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// cmdChat is the entry point for `nanite chat` — a CLI client of Nanite's
// own harness, driven over the existing GUI-agnostic /api/agent/v1
// control plane (internal/api/agent_v1.go). It talks to an already-running
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
	project := fs.String("project", "", "project_id for a new session")
	definitionID := fs.String("definition-id", "", "definition ID for a new session (requires revision and digest)")
	definitionRevision := fs.String("definition-revision", "", "pinned definition revision")
	definitionDigest := fs.String("definition-digest", "", "pinned semantic digest")
	sessionID := fs.String("session", "", "resume an existing session id instead of creating a new one")
	title := fs.String("title", "", "title for a new session")
	noAutostart := fs.Bool("no-autostart", false, "fail fast instead of auto-starting `nanite serve` if it isn't already running")
	_ = fs.Parse(args) // ExitOnError terminates on parse failure; the returned error is unreachable.

	ctx := context.Background()

	resolvedURL := *url
	if resolvedURL == "" {
		resolvedURL = apiBaseURL()
	}
	if err := ensureServeRunning(ctx, resolvedURL, *noAutostart); err != nil {
		fmt.Fprintf(os.Stderr, "chat: %v\n", err)
		os.Exit(1)
	}

	client := newAgentClient(*url)

	sess, err := resolveChatSession(ctx, client, *sessionID, agentCreateSessionRequest{
		ProjectID:     *project,
		DefinitionRef: service.DefinitionRef{DefinitionID: *definitionID, Revision: *definitionRevision, SemanticDigest: *definitionDigest},
		Title:         *title,
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
			if scanErr := scanner.Err(); scanErr != nil {
				fmt.Fprintf(os.Stderr, "input error: %v\n", scanErr)
			} else {
				fmt.Println()
			}
			return
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == "/quit" || line == "/exit" {
			return
		}

		// Scope signal handling to just this turn: Ctrl-C cancels the
		// in-flight turn (Python/Node REPL convention), not the process.
		// Once stop() runs, a Ctrl-C at the idle "> " prompt reverts to the
		// default OS disposition (process exits). SIGTERM is deliberately
		// NOT included here — a service manager sending SIGTERM expects the
		// process to terminate, not have it swallowed as a turn-cancel and
		// have the REPL keep running.
		turnCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
		err := runChatTurn(turnCtx, client, sess.ID, line)
		stop()
		if err != nil {
			fmt.Fprintf(os.Stderr, "chat: %v\n", err)
		}
	}
}

func resolveChatSession(ctx context.Context, client *agentClient, sessionID string, createReq agentCreateSessionRequest) (*store.Session, error) {
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
func runChatTurn(ctx context.Context, client *agentClient, sessionID, content string) error {
	turn, err := client.SendTurn(ctx, sessionID, content)
	if err != nil {
		return err
	}
	if turn.TurnID == "" {
		return errors.New("accepted turn has no turn_id")
	}
	defer func() {
		if ctx.Err() != nil {
			if _, cancelErr := client.Cancel(context.Background(), sessionID, turn.TurnID); cancelErr != nil {
				fmt.Fprintf(os.Stderr, "chat: cancel failed: %v\n", cancelErr)
			}
		}
	}()
	events, err := client.StreamEvents(ctx, turn.StreamURL, turn.TurnID)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	for {
		select {
		case <-ctx.Done():
			fmt.Print("\nturn cancellation requested\n")
			return nil
		case evt, ok := <-events:
			if !ok {
				return nil
			}
			switch evt.Type {
			case "delta":
				fmt.Print(evt.Content)
			case "replace_content":
				fmt.Printf("\n[updated answer] %s", evt.Content)
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
				var payload chat.ApprovalRequestPayload
				if jsonErr := json.Unmarshal([]byte(evt.Data), &payload); jsonErr != nil || payload.RequestID == "" {
					fmt.Println("\n[approval requested] respond via the GUI — could not parse the request id from the event payload")
					continue
				}
				fmt.Printf("\n[approval requested] tool=%s reason=%q — respond via the GUI, or POST /api/agent/v1/sessions/%s/approvals/%s/responses (once scope)\n",
					payload.Tool, payload.Reason, sessionID, payload.RequestID)
			case "plugin_envelope":
				fmt.Printf("\n[envelope: %s]\n", evt.PluginID)
			case "error":
				// By the time an "error" event reaches here, StreamEvents has
				// already opened the connection (CW-20260813-0008's retry
				// only covers connection establishment, not this point
				// onward), so this is never retried — resuming is the user's
				// job, not the client's.
				return fmt.Errorf("%s; inspect turn %s status before submitting again (session %s)", evt.Error, turn.TurnID, sessionID)
			case "stream_end":
				fmt.Println()
			}
		}
	}
}
