package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	permissionlib "github.com/hollis-labs/go-permission"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/modelsdevtest"
	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
	"github.com/hollis-labs/nanite/internal/storetest"
)

// CW-20261001-0232: the container installs the policy that confines a mention
// to $HOME and the configured allowed bases, and keeps this instance's own data
// directory out of reach. Without it the chat turn's confinement would
// silently be the built-in denylist alone.
func TestNewContainer_ConfinesPathMentions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, env := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
		t.Setenv(env, "")
	}
	base := t.TempDir()  // a configured allowed base outside $HOME
	other := t.TempDir() // outside $HOME and not configured
	dataDir := filepath.Join(home, "instance-data")
	controlDir := filepath.Join(home, "instance-control") // a control-plane directory, apart from the database's
	for _, d := range []string{dataDir, controlDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	st, err := storetest.New(t, context.Background(), filepath.Join(dataDir, "nanite.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })

	c, err := NewContainer(ContainerConfig{
		ModelCatalogOptions: modelsdevtest.Options(t),
		Store:               st, Providers: provider.NewRegistry(), WorkingDir: home,
		DevToolsAllowedPaths:     []string{base},
		AgentControlPlane:        runtimeagent.ControlPlane{Dirs: []string{controlDir}},
		DisableEmbeddedTesseract: true,
	})
	if err != nil {
		t.Fatalf("NewContainer: %v", err)
	}
	t.Cleanup(c.Shutdown)
	c.Permissions.SetMode(permissionlib.ModeAcceptEdits)
	for _, tool := range []string{"dev_edit", "dev_write"} {
		if got := c.Permissions.Check(t.Context(), "session", tool, nil, permissionlib.ToolMeta{}); got.Decision != permissionlib.DecisionAllow {
			t.Fatalf("host file-edit registration missing for %s: %+v", tool, got)
		}
	}
	if got := c.Permissions.Check(t.Context(), "session", "shell", nil, permissionlib.ToolMeta{}); got.Decision != permissionlib.DecisionAsk {
		t.Fatalf("accept-edits widened to a non-edit write: %+v", got)
	}

	for _, tc := range []struct {
		name, path string
		want       bool
	}{
		{"inside home", filepath.Join(home, "dev", "x.go"), true},
		{"inside a configured base", filepath.Join(base, "x.go"), true},
		{"outside home and bases", filepath.Join(other, "x.go"), false},
		{"system path", "/etc/hosts", false},
		{"this instance's data directory", filepath.Join(dataDir, "nanite.db"), false},
		{"a control-plane directory", filepath.Join(controlDir, "coordination", "x"), false},
		{"ssh", filepath.Join(home, ".ssh", "id_ed25519"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sess := "sess-" + tc.name
			c.PathGrants.RegisterFromUserMessage(sess, "please change "+tc.path)
			if got := c.PathGrants.IsPathAllowed(sess, tc.path); got != tc.want {
				t.Fatalf("mention of %s: granted = %v, want %v (grants %v)", tc.path, got, tc.want, c.PathGrants.ListGrants(sess))
			}
		})
	}
}
