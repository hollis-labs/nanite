package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

const agentRunUsage = "usage: nanite agent run [<dir>] [-p <prompt|->] [--session ID] [--grant-same-as H] [--force] [--url U] [--no-autostart]"

// runAgentRun is `nanite agent run`: publish the agent directory's current
// state (the same idempotent load as `agent load`), start a session on it with
// the current directory as the work root, and attach the terminal. An edit to
// the directory takes effect on the next run; the server is not restarted and
// sessions already running keep the revision they started under.
func runAgentRun(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("agent run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	prompt := fs.String("p", "", "send this prompt (- reads stdin), print the reply and exit")
	sessionID := fs.String("session", "", "resume this session instead of starting one")
	url := fs.String("url", "", "harness API base URL (default: "+apiBaseURL()+")")
	noAutostart := fs.Bool("no-autostart", false, "fail instead of starting `nanite serve` when it is not running")
	force := fs.Bool("force", false, "publish a new revision of a pinned agent")
	grantSame := fs.String("grant-same-as", "", "re-grant the tools of this host (slug or id) to the new revision")
	reason := fs.String("reason", "", "why the tools are re-granted (recorded)")
	title := fs.String("title", "", "title for the new session")
	var positional []string
	rest := args
	for len(rest) > 0 {
		if err := fs.Parse(rest); err != nil {
			return fmt.Errorf("%w\n%s", err, agentRunUsage)
		}
		if fs.NArg() == 0 {
			break
		}
		positional = append(positional, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	if len(positional) > 1 {
		return errors.New("at most one directory\n" + agentRunUsage)
	}
	arg := ""
	if len(positional) == 1 {
		arg = positional[0]
	}
	text := *prompt
	if text == "-" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read prompt from stdin: %w", err)
		}
		text = strings.TrimSpace(string(b))
		if text == "" {
			return errors.New("-p -: stdin is empty")
		}
	}

	baseURL := *url
	if baseURL == "" {
		baseURL = apiBaseURL()
	}
	if err := ensureServeRunning(ctx, baseURL, *noAutostart); err != nil {
		return err
	}
	client := newAgentClient(*url)

	var sess *store.Session
	if *sessionID != "" {
		s, err := client.GetSession(ctx, *sessionID)
		if err != nil {
			return err
		}
		sess = s
	} else {
		req, err := loadForRun(ctx, arg, service.AgentLoadOptions{Force: *force, GrantFrom: *grantSame, Reason: *reason})
		if err != nil {
			return err
		}
		req.Title = *title
		if sess, err = client.CreateSession(ctx, req); err != nil {
			return err
		}
	}
	if text != "" {
		turnCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
		defer stop()
		return runChatTurn(turnCtx, client, sess.ID, text)
	}
	fmt.Fprintf(out, "session %s (%s) — type a message, Ctrl-D or /quit to exit\n", sess.ID, client.baseURL)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for {
		fmt.Fprint(out, "> ")
		if !scanner.Scan() {
			fmt.Fprintln(out)
			return scanner.Err()
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == "/quit" || line == "/exit" {
			return nil
		}
		turnCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
		err := runChatTurn(turnCtx, client, sess.ID, line)
		stop()
		if err != nil {
			fmt.Fprintf(os.Stderr, "agent run: %v\n", err)
		}
	}
}

// loadForRun publishes the directory and returns the session request for it.
// The database is opened only for the load and closed before the server is
// talked to. The current directory is the session's work root: required for a CLI
// agent, and for an API agent when a host directory contains it (the server
// refuses a work root outside the host's directories, so one that is not inside
// is not sent).
func loadForRun(ctx context.Context, arg string, opts service.AgentLoadOptions) (agentCreateSessionRequest, error) {
	var req agentCreateSessionRequest
	st, err := store.New(ctx, resolveDBPath())
	if err != nil {
		return req, err
	}
	defer closeStoreBestEffort(ctx, st)
	dir, res, err := loadAgentDirectory(ctx, st, arg, opts)
	if err != nil {
		return req, err
	}
	req.DefinitionRef = res.DefinitionRef
	hs := res.HostSettings
	req.HostSettings = &hs
	if res.Changed {
		fmt.Fprintf(os.Stderr, "loaded %s as host %s\n", res.Agent, res.Slug)
	}
	cwd, cwdErr := os.Getwd()
	switch {
	case dir.Host.Runtime == "cli":
		if cwdErr != nil {
			return req, cwdErr
		}
		req.WorkRoot = cwd
		if res.Changed && res.Previous != "" && len(res.Tools) == 0 {
			fmt.Fprintf(os.Stderr, "this revision has no tool grants; to give it the tools of %s run:\n  nanite agent load --grant-same-as %s\n", res.Previous, res.Previous)
		}
	case cwdErr == nil && insideAny(cwd, dir.Host.Directories):
		// An API agent works in the current directory too when the host grants
		// it; otherwise it keeps the host's first directory as its work root.
		req.WorkRoot = cwd
	}
	return req, nil
}

// insideAny reports whether path is, or is inside, one of dirs, comparing
// symlink-resolved paths so it agrees with the server's check.
func insideAny(path string, dirs []string) bool {
	resolve := func(p string) string {
		p = filepath.Clean(p)
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return p
	}
	target := resolve(path)
	for _, d := range dirs {
		rel, err := filepath.Rel(resolve(d), target)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
