package main

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/hollis-labs/go-apppaths/paths"
	fplugin "github.com/hollis-labs/plugin-sdk"

	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/config"
	"github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/plugin/scaffold"
	"github.com/hollis-labs/nanite/internal/slogx"
	"github.com/hollis-labs/nanite/internal/store"
)

const pluginGitOrg = "hollis-labs"

// noRestart is set via the --no-restart flag to skip auto-restart after
// install/uninstall/disable/enable operations.
var noRestart bool

// installLink is set via the --link flag to symlink the source directory
// into the plugins dir instead of copying. Only meaningful for local-path
// install.
var installLink bool

func cmdPlugin(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: %s plugin <command> [--no-restart]\n", brand.BinaryName)
		fmt.Fprintln(os.Stderr, "commands: new, install <name|path> [--link], update <name>, uninstall, list, disable, enable, logs, reload, watch, release")
		os.Exit(1)
	}

	// Extract --no-restart and --link flags from anywhere in the args.
	var filtered []string
	for _, a := range args {
		switch a {
		case "--no-restart":
			noRestart = true
		case "--link":
			installLink = true
		default:
			filtered = append(filtered, a)
		}
	}
	args = filtered

	if installLink && len(args) > 0 && args[0] != "install" {
		fmt.Fprintf(os.Stderr, "--link is only valid with %s plugin install\n", brand.BinaryName)
		os.Exit(1)
	}

	switch args[0] {
	case "new":
		pluginNew(args[1:])
		return
	case "install":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: "+brand.BinaryName+" plugin install <name|path> [--link]")
			fmt.Fprintln(os.Stderr, "  <name>  clone github.com/"+pluginGitOrg+"/<name>.git")
			fmt.Fprintln(os.Stderr, "  <path>  install from a local directory (./, ../, /, or existing dir name)")
			fmt.Fprintln(os.Stderr, "  --link  symlink source into plugins dir instead of copying (local path only)")
			os.Exit(1)
		}
		pluginInstall(args[1])
	case "uninstall":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: "+brand.BinaryName+" plugin uninstall <name>")
			os.Exit(1)
		}
		pluginUninstall(args[1])
	case "update":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: "+brand.BinaryName+" plugin update <name>")
			os.Exit(1)
		}
		pluginUpdate(args[1])
	case "logs":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: "+brand.BinaryName+" plugin logs <name>")
			os.Exit(1)
		}
		pluginLogs(args[1])
	case "reload":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: "+brand.BinaryName+" plugin reload <name>")
			os.Exit(1)
		}
		pluginReload(args[1])
	case "watch":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: "+brand.BinaryName+" plugin watch <plugin-src-dir>")
			os.Exit(1)
		}
		pluginWatch(args[1])
	case "release":
		target := "."
		if len(args) >= 2 {
			target = args[1]
		}
		pluginRelease(target)
	case "list":
		pluginList()
	case "disable":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: "+brand.BinaryName+" plugin disable <name>")
			os.Exit(1)
		}
		pluginDisable(args[1])
	case "enable":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: "+brand.BinaryName+" plugin enable <name>")
			os.Exit(1)
		}
		pluginEnable(args[1])
	default:
		fmt.Fprintf(os.Stderr, "unknown plugin command: %s\n", args[0])
		os.Exit(1)
	}
}

func resolvePluginsDir() string {
	if d := os.Getenv(brand.Env("PLUGINS_DIR")); d != "" {
		return d
	}
	return "./plugins"
}

// resolveDBPath returns the path to nanite's main SQLite database, resolved
// via go-apppaths (CW-20260517-0061). It is the shared resolver behind every
// `cmd/nanite` entry point — the six former `./` + brand.DefaultDBName
// CWD-relative defaults all funnel through resolveDBPathWith.
//
// resolveDBPath takes no explicit --db flag; callers that have one pass it to
// resolveDBPathWith directly.
func resolveDBPath() string {
	return resolveDBPathWith("")
}

