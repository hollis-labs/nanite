package install

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestDetectAdapters(t *testing.T) {
	cases := []struct {
		name  string
		setup func(dir string)
		want  []string
	}{
		{
			name:  "no evidence",
			setup: func(dir string) {},
			want:  nil,
		},
		{
			name: "CLAUDE.md only",
			setup: func(dir string) {
				_ = os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# hi"), 0o644)
			},
			want: []string{"claude"},
		},
		{
			name: ".claude/ dir only",
			setup: func(dir string) {
				_ = os.MkdirAll(filepath.Join(dir, ".claude"), 0o755)
			},
			want: []string{"claude"},
		},
		{
			name: "all four root files",
			setup: func(dir string) {
				for _, f := range []string{"CLAUDE.md", "AGENTS.md", "GEMINI.md", "OPENCODE.md"} {
					_ = os.WriteFile(filepath.Join(dir, f), []byte("# hi"), 0o644)
				}
			},
			want: []string{"claude", "codex", "gemini", "opencode"},
		},
		{
			name: "AGENTS.md and .gemini/ only",
			setup: func(dir string) {
				_ = os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# hi"), 0o644)
				_ = os.MkdirAll(filepath.Join(dir, ".gemini"), 0o755)
			},
			want: []string{"codex", "gemini"},
		},
		{
			name: "both file and dir for one adapter — counts once",
			setup: func(dir string) {
				_ = os.WriteFile(filepath.Join(dir, "OPENCODE.md"), []byte("# hi"), 0o644)
				_ = os.MkdirAll(filepath.Join(dir, ".opencode"), 0o755)
			},
			want: []string{"opencode"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.setup(dir)
			got := DetectAdapters(dir)
			sort.Strings(got)
			sort.Strings(tc.want)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
