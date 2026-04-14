// Package scaffold generates new Nanite plugin skeletons for the
// `nanite plugin new` command. Two kinds are supported:
//
//   - KindSubprocess: out-of-process plugins that speak JSON-RPC over
//     stdio via plugin-sdk v0.3.0. The generated directory is a
//     stand-alone Go module + vite UI project with a working Makefile
//     that produces catalog-installable archives (the BLG-20260414-008
//     ui/ vs ui/dist/ mismatch is fixed in the generated Makefile and
//     ui.bundle_dir both target ui/dist).
//
//   - KindBuiltin: compiled-in plugins under internal/plugin/builtin/.
//     Mirrors the bookmarks shape — embedded plugin.yaml, init-time
//     registration, no subprocess binary, no UI bundle.
//
// All templates live under templates/{subprocess,builtin}/ and are
// embedded into the binary. Output file paths mirror the template path
// with the .tmpl suffix stripped (gitignore.tmpl → .gitignore is the
// one exception — hidden-dotfile templates are kept visible in-repo).
package scaffold

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
	"time"
	"unicode"
)

// pluginIDPattern mirrors the v1 manifest schema's id regex so scaffold
// fails fast on invalid names instead of producing a directory tree whose
// plugin.yaml would later fail schema validation.
var pluginIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,62}$`)

// all:templates is required so hidden directories like .github/ are
// included in the embedded FS. Plain `embed templates` skips dotfiles.
//
//go:embed all:templates
var templateFS embed.FS

// Kind selects which template tree to render.
type Kind string

const (
	KindSubprocess Kind = "subprocess"
	KindBuiltin    Kind = "builtin"
)

// Options configures a scaffold run.
type Options struct {
	// Kind selects the template tree. Required.
	Kind Kind

	// Name is the plugin id (lowercase kebab-case, e.g. "my-plugin").
	// Required. Must match the schema pattern ^[a-z][a-z0-9-]{1,62}$.
	Name string

	// Description is the short manifest description. Defaults to a
	// placeholder when empty.
	Description string

	// Author populates the LICENSE copyright and manifest author field.
	// Defaults to "Plugin Author" when empty.
	Author string

	// ModulePath is the Go module path for subprocess plugins
	// (e.g. "github.com/acme/nanite-plugin-foo"). Defaults to
	// "github.com/example/nanite-plugin-<name>" when empty. Unused for
	// builtin kind.
	ModulePath string

	// OutputDir is where the scaffold lands. Required.
	OutputDir string
}

// templateData is the struct exposed to all templates.
type templateData struct {
	Name              string // plugin id (kebab-case)
	DisplayName       string // Title Case
	PackageName       string // Go package name (lowercase, no dashes)
	StructName        string // Go struct name (PascalCase + "Plugin")
	Description       string
	Author            string
	ModulePath        string
	Year              string
	EnvelopeType      string // "<name>-card"
	EnvelopeComponent string // "<PascalName>Card"
}

// Run renders the templates for opts.Kind into opts.OutputDir.
func Run(opts Options) error {
	if opts.Name == "" {
		return fmt.Errorf("plugin name is required")
	}
	if !pluginIDPattern.MatchString(opts.Name) {
		return fmt.Errorf("plugin name %q must match %s (lowercase, starts with a letter, kebab-case, 2-63 chars)", opts.Name, pluginIDPattern)
	}
	if opts.Kind == "" {
		return fmt.Errorf("plugin kind is required (subprocess or builtin)")
	}
	if opts.OutputDir == "" {
		return fmt.Errorf("output directory is required")
	}

	if _, err := os.Stat(filepath.Join(opts.OutputDir, "plugin.yaml")); err == nil {
		return fmt.Errorf("plugin already exists at %s", opts.OutputDir)
	}

	data := buildTemplateData(opts)

	var root string
	switch opts.Kind {
	case KindSubprocess:
		root = "templates/subprocess"
	case KindBuiltin:
		root = "templates/builtin"
	default:
		return fmt.Errorf("unknown plugin kind %q (want subprocess or builtin)", opts.Kind)
	}

	return renderTree(root, opts.OutputDir, data)
}

// buildTemplateData derives every template variable from Options,
// applying defaults. Kept separate so tests can assert the derivation
// rules without touching the filesystem.
func buildTemplateData(opts Options) templateData {
	desc := opts.Description
	if desc == "" {
		desc = fmt.Sprintf("A Nanite plugin named %s.", opts.Name)
	}
	author := opts.Author
	if author == "" {
		author = "Plugin Author"
	}
	mod := opts.ModulePath
	if mod == "" {
		mod = "github.com/example/nanite-plugin-" + opts.Name
	}
	envType := opts.Name + "-card"

	return templateData{
		Name:              opts.Name,
		DisplayName:       toTitle(opts.Name),
		PackageName:       toPackageName(opts.Name),
		StructName:        toStructName(opts.Name),
		Description:       desc,
		Author:            author,
		ModulePath:        mod,
		Year:              fmt.Sprintf("%d", time.Now().Year()),
		EnvelopeType:      envType,
		EnvelopeComponent: toPascal(opts.Name) + "Card",
	}
}

// renderTree walks the embedded template root and writes every file
// to dst, executing .tmpl files through text/template and passing
// non-.tmpl files through verbatim. Directory structure is preserved.
// The `gitignore.tmpl` basename is renamed to `.gitignore` on output
// (Go's embed.FS cannot ship files whose names start with ".").
func renderTree(root, dst string, data templateData) error {
	return fs.WalkDir(templateFS, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return os.MkdirAll(dst, 0o755)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}

		// Strip .tmpl suffix, and map bare `gitignore` → `.gitignore`
		// so the generated tree uses the real dotfile name.
		base := filepath.Base(target)
		dir := filepath.Dir(target)
		base = strings.TrimSuffix(base, ".tmpl")
		if base == "gitignore" {
			base = ".gitignore"
		}
		outPath := filepath.Join(dir, base)

		content, err := templateFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read template %s: %w", path, err)
		}

		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return err
		}

		if strings.HasSuffix(path, ".tmpl") {
			tmpl, err := template.New(filepath.Base(path)).Parse(string(content))
			if err != nil {
				return fmt.Errorf("parse %s: %w", path, err)
			}
			f, err := os.Create(outPath)
			if err != nil {
				return fmt.Errorf("create %s: %w", outPath, err)
			}
			if err := tmpl.Execute(f, data); err != nil {
				f.Close()
				return fmt.Errorf("execute %s: %w", path, err)
			}
			if err := f.Close(); err != nil {
				return err
			}
		} else {
			if err := os.WriteFile(outPath, content, 0o644); err != nil {
				return fmt.Errorf("write %s: %w", outPath, err)
			}
		}
		return nil
	})
}

// toPackageName maps "my-plugin" → "myplugin" (valid Go package name).
func toPackageName(name string) string {
	return strings.ReplaceAll(strings.ReplaceAll(name, "-", ""), "_", "")
}

// toPascal maps "my-plugin" → "MyPlugin".
func toPascal(name string) string {
	parts := strings.FieldsFunc(name, func(r rune) bool {
		return r == '-' || r == '_'
	})
	var out strings.Builder
	for _, part := range parts {
		if len(part) > 0 {
			runes := []rune(part)
			runes[0] = unicode.ToUpper(runes[0])
			out.WriteString(string(runes))
		}
	}
	return out.String()
}

// toStructName maps "my-plugin" → "MyPluginPlugin".
func toStructName(name string) string {
	return toPascal(name) + "Plugin"
}

// toTitle maps "my-plugin" → "My Plugin".
func toTitle(name string) string {
	parts := strings.FieldsFunc(name, func(r rune) bool {
		return r == '-' || r == '_'
	})
	var titled []string
	for _, part := range parts {
		if len(part) > 0 {
			runes := []rune(part)
			runes[0] = unicode.ToUpper(runes[0])
			titled = append(titled, string(runes))
		}
	}
	return strings.Join(titled, " ")
}
