package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hollis-labs/libs/agent/agentdir"
	"github.com/hollis-labs/nanite/internal/authored"
	"github.com/hollis-labs/nanite/internal/background"
	"github.com/hollis-labs/nanite/internal/mcpclient"
	"github.com/hollis-labs/nanite/internal/processenv"
	"github.com/hollis-labs/nanite/internal/shell"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/tools"
)

// MaxBackgroundToolJobs bounds the background tool-package jobs running at
// once across the agent. A call over the limit is refused.
const MaxBackgroundToolJobs = 4

// toolJobKind is the jobs-table kind of a background tool-package call: a
// shell job (it runs a process like one), whose spec names the tool.
const toolJobKind = "shell"

// toolPackages reads the agent's tool packages for a turn. A package that
// does not load is skipped with a warning; nanite validate reports it.
func (e *Engine) toolPackages() (map[string]authored.ToolPackage, string) {
	if !e.cfg.Builtins.Has(tools.BuiltinTools) {
		return nil, ""
	}
	pkgs, err := authored.LoadTools(e.cfg.Dir)
	if err != nil {
		return pkgs, "Some tool packages did not load: " + err.Error()
	}
	return pkgs, ""
}

// toolServers adds each mcp-kind tool package to the authored MCP servers
// as a stdio server named "tool:<package>". Its environment is the base set,
// the host variables the package lists, and the Warden tokens it needs,
// minted now: a token renewed later changes the server's configuration, so
// the server restarts with the new token at the next turn.
func (e *Engine) toolServers(ctx context.Context, authoredCfg agentdir.MCPConfig, pkgs map[string]authored.ToolPackage) (agentdir.MCPConfig, []string) {
	out := agentdir.MCPConfig{Servers: maps.Clone(authoredCfg.Servers)}
	var warnings []string
	for name := range authoredCfg.Servers {
		if strings.HasPrefix(name, mcpclient.ToolPackageServerPrefix) {
			delete(out.Servers, name)
			warnings = append(warnings, fmt.Sprintf("MCP server %q left out: the %q prefix is reserved for tool packages", name, mcpclient.ToolPackageServerPrefix))
		}
	}
	for _, name := range authored.ToolNames(pkgs) {
		pkg := pkgs[name]
		if pkg.Kind != authored.ToolMCP {
			continue
		}
		env := map[string]string{}
		for _, key := range pkg.Env {
			if _, ok := os.LookupEnv(key); ok {
				// Expanded (and redacted) by the MCP client.
				env[key] = "${" + key + "}"
			}
		}
		tokens, err := e.toolTokens(ctx, pkg)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("tool package %q left out: %v", name, err))
			continue
		}
		maps.Copy(env, tokens)
		if out.Servers == nil {
			out.Servers = map[string]agentdir.MCPServer{}
		}
		out.Servers[mcpclient.ToolPackageServerPrefix+name] = agentdir.MCPServer{Type: "stdio", Command: e.toolEntry(pkg), Env: env}
	}
	return out, warnings
}

// toolEntry is the absolute path of a package's entry.
func (e *Engine) toolEntry(pkg authored.ToolPackage) string {
	dir, err := filepath.Abs(e.cfg.Dir)
	if err != nil {
		dir = e.cfg.Dir
	}
	return filepath.Join(dir, filepath.FromSlash(pkg.Dir()), filepath.FromSlash(pkg.Entry))
}

// toolTokens mints the Warden tokens a package needs, by variable name.
func (e *Engine) toolTokens(ctx context.Context, pkg authored.ToolPackage) (map[string]string, error) {
	if len(pkg.Needs) == 0 {
		return nil, nil
	}
	src := e.cfg.ProviderConfig.Warden
	if src == nil {
		return nil, fmt.Errorf("it needs Warden tokens, but identity.yaml has no warden section")
	}
	out := make(map[string]string, len(pkg.Needs))
	for _, need := range pkg.Needs {
		token, err := src.Token(ctx, need.Audience, need.Scopes)
		if err != nil {
			return nil, fmt.Errorf("token for %s: %w", need.Audience, err)
		}
		out[need.Env] = token
	}
	return out, nil
}

