package authored

import (
	"os"
	"path/filepath"
	"testing"
)

// providers.yaml and identity.yaml are read through an os.Root, like
// variants: a symlink that leaves the agent directory is an error, even when
// the target is a valid file.
func TestAuthoredFilesRefuseSymlinkEscape(t *testing.T) {
	for _, tc := range []struct {
		name, content string
		load          func(dir string) error
	}{
		{"providers.yaml", "use:\n  chat: {provider: anthropic, model: claude-haiku-5-5}\n", func(dir string) error { _, err := LoadProviders(dir); return err }},
		{"identity.yaml", "principal: agent:outside\n", func(dir string) error { _, _, err := LoadIdentity(dir); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, outside := t.TempDir(), t.TempDir()
			target := filepath.Join(outside, tc.name)
			if err := os.WriteFile(target, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			// Control: the same content inside the directory loads.
			if err := os.WriteFile(filepath.Join(dir, tc.name), []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := tc.load(dir); err != nil {
				t.Fatalf("valid file refused: %v", err)
			}
			if err := os.Remove(filepath.Join(dir, tc.name)); err != nil {
				t.Fatal(err)
			}
			for _, link := range []string{target, filepath.Join("..", filepath.Base(outside), tc.name)} {
				if err := os.Symlink(link, filepath.Join(dir, tc.name)); err != nil {
					t.Fatal(err)
				}
				if err := tc.load(dir); err == nil {
					t.Fatalf("symlink to %s followed out of the agent directory", link)
				}
				if err := os.Remove(filepath.Join(dir, tc.name)); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
