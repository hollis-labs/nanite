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
	a2asvc "github.com/hollis-labs/nanite/internal/service/a2a"
	"github.com/hollis-labs/nanite/internal/slogx"
	"github.com/hollis-labs/nanite/internal/store"
)

// cmdA2A is the entry point for the `nanite a2a ...` command group. It
// parses a single optional top-level flag (--db) that must appear before
// the subcommand, opens the store, constructs a minimal A2A service, and
// dispatches to the matching sub-handler.
func cmdA2A(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: %s a2a [--db path] <send|inbox|thread|ack|resolve|catch-up|handoff>\n", brand.BinaryName)
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
		fmt.Fprintf(os.Stderr, "usage: %s a2a [--db path] <send|inbox|thread|ack|resolve|catch-up|handoff>\n", brand.BinaryName)
		os.Exit(1)
	}

	sub := remaining[0]
	rest := remaining[1:]

	s, err := store.New(dbPath)
	if err != nil {
		slogx.Fatal("a2a: open db", "path", dbPath, "err", err)
	}
	defer s.Close()

	svc, err := newA2AServiceForCLI(s)
	if err != nil {
		slogx.Fatal("a2a: init service", "err", err)
	}

	switch sub {
	case "send":
		a2aSend(svc, rest)
	case "inbox":
		a2aInbox(svc, rest)
	case "thread":
		a2aThread(svc, rest)
	case "ack":
		a2aAck(svc, rest)
	case "resolve":
		a2aResolve(svc, rest)
	case "catch-up":
		a2aCatchUp(svc, rest)
	case "handoff":
		a2aHandoff(svc, rest)
	default:
		fmt.Fprintf(os.Stderr, "unknown a2a subcommand: %s\n", sub)
		os.Exit(1)
	}
}

func a2aSend(svc *a2asvc.Service, args []string) {
	fs := flag.NewFlagSet("a2a send", flag.ExitOnError)
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
		fmt.Fprintf(os.Stderr, "a2a send: missing required flag(s): %s\n", strings.Join(missing, ", "))
		os.Exit(1)
	}
	msg := &store.A2AMessage{
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
		slogx.Fatal("a2a send", "err", err)
	}
	fmt.Printf("sent: %s\n", out.ID)
}

func a2aInbox(svc *a2asvc.Service, args []string) {
	fs := flag.NewFlagSet("a2a inbox", flag.ExitOnError)
	session := fs.String("session", "", "session id")
	agentID := fs.String("agent", "", "agent id")
	status := fs.String("status", "", "filter: unread|read|acknowledged|resolved")
	fs.Parse(args)

	if *session == "" || *agentID == "" {
		fmt.Fprintln(os.Stderr, "a2a inbox: --session and --agent are required")
		os.Exit(1)
	}
	if *status != "" {
		switch *status {
		case "unread", "read", "acknowledged", "resolved":
		default:
			fmt.Fprintf(os.Stderr, "a2a inbox: invalid --status %q (want: unread|read|acknowledged|resolved)\n", *status)
			os.Exit(1)
		}
	}

	// CLI caller identity is the same (session, agent) pair by
	// construction — the user authenticates via flags and reads their
	// own inbox. The service still requires the match.
	inbox, err := svc.Inbox(context.Background(), *session, *agentID, *status, *session, *agentID)
	if err != nil {
		slogx.Fatal("a2a inbox", "err", err)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(inbox); err != nil {
		slogx.Fatal("a2a inbox: encode", "err", err)
	}
}

func a2aThread(svc *a2asvc.Service, args []string) {
	fs := flag.NewFlagSet("a2a thread", flag.ExitOnError)
	session := fs.String("session", "", "caller session id")
	agentID := fs.String("agent", "", "caller agent id")
	fs.Parse(args)
	if fs.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "usage: %s a2a thread --session X --agent Y <threadID>\n", brand.BinaryName)
		os.Exit(1)
	}
	if *session == "" || *agentID == "" {
		fmt.Fprintln(os.Stderr, "a2a thread: --session and --agent are required (caller identity for participant filtering)")
		os.Exit(1)
	}
	// Thread is participant-filtered: only messages where the caller is
	// sender or recipient are returned. Non-participants see empty.
	messages, err := svc.Thread(context.Background(), fs.Arg(0), *session, *agentID)
	if err != nil {
		slogx.Fatal("a2a thread", "err", err)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(messages); err != nil {
		slogx.Fatal("a2a thread: encode", "err", err)
	}
}

func a2aAck(svc *a2asvc.Service, args []string) {
	fs := flag.NewFlagSet("a2a ack", flag.ExitOnError)
	session := fs.String("session", "", "session id")
	agentID := fs.String("agent", "", "agent id")
	fs.Parse(args)
	if *session == "" || *agentID == "" {
		fmt.Fprintln(os.Stderr, "a2a ack: --session and --agent are required")
		os.Exit(1)
	}
	if fs.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "usage: %s a2a ack --session X --agent Y <msgID>\n", brand.BinaryName)
		os.Exit(1)
	}
	if err := svc.Ack(context.Background(), *session, *agentID, fs.Arg(0)); err != nil {
		slogx.Fatal("a2a ack", "err", err)
	}
	fmt.Printf("acked: %s\n", fs.Arg(0))
}

