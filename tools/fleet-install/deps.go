package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Runner executes external commands (gh, sqlite3, systemctl, cerberus, custom argv).
// A non-zero exit is returned as an error carrying trimmed stderr.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (stdout string, err error)
}

// HTTPDoer is the subset of http.Client used for health checks.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if len(msg) > 400 {
			msg = msg[:400]
		}
		if msg != "" {
			return out.String(), fmt.Errorf("%s: %v: %s", name, err, msg)
		}
		return out.String(), fmt.Errorf("%s: %v", name, err)
	}
	return out.String(), nil
}

// Deps carries everything that touches the outside world so tests can fake it.
type Deps struct {
	Run       Runner
	HTTP      HTTPDoer
	Out, Err  io.Writer
	Home      string // ~ expansion and the systemd user drop-in directory
	Hostname  string
	OS, Arch  string
	Sleep     func(time.Duration)
	Now       func() time.Time
	LocalUser bool // cerberus `run: true` is honoured only for a local user
}

func defaultDeps() *Deps {
	home, _ := os.UserHomeDir()
	host, _ := os.Hostname()
	return &Deps{
		Run: execRunner{}, HTTP: &http.Client{Timeout: 10 * time.Second},
		Out: os.Stdout, Err: os.Stderr, Home: home, Hostname: host,
		OS: goos, Arch: goarch, Sleep: time.Sleep, Now: time.Now, LocalUser: true,
	}
}

func (d *Deps) say(f string, a ...any) { fmt.Fprintf(d.Out, f+"\n", a...) }

// expand resolves a leading ~/ against Deps.Home.
func (d *Deps) expand(p string) string {
	if p == "~" {
		return d.Home
	}
	if strings.HasPrefix(p, "~/") {
		return d.Home + p[1:]
	}
	return p
}

// ExitError carries a process exit code and a one-line reason.
type ExitError struct {
	Code int
	Msg  string
}

func (e *ExitError) Error() string { return e.Msg }

const (
	ExitFail        = 1
	ExitUsage       = 2
	ExitRefused     = 3  // preflight refused (active work, migration, failed backup)
	ExitRolledBack  = 4  // post-install failure, automatically rolled back
	ExitRollbackErr = 5  // post-install failure and the rollback also failed
	ExitAwaiting    = 10 // installed; waiting for an operator-run restart
)

func fail(code int, f string, a ...any) *ExitError {
	return &ExitError{Code: code, Msg: fmt.Sprintf(f, a...)}
}
