// Package scaffold provides plugin scaffolding for the `nanite plugin new` command.
// It uses embedded Go templates to generate plugin boilerplate that matches
// the patterns used by existing Nanite plugins (e.g., support-ticket).
package scaffold

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"unicode"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

// EnvelopeDef describes an envelope component to generate.
type EnvelopeDef struct {
	Type   string // e.g. "card", "form"
	Export string // e.g. "CardCard", "FormCard"
}

// Options configures what the scaffolder generates.
type Options struct {
	Name          string        // plugin name/ID (e.g. "my-plugin")
	Description   string        // human-readable description
	WithAgent     bool          // generate agents/<name>.yaml
	Envelopes     []EnvelopeDef // envelope components to generate
	CRUDResources []string      // CRUD resource names (e.g. "items")
	OutputDir     string        // base output directory (e.g. "plugins/my-plugin")
	SchemaDir     string        // directory for envelope schemas (default: "internal/envelope/schemas")
}

// templateData holds all data passed to templates.
type templateData struct {
	PluginID          string
	PluginName        string
	PackageName       string
	StructName        string
	Description       string
	WithAgent         bool
	Envelopes         []EnvelopeDef
	CRUDResources     []string
	CRUDHandlerPrefix string
}

// Run generates the plugin scaffold files in opts.OutputDir.
func Run(opts Options) error {
	if opts.Name == "" {
		return fmt.Errorf("plugin name is required")
	}
	if opts.Description == "" {
		opts.Description = "A Nanite plugin"
	}
	if opts.OutputDir == "" {
		opts.OutputDir = filepath.Join("plugins", opts.Name)
	}

	// Check if directory already exists with a plugin.yaml
	if _, err := os.Stat(filepath.Join(opts.OutputDir, "plugin.yaml")); err == nil {
		return fmt.Errorf("plugin already exists at %s", opts.OutputDir)
	}

	data := templateData{
		PluginID:          opts.Name,
		PluginName:        toTitle(opts.Name),
		PackageName:       toPackageName(opts.Name),
		StructName:        toStructName(opts.Name),
		Description:       opts.Description,
		WithAgent:         opts.WithAgent,
		Envelopes:         opts.Envelopes,
		CRUDResources:     opts.CRUDResources,
		CRUDHandlerPrefix: "",
	}

	funcMap := template.FuncMap{
		"title": strings.Title,
	}

	// Create output directory
	if err := os.MkdirAll(opts.OutputDir, 0755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	// Generate plugin.yaml
	if err := renderTemplate(funcMap, "templates/plugin.yaml.tmpl", filepath.Join(opts.OutputDir, "plugin.yaml"), data); err != nil {
		return fmt.Errorf("generate plugin.yaml: %w", err)
	}

	// Generate plugin.go
	if err := renderTemplate(funcMap, "templates/plugin.go.tmpl", filepath.Join(opts.OutputDir, "plugin.go"), data); err != nil {
		return fmt.Errorf("generate plugin.go: %w", err)
	}

	// Generate README.md
	if err := renderTemplate(funcMap, "templates/readme.md.tmpl", filepath.Join(opts.OutputDir, "README.md"), data); err != nil {
		return fmt.Errorf("generate README.md: %w", err)
	}

	// Generate agent profile if requested
	if opts.WithAgent {
		agentsDir := filepath.Join(opts.OutputDir, "agents")
		if err := os.MkdirAll(agentsDir, 0755); err != nil {
			return fmt.Errorf("create agents directory: %w", err)
		}
		if err := renderTemplate(funcMap, "templates/agent.yaml.tmpl", filepath.Join(agentsDir, opts.Name+".yaml"), data); err != nil {
			return fmt.Errorf("generate agent.yaml: %w", err)
		}
	}

	// Generate envelope components and schemas if requested
	if len(opts.Envelopes) > 0 {
		uiDir := filepath.Join(opts.OutputDir, "ui")
		if err := os.MkdirAll(uiDir, 0755); err != nil {
			return fmt.Errorf("create ui directory: %w", err)
		}

		schemaDir := opts.SchemaDir
		if schemaDir == "" {
			schemaDir = filepath.Join("internal", "envelope", "schemas")
		}
		if err := os.MkdirAll(schemaDir, 0755); err != nil {
			return fmt.Errorf("create schema directory: %w", err)
		}

		for _, env := range opts.Envelopes {
			envData := struct {
				Type   string
				Export string
			}{
				Type:   env.Type,
				Export: env.Export,
			}
			if err := renderTemplate(funcMap, "templates/envelope.tsx.tmpl", filepath.Join(uiDir, env.Export+".tsx"), envData); err != nil {
				return fmt.Errorf("generate envelope %s: %w", env.Export, err)
			}
			// Generate a JSON Schema file for the envelope type.
			// The schema is the source of truth — edit it, then run codegen.
			schemaPath := filepath.Join(schemaDir, env.Type+".schema.json")
			if err := renderTemplate(funcMap, "templates/envelope.schema.json.tmpl", schemaPath, envData); err != nil {
				return fmt.Errorf("generate schema %s: %w", env.Type, err)
			}
		}
	}

	return nil
}

// renderTemplate parses a template from the embedded FS and writes it to outPath.
func renderTemplate(funcMap template.FuncMap, tmplName, outPath string, data interface{}) error {
	content, err := templateFS.ReadFile(tmplName)
	if err != nil {
		return fmt.Errorf("read template %s: %w", tmplName, err)
	}

	tmpl, err := template.New(filepath.Base(tmplName)).Funcs(funcMap).Parse(string(content))
	if err != nil {
		return fmt.Errorf("parse template %s: %w", tmplName, err)
	}

	f, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", outPath, err)
	}
	defer f.Close()

	if err := tmpl.Execute(f, data); err != nil {
		return fmt.Errorf("execute template %s: %w", tmplName, err)
	}

	return nil
}

// toPackageName converts "my-plugin" to "myplugin" (valid Go package name).
func toPackageName(name string) string {
	return strings.ReplaceAll(strings.ReplaceAll(name, "-", ""), "_", "")
}

// toStructName converts "my-plugin" to "MyPluginPlugin".
func toStructName(name string) string {
	parts := strings.FieldsFunc(name, func(r rune) bool {
		return r == '-' || r == '_'
	})
	var result string
	for _, part := range parts {
		if len(part) > 0 {
			runes := []rune(part)
			runes[0] = unicode.ToUpper(runes[0])
			result += string(runes)
		}
	}
	return result + "Plugin"
}

// toTitle converts "my-plugin" to "My Plugin".
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

// ToEnvelopeDef creates an EnvelopeDef from an envelope type string.
// e.g. "card" -> EnvelopeDef{Type: "card", Export: "CardCard"}
func ToEnvelopeDef(envelopeType string) EnvelopeDef {
	runes := []rune(envelopeType)
	if len(runes) > 0 {
		runes[0] = unicode.ToUpper(runes[0])
	}
	export := string(runes) + "Card"
	return EnvelopeDef{
		Type:   envelopeType,
		Export: export,
	}
}
