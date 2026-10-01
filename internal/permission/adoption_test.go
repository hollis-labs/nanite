package permission

import (
	"os"
	"path/filepath"
	"testing"

	permissionlib "github.com/hollis-labs/go-permission"
)

func TestSharedRules_DirectoryBoundaryAndInputKeys(t *testing.T) {
	allow := &permissionlib.RuleSet{Rules: []permissionlib.Rule{{Tool: "dev_*", Pattern: "/work/proj/**", Behavior: permissionlib.DecisionAllow}}}
	for _, key := range []string{"path", "file", "directory", "file_path"} {
		for _, tc := range []struct {
			path string
			want bool
		}{
			{"/work/proj", true},
			{"/work/proj/nested/source.go", true},
			{"/work/proj-secret/source.go", false},
			{"/work/proj/../proj-secret/source.go", false},
		} {
			t.Run(key+tc.path, func(t *testing.T) {
				got := allow.Evaluate("dev_write", map[string]any{key: tc.path})
				if (got != nil) != tc.want {
					t.Fatalf("rule matched outside its directory: %+v, want match=%v", got, tc.want)
				}
			})
		}
	}
	deny := &permissionlib.RuleSet{Rules: []permissionlib.Rule{{Tool: "dev_*", Pattern: "**/.env", Behavior: permissionlib.DecisionDeny}}}
	if got := deny.Evaluate("dev_read", map[string]any{"path": "/work/proj/nested/.env"}); got == nil || got.Decision != permissionlib.DecisionDeny {
		t.Fatalf("recursive basename deny not enforced: %+v", got)
	}
	command := &permissionlib.RuleSet{Rules: []permissionlib.Rule{{Tool: "shell", Pattern: "rm -rf", Behavior: permissionlib.DecisionAsk}}}
	if got := command.Evaluate("shell", map[string]any{"command": "echo start; rm -rf output"}); got == nil || got.Decision != permissionlib.DecisionAsk {
		t.Fatalf("existing command substring rule lost: %+v", got)
	}
}

func TestSharedRules_InvalidConfigurationIsRejected(t *testing.T) {
	for _, body := range []string{
		"permissions:\n  mode: typo\n",
		"permissions:\n  rules:\n    - tool: shell\n      behavior: typo\n",
	} {
		path := filepath.Join(t.TempDir(), "permissions.yaml")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := permissionlib.LoadRulesFromFile(path); err == nil {
			t.Fatal("invalid permission configuration silently accepted")
		}
	}
}