func a2aResolve(svc *a2asvc.Service, args []string) {
	fs := flag.NewFlagSet("a2a resolve", flag.ExitOnError)
	session := fs.String("session", "", "session id")
	agentID := fs.String("agent", "", "agent id")
	fs.Parse(args)
	if *session == "" || *agentID == "" {
		fmt.Fprintln(os.Stderr, "a2a resolve: --session and --agent are required")
		os.Exit(1)
	}
	if fs.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "usage: %s a2a resolve --session X --agent Y <msgID>\n", brand.BinaryName)
		os.Exit(1)
	}
	if err := svc.Resolve(context.Background(), *session, *agentID, fs.Arg(0)); err != nil {
		slogx.Fatal("a2a resolve", "err", err)
	}
	fmt.Printf("resolved: %s\n", fs.Arg(0))
}

func a2aCatchUp(svc *a2asvc.Service, args []string) {
	fs := flag.NewFlagSet("a2a catch-up", flag.ExitOnError)
	session := fs.String("session", "", "session id")
	last := fs.Int("last", 20, "number of recent messages")
	fs.Parse(args)

	if *session == "" {
		fmt.Fprintln(os.Stderr, "a2a catch-up: --session is required")
		os.Exit(1)
	}
	if *last <= 0 {
		fmt.Fprintln(os.Stderr, "a2a catch-up: --last must be positive")
		os.Exit(1)
	}

	messages, err := svc.RecentForSession(context.Background(), *session, *last)
	if err != nil {
		slogx.Fatal("a2a catch-up", "err", err)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(messages); err != nil {
		slogx.Fatal("a2a catch-up: encode", "err", err)
	}
}

func a2aHandoff(svc *a2asvc.Service, args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: %s a2a handoff <request|approve|reject>\n", brand.BinaryName)
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
			fmt.Fprintln(os.Stderr, "a2a handoff request: --session and --to are required")
			os.Exit(1)
		}
		switch *reqBy {
		case "departing", "incoming", "user":
		default:
			fmt.Fprintf(os.Stderr, "a2a handoff request: invalid --requested-by %q (want: departing|incoming|user)\n", *reqBy)
			os.Exit(1)
		}

		id, err := svc.RequestHandoff(context.Background(), *session, *from, *to, *reqBy)
		if err != nil {
			slogx.Fatal("a2a handoff request", "err", err)
		}
		fmt.Printf("handoff requested: %s\n", id)
	case "approve":
		if len(rest) < 1 {
			fmt.Fprintf(os.Stderr, "usage: %s a2a handoff approve <handoffID>\n", brand.BinaryName)
			os.Exit(1)
		}
		if err := svc.ApproveHandoff(context.Background(), rest[0]); err != nil {
			slogx.Fatal("a2a handoff approve", "err", err)
		}
		fmt.Printf("approved: %s\n", rest[0])
	case "reject":
		fs := flag.NewFlagSet("handoff reject", flag.ExitOnError)
		reason := fs.String("reason", "", "rejection reason")
		fs.Parse(rest)
		if fs.NArg() < 1 {
			fmt.Fprintf(os.Stderr, "usage: %s a2a handoff reject --reason X <handoffID>\n", brand.BinaryName)
			os.Exit(1)
		}
		if err := svc.RejectHandoff(context.Background(), fs.Arg(0), *reason); err != nil {
			slogx.Fatal("a2a handoff reject", "err", err)
		}
		fmt.Printf("rejected: %s\n", fs.Arg(0))
	default:
		fmt.Fprintf(os.Stderr, "unknown handoff subcommand: %s\n", sub)
		os.Exit(1)
	}
}

// newA2AServiceForCLI wires a minimal AgentService as the a2a.Service
// resolver for CLI use. It discovers file-based agents from the working
// directory and plugins dir (same as the server) and appends the built-in
// default agent so "file-default" resolves. Events is nil: the CLI doesn't
// emit activity, and AgentService.Get — the only method a2a.Service calls
// via AgentResolver — never dereferences the Events field.
func newA2AServiceForCLI(s *store.Store) (*a2asvc.Service, error) {
	agentDefs, err := agent.Discover(agent.DiscoverOptions{
		WorkingDir: ".",
		PluginsDir: "plugins",
		Adapters:   agent.NewAdapterRegistry(),
	})
	if err != nil {
		// Non-fatal: CLI can still address the "user" sentinel and DB
		// agents even if file discovery hits a parse error on one tier.
		slog.Warn("a2a: agent discovery", "err", err)
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
	return a2asvc.NewService(s, agents), nil
}
