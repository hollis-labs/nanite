# [Medium] `filepath.Base(projectDir)` is templated into YAML and markdown without escaping

**Scope:** installer / scaffold templates
**Topic:** Security — unescaped user input at a trust boundary
**Date:** 2026-04-10

## Problem

`ScaffoldSource.ProjectName` is set to `filepath.Base(projectDir)` and substituted into two embedded templates — `nanite-config.yaml.tmpl` and `NANITE.md.tmpl` — using `text/template`. `text/template` does not escape special characters for the target content type; it just does string substitution. Neither template uses the `html/template` auto-escape, and no custom escape is applied.

For YAML this means a project directory named, e.g., `"test: injected\nagents: { evil: x }"` (which is a valid POSIX filename on any filesystem) would produce a config.yaml whose semantics are not what the installer intends. For markdown it is less impactful (the project name is displayed, not parsed), but the same injection vector applies — a project name containing `](javascript:...)` or raw HTML would land in `NANITE.md` unchanged.

Real-world likelihood of a user creating such a directory name is low. The installer is not the attacker surface it would be if `--project` accepted arbitrary strings — `filepath.Abs` and `filepath.Base` constrain the input to filesystem-valid paths. But filesystem-valid names include newlines, colons, brackets, backticks, and every YAML/markdown special character on every Unix-like OS. A mischievous (or automated) build pipeline could produce a name that breaks the template.

The reviewer-context's `## Trust boundaries` list includes "File system — read/write tools, sandbox population, plugin discovery. Path traversal attacks." Templated project names are a variant of the same concern: the installer is the write side of the trust boundary.

## Evidence

```go
// internal/service/install/scaffold.go:L101-113
func renderTemplate(name, body string, data any) (string, error) {
    t, err := template.New(name).Parse(body)
    if err != nil {
        return "", fmt.Errorf("parse template %s: %w", name, err)
    }
    var buf bytes.Buffer
    if err := t.Execute(&buf, data); err != nil {
        return "", fmt.Errorf("execute template %s: %w", name, err)
    }
    return buf.String(), nil
}
```

`template` is `text/template` (`import "text/template"` at line 14) — no auto-escaping. Caller:

```go
// internal/service/install/scaffold.go:L19-24
type ScaffoldSource struct {
    FrameworkVersion string
    ProjectName      string
}
```

Populated from:

```go
// internal/service/install/install.go:L176-180 (freshScaffold)
src := ScaffoldSource{
    FrameworkVersion: assets.Version(),
    ProjectName:      filepath.Base(projectDir),
}
```

And the embedded template:

```yaml
# internal/assets/framework/templates/nanite-config.yaml.tmpl
# internal/assets/framework/templates/nanite-config.yaml.tmpl
# Project nanite config — {{.ProjectName}}
nanite_version: {{.FrameworkVersion}}
```

```markdown
# internal/assets/framework/templates/NANITE.md.tmpl  (shape; read in full)
# ... references {{.ProjectName}} without escaping ...
```

### Repro sketch

```bash
mkdir -p "/tmp/$(printf 'evil\nadapters: [\"attacker\"]\n#')"
nanite install --project "/tmp/evil"$'\nadapters: ["attacker"]\n#'
```

The resulting `.nanite/config.yaml` has the attacker-chosen adapters list injected as a top-level key. The installer's own adapter persistence then collides with it, producing either a parse error (loud — good) or a duplicate key (silent — bad, depends on yaml.v3 duplicate handling).

I did not run this repro — flagging as "requires verification" for the exact outcome depending on yaml.v3's duplicate-key behavior (it emits a warning but still parses in some modes). The injection point is confirmed by reading the code; the severity of the resulting file depends on the YAML library's tolerance.

## Impact

- **Who:** any user whose project directory has an unusual name containing YAML/markdown special characters. Unlikely to be adversarial; much more likely to be an automated test fixture with a newline in its name (Go's `t.TempDir()` would never produce one, but a CI matrix or a templated path might).
- **What:** the first install into that directory writes a `.nanite/config.yaml` that either fails to parse on subsequent runs (recoverable; user gets a clear error) or parses with attacker-controlled top-level keys (not recoverable; user has a silently-wrong config). The installer is not the attacker; the attacker would have to control the directory name.
- **Release impact:** low probability, high surprise factor. The fix is cheap.

## Recommendation

Either:

1. **Validate `filepath.Base(projectDir)` at the top of `InstallProject`.** Reject any basename that contains characters outside `[A-Za-z0-9._-]`. This is the simplest fix and covers every downstream use of `ProjectName`. The downside is it breaks legitimate project names that contain spaces (common on macOS user directories) — `foo bar project` would be rejected.

2. **Use a context-appropriate escaper in the template functions.** For the YAML template, pass `ProjectName` through a helper that quotes it with double-quote YAML syntax and escapes embedded quotes/newlines. For the markdown template, backtick-escape special characters. Both are ~10 lines of code.

   ```go
   // Example YAML-safe template func
   yamlSafe := func(s string) string {
       return strconv.Quote(s) // good-enough: YAML accepts double-quoted strings with Go escape sequences
   }
   t, _ := template.New(name).Funcs(template.FuncMap{"yamlSafe": yamlSafe}).Parse(body)
   ```

   And update the template:

   ```yaml
   # Project nanite config — {{.ProjectName}}
   nanite_version: {{.FrameworkVersion}}
   ```

   becomes:

   ```yaml
   # Project nanite config — {{.ProjectName | yamlSafe}}
   nanite_version: {{.FrameworkVersion}}
   ```

   (The `#` line is a comment so its escaping matters less; this example is for illustration. The actual risky substitution would be if `ProjectName` ever landed in a key or value position, which is not currently the case in the template but could regress.)

3. **(Belt-and-suspenders) Sanitize once and store on `ScaffoldSource`.** Add a `ProjectNameSafe` field computed at struct construction time, and use it in the templates. The unsafe version is still available for display.

Recommended: option 1 for the first beta (fast, safe, loud failure on unusual names), then option 2 if the space-in-name case becomes a real complaint.

Also: add a test that passes a project name containing special characters and asserts either clean rejection or escaped output. Currently `scaffold_test.go` only exercises boring ASCII names.

## References

- `internal/service/install/scaffold.go:L19-113` — ScaffoldSource and template rendering.
- `internal/service/install/install.go:L176-180`, `L193-199` — the two call sites passing `filepath.Base(projectDir)` as `ProjectName`.
- `internal/assets/framework/templates/nanite-config.yaml.tmpl` — the embedded config template.
- `internal/assets/framework/templates/NANITE.md.tmpl` — the embedded NANITE.md template.
- Go text/template docs explicitly state "This package is NOT automatically escaping data for any specific output context" — https://pkg.go.dev/text/template.