// resolveDBPathWith resolves the main database path, honoring (in precedence
// order):
//
//  1. the explicit --db flag value (flagDB), when non-empty;
//  2. the NANITE_DB legacy env var — a compat alias kept so existing shell
//     profiles / scripts / the rollback plan do not silently break;
//  3. go-apppaths native resolution, which itself honors NANITE_DB_PATH and
//     NANITE_WORKSPACE before falling back to the XDG default
//     ~/.local/share/nanite/workspaces/default/main.db.
//
// NANITE_DB vs NANITE_DB_PATH: go-apppaths reads <APP>_DB_PATH natively, i.e.
// NANITE_DB_PATH. The legacy var was NANITE_DB; it is mapped here through
// WithDBOverride so both work. NANITE_DB_PATH is the canonical going-forward
// name; NANITE_DB is the deprecated alias.
//
// On a resolution error the process exits — a daemon that cannot resolve its
// DB path must not silently open one at the wrong location (the data-loss
// failure mode this migration removes).
func resolveDBPathWith(flagDB string) string {
	var opts []paths.Option
	switch {
	case flagDB != "":
		opts = append(opts, paths.WithDBOverride(flagDB))
	case os.Getenv(brand.Env("DB")) != "":
		// Legacy NANITE_DB compat alias. NANITE_DB_PATH (read natively by
		// go-apppaths) takes precedence if both are set, since it is wired
		// inside paths.Resolve and only applies here when no flag/alias wins.
		opts = append(opts, paths.WithDBOverride(os.Getenv(brand.Env("DB"))))
	}
	layout, err := config.ResolveLayout(opts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: resolve database path: %v\n", brand.BinaryName, err)
		os.Exit(1)
	}
	return layout.MainDB()
}

// triggerRestart attempts to restart the service via cerberus.
// If cerberus is unavailable, prints a manual restart message.
func triggerRestart() {
	if noRestart {
		return
	}
	fmt.Printf("Restarting %s...\n", brand.ServiceName)
	cmd := exec.Command("cerberus", "restart", brand.ServiceName)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Println("  Could not auto-restart (cerberus not available).")
		fmt.Printf("  Restart manually: cerberus restart %s\n", brand.ServiceName)
	}
}

// isLocalPath returns true if arg refers to a local filesystem path
// rather than a plugin name to clone from GitHub. Explicit path
// prefixes (./, ../, /) are always local; a bare token is local if it
// names an existing directory (so `nanite plugin install my-plugin`
// works from a parent dir that has a my-plugin checkout).
func isLocalPath(arg string) bool {
	if strings.HasPrefix(arg, "./") || strings.HasPrefix(arg, "../") ||
		filepath.IsAbs(arg) || arg == "." || arg == ".." {
		return true
	}
	info, err := os.Stat(arg)
	return err == nil && info.IsDir()
}

func pluginInstall(arg string) {
	if installLink && !isLocalPath(arg) {
		fmt.Fprintln(os.Stderr, "--link requires a local path, not a plugin name")
		os.Exit(1)
	}
	if isLocalPath(arg) {
		pluginInstallLocal(arg)
		return
	}
	pluginInstallRemote(arg)
}

// pluginInstallLocal installs a plugin from a local directory by
// copying (or symlinking with --link) its contents into the resolved
// plugins dir under the canonical id from plugin.yaml.
func pluginInstallLocal(src string) {
	absSrc, err := filepath.Abs(src)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to resolve %q: %v\n", src, err)
		os.Exit(1)
	}
	manifestPath := filepath.Join(absSrc, "plugin.yaml")
	if _, err := os.Stat(manifestPath); err != nil {
		fmt.Fprintf(os.Stderr, "No plugin.yaml at %s — not a valid plugin source\n", manifestPath)
		os.Exit(1)
	}
	manifest, err := plugin.ParseManifest(manifestPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to parse plugin.yaml: %v\n", err)
		os.Exit(1)
	}
	id := manifest.Identifier()
	if id == "" {
		fmt.Fprintln(os.Stderr, "plugin.yaml missing id/name — cannot determine install target")
		os.Exit(1)
	}

	dir := resolvePluginsDir()
	target := filepath.Join(dir, id)
	// Fail fast on any existing state at target — active install
	// (plugin.yaml), disabled install (plugin.yaml.disabled), or a
	// leftover partial/empty dir/symlink. Refusing to overwrite avoids
	// merging fresh source files into stale artifacts.
	if _, err := os.Lstat(target); err == nil {
		fmt.Fprintf(os.Stderr, "Target %s already exists. Run `%s plugin uninstall %s` or remove the directory first.\n",
			target, brand.BinaryName, id)
		os.Exit(1)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create plugins dir: %v\n", err)
		os.Exit(1)
	}

	if installLink {
		fmt.Printf("Linking %s → %s...\n", absSrc, target)
		if err := os.Symlink(absSrc, target); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to symlink: %v\n", err)
			os.Exit(1)
		}
	} else {
		fmt.Printf("Copying %s → %s...\n", absSrc, target)
		if err := copyPluginDir(absSrc, target); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to copy: %v\n", err)
			os.RemoveAll(target)
			os.Exit(1)
		}
	}

	if _, ok := plugin.LookupConstructor(id); ok {
		fmt.Printf("Found compiled-in code for %q\n", id)
	} else if manifest.Runtime != "subprocess" {
		fmt.Printf("Warning: no compiled-in code for %q and runtime is not subprocess — plugin will not load\n", id)
	}

	fmt.Printf("\nPlugin %q installed to %s\n", id, target)
	triggerRestart()
}

