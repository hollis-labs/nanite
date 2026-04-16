package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/agent/builtin"
	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/messaging"
	"github.com/hollis-labs/nanite/internal/slogx"
	"github.com/hollis-labs/nanite/internal/store"
)

// cmdMessage is the entry point for the `nanite message ...` command group. It
// parses a single optional top-level flag (--db) that must appear before
// the subcommand, opens the store, constructs a minimal messaging service, and
// dispatches to the matching sub-handler.
func cmdMessage(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: %s message [--db path] <send|inbox|thread|ack|resolve|catch-up|handoff>\n", brand.BinaryName)
		os.Exit(1)
	}

	// Parse an optional leading --db flag so users can point at a
	// non-default database without wrapping every subcommand in its own
	// flag set. Any other form falls through to the subcommand dispatcher.
	dbPath := "./" + brand.DefaultDBName
	remaining := args
	if len(args) >= 2 && args[0] == "--db" {
		dbPath = args[1]
		remaining = args[2:]
	}
	if len(remaining) < 1 {
		fmt.Fprintf(os.Stderr, "usage: %s message [--db path] <send|inbox|thread|ack|resolve|catch-up|handoff>\n", brand.BinaryName)
		os.Exit(1)
	}

	sub := remaining[0]
	rest := remaining[1:]

	s, err := store.New(dbPath)
	if err != nil {
		slogx.Fatal("message: open db", "path", dbPath, "err", err)
	}
	defer s.Close()

	svc, err := newMessagingServiceForCLI(s)
	if err != nil {
		slogx.Fatal("message: init service", "err", err)
	}

	switch sub {
	case "send":
		messageSend(svc, rest)
	case "inbox":
		messageInbox(svc, rest)
	case "thread":
		messageThread(svc, rest)
	case "ack":
		messageAck(svc, rest)
	case "resolve":
		messageResolve(svc, rest)
	case "catch-up":
		messageCatchUp(svc, rest)
	case "handoff":
		messageHandoff(svc, rest)
	default:
		fmt.Fprintf(os.Stderr, "unknown message subcommand: %s\n", sub)
		os.Exit(1)
	}
}

func messageSend(svc *messaging.Service, args []string) {
	fs := flag.NewFlagSet("message send", flag.ExitOnError)
	session := fs.String("session", "", "session id")
	to := fs.String("to", "", "to agent id (or 'user')")
	from := fs.String("from", "user", "from agent id")
	subject := fs.String("subject", "", "subject line")
	body := fs.String("body", "", "message body")
	msgType := fs.String("type", "message", "message type")
	fs.Parse(args)

	var missing []string
	if *session == "" {
		missing = append(missing, "--session")
	}
	if *to == "" {
		missing = append(missing, "--to")
	}
	if *body == "" {
		missing = append(missing, "--body")
	}
	if len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "message send: missing required flag(s): %s\n", strings.Join(missing, ", "))
		os.Exit(1)
	}
	msg := messaging.SendInput{
		FromSessionID: *session,
		FromAgentID:   *from,
		ToSessionID:   *session,
		ToAgentID:     *to,
		Subject:       *subject,
		Body:          *body,
		Type:          *msgType,
	}
	out, err := svc.SendMessage(context.Background(), msg)
	if err != nil {
		slogx.Fatal("message send", "err", err)
	}
	fmt.Printf("sent: %s\n", out.ID)
}

