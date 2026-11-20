package authored

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseToolDefaultsAndFields(t *testing.T) {
	pkg, err := ParseTool("queue-peek", []byte(`name: queue-peek
description: Look at the queue.
version: 0.2.0
kind: command
entry: bin/run
timeout: 30s
network: true
env: [QUEUE_URL]
needs:
  - audience: queue
    scopes: [queue.read]
    env: QUEUE_TOKEN
`))
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Timeout != 30*time.Second || !pkg.Network || string(pkg.InputSchema) != `{"type":"object"}` || pkg.Dir() != "tools/queue-peek" ||
		len(pkg.Needs) != 1 || pkg.Needs[0].Env != "QUEUE_TOKEN" || pkg.Needs[0].Scopes[0] != "queue.read" {
		t.Fatalf("%+v", pkg)
	}
	mcp, err := ParseTool("srv", []byte("description: A server.\nkind: mcp\nentry: run.sh\n"))
	if err != nil || mcp.Timeout != 0 || mcp.InputSchema != nil {
		t.Fatalf("%+v, %v", mcp, err)
	}
}

func TestParseToolRejects(t *testing.T) {
	base := "description: d\nkind: command\nentry: run.sh\n"
	for _, tc := range []struct{ name, pkg, yaml, want string }{
		{"bad name", "Bad_Name", base, "invalid tool package name"},
		{"name mismatch", "a", "name: b\n" + base, "does not match"},
		{"no description", "a", "kind: command\nentry: run.sh\n", "description is required"},
		{"no kind", "a", "description: d\nentry: run.sh\n", "kind is required"},
		{"unknown kind", "a", "description: d\nkind: http\nentry: run.sh\n", "unknown kind"},
		{"no entry", "a", "description: d\nkind: command\n", "entry is required"},
		{"escaping entry", "a", "description: d\nkind: command\nentry: ../x\n", "clean relative path"},
		{"absolute entry", "a", "description: d\nkind: command\nentry: /bin/sh\n", "clean relative path"},
		{"unknown field", "a", base + "shell: true\n", "field shell not found"},
		{"schema type", "a", base + "input_schema: {type: string}\n", "type must be object"},
		{"timeout format", "a", base + "timeout: soon\n", "positive duration"},
		{"timeout limit", "a", base + "timeout: 1h\n", "above the"},
		{"mcp schema", "a", "description: d\nkind: mcp\nentry: run.sh\ninput_schema: {}\n", "input_schema is for kind command"},
		{"mcp timeout", "a", "description: d\nkind: mcp\nentry: run.sh\ntimeout: 5s\n", "timeout is for kind command"},
		{"mcp background", "a", "description: d\nkind: mcp\nentry: run.sh\nbackground: true\n", "background is for kind command"},
		{"bad env", "a", base + "env: [lower]\n", "invalid variable name"},
		{"reserved env", "a", base + "env: [PATH]\n", "reserved"},
		{"duplicate env", "a", base + "env: [A_B, A_B]\n", "listed twice"},
		{"need without audience", "a", base + "needs:\n  - env: T\n", "audience is required"},
		{"need env clash", "a", base + "env: [T]\nneeds:\n  - audience: x\n    env: T\n", "already used"},
		{"need reserved env", "a", base + "needs:\n  - audience: x\n    env: NANITE_TOKEN\n", "reserved"},
		{"two documents", "a", base + "---\n" + base, "one document"},
		{"provider key env", "a", base + "env: [ANTHROPIC_API_KEY]\n", "provider key Nanite uses"},
		{"credential env", "a", base + "env: [GITHUB_TOKEN]\n", "looks like a credential"},
		{"unsafe schema", "a", base + "input_schema:\n  properties:\n    enum:\n      $ref: '#/properties/enum'\n", "unsafe schema"},
		{"empty", "a", "", "empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseTool(tc.pkg, []byte(tc.yaml))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err %v, want %q", err, tc.want)
			}
		})
	}
}

func TestLoadToolsChecksTheEntry(t *testing.T) {
	dir := t.TempDir()
	write := func(path, body string, mode os.FileMode) {
		t.Helper()
		full := filepath.Join(dir, "tools", path)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	manifest := "description: d\nkind: command\nentry: run.sh\n"
	write("good/tool.yaml", manifest, 0o600)
	write("good/run.sh", "#!/bin/sh\n", 0o700)
	write("plain/tool.yaml", manifest, 0o600)
	write("plain/run.sh", "#!/bin/sh\n", 0o600)
	write("missing/tool.yaml", manifest, 0o600)
	write("linked/tool.yaml", manifest, 0o600)
	if err := os.Symlink("/bin/sh", filepath.Join(dir, "tools", "linked", "run.sh")); err != nil {
		t.Fatal(err)
	}
	write("README.md", "not a package", 0o600)

	pkgs, err := LoadTools(dir)
	if len(pkgs) != 1 || pkgs["good"].Name != "good" {
		t.Fatalf("loaded %v", ToolNames(pkgs))
	}
	for _, want := range []string{"tools/plain: entry run.sh is not executable", "tools/missing: entry run.sh", "tools/linked: entry run.sh must be a regular file"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("err %v, want %q", err, want)
		}
	}
	if pkgs, err := LoadTools(t.TempDir()); err != nil || len(pkgs) != 0 {
		t.Fatalf("no tools directory: %v, %v", pkgs, err)
	}
}

func TestCredentialEnvNeedsTheHostsAllowance(t *testing.T) {
	manifest := []byte("description: d\nkind: command\nentry: run.sh\nenv: [GITHUB_TOKEN]\n")
	if _, err := ParseTool("a", manifest); err == nil {
		t.Fatal("a credential-like variable was accepted without an allowance")
	}
	if pkg, err := ParseTool("a", manifest, "GITHUB_TOKEN"); err != nil || pkg.Env[0] != "GITHUB_TOKEN" {
		t.Fatalf("allowed: %+v, %v", pkg, err)
	}
	// A token Nanite mints may have any variable name.
	if _, err := ParseTool("a", []byte("description: d\nkind: command\nentry: run.sh\nneeds:\n  - audience: x\n    env: X_TOKEN\n")); err != nil {
		t.Fatal(err)
	}
}

func TestPackageDigestAndHardLinks(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "tools", "p")
	if err := os.MkdirAll(pkgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "tool.yaml"), []byte("description: d\nkind: mcp\nentry: run.sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(pkgDir, "run.sh")
	if err := os.WriteFile(entry, []byte("#!/bin/sh\n"), 0o700); err != nil { //nolint:gosec // G306: the entry must be executable
		t.Fatal(err)
	}
	first, err := LoadTool(dir, "p")
	if err != nil || len(first.Digest) != 64 {
		t.Fatalf("%+v, %v", first, err)
	}
	if again, _ := LoadTool(dir, "p"); again.Digest != first.Digest {
		t.Fatal("the digest is not stable")
	}
	// Any file in the package changes it, not only the entry.
	if err := os.WriteFile(filepath.Join(pkgDir, "lib.py"), []byte("x = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if changed, _ := LoadTool(dir, "p"); changed.Digest == first.Digest {
		t.Fatal("a new file did not change the digest")
	}
	if err := os.Link(entry, filepath.Join(dir, "outside")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTool(dir, "p"); err == nil || !strings.Contains(err.Error(), "other hard links") {
		t.Fatalf("a hard-linked entry loaded: %v", err)
	}
}