// pluginInstallRemote installs a plugin. Tries the signed catalog first;
// if the plugin is not listed there, falls back to the legacy git-clone
// flow at github.com/hollis-labs/<name>.git.
func pluginInstallRemote(name string) {
	dir := resolvePluginsDir()
	target := filepath.Join(dir, name)

	// Check if already installed
	if _, err := os.Stat(filepath.Join(target, "plugin.yaml")); err == nil {
		fmt.Printf("Plugin %q is already installed at %s\n", name, target)
		os.Exit(1)
	}

	// Try catalog first.
	if os.Getenv(brand.Env("PLUGIN_SKIP_CATALOG")) == "" {
		fmt.Printf("Resolving %q in catalog (%s)...\n", name, resolveCatalogURL())
		final, found, err := installFromCatalog(context.Background(), name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Catalog install failed: %v\n", err)
			os.Exit(1)
		}
		if found {
			fmt.Printf("\nPlugin %q installed from catalog to %s\n", name, final)
			triggerRestart()
			return
		}
		fmt.Printf("  %q not in catalog — falling back to git clone.\n", name)
	}

	// Ensure plugins dir exists
	os.MkdirAll(dir, 0755)

	// Clone from GitHub
	repoURL := fmt.Sprintf("git@github.com:%s/%s.git", pluginGitOrg, name)
	fmt.Printf("Installing %s from %s...\n", name, repoURL)

	cmd := exec.Command("git", "clone", "--depth", "1", repoURL, target)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to clone: %v\n", err)
		os.Exit(1)
	}

	// Verify plugin.yaml exists
	if _, err := os.Stat(filepath.Join(target, "plugin.yaml")); err != nil {
		fmt.Fprintf(os.Stderr, "Cloned repo does not contain plugin.yaml — not a valid plugin\n")
		os.RemoveAll(target)
		os.Exit(1)
	}

	// Parse manifest
	manifest, err := plugin.ParseManifest(filepath.Join(target, "plugin.yaml"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to parse plugin.yaml: %v\n", err)
		os.RemoveAll(target)
		os.Exit(1)
	}

	// Check for compiled-in constructor
	if _, ok := plugin.LookupConstructor(manifest.Name); !ok {
		fmt.Printf("Warning: no compiled-in code for %q — plugin will need to be added to the binary\n", manifest.Name)
	} else {
		fmt.Printf("Found compiled-in code for %q\n", manifest.Name)
	}

	fmt.Printf("\nPlugin %q installed to %s\n", name, target)
	triggerRestart()
}

// skipCopyNames are entry names (directories or files) that are never
// copied during a local plugin install. Build artifacts, git state,
// OS metadata — never part of a plugin's runtime surface.
var skipCopyNames = map[string]bool{
	".git":         true,
	"node_modules": true,
	"dist":         true,
	".DS_Store":    true,
}

// copyPluginDir recursively copies src to dst, skipping directories
// listed in skipCopyDirs and preserving file modes. Symlinks inside
// src are recreated as symlinks in dst (not resolved).
func copyPluginDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0755)
		}
		if skipCopyNames[d.Name()] {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			info, err := d.Info()
			if err != nil {
				return err
			}
			return os.MkdirAll(target, info.Mode()&os.ModePerm)
		case d.Type()&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		default:
			return copyFile(path, target)
		}
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode()&os.ModePerm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		if cerr := out.Close(); cerr != nil {
			return fmt.Errorf("copy %s: %w (close: %v)", src, err, cerr)
		}
		return err
	}
	return out.Close()
}