// toolEnv is a command package's environment: the base set, the host
// variables it lists (redacted wherever they appear), and its tokens.
func (e *Engine) toolEnv(ctx context.Context, pkg authored.ToolPackage) ([]string, error) {
	env := processenv.Build(nil)
	for _, key := range pkg.Env {
		if value, ok := os.LookupEnv(key); ok {
			e.cfg.ProviderConfig.Redactor.Add(key, value)
			env[key] = value
		}
	}
	tokens, err := e.toolTokens(ctx, pkg)
	if err != nil {
		return nil, err
	}
	maps.Copy(env, tokens)
	return processenv.List(env), nil
}

// toolDir is where a command package runs: the first workspace root, else
// its own directory.
func (e *Engine) toolDir(pkg authored.ToolPackage) string {
	if roots := e.cfg.WorkspaceRoots; len(roots) > 0 {
		return roots[0]
	}
	return filepath.Dir(e.toolEntry(pkg))
}

// packageTools offers each command-kind package as tool__<package>.
func (x *execution) packageTools() []tools.Tool {
	var out []tools.Tool
	for _, name := range authored.ToolNames(x.toolPkgs) {
		pkg := x.toolPkgs[name]
		if pkg.Kind != authored.ToolCommand {
			continue
		}
		out = append(out, tools.Tool{Name: tools.ToolPackagePrefix + name, Description: toolDescription(pkg), InputSchema: pkg.InputSchema,
			// The agent's own code: never read-only to policy, so the default
			// permission mode asks before each call.
			Destructive: true,
			Handler: func(ctx context.Context, call tools.Call) (tools.Result, error) {
				if pkg.Background {
					return x.startToolJob(ctx, pkg, call.Input)
				}
				return x.runTool(ctx, pkg, call.Input)
			},
		})
	}
	return out
}

func toolDescription(pkg authored.ToolPackage) string {
	notes := []string{"tool package " + pkg.Name}
	if pkg.Version != "" {
		notes[0] += " " + pkg.Version
	}
	if pkg.Background {
		notes = append(notes, "runs as a background job: returns the job at once, and its finish arrives as a notice")
	}
	if pkg.Network {
		notes = append(notes, "uses the network")
	}
	return pkg.Description + " (" + strings.Join(notes, "; ") + ")"
}

// runTool runs a command package once and returns its JSON result.
func (x *execution) runTool(ctx context.Context, pkg authored.ToolPackage, input json.RawMessage) (tools.Result, error) {
	e := x.engine
	env, err := e.toolEnv(ctx, pkg)
	if err != nil {
		return tools.Result{Text: e.scrub.Text(fmt.Sprintf("tool package %s: %v", pkg.Name, err)), IsError: true}, nil
	}
	result, err := shell.RunExec(ctx, shell.Exec{Path: e.toolEntry(pkg), Dir: e.toolDir(pkg), Env: env, Stdin: input, Timeout: pkg.Timeout})
	stderr := strings.TrimSpace(result.Stderr)
	fail := func(format string, args ...any) (tools.Result, error) {
		text := fmt.Sprintf(format, args...)
		if stderr != "" {
			text += "\nstderr:\n" + stderr
		}
		return tools.Result{Text: e.scrub.Text(text), IsError: true}, nil
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil:
		return fail("tool package %s timed out after %s", pkg.Name, pkg.Timeout)
	case err != nil:
		return fail("tool package %s: %v", pkg.Name, err)
	case result.ExitCode != 0:
		return fail("tool package %s exited with status %d\n%s", pkg.Name, result.ExitCode, strings.TrimSpace(result.Stdout))
	case result.StdoutTruncated:
		return fail("tool package %s wrote more than the output limit; write a smaller JSON result", pkg.Name)
	}
	out := strings.TrimSpace(result.Stdout)
	if !json.Valid([]byte(out)) {
		return fail("tool package %s did not write a JSON result on standard output", pkg.Name)
	}
	return tools.Result{Text: e.scrub.Text(out)}, nil
}

