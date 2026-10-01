package mcp

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/hollis-labs/nanite/internal/permission"
)

// CW-20260930-0251: dev_write and dev_edit write wherever resolveAllowed
// says, and an agent can plant a symlink with dev_bash before it calls
// them. These pin that a planted symlink cannot carry either write outside
// the directory that admitted the path: a static allowed root, or the
// directory a session grant covers.

// confinementCase plants one symlink shape under base and names the path the
// agent hands dev_write / dev_edit, plus the outside file that must stay
// untouched.
type confinementCase struct {
	name string
	// plant creates the symlink (and any outside fixture) and returns the
	// path to pass to the tool and the outside file to watch.
	plant func(t *testing.T, base, outsideDir string) (toolPath, outsideFile string)
	// edit runs dev_edit instead of dev_write.
	edit bool
}

const outsideOriginal = "outside original\n"

func confinementCases() []confinementCase {
	return []confinementCase{
		{
			name: "dev_write through a dangling final link",
			plant: func(t *testing.T, base, outsideDir string) (string, string) {
				outside := filepath.Join(outsideDir, "planted.txt")                      //nolint:forbidigo // test fixture under t.TempDir()
				return mustSymlink(t, outside, filepath.Join(base, "dangling")), outside //nolint:forbidigo // test fixture under t.TempDir()
			},
		},
		{
			name: "dev_write through a final link to an existing outside file",
			plant: func(t *testing.T, base, outsideDir string) (string, string) {
				outside := mustWriteFixture(t, outsideDir, "existing.txt")
				return mustSymlink(t, outside, filepath.Join(base, "to-file")), outside //nolint:forbidigo // test fixture under t.TempDir()
			},
		},
		{
			name: "dev_write under a directory link to outside",
			plant: func(t *testing.T, base, outsideDir string) (string, string) {
				link := mustSymlink(t, outsideDir, filepath.Join(base, "to-dir"))           //nolint:forbidigo // test fixture under t.TempDir()
				return filepath.Join(link, "new.txt"), filepath.Join(outsideDir, "new.txt") //nolint:forbidigo // test fixture under t.TempDir()
			},
		},
		{
			name: "dev_edit through a final link to an existing outside file",
			edit: true,
			plant: func(t *testing.T, base, outsideDir string) (string, string) {
				outside := mustWriteFixture(t, outsideDir, "existing.txt")
				return mustSymlink(t, outside, filepath.Join(base, "to-file")), outside //nolint:forbidigo // test fixture under t.TempDir()
			},
		},
		{
			name: "dev_edit under a directory link to outside",
			edit: true,
			plant: func(t *testing.T, base, outsideDir string) (string, string) {
				outside := mustWriteFixture(t, outsideDir, "existing.txt")
				link := mustSymlink(t, outsideDir, filepath.Join(base, "to-dir")) //nolint:forbidigo // test fixture under t.TempDir()
				return filepath.Join(link, "existing.txt"), outside               //nolint:forbidigo // test fixture under t.TempDir()
			},
		},
	}
}

func TestDevWriteEdit_AllowedRoot_PlantedSymlinkCannotEscape(t *testing.T) {
	skipWithoutSymlinks(t)
	for _, tc := range confinementCases() {
		t.Run(tc.name, func(t *testing.T) {
			dt, root := tempDevTools(t)
			toolPath, outside := tc.plant(t, root, canonicalDir(t))
			existed := fixtureExists(outside)
			assertResolutionRefused(context.Background(), t, dt, toolPath)
			assertConfined(t, callWriteOrEdit(context.Background(), t, dt, toolPath, tc.edit), outside, existed)
		})
	}
}

