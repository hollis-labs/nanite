package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	paths "github.com/hollis-labs/libs/util/apppaths"
	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/config"
	"github.com/hollis-labs/nanite/internal/harnessprofile"
)

// buildHarnessRegistry builds the harness profile registry the server and the
// `profile` command share: the default profile name comes from the app config
// (overridden by NANITE_HARNESS_PROFILE) and user profiles from the configured
// directory, else <config-dir>/profiles.
func buildHarnessRegistry(appCfg *config.TunablesConfig, configDir string) (*harnessprofile.Registry, error) {
	name := appCfg.Harness.Profile
	if env := strings.TrimSpace(os.Getenv("NANITE_HARNESS_PROFILE")); env != "" {
		name = env
	}
	dir := appCfg.Harness.ProfilesDir
	if dir == "" {
		dir = filepath.Join(configDir, "profiles")
	}
	return harnessprofile.NewRegistry(dir, name)
}

// cmdProfile dispatches `nanite profile <list|show> ...`. It reads profile
// files directly and needs no running server.
func cmdProfile(args []string) {
	if len(args) < 1 {
		profileUsage()
		os.Exit(1)
	}
	reg, err := loadHarnessRegistry()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s profile: %v\n", brand.BinaryName, err)
		os.Exit(1)
	}
	switch args[0] {
	case "list":
		for _, n := range reg.Names() {
			marker := ""
			if n == reg.DefaultName() {
				marker = "  (default)"
			}
			fmt.Println(n + marker)
		}
	case "show":
		if err := profileShow(reg, args[1:], os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "%s profile show: %v\n", brand.BinaryName, err)
			os.Exit(1)
		}
	default:
		profileUsage()
		os.Exit(1)
	}
}

func profileUsage() {
	fmt.Fprintf(os.Stderr, "usage: %s profile <list|show> ...\n", brand.BinaryName)
	fmt.Fprintln(os.Stderr, "commands:")
	fmt.Fprintln(os.Stderr, "  list                            list selectable harness profiles")
	fmt.Fprintln(os.Stderr, "  show <name> [--model M] [--json] resolve a profile and show each value and its source")
}

func loadHarnessRegistry() (*harnessprofile.Registry, error) {
	appCfg, err := config.LoadAppConfig("config/" + brand.ConfigFileName + ".yaml")
	if err != nil {
		appCfg = config.DefaultAppConfig()
	}
	layout, err := config.ResolveLayout(paths.WithoutMaterialize())
	if err != nil {
		return nil, fmt.Errorf("resolve layout: %w", err)
	}
	return buildHarnessRegistry(appCfg, layout.ConfigDir())
}

// profileShow resolves a profile for an optional model, with the current
// environment overrides, and prints each value with the layer that supplied it.
// Layers that come from a session, its agent or the user settings are not
// present here; the session endpoint shows those.
func profileShow(reg *harnessprofile.Registry, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("profile show", flag.ContinueOnError)
	model := fs.String("model", "", "resolve per-model blocks for this model id")
	asJSON := fs.Bool("json", false, "print the recorded JSON form")
	// flags may follow the name: `show dev --model X`
	var name string
	rest := args
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		name, rest = rest[0], rest[1:]
	}
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if name == "" && fs.NArg() > 0 {
		name = fs.Arg(0)
	}
	res, err := reg.Resolve(harnessprofile.Inputs{Profile: name, Model: *model})
	if err != nil {
		return err
	}
	eff := res.Effective()
	if *asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(eff)
	}
	fmt.Fprintf(out, "profile  %s\ndigest   %s\n", eff.Profile, eff.Digest)
	if eff.Model != "" {
		fmt.Fprintf(out, "model    %s\n", eff.Model)
	}
	fmt.Fprintln(out)
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "KNOB\tVALUE\tSOURCE")
	keys := make([]string, 0, len(eff.Values))
	for k := range eff.Values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		src := eff.Sources[k]
		note := src.Layer
		if src.Clamped {
			note += fmt.Sprintf("  (clamped from %s)", src.Requested)
		}
		fmt.Fprintf(w, "%s\t%v\t%s\n", k, eff.Values[k], note)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if len(eff.Unenforced) > 0 {
		fmt.Fprintf(out, "\ncarried, not enforced by the chat loop: %s\n", strings.Join(eff.Unenforced, ", "))
	}
	return nil
}