// startToolJob runs a background command package as a job.
func (x *execution) startToolJob(ctx context.Context, pkg authored.ToolPackage, input json.RawMessage) (tools.Result, error) {
	e := x.engine
	select {
	case e.toolJobs <- struct{}{}:
	default:
		return tools.Result{Text: fmt.Sprintf("%d background tool jobs are already running; wait for one to finish", MaxBackgroundToolJobs), IsError: true}, nil
	}
	held := true
	release := func() {
		if held {
			held = false
			<-e.toolJobs
		}
	}
	defer func() {
		// A job that did not start gives its slot back here; a started one
		// gives it back when it ends.
		release()
	}()
	env, err := e.toolEnv(ctx, pkg)
	if err != nil {
		return tools.Result{Text: e.scrub.Text(fmt.Sprintf("tool package %s: %v", pkg.Name, err)), IsError: true}, nil
	}
	spec, err := json.Marshal(struct {
		Tool  string          `json:"tool"`
		Input json.RawMessage `json:"input"`
	}{pkg.Name, input})
	if err != nil {
		return tools.Result{}, err
	}
	jobID := store.NewID(time.Now())
	if x.source == SourceNotice {
		// What this job causes must not start another notice turn.
		e.markNoticeWork(jobID)
	}
	command := shell.Exec{Path: e.toolEntry(pkg), Dir: e.toolDir(pkg), Env: env, Stdin: input, Timeout: pkg.Timeout}
	job, err := e.jobs.Start(ctx, store.Job{ID: jobID, ConversationID: x.info.ID, Kind: toolJobKind, Spec: e.scrub.Text(string(spec))},
		func(runCtx context.Context, output io.Writer) (background.Task, error) {
			process, err := shell.StartExecStreaming(runCtx, command, output)
			if err != nil {
				return background.Task{}, err
			}
			held = false // the job owns the slot now
			return background.Task{PID: process.PID(), Wait: func() background.Outcome {
				defer func() { <-e.toolJobs }()
				result, err := process.Wait()
				process.CleanupGroup()
				var exit *int
				if err == nil {
					exit = &result.ExitCode
				}
				return background.Outcome{ExitCode: exit, Err: err}
			}}, nil
		})
	if err != nil {
		e.forgetNoticeWork(jobID)
		return tools.Result{}, err
	}
	text, err := jobJSON(job, "")
	return tools.Result{Text: text}, err
}

// toolValidateSchema is tool_validate's input.
const toolValidateSchema = `{"type":"object","properties":{"name":{"type":"string","description":"One package to check; omit to check every package in tools/."}},"additionalProperties":false}`

// toolValidate checks the agent's tool packages the way the next turn will
// load them, so an agent can fix a package it is writing before using it.
func (x *execution) toolValidate() tools.Tool {
	return tools.Tool{Name: "tool_validate", ReadOnly: true, InputSchema: json.RawMessage(toolValidateSchema),
		Description: "Check the tool packages in tools/<name>/ (tool.yaml and its entry) the way the next turn loads them. A package that passes is offered from the next turn.",
		Handler: func(ctx context.Context, call tools.Call) (tools.Result, error) {
			var in struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(call.Input, &in); err != nil {
				return tools.Result{}, err
			}
			pkgs, err := authored.LoadTools(x.engine.cfg.Dir)
			var lines []string
			for _, name := range authored.ToolNames(pkgs) {
				if in.Name == "" || in.Name == name {
					lines = append(lines, fmt.Sprintf("ok: %s (%s)", name, pkgs[name].Kind))
				}
			}
			if err != nil {
				for _, line := range strings.Split(err.Error(), "\n") {
					if in.Name == "" || strings.HasPrefix(line, "tools/"+in.Name+":") {
						lines = append(lines, "error: "+line)
					}
				}
			}
			if len(lines) == 0 {
				if in.Name != "" {
					return tools.Result{Text: fmt.Sprintf("no tool package %q in tools/", in.Name), IsError: true}, nil
				}
				return tools.Result{Text: "no tool packages in tools/"}, nil
			}
			failed := strings.Contains(strings.Join(lines, "\n"), "error: ")
			return tools.Result{Text: strings.Join(lines, "\n"), IsError: failed}, nil
		},
	}
}
