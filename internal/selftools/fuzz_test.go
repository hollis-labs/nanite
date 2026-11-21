package selftools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// Fuzz targets for the file and shell self-tools: model-supplied input must
// never panic, hang, escape the workspace or reach a protected path. The f.Add
// seeds run as ordinary tests in CI; `go test -fuzz` explores further.

// fuzzWorkspace builds base/ws with ordinary and non-ASCII files, symlinks
// leading out of the root, and a canary file outside it.
func fuzzWorkspace(t testing.TB) (w workspace, base string) {
	base = t.TempDir()
	root := filepath.Join(base, "ws")
	for name, text := range map[string]string{
		"a.txt": "alpha\nbeta\n", "d/b.md": "gate\n", "é.md": "accent\n", "日本/語.txt": "kanji\n",
		"long.txt": strings.Repeat("x", 2000) + "é\n", "empty.txt": "", ".git/config": "CANARY_PROTECTED\n",
	} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(base, "outside.txt"), []byte("CANARY_OUTSIDE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for link, target := range map[string]string{"esc": base, "esc.txt": filepath.Join(base, "outside.txt")} {
		if err := os.Symlink(target, filepath.Join(root, link)); err != nil {
			t.Fatal(err)
		}
	}
	return workspace{roots: []string{root}}, base
}

// outsideUntouched fails if anything outside base/ws was created or changed.
func outsideUntouched(t *testing.T, base string) {
	t.Helper()
	entries, err := os.ReadDir(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "ws" && e.Name() != "outside.txt" {
			t.Fatalf("created outside the workspace: %s", e.Name())
		}
	}
	if got, err := os.ReadFile(filepath.Join(base, "outside.txt")); err != nil || string(got) != "CANARY_OUTSIDE\n" { // #nosec G304 -- under t.TempDir.
		t.Fatalf("outside file changed: %q %v", got, err)
	}
}

// independentlyProtected restates the protected names without sharing
// protected()'s code, comparing case-insensitively.
func independentlyProtected(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		for _, name := range []string{".nanite", ".ssh", ".gnupg", ".aws", ".kube", ".docker", ".netrc", ".codex", ".config", ".local", ".bash_profile", ".bashrc", ".zshrc", ".zprofile", ".profile", ".git"} {
			if strings.EqualFold(part, name) {
				return true
			}
		}
		if len(part) >= 7 && strings.EqualFold(part[:7], ".claude") {
			return true
		}
	}
	return false
}

func FuzzSearch(f *testing.F) {
	for _, s := range [][3]string{
		{"", "", ""}, {"^", "", ""}, {"$", ".", ""}, {"(a*)*b", "", ""}, {"CANARY", "..", ""}, {"CANARY", "esc", ""},
		{"CANARY", "esc.txt", ""}, {"CANARY", ".git", ""}, {"CANARY", ".GIT", ""}, {".", "long.txt", ""}, {"x{1000}", "", ""},
		{"(?:(?:a{1000}){1000})", "", ""}, {"\xff", "", "\xff"}, {"a", "/", "*"}, {"a", "", "**"}, {"é", "", "é.md"},
	} {
		f.Add(s[0], s[1], s[2])
	}
	w, _ := fuzzWorkspace(f)
	f.Fuzz(func(t *testing.T, pattern, path, glob string) {
		start := time.Now()
		out, err := w.search(t.Context(), searchArgs{Pattern: pattern, Path: path, Glob: glob})
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Fatalf("search took %v", elapsed)
		}
		if err == nil && strings.Contains(out, "CANARY") {
			t.Fatalf("search reached an outside or protected file: %q", out)
		}
	})
}

