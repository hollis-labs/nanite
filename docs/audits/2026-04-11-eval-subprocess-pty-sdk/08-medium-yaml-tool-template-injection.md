# [Medium] YAML tool exec renders Go templates from user input before shell execution

**Scope:** Tool execution pipeline
**Topic:** Security — command injection via template
**Date:** 2026-04-11

## Problem

The YAML tool loader uses Go's `text/template` to render command strings that are then passed to `sh -c`. Template input comes from LLM-generated tool arguments (trust boundary #3). A crafted input could escape the intended template substitution and inject arbitrary shell commands.

## Evidence

`internal/tool/yaml_loader.go:L160-193`:
```go
// Template rendering with user input.
tmpl, err := template.New("cmd").Parse(td.Exec.Command)
if err != nil {
    return &ToolResult{Output: fmt.Sprintf("Error: invalid command template: %v", err), IsError: true}, nil
}
var buf bytes.Buffer
if err := tmpl.Execute(&buf, input); err != nil {  // input = LLM tool arguments
    return &ToolResult{Output: fmt.Sprintf("Error: template render failed: %v", err), IsError: true}, nil
}

rendered := buf.String()

// ...
cmd := exec.CommandContext(ctx, "sh", "-c", rendered)  // shell execution of rendered string
```

If a YAML tool definition has a command template like:
```yaml
exec:
  command: "grep {{.pattern}} {{.file}}"
```

And the LLM provides `pattern` as `; rm -rf /; echo `, the rendered command becomes:
```
grep ; rm -rf /; echo  somefile
```

Go's `text/template` does NO escaping for shell context. `html/template` escapes for HTML, but `text/template` outputs verbatim.

The Hadron blueprint path at `:233` is safer because it uses `--input` flags rather than shell interpolation, but the input values still pass through `text/template` rendering at `:206-220`.

## Impact

- Any YAML tool with shell template variables is vulnerable to command injection via LLM tool arguments.
- The attack surface depends on which YAML tools are defined. If no YAML tools use `exec.command` with templates, this is theoretical. But the mechanism is live and any future YAML tool definition that uses template variables becomes exploitable.
- The executed command runs unsandboxed (finding 02).

## Recommendation

1. **Shell-escape template outputs.** Use a custom template function that shell-escapes values:
```go
funcMap := template.FuncMap{
    "shesc": func(s string) string {
        return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
    },
}
```
Require tool authors to use `{{shesc .pattern}}` in templates.

2. **Better: use exec.Command with argument list** instead of `sh -c`. Parse the command template into a command + args array, substituting template values only into arguments (not the shell string).

3. **Run through sandbox.** Route YAML tool execution through `sandbox.UserExec` at minimum.

## References

- Go `text/template` documentation — no shell escaping
- `internal/tool/yaml_loader.go:L140-195` — full exec path
- Reviewer context: trust boundary #3 (tool arguments)
