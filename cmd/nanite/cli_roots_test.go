package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/config"
	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
)

func TestCLIRootsCompatWarning(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  *config.RuntimeConfig
		warn bool
	}{
		{"nil config", nil, false},
		{"nothing set", &config.RuntimeConfig{}, false},
		{"dev tools set, cli roots unset (nil)", &config.RuntimeConfig{DevToolsAllowedPaths: []string{"/a"}}, true},
		{"dev tools set, cli roots explicit empty list", &config.RuntimeConfig{DevToolsAllowedPaths: []string{"/a"}, CLIWritableRoots: []string{}}, false},
		{"dev tools set, cli roots set", &config.RuntimeConfig{DevToolsAllowedPaths: []string{"/a"}, CLIWritableRoots: []string{"/b"}}, false},
		{"dev tools explicitly empty", &config.RuntimeConfig{DevToolsAllowedPaths: []string{}}, false},
		{"only cli roots", &config.RuntimeConfig{CLIWritableRoots: []string{"/b"}}, false},
	} {
		w := cliRootsCompatWarning(tc.cfg)
		if (w != "") != tc.warn {
			t.Errorf("%s: warning=%q, want warn=%v", tc.name, w, tc.warn)
		}
		if w != "" && !(strings.Contains(w, "dev_tools_allowed_paths") && strings.Contains(w, "cli_writable_roots")) {
			t.Errorf("%s: warning must name both keys: %q", tc.name, w)
		}
	}
}

func TestResolveCLIWritableRoots(t *testing.T) {
	cfg := &config.RuntimeConfig{DevToolsAllowedPaths: []string{"/dev-tools"}, CLIWritableRoots: []string{"/cli"}}

	roots, warns := resolveCLIWritableRoots(cfg, false)
	if !slices.Equal(roots, []string{"/cli"}) || len(warns) != 0 {
		t.Errorf("default: roots=%v warns=%v; dev tools list must not leak in", roots, warns)
	}

	// Unset cli_writable_roots: no roots, exactly one (compat) warning.
	roots, warns = resolveCLIWritableRoots(&config.RuntimeConfig{DevToolsAllowedPaths: []string{"/dev-tools"}}, false)
	if len(roots) != 0 || len(warns) != 1 {
		t.Errorf("unset: roots=%v warns=%v", roots, warns)
	}

	// Escape hatch: old behaviour plus a deprecation warning.
	roots, warns = resolveCLIWritableRoots(cfg, true)
	if !slices.Equal(roots, []string{"/cli", "/dev-tools"}) {
		t.Errorf("escape hatch roots = %v", roots)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], runtimeagent.CLIRootsFromDevToolsEnv) {
		t.Errorf("escape hatch must warn naming the env: %v", warns)
	}
	roots, warns = resolveCLIWritableRoots(&config.RuntimeConfig{DevToolsAllowedPaths: []string{"/dev-tools"}}, true)
	if !slices.Equal(roots, []string{"/dev-tools"}) || len(warns) != 2 {
		t.Errorf("hatch with unset cli roots: roots=%v warns=%v", roots, warns)
	}

	if roots, warns = resolveCLIWritableRoots(nil, true); roots != nil || warns != nil {
		t.Errorf("nil cfg: %v %v", roots, warns)
	}
}