func messageInbox(svc *messaging.Service, args []string) {
	fs := flag.NewFlagSet("message inbox", flag.ExitOnError)
	session := fs.String("session", "", "session id")
	agentID := fs.String("agent", "", "agent id")
	status := fs.String("status", "", "filter: unread|read|acknowledged|resolved")
	fs.Parse(args)

	if *session == "" || *agentID == "" {
		fmt.Fprintln(os.Stderr, "message inbox: --session and --agent are required")
		os.Exit(1)
	}
	if *status != "" {
		switch *status {
		case "unread", "read", "acknowledged", "resolved":
		default:
			fmt.Fprintf(os.Stderr, "message inbox: invalid --status %q (want: unread|read|acknowledged|resolved)\n", *status)
			os.Exit(1)
		}
	}

	// CLI caller identity is the same (session, agent) pair by
	// construction — the user authenticates via flags and reads their
	// own inbox. The service still requires the match.
	inbox, err := svc.Inbox(
		context.Background(),
		*session, *agentID,
		messaging.InboxFilter{Status: *status},
		*session, *agentID,
	)
	if err != nil {
		slogx.Fatal("message inbox", "err", err)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(inbox); err != nil {
		slogx.Fatal("message inbox: encode", "err", err)
	}
}

func messageThread(svc *messaging.Service, args []string) {
	fs := flag.NewFlagSet("message thread", flag.ExitOnError)
	session := fs.String("session", "", "caller session id")
	agentID := fs.String("agent", "", "caller agent id")
	fs.Parse(args)
	if fs.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "usage: %s message thread --session X --agent Y <threadID>\n", brand.BinaryName)
		os.Exit(1)
	}
	if *session == "" || *agentID == "" {
		fmt.Fprintln(os.Stderr, "message thread: --session and --agent are required (caller identity for participant filtering)")
		os.Exit(1)
	}
	// Thread is participant-filtered: only messages where the caller is
	// sender or recipient are returned. Non-participants see empty.
	messages, err := svc.Thread(context.Background(), fs.Arg(0), *session, *agentID)
	if err != nil {
		slogx.Fatal("message thread", "err", err)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(messages); err != nil {
		slogx.Fatal("message thread: encode", "err", err)
	}
}

func messageAck(svc *messaging.Service, args []string) {
	fs := flag.NewFlagSet("message ack", flag.ExitOnError)
	session := fs.String("session", "", "session id")
	agentID := fs.String("agent", "", "agent id")
	fs.Parse(args)
	if *session == "" || *agentID == "" {
		fmt.Fprintln(os.Stderr, "message ack: --session and --agent are required")
		os.Exit(1)
	}
	if fs.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "usage: %s message ack --session X --agent Y <msgID>\n", brand.BinaryName)
		os.Exit(1)
	}
	if err := svc.Ack(context.Background(), *session, *agentID, fs.Arg(0)); err != nil {
		slogx.Fatal("message ack", "err", err)
	}
	fmt.Printf("acked: %s\n", fs.Arg(0))
}

func messageResolve(svc *messaging.Service, args []string) {
	fs := flag.NewFlagSet("message resolve", flag.ExitOnError)
	session := fs.String("session", "", "session id")
	agentID := fs.String("agent", "", "agent id")
	fs.Parse(args)
	if *session == "" || *agentID == "" {
		fmt.Fprintln(os.Stderr, "message resolve: --session and --agent are required")
		os.Exit(1)
	}
	if fs.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "usage: %s message resolve --session X --agent Y <msgID>\n", brand.BinaryName)
		os.Exit(1)
	}
	if err := svc.Resolve(context.Background(), *session, *agentID, fs.Arg(0)); err != nil {
		slogx.Fatal("message resolve", "err", err)
	}
	fmt.Printf("resolved: %s\n", fs.Arg(0))
}

