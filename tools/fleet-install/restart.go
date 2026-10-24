package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// cerberusArgs is the exact request for a cerberus restart step.
func cerberusArgs(r Restart) []string {
	verb := r.Verb
	if verb == "" {
		verb = "reload"
	}
	return []string{"resource", verb, r.Resource, "--ack"}
}

// restartAll runs the ordered restart list. pending is true when a cerberus step was
// printed for a human and --restart-done was not given; the caller then stops after
// the install (nothing is rolled back).
func (d *Deps) restartAll(ctx context.Context, steps []Restart, vars map[string]string, restartDone, dry bool) (pending bool, err error) {
	for i, r := range steps {
		label := fmt.Sprintf("restart[%d] %s", i, r.Via)
		switch r.Via {
		case "none":
			d.say("%s: nothing to do", label)
		case "systemd-user":
			d.say("%s: systemctl --user restart %s", label, r.Unit)
			if dry {
				continue
			}
			if _, err := d.Run.Run(ctx, "systemctl", "--user", "restart", r.Unit); err != nil {
				return false, fmt.Errorf("restart %s: %v", r.Unit, err)
			}
		case "command":
			argv := make([]string, len(r.Argv))
			for j, a := range r.Argv {
				argv[j] = ExpandTemplate(a, vars)
			}
			d.say("%s: %s", label, strings.Join(argv, " "))
			if dry {
				continue
			}
			if _, err := d.Run.Run(ctx, argv[0], argv[1:]...); err != nil {
				return false, fmt.Errorf("restart command: %v", err)
			}
		case "cerberus":
			args := cerberusArgs(r)
			line := "cerberus " + strings.Join(args, " ")
			if r.Run && d.LocalUser {
				d.say("%s: %s", label, line)
				if dry {
					continue
				}
				if _, err := d.Run.Run(ctx, "cerberus", args...); err != nil {
					return false, fmt.Errorf("restart %s: %v", r.Resource, err)
				}
				continue
			}
			if restartDone {
				d.say("%s: %s (run by an operator, --restart-done given)", label, line)
				continue
			}
			d.say("%s: REQUEST FOR A HUMAN (Approve and Run), not run by this tool:\n    %s", label, line)
			pending = true
		}
	}
	return pending, nil
}

// checkHealth polls each health entry in order until it passes or its timeout elapses.
// want lists acceptable version strings (empty disables version_json checks).
func (d *Deps) checkHealth(ctx context.Context, hs []Health, want []string, once bool) error {
	for i, h := range hs {
		if err := d.pollHealth(ctx, i, h, want, once); err != nil {
			return err
		}
	}
	return nil
}

func (d *Deps) pollHealth(ctx context.Context, i int, h Health, want []string, once bool) error {
	timeout, interval := h.Durations()
	if once {
		timeout = 0
	}
	method := h.Method
	if method == "" {
		method = "GET"
	}
	expect := h.ExpectStatus
	if len(expect) == 0 {
		expect = []int{200}
	}
	deadline := d.Now().Add(timeout)
	var last string
	for {
		last = d.healthOnce(ctx, method, h, expect, want)
		if last == "" {
			d.say("health[%d]: %s ok", i, h.URL)
			return nil
		}
		if !d.Now().Before(deadline) {
			return fmt.Errorf("health[%d] %s: %s", i, h.URL, last)
		}
		d.Sleep(interval)
	}
}

func (d *Deps) healthOnce(ctx context.Context, method string, h Health, expect []int, want []string) string {
	rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, method, h.URL, nil)
	if err != nil {
		return err.Error()
	}
	resp, err := d.HTTP.Do(req)
	if err != nil {
		return "request failed: " + err.Error()
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	okStatus := false
	for _, c := range expect {
		if resp.StatusCode == c {
			okStatus = true
		}
	}
	if !okStatus {
		return fmt.Sprintf("status %d not in %v", resp.StatusCode, expect)
	}
	if h.VersionJSON != "" && len(want) > 0 {
		var doc any
		if err := json.Unmarshal(body, &doc); err != nil {
			return "version_json: body is not JSON"
		}
		for _, k := range strings.Split(h.VersionJSON, ".") {
			m, ok := doc.(map[string]any)
			if !ok {
				return fmt.Sprintf("version_json: %q not found", h.VersionJSON)
			}
			doc = m[k]
		}
		got := fmt.Sprint(doc)
		for _, w := range want {
			if w != "" && strings.Contains(got, w) {
				return ""
			}
		}
		return fmt.Sprintf("version_json %s is %q, want %v", h.VersionJSON, got, want)
	}
	return ""
}
