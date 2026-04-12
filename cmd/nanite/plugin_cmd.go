package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	fplugin "github.com/hollis-labs/go-plugin"

	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/plugin/scaffold"
	"github.com/hollis-labs/nanite/internal/slogx"
	"github.com/hollis-labs/nanite/internal/store"
)

const pluginGitOrg = "hollis-labs"

// noRestart is set via the --no-restart flag to skip auto-restart after
// install/uninstall/disable/enable operations.
var noRestart bool

func cmdPlugin(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: %s plugin <command> [--no-restart]\n", brand.BinaryName)
		fmt.Fprintln(os.Stderr, "commands: new, install, uninstall, list, disable, enable")
		os.Exit(1)
	}

	// Extract --no-restart flag from anywhere in the args.
	var filtered []string
	for _, a := range args {
		if a == "--no-restart" {
			noRestart = true
		} else {
			filtered = append(filtered, a)
		}
	}
	args = filtered

	switch args[0] {
	case "new":
		pluginNew(args[1:])
		return
	case "install":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: " + brand.BinaryName + " plugin install <name>")
			os.Exit(1)
		}
		pluginInstall(args[1])
	case "uninstall":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: " + brand.BinaryName + " plugin uninstall <name>")
			os.Exit(1)
		}
		pluginUninstall(args[1])
	case "list":
		pluginList()
	case "disable":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: " + brand.BinaryName + " plugin disable <name>")
			os.Exit(1)
		}
		pluginDisable(args[1])
	case "enable":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: " + brand.BinaryName + " plugin enable <name>")
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

func resolveDBPath() string {
	if d := os.Getenv(brand.Env("DB")); d != "" {
		return d
	}
	return "./" + brand.DefaultDBName
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

func pluginInstall(name string) {
	dir := resolvePluginsDir()
	target := filepath.Join(dir, name)

	// Check if already installed
	if _, err := os.Stat(filepath.Join(target, "plugin.yaml")); err == nil {
		fmt.Printf("Plugin %q is already installed at %s\n", name, target)
		os.Exit(1)
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
	s, err := store.New(dbPath)
	if err != nil {
		return nil, fmt.Errorf("open database %s: %w", dbPath, err)
	}
	return plugin.NewHostWithStore(s), nil
}

// pluginNew scaffolds a new plugin using embedded templates.
func pluginNew(args []string) {
	// Parse flags
	var (
		withAgent    bool
		envelopeType string
		crudResource string
		description  string
	)

	var positional []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--with-agent":
			withAgent = true
		case "--with-envelope":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "error: --with-envelope requires a value")
				os.Exit(1)
			}
			envelopeType = args[i]
		case "--with-crud":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "error: --with-crud requires a value")
				os.Exit(1)
			}
			crudResource = args[i]
		case "--description":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "error: --description requires a value")
				os.Exit(1)
			}
			description = args[i]
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

	if len(positional) < 1 {
		fmt.Fprintf(os.Stderr, "usage: %s plugin new <name> [flags]\n", brand.BinaryName)
		fmt.Fprintf(os.Stderr, "Run '%s plugin new --help' for details.\n", brand.BinaryName)
		os.Exit(1)
	}

	name := positional[0]

	opts := scaffold.Options{
		Name:        name,
		Description: description,
		WithAgent:   withAgent,
		OutputDir:   filepath.Join(resolvePluginsDir(), name),
	}

	if envelopeType != "" {
		opts.Envelopes = []scaffold.EnvelopeDef{scaffold.ToEnvelopeDef(envelopeType)}
	}

	if crudResource != "" {
		opts.CRUDResources = []string{crudResource}
	}

	fmt.Printf("Scaffolding plugin %q in %s...\n", name, opts.OutputDir)

	if err := scaffold.Run(opts); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Print summary
	fmt.Println()
	fmt.Printf("Plugin %q created successfully!\n", name)
	fmt.Println()
	fmt.Println("Generated files:")
	fmt.Printf("  %s/plugin.yaml\n", opts.OutputDir)
	fmt.Printf("  %s/plugin.go\n", opts.OutputDir)
	fmt.Printf("  %s/README.md\n", opts.OutputDir)
	if withAgent {
		fmt.Printf("  %s/agents/%s.yaml\n", opts.OutputDir, name)
	}
	if envelopeType != "" {
		env := scaffold.ToEnvelopeDef(envelopeType)
		fmt.Printf("  %s/ui/%s.tsx\n", opts.OutputDir, env.Export)
	}

	fmt.Println()
	fmt.Println("Next steps:")
	pkgName := strings.ReplaceAll(strings.ReplaceAll(name, "-", ""), "_", "")
	fmt.Printf("  1. Copy Go source to internal/plugin/builtin/%s/\n", pkgName)
	fmt.Printf("  2. Add import to internal/plugin/allplugins/allplugins.go:\n")
	fmt.Printf("     _ \"github.com/hollis-labs/nanite/internal/plugin/builtin/%s\"\n", pkgName)
	fmt.Printf("  3. Rebuild: go install ./cmd/%s/\n", brand.BinaryName)
	fmt.Printf("  4. Restart: cerberus restart %s\n", brand.ServiceName)
}

func pluginNewHelp() {
	fmt.Printf("Usage: %s plugin new <name> [flags]\n", brand.BinaryName)
	fmt.Println()
	fmt.Printf("Scaffold a new %s plugin with boilerplate files.\n", brand.Name)
	fmt.Println()
	fmt.Println("Flags:")
	fmt.Printf("  --description <desc>      Plugin description (default: \"A %s plugin\")\n", brand.Name)
	fmt.Println("  --with-agent              Generate an agent profile in agents/<name>.yaml")
	fmt.Println("  --with-envelope <type>    Generate a React envelope component (e.g. card, form)")
	fmt.Println("  --with-crud <resource>    Generate CRUD handler boilerplate in plugin.go")
	fmt.Println("  -h, --help                Show this help message")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Printf("  %s plugin new my-plugin\n", brand.BinaryName)
	fmt.Printf("  %s plugin new my-plugin --with-agent --description \"My awesome plugin\"\n", brand.BinaryName)
	fmt.Printf("  %s plugin new my-plugin --with-envelope card --with-crud items\n", brand.BinaryName)
}
