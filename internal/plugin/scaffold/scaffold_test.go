package scaffold

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	hostplugin "github.com/hollis-labs/nanite/internal/plugin"
	"gopkg.in/yaml.v3"
)

// Files that MUST exist after scaffolding a subprocess plugin. Golden
// list — changes to the subprocess template must update this.
var expectedSubprocessFiles = []string{
	"main.go",
	"go.mod",
	"Makefile",
	"plugin.yaml",
	"README.md",
	"LICENSE",
	"CHANGELOG.md",
	".gitignore",
	"envelopes/example.schema.json",
	"ui/package.json",
	"ui/tsconfig.json",
	"ui/vite.config.ts",
	"ui/src/index.tsx",
	".github/workflows/release.yml",
}

var expectedBuiltinFiles = []string{
	"plugin.go",
	"plugin.yaml",
	"README.md",
}

func TestRun_Subprocess_FileTree(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "foo")

	if err := Run(Options{
		Kind:      KindSubprocess,
		Name:      "foo",
		Author:    "Test Author",
		OutputDir: out,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, rel := range expectedSubprocessFiles {
		if _, err := os.Stat(filepath.Join(out, rel)); err != nil {
			t.Errorf("missing expected file %q: %v", rel, err)
		}
	}
}

func TestRun_Subprocess_ManifestValid(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "foo")

	if err := Run(Options{
		Kind:      KindSubprocess,
		Name:      "foo",
		Author:    "Test Author",
		OutputDir: out,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	yamlBytes, err := os.ReadFile(filepath.Join(out, "plugin.yaml"))
	if err != nil {
		t.Fatalf("read plugin.yaml: %v", err)
	}

	// Normalize yaml → json so the embedded v1 schema can validate it.
	var raw any
	if err := yaml.Unmarshal(yamlBytes, &raw); err != nil {
		t.Fatalf("parse yaml: %v", err)
	}
	jsonBytes, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	var doc any
	if err := json.NewDecoder(bytes.NewReader(jsonBytes)).Decode(&doc); err != nil {
		t.Fatalf("decode json: %v", err)
	}

	schema, err := hostplugin.SchemaV1()
	if err != nil {
		t.Fatalf("load schema: %v", err)
	}
	if err := schema.Validate(doc); err != nil {
		t.Fatalf("manifest fails schema validation:\n%v\n\nyaml:\n%s", err, yamlBytes)
	}

	// Bundle dir must point at ui/dist (the BLG-20260414-008 fix).
	if !strings.Contains(string(yamlBytes), "bundle_dir: ui/dist") {
		t.Errorf("plugin.yaml missing `bundle_dir: ui/dist`:\n%s", yamlBytes)
	}
	// shadcn_version must exercise the J.5 compat check.
	if !strings.Contains(string(yamlBytes), "shadcn_version:") {
		t.Errorf("plugin.yaml missing ui.shadcn_version:\n%s", yamlBytes)
	}
}

func TestRun_Subprocess_ViteExternalsIncludeSharedPrimitives(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "foo")
	if err := Run(Options{
		Kind:      KindSubprocess,
		Name:      "foo",
		OutputDir: out,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	vite, err := os.ReadFile(filepath.Join(out, "ui", "vite.config.ts"))
	if err != nil {
		t.Fatalf("read vite.config.ts: %v", err)
	}
	// All 11 J.5 primitives must be externalized so the host provides
	// them via the importmap instead of the plugin bundling its own.
	primitives := []string{
		"button", "card", "dialog", "dropdown-menu", "input",
		"popover", "scroll-area", "select", "separator", "textarea", "tooltip",
	}
	for _, p := range primitives {
		needle := "'@nanite/ui/" + p + "'"
		if !strings.Contains(string(vite), needle) {
			t.Errorf("vite.config.ts missing external %s", needle)
		}
	}
}

func TestRun_Subprocess_MakefileStagesUIDist(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "foo")
	if err := Run(Options{
		Kind:      KindSubprocess,
		Name:      "foo",
		OutputDir: out,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	mk, err := os.ReadFile(filepath.Join(out, "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	// The Makefile must stage ui/dist (matching ui.bundle_dir) — not
	// bare ui/. This is the BLG-20260414-008 canonical fix.
	if !strings.Contains(string(mk), "$$stage/ui/dist") {
		t.Errorf("Makefile does not stage into ui/dist:\n%s", mk)
	}
}

func TestRun_Subprocess_MainGoReferencesSDK(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "foo-bar")
	if err := Run(Options{
		Kind:      KindSubprocess,
		Name:      "foo-bar",
		OutputDir: out,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	main, err := os.ReadFile(filepath.Join(out, "main.go"))
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	src := string(main)
	if !strings.Contains(src, `"github.com/hollis-labs/plugin-sdk/subprocess"`) {
		t.Errorf("main.go missing plugin-sdk subprocess import")
	}
	if !strings.Contains(src, "subprocess.Serve") {
		t.Errorf("main.go does not call subprocess.Serve")
	}
	if !strings.Contains(src, "MCPCallTool") {
		t.Errorf("main.go missing MCPCallTool handler")
	}
	if !strings.Contains(src, "FooBarPlugin") {
		t.Errorf("main.go missing derived struct name FooBarPlugin: %s", src)
	}

	gomod, err := os.ReadFile(filepath.Join(out, "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	if !strings.Contains(string(gomod), "github.com/hollis-labs/plugin-sdk v0.3.0") {
		t.Errorf("go.mod missing plugin-sdk v0.3.0 pin:\n%s", gomod)
	}
}

func TestRun_Builtin_Basic(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "my-widget")
	if err := Run(Options{
		Kind:      KindBuiltin,
		Name:      "my-widget",
		OutputDir: out,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, rel := range expectedBuiltinFiles {
		if _, err := os.Stat(filepath.Join(out, rel)); err != nil {
			t.Errorf("missing expected file %q: %v", rel, err)
		}
	}

	goSrc, err := os.ReadFile(filepath.Join(out, "plugin.go"))
	if err != nil {
		t.Fatalf("read plugin.go: %v", err)
	}
	if !strings.Contains(string(goSrc), "package mywidget") {
		t.Errorf("plugin.go wrong package:\n%s", goSrc)
	}
	if !strings.Contains(string(goSrc), "MyWidgetPlugin") {
		t.Errorf("plugin.go missing MyWidgetPlugin struct")
	}
	if !strings.Contains(string(goSrc), `hostplugin.RegisterPlugin("my-widget"`) {
		t.Errorf("plugin.go missing RegisterPlugin call")
	}

	// The builtin manifest must also validate against the v1 schema.
	yamlBytes, err := os.ReadFile(filepath.Join(out, "plugin.yaml"))
	if err != nil {
		t.Fatalf("read plugin.yaml: %v", err)
	}
	var raw any
	if err := yaml.Unmarshal(yamlBytes, &raw); err != nil {
		t.Fatalf("parse yaml: %v", err)
	}
	jsonBytes, _ := json.Marshal(raw)
	var doc any
	_ = json.Unmarshal(jsonBytes, &doc)
	schema, err := hostplugin.SchemaV1()
	if err != nil {
		t.Fatalf("load schema: %v", err)
	}
	if err := schema.Validate(doc); err != nil {
		t.Fatalf("builtin manifest fails schema:\n%v\n\nyaml:\n%s", err, yamlBytes)
	}
}

func TestRun_AlreadyExists(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "existing")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "plugin.yaml"), []byte("name: existing"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := Run(Options{Kind: KindSubprocess, Name: "existing", OutputDir: out})
	if err == nil {
		t.Fatal("expected error for existing plugin")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRun_RequiresKind(t *testing.T) {
	if err := Run(Options{Name: "foo", OutputDir: t.TempDir()}); err == nil {
		t.Fatal("expected error when Kind is empty")
	}
}

func TestRun_RejectsInvalidName(t *testing.T) {
	for _, name := range []string{
		"Foo",          // uppercase
		"1foo",         // leading digit
		"foo_bar",      // underscore
		"foo.bar",      // dot
		"foo/bar",      // slash — path-traversal vector
		"../foo",       // path-traversal
		"f",            // too short (min 2)
		strings.Repeat("a", 64), // too long (max 63)
	} {
		t.Run(name, func(t *testing.T) {
			err := Run(Options{Kind: KindSubprocess, Name: name, OutputDir: t.TempDir()})
			if err == nil {
				t.Fatalf("expected Run to reject invalid name %q, got nil error", name)
			}
		})
	}
}

func TestBuildTemplateData_Defaults(t *testing.T) {
	d := buildTemplateData(Options{Kind: KindSubprocess, Name: "my-plugin"})
	if d.Author != "Plugin Author" {
		t.Errorf("default Author = %q, want %q", d.Author, "Plugin Author")
	}
	if d.ModulePath != "github.com/example/nanite-plugin-my-plugin" {
		t.Errorf("default ModulePath = %q", d.ModulePath)
	}
	if d.DisplayName != "My Plugin" {
		t.Errorf("DisplayName = %q, want %q", d.DisplayName, "My Plugin")
	}
	if d.PackageName != "myplugin" {
		t.Errorf("PackageName = %q, want %q", d.PackageName, "myplugin")
	}
	if d.StructName != "MyPluginPlugin" {
		t.Errorf("StructName = %q", d.StructName)
	}
	if d.EnvelopeType != "my-plugin-card" {
		t.Errorf("EnvelopeType = %q", d.EnvelopeType)
	}
	if d.EnvelopeComponent != "MyPluginCard" {
		t.Errorf("EnvelopeComponent = %q", d.EnvelopeComponent)
	}
}

func TestToStructName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"my-plugin", "MyPluginPlugin"},
		{"simple", "SimplePlugin"},
		{"multi-word-name", "MultiWordNamePlugin"},
	}
	for _, c := range cases {
		if got := toStructName(c.in); got != c.want {
			t.Errorf("toStructName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestToPackageName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"my-plugin", "myplugin"},
		{"simple", "simple"},
		{"multi-word-name", "multiwordname"},
	}
	for _, c := range cases {
		if got := toPackageName(c.in); got != c.want {
			t.Errorf("toPackageName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