func pluginUninstall(name string) {
	dir := resolvePluginsDir()
	target := filepath.Join(dir, name)

	// Check if installed (active or disabled)
	manifestPath := filepath.Join(target, "plugin.yaml")
	disabledPath := filepath.Join(target, "plugin.yaml.disabled")
	if _, err := os.Stat(manifestPath); err != nil {
		if _, err2 := os.Stat(disabledPath); err2 != nil {
			fmt.Fprintf(os.Stderr, "Plugin %q is not installed\n", name)
			os.Exit(1)
		}
		// Disabled plugin — use the disabled manifest path for parsing
		manifestPath = disabledPath
	}

	// Parse manifest
	manifest, err := plugin.ParseManifest(manifestPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to parse plugin.yaml: %v\n", err)
		os.Exit(1)
	}

	// Run plugin's Uninstall() if it implements Uninstallable
	if constructor, ok := plugin.LookupConstructor(manifest.Name); ok {
		p := constructor()
		if uninstallable, ok := p.(fplugin.Uninstallable); ok {
			fmt.Printf("Running %s cleanup...\n", manifest.Name)
			host, hostErr := buildMinimalHost()
			if hostErr != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not initialize for cleanup: %v\n", hostErr)
			} else {
				if err := uninstallable.Uninstall(host); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: cleanup error: %v\n", err)
				} else {
					fmt.Println("Cleanup complete — agent profile removed.")
				}
			}
		}
	}

	// Remove the plugin directory
	if err := os.RemoveAll(target); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to remove %s: %v\n", target, err)
		os.Exit(1)
	}

	fmt.Printf("\nPlugin %q uninstalled.\n", name)
	triggerRestart()
}

func pluginDisable(name string) {
	dir := resolvePluginsDir()
	if err := plugin.DisablePlugin(dir, name); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Plugin %q disabled.\n", name)
	triggerRestart()
}

func pluginEnable(name string) {
	dir := resolvePluginsDir()
	if err := plugin.EnablePlugin(dir, name); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Plugin %q enabled.\n", name)
	triggerRestart()
}

func pluginList() {
	dir := resolvePluginsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("No plugins installed.")
			return
		}
		slogx.Fatal("read plugins dir", "err", err)
	}

	found := false
	fmt.Printf("%-25s %-10s %-10s %s\n", "PLUGIN", "VERSION", "STATUS", "DESCRIPTION")
	fmt.Println(strings.Repeat("-", 80))

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		name := entry.Name()
		status := plugin.PluginStatus(dir, name)

		// Try to parse manifest (active or disabled)
		manifestPath := filepath.Join(dir, name, "plugin.yaml")
		if status == "disabled" {
			manifestPath = filepath.Join(dir, name, "plugin.yaml.disabled")
		}
		manifest, err := plugin.ParseManifest(manifestPath)
		if err != nil {
			continue
		}

		found = true
		fmt.Printf("%-25s %-10s %-10s %s\n",
			name, manifest.Version, status, manifest.Description)
	}

	if !found {
		fmt.Println("No plugins installed.")
	}
}

// buildMinimalHost creates a plugin host with just the store service,
// sufficient for running Uninstall() cleanup methods.
func buildMinimalHost() (*plugin.Host, error) {
	dbPath := resolveDBPath()
	s, err := store.New(context.Background(), dbPath)
	if err != nil {
		return nil, fmt.Errorf("open database %s: %w", dbPath, err)
	}
	return plugin.NewHostWithStore(s), nil
}

