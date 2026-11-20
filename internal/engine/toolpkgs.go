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
	pkgs, err := authored.LoadTools(e.cfg.Dir, e.cfg.ToolPackageEnv...)
	if err != nil {
		return pkgs, "Some tool packages did not load: " + err.Error()
	}
	return pkgs, ""
}

// recheckTool reads a package again just before it runs and refuses it if
// it changed since the turn loaded it (and so since the call was approved).
// A change between this check and the start of the process is still
// possible; the agent's own files are not a security boundary against the
// agent, which runs as the same OS user.
func (e *Engine) recheckTool(ctx context.Context, pkg authored.ToolPackage) error {
	now, err := authored.LoadTool(e.profileDir(ctx), pkg.Name, e.cfg.ToolPackageEnv...)
	if err != nil {
		return fmt.Errorf("tool package %s no longer loads: %w", pkg.Name, err)
	}
	if now.Digest != pkg.Digest {
		return fmt.Errorf("tool package %s changed after it was approved; call it again", pkg.Name)
	}
	return nil
}

// toolNote describes a package for the person approving a call: what runs,
// as whom, and the names (never values) of what it is given.
func toolNote(pkg authored.ToolPackage) string {
	parts := []string{fmt.Sprintf("tool package %s", pkg.Name)}
	if pkg.Version != "" {
		parts[0] += " " + pkg.Version
	}
	parts[0] += fmt.Sprintf(" (%s, digest %s) runs %s as the agent's OS user", pkg.Kind, shortDigest(pkg.Digest), pkg.Dir()+"/"+pkg.Entry)
	if len(pkg.Env) > 0 {
		parts = append(parts, "host variables: "+strings.Join(pkg.Env, ", "))
	}
	for _, need := range pkg.Needs {
		need := need
		scopes := ""
		if len(need.Scopes) > 0 {
			scopes = " [" + strings.Join(need.Scopes, " ") + "]"
		}
		parts = append(parts, fmt.Sprintf("Warden token for %s%s in %s", need.Audience, scopes, need.Env))
	}
	if pkg.Network {
		parts = append(parts, "uses the network")
	}
	return strings.Join(parts, "; ")
}

// errorResult reports err to the model as a failed tool call.
func errorResult(err error) tools.Result { return tools.Result{Text: err.Error(), IsError: true} }

func shortDigest(d string) string {
	if len(d) > 12 {
		return d[:12]
	}
	return d
}

// packageOf names the package a package tool belongs to: tool__<name> or
// tool__<name>__<tool>.
func packageOf(tool string) (string, bool) {
	rest, ok := strings.CutPrefix(tool, tools.ToolPackagePrefix)
	if !ok {
		return "", false
	}
	name, _, _ := strings.Cut(rest, "__")
	return name, true
}

// toolServers adds each approved mcp-kind tool package to the authored MCP
// servers as a stdio server named "tool:<package>". Its environment is the base set,
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
		if pkg.Kind != authored.ToolMCP || !e.toolApproved(ctx, pkg) {
			// An MCP package starts (and gets its tokens) only once a person
			// approved this exact package; until then it is offered as its
			// approval tool (packageTools).
			continue
		}
		if err := e.recheckTool(ctx, pkg); err != nil {
			warnings = append(warnings, err.Error())
			continue
		}
		check, err := json.Marshal(mcpclient.PackageCheck{Dir: e.profileDir(ctx), Name: pkg.Name, Digest: pkg.Digest, AllowEnv: e.cfg.ToolPackageEnv})
		if err != nil {
			warnings = append(warnings, err.Error())
			continue
		}
		env := map[string]string{
			mcpclient.PackageCheckEnv: string(check),
			// The digest also restarts the server when the package changes.
			"NANITE_TOOL_DIGEST": pkg.Digest,
		}
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
		out.Servers[mcpclient.ToolPackageServerPrefix+name] = agentdir.MCPServer{Type: "stdio", Command: e.toolEntry(ctx, pkg), Env: env}
	}
	return out, warnings
}

