package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// runAgentSessionCommand handles `agent list` and `agent stop`. A session's
// runtime lives inside the running server, so these go through the server's
// /api/agent/v1 routes: the CLI and the API share one stop path rather than
// the CLI reaching into the database.
func runAgentSessionCommand(args []string, out io.Writer) error {
	switch args[0] {
	case "list":
		path := "/api/agent/v1/sessions"
		if len(args) > 1 && args[1] == "--all" {
			path += "?include_archived=true"
		} else if len(args) > 1 {
			return errors.New("usage: nanite agent list [--all]")
		}
		return agentSessionCall(out, http.MethodGet, path)
	case "stop":
		if len(args) != 2 || strings.HasPrefix(args[1], "-") {
			return errors.New("usage: nanite agent stop <session>")
		}
		return agentSessionCall(out, http.MethodPost, "/api/agent/v1/sessions/"+url.PathEscape(args[1])+"/stop")
	}
	return fmt.Errorf("unknown agent session command %q", args[0])
}

func agentSessionCall(out io.Writer, method, path string) error {
	data, status, err := authDo(method, path, nil)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("%s %s: HTTP %d: %s", method, path, status, strings.TrimSpace(string(data)))
	}
	_, err = fmt.Fprintln(out, strings.TrimSpace(string(data)))
	return err
}