// pluginNew scaffolds a new plugin using embedded templates. Two kinds
// are supported via mutually-exclusive flags:
//
//	--subprocess <name>   out-of-process plugin (stand-alone repo layout)
//	--builtin <name>      compiled-in plugin under internal/plugin/builtin/
//
// A bare positional name (legacy form) still works and defaults to
// --subprocess for compatibility.
func pluginNew(args []string) {
	var (
		subprocessName string
		builtinName    string
		description    string
		author         string
		modulePath     string
		outputDir      string
	)

	var positional []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--subprocess":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "error: --subprocess requires a name")
				os.Exit(1)
			}
			subprocessName = args[i]
		case "--builtin":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "error: --builtin requires a name")
				os.Exit(1)
			}
			builtinName = args[i]
		case "--description":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "error: --description requires a value")
				os.Exit(1)
			}
			description = args[i]
		case "--author":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "error: --author requires a value")
				os.Exit(1)
			}
			author = args[i]
		case "--module":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "error: --module requires a value")
				os.Exit(1)
			}
			modulePath = args[i]
		case "--output", "-o":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "error: --output requires a value")
				os.Exit(1)
			}
			outputDir = args[i]
		case "--help", "-h":
			pluginNewHelp()
			return
		default:
			if strings.HasPrefix(args[i], "-") {
				fmt.Fprintf(os.Stderr, "unknown flag: %s\n", args[i])
				pluginNewHelp()
				os.Exit(1)
			}
			positional = append(positional, args[i])
		}
	}

	if subprocessName != "" && builtinName != "" {
		fmt.Fprintln(os.Stderr, "error: --subprocess and --builtin are mutually exclusive")
		os.Exit(1)
	}

	var (
		kind scaffold.Kind
		name string
	)
	switch {
	case subprocessName != "":
		kind = scaffold.KindSubprocess
		name = subprocessName
	case builtinName != "":
		kind = scaffold.KindBuiltin
		name = builtinName
	case len(positional) >= 1:
		// Legacy form: `nanite plugin new <name>` → subprocess.
		kind = scaffold.KindSubprocess
		name = positional[0]
	default:
		fmt.Fprintf(os.Stderr, "usage: %s plugin new --subprocess <name> | --builtin <name> [flags]\n", brand.BinaryName)
		fmt.Fprintf(os.Stderr, "Run '%s plugin new --help' for details.\n", brand.BinaryName)
		os.Exit(1)
	}

	if outputDir == "" {
		switch kind {
		case scaffold.KindSubprocess:
			outputDir = filepath.Join(resolvePluginsDir(), name)
		case scaffold.KindBuiltin:
			outputDir = filepath.Join("internal", "plugin", "builtin", strings.ReplaceAll(strings.ReplaceAll(name, "-", ""), "_", ""))
		}
	}

	opts := scaffold.Options{
		Kind:        kind,
		Name:        name,
		Description: description,
		Author:      author,
		ModulePath:  modulePath,
		OutputDir:   outputDir,
	}

	fmt.Printf("Scaffolding %s plugin %q in %s...\n", kind, name, opts.OutputDir)

	if err := scaffold.Run(opts); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println()
	fmt.Printf("Plugin %q created.\n", name)
	fmt.Println()
	switch kind {
	case scaffold.KindSubprocess:
		fmt.Println("Next steps:")
		fmt.Printf("  1. cd %s\n", opts.OutputDir)
		fmt.Println("  2. (optional) edit go.mod module path and README")
		fmt.Println("  3. make build            # native binary + UI bundle")
		fmt.Printf("  4. %s plugin install ./ --link\n", brand.BinaryName)
		fmt.Printf("  5. %s plugin release .   # cross-platform archives for catalog\n", brand.BinaryName)
	case scaffold.KindBuiltin:
		pkg := strings.ReplaceAll(strings.ReplaceAll(name, "-", ""), "_", "")
		fmt.Println("Next steps:")
		fmt.Println("  1. Add the import to internal/plugin/allplugins/allplugins.go:")
		fmt.Printf("     _ \"github.com/hollis-labs/nanite/internal/plugin/builtin/%s\"\n", pkg)
		fmt.Printf("  2. go install ./cmd/%s\n", brand.BinaryName)
		fmt.Printf("  3. cerberus restart %s\n", brand.ServiceName)
	}
}

func pluginNewHelp() {
	fmt.Printf("Usage: %s plugin new --subprocess <name> | --builtin <name> [flags]\n", brand.BinaryName)
	fmt.Println()
	fmt.Printf("Scaffold a new %s plugin.\n", brand.Name)
	fmt.Println()
	fmt.Println("Kinds:")
	fmt.Println("  --subprocess <name>   Stand-alone out-of-process plugin (repo layout with")
	fmt.Println("                        main.go, ui/, Makefile, GitHub release workflow).")
	fmt.Println("  --builtin <name>      Compiled-in plugin under internal/plugin/builtin/.")
	fmt.Println()
	fmt.Println("Flags:")
	fmt.Println("  --description <desc>  Plugin description (shown in manifest and README).")
	fmt.Println("  --author <name>       LICENSE copyright + manifest author.")
	fmt.Println("  --module <path>       Go module path (subprocess only).")
	fmt.Println("  --output, -o <dir>    Override the output directory.")
	fmt.Println("  -h, --help            Show this help.")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Printf("  %s plugin new --subprocess my-plugin --author \"Jane Doe\"\n", brand.BinaryName)
	fmt.Printf("  %s plugin new --builtin my-widget --description \"A quick widget\"\n", brand.BinaryName)
}
