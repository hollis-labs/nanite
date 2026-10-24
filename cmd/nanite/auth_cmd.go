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
	"regexp"
	"strings"
	"time"

	"github.com/hollis-labs/nanite/internal/apiauth"
	"github.com/hollis-labs/nanite/internal/brand"
)

const authUsage = `Usage: nanite auth <command>

Credentials for the Nanite HTTP API (CW-20261011-0036).

  whoami                          show the credential this CLI presents
  list [--all]                    list credentials (never secrets)
  create --kind client --label L --scope read,operate [--ttl-days N] [--output FILE [--force]]
                                  create a client credential; the secret is shown ONCE.
                                  With --output the secret goes only to FILE (mode 0600,
                                  refused if it exists unless --force) and is never printed
  revoke <id>                     revoke a credential (open streams close)
  rotate <id> [--output FILE [--force]]
                                  replace a credential; the new secret is shown ONCE (or
                                  written only to FILE with --output)
  login-link                      print a one-time (60 s) browser login URL (on the server's
                                  NANITE_PUBLIC_URL, else this shell's, else the API base URL)
  token-path                      print the operator token file path
  reset-operator                  OFFLINE recovery: revoke every operator credential and
                                  write a fresh one to the token file

Commands talk to the server at --url <url> (else NANITE_API_URL, else NANITE_PORT,
else http://127.0.0.1:8090); -db does not select a server.

Every command takes -db <path> (or NANITE_DB_PATH) to find the credentials
of a server started with serve -db <path>; they live beside that database.

Scopes: read, operate, tools, maintain, agents:manage, admin (admin is operator only; agents:manage lets a client set up and manage agents but never grant tools).`

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
		output := fs.String("output", "", "write the secret to this file (mode 0600) instead of printing it")
		force := fs.Bool("force", false, "with --output, replace an existing file")
		_ = fs.Parse(args[1:])
		body := map[string]any{"kind": *kind, "label": *label, "scopes": splitList(*scopes), "ttl_days": *ttl}
		if *output != "" {
			err = authIssue(os.Stdout, http.MethodPost, "/api/auth/credentials", body, *output, *force)
			break
		}
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
		fs := flag.NewFlagSet("auth rotate", flag.ExitOnError)
		output := fs.String("output", "", "write the new secret to this file (mode 0600) instead of printing it")
		force := fs.Bool("force", false, "with --output, replace an existing file")
		rest := args[1:]
		var id string
		if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
			id, rest = rest[0], rest[1:]
		}
		_ = fs.Parse(rest)
		if id == "" && fs.NArg() == 1 {
			id = fs.Arg(0)
		} else if fs.NArg() != 0 {
			id = ""
		}
		if id == "" {
			fmt.Fprintln(os.Stderr, "usage: nanite auth rotate <id> [--output FILE [--force]]")
			os.Exit(2)
		}
		if *output != "" {
			err = authIssue(os.Stdout, http.MethodPost, "/api/auth/credentials/"+id+"/rotate", map[string]any{}, *output, *force)
			break
		}
		err = authCall(http.MethodPost, "/api/auth/credentials/"+id+"/rotate", map[string]any{})
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
		return nil, 0, fmt.Errorf("could not reach %s: %w (is `%s serve` running? -db does not choose the server: pass --url or set %s / %s)", apiBaseURL(), err, brand.BinaryName, brand.Env("API_URL"), brand.Env("PORT"))
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

// authIssue mints (or rotates) a credential and writes its secret only to the
// 0600 file at output. The secret is never printed, logged or placed in an
// error: out gets the credential's metadata with the secret removed. The
// output path is checked before the server is asked to mint, and if the file
// cannot be written after minting the new credential is revoked, so no
// credential exists whose secret nobody holds.
func authIssue(out io.Writer, method, path string, body any, output string, force bool) error {
	if !force {
		if _, err := os.Lstat(output); err == nil {
			return fmt.Errorf("%s already exists; nothing was issued (use --force to replace it)", output)
		}
	}
	data, status, err := authDo(method, path, body)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("%s %s: HTTP %d: %s", method, path, status, redactSecrets(strings.TrimSpace(string(data))))
	}
	var view map[string]any
	if err := json.Unmarshal(data, &view); err != nil {
		return fmt.Errorf("%s %s: unreadable response; the credential may have been issued, check `%s auth list`", method, path, brand.BinaryName)
	}
	secret, _ := view["secret"].(string)
	id, _ := view["id"].(string)
	if secret == "" {
		return fmt.Errorf("%s %s: the response carried no secret", method, path)
	}
	delete(view, "secret")
	if err := apiauth.WriteSecretFile(output, secret, force); err != nil {
		// The secret was never stored; do not leave a credential nobody holds.
		if id != "" && method == http.MethodPost && strings.HasSuffix(path, "/api/auth/credentials") {
			_, _, _ = authDo(http.MethodDelete, "/api/auth/credentials/"+id, nil)
			return fmt.Errorf("could not write %s (%v); the new credential %s was revoked", output, err, id)
		}
		return fmt.Errorf("could not write %s (%v); the credential %s was issued but its secret was not saved: rotate it again", output, err, id)
	}
	view["written_to"] = output
	pretty, err := json.MarshalIndent(view, "", "  ")
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(out, string(pretty))
	fmt.Fprintf(os.Stderr, "\nThe secret was written to %s (mode 0600) and is not shown again.\n", output)
	return nil
}

// redactSecrets removes anything shaped like a Nanite credential from text
// about to be shown.
func redactSecrets(s string) string {
	return secretShape.ReplaceAllString(s, "[redacted]")
}

var secretShape = regexp.MustCompile(`nan_(operator|client|agent)_[A-Za-z0-9_-]{20,}`)

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
		URL       string `json:"url"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return err
	}
	if out.URL != "" {
		fmt.Println(out.URL)
	} else {
		fmt.Println(loginLinkBase() + out.Path)
	}
	fmt.Fprintf(os.Stderr, "Open it in a browser within 60 seconds; it works once (expires %s).\n", out.ExpiresAt)
	return nil
}

// loginLinkBase is the fallback origin, used when the server does not return a
// ready-to-open url (an older server). The origin a browser should open the login link on. The
// link is minted over the loopback API, but a deployed server sits behind a
// proxy, so NANITE_PUBLIC_URL (the public URL) wins when set.
func loginLinkBase() string {
	if u := strings.TrimSpace(os.Getenv(brand.Env("PUBLIC_URL"))); u != "" {
		return strings.TrimRight(u, "/")
	}
	return strings.TrimRight(apiBaseURL(), "/")
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
