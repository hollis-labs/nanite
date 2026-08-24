package main

import (
	"context"
	"fmt"
	"os"

	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/config"
	"github.com/hollis-labs/nanite/internal/skill"
	"github.com/hollis-labs/nanite/internal/skillinstall"
	"github.com/hollis-labs/nanite/internal/skillvendor"
	"github.com/hollis-labs/nanite/internal/store"
)

// cmdSkill dispatches `nanite skill <install|sync> ...` — the CLI trigger
// for TASKS/skills/04's explicit, single-target install/sync pipeline
// (internal/skillinstall.Installer), mirroring TASKS/skills/05's REST
// counterpart (POST /api/skills/install, POST /api/skills/{slug}/sync).
//
// Unlike cmd/nanite/plugin_cmd.go's install flow — a pure filesystem-copy
// operation that needs no DB access at all, notifying a *running* service
// only for the hot-reload tail step over HTTP (triggerHotReload) — a skill
// install/sync writes both a vendored-store copy and a DB index row: state
// a running `nanite serve` process and this CLI invocation genuinely share.
// This command opens the same SQLite database directly (store.New,
// resolveDBPath — the same helper mcp_cmd.go's import/export subcommands
// use) rather than routing through the REST API: the store's own WAL +
// busy_timeout(5s) pragmas (internal/store/store.go) are what make that
// safe across concurrent processes, matching this codebase's existing
// multi-process-CLI convention rather than requiring a running server at
// all for this command to work.
func cmdSkill(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: %s skill <install|sync> ...\n", brand.BinaryName)
		fmt.Fprintln(os.Stderr, "commands:")
		fmt.Fprintln(os.Stderr, "  install <path>        install a skill package from a local directory")
		fmt.Fprintln(os.Stderr, "  sync <slug> <path>    re-run install for an already-indexed skill")
		os.Exit(1)
	}

	switch args[0] {
	case "install":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "usage: %s skill install <path>\n", brand.BinaryName)
			os.Exit(1)
		}
		skillInstallCmd(args[1])
	case "sync":
		if len(args) < 3 {
			fmt.Fprintf(os.Stderr, "usage: %s skill sync <slug> <path>\n", brand.BinaryName)
			os.Exit(1)
		}
		skillSyncCmd(args[1], args[2])
	default:
		fmt.Fprintf(os.Stderr, "unknown skill command: %s\n", args[0])
		os.Exit(1)
	}
}

// resolveSkillVendorRoot resolves the content-addressed vendored skill
// store's filesystem root the same way cmd/nanite/main.go's initMCP
// resolves the artifacts root: load config/nanite.yaml's tunables, falling back
// to config.DefaultAppConfig() on any load error (missing/malformed config
// file) or an empty configured value, so a CLI invocation is never blocked
// by config trouble a running `nanite serve` would itself tolerate.
func resolveSkillVendorRoot() string {
	appCfg, err := config.LoadAppConfig("config/" + brand.ConfigFileName + ".yaml")
	if err != nil || appCfg == nil {
		appCfg = config.DefaultAppConfig()
	}
	if appCfg.Skills.VendorStorageDir != "" {
		return appCfg.Skills.VendorStorageDir
	}
	return config.DefaultAppConfig().Skills.VendorStorageDir
}

// openSkillInstaller opens the shared DB (resolveDBPath, matching
// mcp_cmd.go's import/export commands) and the vendored skill store, and
// returns a fresh *skillinstall.Installer plus the underlying store handle
// for the caller to close. A fresh Installer is built per invocation
// deliberately — see internal/service/container.go's SkillVendor doc
// comment for why this codebase never keeps one shared, long-lived
// Installer around across calls.
func openSkillInstaller() (*skillinstall.Installer, *store.Store) {
	dbPath := resolveDBPath()
	s, err := store.New(context.Background(), dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: open db: %v\n", err)
		os.Exit(1)
	}

	vendorRoot := resolveSkillVendorRoot()
	vendor, err := skillvendor.New(vendorRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: init skill vendor store at %q: %v\n", vendorRoot, err)
		s.Close(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */)
		os.Exit(1)
	}

	return &skillinstall.Installer{
		Vendor: vendor,
		Index:  s,
		Emit:   printSkillInstallEvents(),
	}, s
}

// printSkillInstallEvents mirrors cmd/nanite/plugin_install_flow.go's
// printEvents: throttled, human-readable step progress on stdout.
func printSkillInstallEvents() skillinstall.EventFunc {
	last := skillinstall.State("")
	return func(e skillinstall.Event) {
		if e.State != last {
			fmt.Printf("  [%s] %s\n", e.State, e.Message)
			last = e.State
		}
		if e.Err != nil {
			fmt.Printf("  failure: %v\n", e.Err)
		}
	}
}

// skillInstallCmd runs `nanite skill install <path>`. Errors from any
// pipeline step (a malformed package failing Parse/Validate just as much as
// a Vendor/Index infra problem) are reported with a specific message and a
// non-zero exit — never a panic or a bare stack trace.
func skillInstallCmd(path string) {
	installer, s := openSkillInstaller()
	defer s.Close(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */)

	result, err := installer.Install(context.Background(), skillinstall.Source{Path: path})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s skill install: failed: %v\n", brand.BinaryName, err)
		os.Exit(1)
	}

	fmt.Printf("\nSkill %q installed (slug=%s, version=%d)\n", result.Skill.Name, result.Skill.Slug, result.Skill.Version)
	fmt.Printf("  address: %s%s\n", result.Address, reusedSuffix(result.Reused))
}

// skillSyncCmd runs `nanite skill sync <slug> <path>`. Requires slug to
// already be indexed (a clear error, not a silent create, when it isn't)
// and requires the package at path to declare that same slug in its own
// SKILL.md frontmatter — mirroring handleSyncSkill's REST-side guard so
// both entry points reject the same "wrong package for this slug"
// mistake before anything is vendored or indexed under it.
func skillSyncCmd(slug, path string) {
	installer, s := openSkillInstaller()
	defer s.Close(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */)

	existing, err := s.GetSkillBySlug(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, slug)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s skill sync: %v\n", brand.BinaryName, err)
		os.Exit(1)
	}
	if existing == nil {
		fmt.Fprintf(os.Stderr, "%s skill sync: skill %q not found — run `%s skill install %s` first\n",
			brand.BinaryName, slug, brand.BinaryName, path)
		os.Exit(1)
	}

	def, _, err := skill.ParsePackageDir(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s skill sync: parse: %v\n", brand.BinaryName, err)
		os.Exit(1)
	}
	if def.Slug != slug {
		fmt.Fprintf(os.Stderr, "%s skill sync: package at %q declares slug %q, does not match sync target %q\n",
			brand.BinaryName, path, def.Slug, slug)
		os.Exit(1)
	}

	result, err := installer.Install(context.Background(), skillinstall.Source{Path: path})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s skill sync: failed: %v\n", brand.BinaryName, err)
		os.Exit(1)
	}

	fmt.Printf("\nSkill %q synced (slug=%s, version=%d)\n", result.Skill.Name, result.Skill.Slug, result.Skill.Version)
	fmt.Printf("  address: %s%s\n", result.Address, reusedSuffix(result.Reused))
}

func reusedSuffix(reused bool) string {
	if reused {
		return " (reused — content unchanged)"
	}
	return ""
}
