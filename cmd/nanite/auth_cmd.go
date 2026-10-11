package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/hollis-labs/nanite/internal/apiauth"
	"github.com/hollis-labs/nanite/internal/brand"
)

const authUsage = `Usage: nanite auth <command>

Credentials for the Nanite HTTP API (CW-20261011-0036).

  whoami                          show the credential this CLI presents
  list [--all]                    list credentials (never secrets)
  create --kind client --label L --scope read,operate [--ttl-days N]
                                  create a client credential; the secret is shown ONCE
  revoke <id>                     revoke a credential (open streams close)
  rotate <id>                     replace a credential; the new secret is shown ONCE
  login-link                      print a one-time (60 s) browser login URL
  token-path                      print the operator token file path
  reset-operator                  OFFLINE recovery: revoke every operator credential and
                                  write a fresh one to the token file

Scopes: read, operate, tools, maintain, admin (operator only).`

func cmdAuth(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, authUsage)
		os.Exit(2)
	}
	var err error
	switch args[0] {
	case "whoami":
		err = authCall(http.MethodGet, "/api/auth/session", nil)
	case "list":
		fs := flag.NewFlagSet("auth list", flag.ExitOnError)
		all := fs.Bool("all", false, "include revoked credentials")
		_ = fs.Parse(args[1:])
		path := "/api/auth/credentials"
		if *all {
			path += "?include_revoked=true"
		}
		err = authCall(http.MethodGet, path, nil)
	case "create":
		fs := flag.NewFlagSet("auth create", flag.ExitOnError)
		kind := fs.String("kind", "client", "credential kind (client)")
		label := fs.String("label", "", "what this credential is for (required)")
		scopes := fs.String("scope", "", "comma-separated scopes (required)")
		ttl := fs.Int("ttl-days", 0, "lifetime in days (default 30)")
		_ = fs.Parse(args[1:])
		body := map[string]any{"kind": *kind, "label": *label, "scopes": splitList(*scopes), "ttl_days": *ttl}
		err = authCall(http.MethodPost, "/api/auth/credentials", body)
		if err == nil {
			fmt.Fprintln(os.Stderr, "\nStore the secret now: it is not shown again and cannot be recovered.")
		}
	case "revoke":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: nanite auth revoke <id>")
			os.Exit(2)
		}
		err = authCall(http.MethodDelete, "/api/auth/credentials/"+args[1], nil)
	case "rotate":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: nanite auth rotate <id>")
			os.Exit(2)
		}
		err = authCall(http.MethodPost, "/api/auth/credentials/"+args[1]+"/rotate", map[string]any{})
		if err == nil {
			fmt.Fprintln(os.Stderr, "\nStore the new secret now: it is not shown again. The old secret no longer works.")
		}
	case "login-link":
		err = authLoginLink()
	case "token-path":
		var p authPaths
		if p, err = resolveAuthPaths(""); err == nil {
			fmt.Println(p.token)
		}
	case "reset-operator":
		err = authResetOperator()
	default:
		fmt.Fprintln(os.Stderr, authUsage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s auth: %v\n", brand.BinaryName, err)
		os.Exit(1)
	}
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// authDo sends a request to the running server with the operator credential
// and returns the body.
func authDo(method, path string, body any) ([]byte, int, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		reader = bytes.NewReader(raw)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// #nosec G704 -- the URL is the operator's own loopback API base (NANITE_API_URL / --port), not request input
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(apiBaseURL(), "/")+path, reader)
	if err != nil {
		return nil, 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	applyClientAuth(req)
	resp, err := http.DefaultClient.Do(req) // #nosec G704 -- same loopback API base as above
	if err != nil {
		return nil, 0, fmt.Errorf("could not reach %s: %w (is `%s serve` running?)", apiBaseURL(), err, brand.BinaryName)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	return data, resp.StatusCode, err
}

func authCall(method, path string, body any) error {
	data, status, err := authDo(method, path, body)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("%s %s: HTTP %d: %s", method, path, status, strings.TrimSpace(string(data)))
	}
	if len(bytes.TrimSpace(data)) == 0 {
		fmt.Println("ok")
		return nil
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, data, "", "  ") == nil {
		fmt.Println(pretty.String())
	} else {
		fmt.Println(string(data))
	}
	return nil
}

func authLoginLink() error {
	data, status, err := authDo(http.MethodPost, "/api/auth/login-links", map[string]any{})
	if err != nil {
		return err
	}
	if status != http.StatusCreated {
		return fmt.Errorf("HTTP %d: %s", status, strings.TrimSpace(string(data)))
	}
	var out struct {
		Path      string `json:"path"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return err
	}
	fmt.Println(strings.TrimRight(apiBaseURL(), "/") + out.Path)
	fmt.Fprintf(os.Stderr, "Open it in a browser within 60 seconds; it works once (expires %s).\n", out.ExpiresAt)
	return nil
}

// authResetOperator is the offline recovery path: it works on the credential
// database directly, so the server need not be running (and an open server
// keeps serving the old operator's open streams until they end).
func authResetOperator() error {
	p, err := resolveAuthPaths("")
	if err != nil {
		return err
	}
	ctx := context.Background()
	st, err := apiauth.Open(ctx, p.db)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	if err := apiauth.ResetOperator(ctx, st, p.token); err != nil {
		return err
	}
	fmt.Printf("A new operator credential was written to %s (mode 0600). Every earlier operator credential is revoked.\n", p.token)
	return nil
}
