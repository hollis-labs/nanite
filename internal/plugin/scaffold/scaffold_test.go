package scaffold

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/manifest"

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

func TestRun_Subprocess_UnbuiltDraftCannotInstall(t *testing.T) {
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

	if _, err := hostplugin.DecodeManifest(bytes.NewReader(yamlBytes)); err == nil {
		t.Fatal("unbuilt scaffold accepted without an artifact inventory")
	}
	var draft manifest.Manifest
	if err := json.Unmarshal(yamlBytes, &draft); err != nil {
		t.Fatal(err)
	}
	if draft.Server.Entry != "bin/foo" || draft.Protocol != 2 || draft.UI == nil || draft.UI.Bundle != "ui/dist/index.js" {
		t.Fatalf("unexpected draft declaration: %+v", draft)
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

func TestGeneratedManifestInventoriesBytesAndRefusesSymlinks(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := Run(Options{Kind: KindSubprocess, Name: "foo", OutputDir: source}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	generator := filepath.Join(root, "generator")
	// #nosec G204 -- literal Go build arguments target this private generated fixture.
	build := exec.CommandContext(ctx, "go", "build", "-mod=mod", "-o", generator, ".")
	build.Dir = source
	build.Env = append(os.Environ(), "GOWORK=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build generated plugin: %v\n%s", err, output)
	}
	bundle := filepath.Join(root, "bundle")
	for _, dir := range []string{"bin", "ui/dist", "envelopes"} {
		if err := os.MkdirAll(filepath.Join(bundle, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range map[string]string{"bin/foo": "binary bytes", "ui/dist/index.js": "UI bytes", "envelopes/example.schema.json": `{ "type": "object" }`} {
		mode := os.FileMode(0o600)
		if name == "bin/foo" {
			mode = 0o700
		}
		if err := os.WriteFile(filepath.Join(bundle, name), []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	generate := func() ([]byte, error) {
		// #nosec G204 -- executes only the generator built above in this private test root.
		return exec.CommandContext(ctx, generator, "--manifest", bundle, "bin/foo").CombinedOutput()
	}
	encoded, err := generate()
	if err != nil {
		t.Fatalf("generate completed bundle: %v\n%s", err, encoded)
	}
	declaration, err := manifest.Decode(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, manifest.Filename), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := declaration.VerifyBundle(bundle); err != nil {
		t.Fatalf("generated inventory does not verify: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "ui/dist/index.js"), []byte("changed UI"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := declaration.VerifyBundle(bundle); err == nil {
		t.Fatal("changed bundle bytes passed the generated inventory")
	}
	if err := os.Symlink(filepath.Join(bundle, "bin/foo"), filepath.Join(bundle, "alias")); err != nil {
		t.Fatal(err)
	}
	if output, err := generate(); err == nil {
		t.Fatalf("symlink accepted: %s", output)
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
	if !strings.Contains(src, `"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess"`) {
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
	if !strings.Contains(string(gomod), "github.com/hollis-labs/libs/plugin-mcp v0.2.0") {
		t.Errorf("go.mod missing published plugin-mcp v0.2.0 pin:\n%s", gomod)
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
		"Foo",                   // uppercase
		"1foo",                  // leading digit
		"foo_bar",               // underscore
		"foo.bar",               // dot
		"foo/bar",               // slash — path-traversal vector
		"../foo",                // path-traversal
		"f",                     // too short (min 2)
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
