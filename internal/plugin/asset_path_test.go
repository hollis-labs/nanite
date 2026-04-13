package plugin

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestResolvePluginAssetPath(t *testing.T) {
	plugin := t.TempDir()
	cases := []struct {
		name    string
		rel     string
		wantErr string
	}{
		{"simple", "envelopes/card.schema.json", ""},
		{"dot-relative", "./envelopes/card.schema.json", ""},
		{"absolute", "/etc/passwd", "absolute"},
		{"parent-escape", "../secrets.json", "escapes"},
		{"parent-deep-escape", "envelopes/../../../etc/passwd", "escapes"},
		{"bare-dotdot", "..", "escapes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolvePluginAssetPath(plugin, tc.rel)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if !strings.HasPrefix(got, plugin+string(filepath.Separator)) && got != filepath.Join(plugin, filepath.Clean(tc.rel)) {
					t.Fatalf("resolved path %q not confined under plugin dir %q", got, plugin)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got path %q", tc.wantErr, got)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}
