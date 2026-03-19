package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	fplugin "github.com/hollis-labs/fragments-engine/plugin"

	"github.com/hollis-labs/conduit/internal/plugin"
	"github.com/hollis-labs/conduit/internal/store"
)

const pluginGitOrg = "hollis-labs"

// noRestart is set via the --no-restart flag to skip auto-restart after
// install/uninstall/disable/enable operations.
var noRestart bool

func cmdPlugin(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: conduit plugin <command> [--no-restart]")
		fmt.Fprintln(os.Stderr, "commands: install, uninstall, list, disable, enable")
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
	case "install":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: conduit plugin install <name>")
			os.Exit(1)
		}
		pluginInstall(args[1])
	case "uninstall":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: conduit plugin uninstall <name>")
			os.Exit(1)
		}
		pluginUninstall(args[1])
	case "list":
		pluginList()
	case "disable":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: conduit plugin disable <name>")
			os.Exit(1)
		}
		pluginDisable(args[1])
	case "enable":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: conduit plugin enable <name>")
			os.Exit(1)
		}
		pluginEnable(args[1])
	default:
		fmt.Fprintf(os.Stderr, "unknown plugin command: %s\n", args[0])
		os.Exit(1)
	}
}

func resolvePluginsDir() string {
	if d := os.Getenv("CONDUIT_PLUGINS_DIR"); d != "" {
		return d
	}
	return "./plugins"
}

func resolveDBPath() string {
	if d := os.Getenv("CONDUIT_DB"); d != "" {
		return d
	}
	return "./conduit.db"
}

// triggerRestart attempts to restart conduit-api via cerberus.
// If cerberus is unavailable, prints a manual restart message.
func triggerRestart() {
	if noRestart {
		return
	}
	fmt.Println("Restarting conduit-api...")
	cmd := exec.Command("cerberus", "restart", "conduit-api")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Println("  Could not auto-restart (cerberus not available).")
		fmt.Println("  Restart Conduit manually: cerberus restart conduit-api")
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
		log.Fatalf("read plugins dir: %v", err)
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
