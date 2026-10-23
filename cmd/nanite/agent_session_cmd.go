package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// runAgentSessionCommand handles `agent list` and `agent stop`. A session's
// runtime lives inside the running server, so these go through the server's
// /api/agent/v1 routes: the CLI and the API share one stop path rather than
// the CLI reaching into the database.
func runAgentSessionCommand(args []string, out io.Writer) error {
	switch args[0] {
	case "list":
		all := false
		if len(args) > 1 && args[1] == "--all" {
			all = true
		} else if len(args) > 1 {
			return errors.New("usage: nanite agent list [--all]")
		}
		return agentSessionList(out, all)
	case "stop":
		if len(args) != 2 || strings.HasPrefix(args[1], "-") {
			return errors.New("usage: nanite agent stop <session>")
		}
		fmt.Fprintf(os.Stderr, "stopping on %s\n", apiBaseURL())
		return agentSessionCall(out, http.MethodPost, "/api/agent/v1/sessions/"+url.PathEscape(args[1])+"/stop")
	}
	return fmt.Errorf("unknown agent session command %q", args[0])
}

// agentSessionList pages through every session and prints one JSON document,
// so a long list is not silently cut at the first page.
func agentSessionList(out io.Writer, includeEnded bool) error {
	var items []json.RawMessage
	cursor := ""
	for {
		q := url.Values{"limit": {"100"}}
		if includeEnded {
			q.Set("include_archived", "true")
		}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		path := "/api/agent/v1/sessions?" + q.Encode()
		data, status, err := authDo(http.MethodGet, path, nil)
		if err != nil {
			return err
		}
		if status < 200 || status >= 300 {
			return fmt.Errorf("GET %s: HTTP %d: %s", path, status, strings.TrimSpace(string(data)))
		}
		var page struct {
			Items []json.RawMessage `json:"items"`
			Next  *string           `json:"next_cursor"`
		}
		if err = json.Unmarshal(data, &page); err != nil {
			return fmt.Errorf("decode session list: %w", err)
		}
		items = append(items, page.Items...)
		if page.Next == nil || *page.Next == "" {
			break
		}
		cursor = *page.Next
	}
	if items == nil {
		items = []json.RawMessage{}
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{"items": items, "server": apiBaseURL()})
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
