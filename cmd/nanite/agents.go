package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/hollis-labs/nanite/registryagent"
	"github.com/hollis-labs/nanite/toolregistry"
	"github.com/spf13/cobra"
)

func agentsCommand() *cobra.Command {
	var registryDir, authority, registryURL, tokenFile, publication, casterExecutable string
	var force bool
	command := &cobra.Command{Use: "agents", Short: "Materialize Registry definitions into local agent folders"}
	materialize := &cobra.Command{Use: "materialize <agent:ref> <dir>", Short: "Stage and validate a local folder; never fetch Registry records at runtime", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		// Explicit local transport selection overrides live environment defaults.
		if cmd.Flags().Changed("registry-dir") && !cmd.Flags().Changed("registry-url") {
			registryURL = ""
			if !cmd.Flags().Changed("registry-token-file") {
				tokenFile = ""
			}
			if !cmd.Flags().Changed("caster-executable") {
				casterExecutable = ""
			}
			if !cmd.Flags().Changed("publication") {
				publication = "published"
			}
		}
		if (registryDir == "") == (registryURL == "") {
			return fmt.Errorf("configure exactly one of --registry-dir or --registry-url")
		}
		var resolver registryagent.Resolver
		var client toolregistry.Client
		if registryDir != "" {
			if tokenFile != "" || casterExecutable != "" || publication != "published" {
				return fmt.Errorf("live Registry options cannot be used with --registry-dir")
			}
			resolver = registryagent.Local{Dir: registryDir}
			client = toolregistry.LocalClient(registryDir)
		} else {
			config := registryagent.LiveConfig{URL: registryURL, Authority: authority, Publication: publication}
			if tokenFile != "" {
				config.Token = registryTokenFile(tokenFile)
			}
			live, err := registryagent.NewLive(config)
			if err != nil {
				return err
			}
			resolver = live
			client = live.ToolClient(registryagent.CasterCommand{Executable: casterExecutable})
		}
		id, ok := strings.CutPrefix(args[0], "agent:")
		if !ok || id == "" {
			return fmt.Errorf("expected agent:<native-id>")
		}
		result, err := registryagent.Materialize(cmd.Context(), resolver, registryagent.Reference{Authority: authority, Kind: "agent", ID: id}, args[1], registryagent.Options{Force: force, Tools: &client})
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "materialized=%t directory=%s backup=%s\n", result.Changed, args[1], result.Backup)
		return err
	}}
	materialize.Flags().StringVar(&registryURL, "registry-url", os.Getenv("NANITE_REGISTRY_URL"), "Registry HTTP API base URL")
	materialize.Flags().StringVar(&tokenFile, "registry-token-file", os.Getenv("NANITE_REGISTRY_TOKEN_FILE"), "File containing the Registry reader bearer token")
	defaultPublication := os.Getenv("NANITE_REGISTRY_PUBLICATION")
	if defaultPublication == "" {
		defaultPublication = "published"
	}
	materialize.Flags().StringVar(&publication, "publication", defaultPublication, "Registry publication: published or draft (explicit; no fallback)")
	materialize.Flags().StringVar(&casterExecutable, "caster-executable", os.Getenv("NANITE_CASTER_EXECUTABLE"), "Absolute path to an already paired Caster CLI, for tool-package bytes")
	materialize.Flags().StringVar(&registryDir, "registry-dir", "", "Development-only filesystem fake registry (records.json; no authentication)")
	materialize.Flags().StringVar(&authority, "authority", "registry", "Authority of the native agent reference")
	materialize.Flags().BoolVar(&force, "force", false, "Permit replacing an unmanaged folder; preserve runtime state and refuse open runtimes")
	command.AddCommand(materialize)
	return command
}

// Resolve lazily, bound the read, and keep file paths and contents out of errors.
func registryTokenFile(path string) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		f, err := os.Open(path) // #nosec G304 -- credential file is explicitly selected by the host.
		if err != nil {
			return "", fmt.Errorf("registry credential file unavailable")
		}
		defer func() { _ = f.Close() }()
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return "", fmt.Errorf("registry credential must be a regular file")
		}
		if info.Mode().Perm()&0o077 != 0 {
			return "", fmt.Errorf("registry credential file must be private")
		}
		b, err := io.ReadAll(io.LimitReader(f, 16385))
		if err != nil || len(b) > 16384 {
			return "", fmt.Errorf("registry credential file unreadable or too large")
		}
		value := strings.TrimSpace(string(b))
		if value == "" {
			return "", fmt.Errorf("registry credential file is empty")
		}
		return value, nil
	}
}