// FuzzFindGlob: any pattern stays inside the workspace and off protected
// paths, and a literal glob equal to a file's base name finds that file.
func FuzzFindGlob(f *testing.F) {
	for _, s := range [][2]string{
		{"a.txt", "*"}, {"é.md", "**"}, {"語.txt", "日本/*"}, {"naïve.md", "/"}, {"x", ".."}, {"[x]", "["}, {"a b", "?"},
		{"\xff", "**/**/**"}, {"-", ""}, {"tab\there", "esc/*"}, {".hidden", ".git/*"},
	} {
		f.Add(s[0], s[1])
	}
	f.Fuzz(func(t *testing.T, name, pattern string) {
		w, base := fuzzWorkspace(t)
		if out, err := w.find(t.Context(), findArgs{Pattern: pattern}); err == nil {
			for _, line := range strings.Split(out, "\n") {
				if strings.HasPrefix(line, "esc") || strings.HasPrefix(line, "/") || strings.HasPrefix(line, ".git") || strings.Contains(line, "outside.txt") {
					t.Fatalf("find %q returned %q", pattern, line)
				}
			}
		}
		// The literal-name property, for names a file can carry and a glob
		// treats literally.
		if name == "" || name == "." || name == ".." || len(name) > 200 || !utf8.ValidString(name) ||
			strings.ContainsAny(name, "/\\*?\x00\n\r") || independentlyProtected(name) {
			return
		}
		if err := os.WriteFile(filepath.Join(base, "ws", name), []byte("x"), 0o600); err != nil {
			return // the filesystem refused the name
		}
		out, err := w.find(t.Context(), findArgs{Pattern: name})
		if err != nil || !slices.Contains(strings.Split(out, "\n"), name) {
			t.Fatalf("find %q did not find its own file: %q, %v", name, out, err)
		}
	})
}

// FuzzWorkspaceMutate: read, list, write and edit with any path never touch
// anything outside the root, never read the outside canary, and never write
// a protected path in any letter case.
func FuzzWorkspaceMutate(f *testing.F) {
	for _, p := range []string{
		"", ".", "..", "/", "../outside.txt", "esc/outside.txt", "esc.txt", "new.txt", "d", "d/", "d/../../outside.txt",
		"a.txt\x00", "./a.txt", "日本/新.txt", "nested/dir/x", "\xff.txt", ".GIT/config", ".Git/hooks/pre-commit",
		"d/.ssh/k", ".Claude/settings.json", ".NANITE/x",
	} {
		f.Add(p, "content", "alpha")
	}
	f.Fuzz(func(t *testing.T, path, content, old string) {
		w, base := fuzzWorkspace(t)
		ctx := t.Context()
		if out, err := w.read(ctx, pathArgs{Path: path}); err == nil && strings.Contains(out, "CANARY") {
			t.Fatalf("read %q reached a canary", path)
		}
		if out, err := w.list(ctx, pathArgs{Path: path}); err == nil && strings.Contains(out, "outside.txt") {
			t.Fatalf("list %q left the workspace", path)
		}
		_, writeErr := w.write(ctx, writeArgs{Path: path, Content: content})
		_, editErr := w.edit(ctx, editArgs{Path: path, Old: old, New: "Z"})
		if independentlyProtected(path) && (writeErr == nil || editErr == nil) {
			t.Fatalf("protected path %q written: write=%v edit=%v", path, writeErr, editErr)
		}
		outsideUntouched(t, base)
		if got, err := os.ReadFile(filepath.Join(base, "ws", ".git", "config")); err != nil || string(got) != "CANARY_PROTECTED\n" { // #nosec G304 -- under t.TempDir.
			t.Fatalf("protected file changed: %q %v", got, err)
		}
	})
}

// FuzzParseBash: an accepted bash call has a non-blank command and a timeout
// within [1s, 600s].
func FuzzParseBash(f *testing.F) {
	for _, s := range []string{
		``, `null`, `{}`, `[]`, `{"command":""}`, `{"command":"   "}`, `{"command":"ls","timeout_seconds":null}`,
		`{"command":"ls","timeout_seconds":1e10}`, `{"command":"ls","timeout_seconds":-1}`, `{"command":"ls","timeout_seconds":1.5}`,
		`{"command":"ls","timeout_seconds":9223372036854775807}`, `{"command":"ls\u0000"}`, `{"Command":"SHUTDOWN"}`,
		"{\"command\":\"\xff\"}", `{"command":"ls","extra":1}`, `{"command":"rm -rf /"}`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		in, err := ParseBash(json.RawMessage(raw))
		if err != nil {
			return
		}
		if strings.TrimSpace(in.Command) == "" {
			t.Fatal("blank command accepted")
		}
		if timeout := in.ShellCommand("/", nil).Timeout; timeout < time.Second || timeout > 600*time.Second {
			t.Fatalf("timeout %v accepted", timeout)
		}
	})
}