func messageCatchUp(svc *messaging.Service, args []string) {
	fs := flag.NewFlagSet("message catch-up", flag.ExitOnError)
	session := fs.String("session", "", "session id")
	last := fs.Int("last", 20, "number of recent messages")
	fs.Parse(args)

	if *session == "" {
		fmt.Fprintln(os.Stderr, "message catch-up: --session is required")
		os.Exit(1)
	}
	if *last <= 0 {
		fmt.Fprintln(os.Stderr, "message catch-up: --last must be positive")
		os.Exit(1)
	}

	messages, err := svc.RecentForSession(context.Background(), *session, *last)
	if err != nil {
		slogx.Fatal("message catch-up", "err", err)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(messages); err != nil {
		slogx.Fatal("message catch-up: encode", "err", err)
	}
}

func messageHandoff(svc *messaging.Service, args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: %s message handoff <request|approve|reject>\n", brand.BinaryName)
		os.Exit(1)
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "request":
		fs := flag.NewFlagSet("handoff request", flag.ExitOnError)
		session := fs.String("session", "", "session id")
		to := fs.String("to", "", "to agent id")
		from := fs.String("from", "", "from agent id (optional)")
		reqBy := fs.String("requested-by", "user", "departing|incoming|user")
		fs.Parse(rest)

		if *session == "" || *to == "" {
			fmt.Fprintln(os.Stderr, "message handoff request: --session and --to are required")
			os.Exit(1)
		}
		switch *reqBy {
		case "departing", "incoming", "user":
		default:
			fmt.Fprintf(os.Stderr, "message handoff request: invalid --requested-by %q (want: departing|incoming|user)\n", *reqBy)
			os.Exit(1)
		}

		id, err := svc.RequestHandoff(context.Background(), *session, *from, *to, *reqBy)
		if err != nil {
			slogx.Fatal("message handoff request", "err", err)
		}
		fmt.Printf("handoff requested: %s\n", id)
	case "approve":
		if len(rest) < 1 {
			fmt.Fprintf(os.Stderr, "usage: %s message handoff approve <handoffID>\n", brand.BinaryName)
			os.Exit(1)
		}
		if err := svc.ApproveHandoff(context.Background(), rest[0]); err != nil {
			slogx.Fatal("message handoff approve", "err", err)
		}
		fmt.Printf("approved: %s\n", rest[0])
	case "reject":
		fs := flag.NewFlagSet("handoff reject", flag.ExitOnError)
		reason := fs.String("reason", "", "rejection reason")
		fs.Parse(rest)
		if fs.NArg() < 1 {
			fmt.Fprintf(os.Stderr, "usage: %s message handoff reject --reason X <handoffID>\n", brand.BinaryName)
			os.Exit(1)
		}
		if err := svc.RejectHandoff(context.Background(), fs.Arg(0), *reason); err != nil {
			slogx.Fatal("message handoff reject", "err", err)
		}
		fmt.Printf("rejected: %s\n", fs.Arg(0))
	default:
		fmt.Fprintf(os.Stderr, "unknown handoff subcommand: %s\n", sub)
		os.Exit(1)
	}
}

// newMessagingServiceForCLI wires a minimal AgentService as the messaging.Service
// resolver for CLI use. It discovers file-based agents from the working
// directory and plugins dir (same as the server) and appends the built-in
// default agent so "file-default" resolves. Events is nil: the CLI doesn't
// emit activity, and AgentService.Get — the only method messaging.Service calls
// via AgentResolver — never dereferences the Events field.
func newMessagingServiceForCLI(s *store.Store) (*messaging.Service, error) {
	agentDefs, err := agent.Discover(agent.DiscoverOptions{
		WorkingDir: ".",
		PluginsDir: "plugins",
		Adapters:   agent.NewAdapterRegistry(),
	})
	if err != nil {
		// Non-fatal: CLI can still address the "user" sentinel and DB
		// agents even if file discovery hits a parse error on one tier.
		slog.Warn("message: agent discovery", "err", err)
	}
	if defaultDef, defErr := builtin.DefaultAgent(); defErr == nil {
		agentDefs = append(agentDefs, defaultDef)
	}

	agents := service.NewAgentService(service.AgentServiceConfig{
		Agents:     s,
		Writers:    s,
		Settings:   s,
		Events:     nil,
		FileAgents: agentDefs,
		Overrides:  s,
	})
	return messaging.NewService(messaging.NewSQLiteStore(s.DB), s.DB, agents), nil
}