func TestDevWriteEdit_SessionGrant_PlantedSymlinkCannotEscape(t *testing.T) {
	skipWithoutSymlinks(t)
	for _, tc := range confinementCases() {
		t.Run(tc.name, func(t *testing.T) {
			// The static allow-list names an unrelated directory, so only
			// the session grant admits the path.
			dt := NewDevToolsTransport([]string{canonicalDir(t)})
			granted := canonicalDir(t)
			grants := permission.NewPathGrants()
			// Mentioning a file grants it and its parent directory (Q2).
			if got := grants.RegisterFromUserMessage("sess-grant", "look at "+filepath.Join(granted, "notes.md")); len(got) == 0 { //nolint:forbidigo // test fixture under t.TempDir()
				t.Fatal("grant registration parsed no path")
			}
			ctx := permission.WithPathGrants(context.Background(), "sess-grant", grants)

			toolPath, outside := tc.plant(t, granted, canonicalDir(t))
			existed := fixtureExists(outside)
			assertResolutionRefused(ctx, t, dt, toolPath)
			assertConfined(t, callWriteOrEdit(ctx, t, dt, toolPath, tc.edit), outside, existed)
		})
	}
}

// TestDevWrite_SessionGrant_StillWritesInsideGrant keeps the grant useful:
// confinement must not turn the grant into a refusal.
func TestDevWrite_SessionGrant_StillWritesInsideGrant(t *testing.T) {
	dt := NewDevToolsTransport([]string{canonicalDir(t)})
	granted := canonicalDir(t)
	grants := permission.NewPathGrants()
	grants.RegisterFromUserMessage("sess-grant", "look at "+filepath.Join(granted, "notes.md")) //nolint:forbidigo // test fixture under t.TempDir()
	ctx := permission.WithPathGrants(context.Background(), "sess-grant", grants)

	target := filepath.Join(granted, "sub", "new.txt") //nolint:forbidigo // test fixture under t.TempDir()
	result := callWriteOrEdit(ctx, t, dt, target, false)
	if result.IsError {
		t.Fatalf("dev_write inside the grant refused: %s", result.Content[0].Text)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "agent wrote this\n" { //nolint:gosec // reads a file this test just wrote under t.TempDir()
		t.Fatalf("granted write = (%q, %v)", got, err)
	}
}

// TestDevWrite_DanglingLinkIntoAnotherAllowedRoot pins the removal of
// resolveAllowed's unresolved-target fallback. A link in one allowed root
// whose not-yet-existing target is in another allowed root is a write the
// allow-list permits; the fallback judged the link's own path instead of its
// target and refused it, while the same link to an existing file passed.
func TestDevWrite_DanglingLinkIntoAnotherAllowedRoot(t *testing.T) {
	skipWithoutSymlinks(t)
	rootA, rootB := canonicalDir(t), canonicalDir(t)
	dt := NewDevToolsTransport([]string{rootA, rootB})
	target := filepath.Join(rootB, "new.txt")                    //nolint:forbidigo // test fixture under t.TempDir()
	link := mustSymlink(t, target, filepath.Join(rootA, "to-b")) //nolint:forbidigo // test fixture under t.TempDir()

	result := callWriteOrEdit(context.Background(), t, dt, link, false)
	if result.IsError {
		t.Fatalf("dev_write through a link into another allowed root refused: %s", result.Content[0].Text)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "agent wrote this\n" { //nolint:gosec // reads a file this test just wrote under t.TempDir()
		t.Fatalf("write through the link = (%q, %v), want it at %s", got, err, target)
	}
}

func callWriteOrEdit(ctx context.Context, t *testing.T, dt *DevToolsTransport, path string, edit bool) *ToolResult {
	t.Helper()
	tool, args := "dev_write", map[string]any{"path": path, "content": "agent wrote this\n"}
	if edit {
		tool, args = "dev_edit", map[string]any{"path": path, "old_string": "original", "new_string": "edited by agent"}
	}
	result, err := dt.CallTool(ctx, tool, args)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// assertResolutionRefused checks the resolution layer on its own: the write
// sinks also confine (writeConfined), so a tool-level refusal alone would not
// show which layer held.
func assertResolutionRefused(ctx context.Context, t *testing.T, dt *DevToolsTransport, path string) {
	t.Helper()
	if got, err := dt.resolveConfined(ctx, path); err == nil {
		t.Fatalf("resolveConfined admitted %s as %+v", path, got)
	}
}

// assertConfined checks the outside file kept its pre-call state (absent, or
// its original content) and that the tool reported a refusal.
func assertConfined(t *testing.T, result *ToolResult, outside string, existed bool) {
	t.Helper()
	got, err := os.ReadFile(outside) //nolint:gosec // reads a fixture this test planted under t.TempDir()
	switch {
	case existed && (err != nil || string(got) != outsideOriginal):
		t.Fatalf("outside file %s changed: (%q, %v)", outside, got, err)
	case !existed && !os.IsNotExist(err):
		t.Fatalf("write escaped to %s (content %q, err %v)", outside, got, err)
	}
	if !result.IsError {
		t.Fatalf("expected the planted symlink to be refused, got: %s", result.Content[0].Text)
	}
}

func fixtureExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func skipWithoutSymlinks(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on windows")
	}
}

func canonicalDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func mustSymlink(t *testing.T, target, link string) string {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	return link
}

func mustWriteFixture(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name) //nolint:forbidigo // test fixture under t.TempDir()
	if err := os.WriteFile(path, []byte(outsideOriginal), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestWriteConfined_SymlinkSwappedInAfterResolution covers the window
// resolution cannot: dev_bash runs alongside dev_write, so a path that was
// clean when resolveConfined admitted it can gain a symlink before the
// write. The sinks hold the write and the read inside the admitting root.
func TestWriteConfined_SymlinkSwappedInAfterResolution(t *testing.T) {
	skipWithoutSymlinks(t)
	dt, root := tempDevTools(t)
	ctx := context.Background()

	t.Run("a parent directory becomes a link out", func(t *testing.T) {
		outsideDir := canonicalDir(t)
		sub := filepath.Join(root, "sub") //nolint:forbidigo // test fixture under t.TempDir()
		if err := os.Mkdir(sub, 0o750); err != nil {
			t.Fatal(err)
		}
		admitted, err := dt.resolveConfined(ctx, filepath.Join(sub, "new.txt")) //nolint:forbidigo // test fixture under t.TempDir()
		if err != nil {
			t.Fatalf("resolveConfined: %v", err)
		}
		if err := os.Remove(sub); err != nil {
			t.Fatal(err)
		}
		mustSymlink(t, outsideDir, sub)

		if err := writeConfined(admitted, []byte("agent wrote this\n")); err == nil {
			t.Fatal("writeConfined followed a directory link swapped in after resolution")
		}
		if fixtureExists(filepath.Join(outsideDir, "new.txt")) { //nolint:forbidigo // test fixture under t.TempDir()
			t.Fatal("the write landed outside the root")
		}
	})

	t.Run("the file becomes a link out", func(t *testing.T) {
		outside := mustWriteFixture(t, canonicalDir(t), "existing.txt")
		file := mustWriteFixture(t, root, "file.txt")
		admitted, err := dt.resolveConfined(ctx, file)
		if err != nil {
			t.Fatalf("resolveConfined: %v", err)
		}
		if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
		mustSymlink(t, outside, file)

		if got, err := readConfined(admitted); err == nil {
			t.Fatalf("readConfined followed a file link swapped in after resolution: %q", got)
		}
		if err := writeConfined(admitted, []byte("agent wrote this\n")); err == nil {
			t.Fatal("writeConfined followed a file link swapped in after resolution")
		}
		if got, err := os.ReadFile(outside); err != nil || string(got) != outsideOriginal { //nolint:gosec // reads a fixture this test planted under t.TempDir()
			t.Fatalf("outside file changed: (%q, %v)", got, err)
		}
	})
}