// toolEntry is the absolute path of a package's entry.
func (e *Engine) toolEntry(ctx context.Context, pkg authored.ToolPackage) string {
	dir, err := filepath.Abs(e.profileDir(ctx))
	if err != nil {
		dir = e.profileDir(ctx)
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
			if err := authored.CheckToolEnvValue(key, value, e.cfg.ToolPackageEnv); err != nil {
				return nil, err
			}
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
func (e *Engine) toolDir(ctx context.Context, pkg authored.ToolPackage) string {
	if roots := e.cfg.WorkspaceRoots; len(roots) > 0 {
		return roots[0]
	}
	return filepath.Dir(e.toolEntry(ctx, pkg))
}

// packageTools offers each command-kind package as tool__<package>, and
// each mcp-kind package not yet approved as its approval tool,
// tool__<package>: calling it asks the person, and once approved the server
// starts and its tools are offered from the next turn. It records what the
// approval prompt shows for every package tool.
func (x *execution) packageTools(ctx context.Context) []tools.Tool {
	var out []tools.Tool
	x.pkgNotes = map[string]string{}
	for _, name := range authored.ToolNames(x.toolPkgs) {
		pkg := x.toolPkgs[name]
		x.pkgNotes[name] = toolNote(pkg)
		switch {
		case pkg.Kind == authored.ToolCommand:
			out = append(out, tools.Tool{Name: tools.ToolPackagePrefix + name, Description: toolDescription(pkg), InputSchema: pkg.InputSchema,
				// The agent's own code: never read-only to policy, so the
				// default permission mode asks before each call.
				Destructive: true,
				Handler: func(ctx context.Context, call tools.Call) (tools.Result, error) {
					// Runs only after the call was approved: nothing from the
					// package runs and no token is minted before.
					if err := x.engine.recheckTool(ctx, pkg); err != nil {
						return errorResult(err), nil
					}
					if pkg.Background {
						return x.startToolJob(ctx, pkg, call.Input)
					}
					return x.runTool(ctx, pkg, call.Input)
				},
			})
		case !x.engine.toolApproved(ctx, pkg):
			out = append(out, tools.Tool{Name: tools.ToolPackagePrefix + name, InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
				Description: "Ask to start tool package " + name + ", an MCP server: " + pkg.Description + " Once approved, its tools are offered from the next turn.",
				Destructive: true,
				Handler: func(ctx context.Context, _ tools.Call) (tools.Result, error) {
					if err := x.engine.recheckTool(ctx, pkg); err != nil {
						return errorResult(err), nil
					}
					if err := x.engine.approveTool(ctx, pkg); err != nil {
						return tools.Result{}, err
					}
					return tools.Result{Text: fmt.Sprintf("Approved tool package %s (digest %s). Its tools are offered from the next turn.", pkg.Name, shortDigest(pkg.Digest))}, nil
				},
			})
		}
	}
	return out
}

// grantKey is the key a conversation-scope approval is kept under. For a
// package tool it includes the package's digest, so a rewritten package
// asks again.
func (x *execution) grantKey(tool string) string {
	if name, ok := packageOf(tool); ok {
		if pkg, ok := x.toolPkgs[name]; ok {
			return tool + "@" + pkg.Digest
		}
	}
	return tool
}

// approvalNote is what the person approving a package tool is shown about
// the package, "" for any other tool.
func (x *execution) approvalNote(tool string) string {
	if name, ok := packageOf(tool); ok {
		return x.pkgNotes[name]
	}
	return ""
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
	result, err := shell.RunExec(ctx, shell.Exec{Name: "tool package " + pkg.Name, Path: e.toolEntry(ctx, pkg), Dir: e.toolDir(ctx, pkg), Env: env, Stdin: input, Timeout: pkg.Timeout})
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
	command := shell.Exec{Name: "tool package " + pkg.Name, Path: e.toolEntry(ctx, pkg), Dir: e.toolDir(ctx, pkg), Env: env, Stdin: input, Timeout: pkg.Timeout}
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
			pkgs, err := authored.LoadTools(x.engine.profileDir(ctx), x.engine.cfg.ToolPackageEnv...)
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
