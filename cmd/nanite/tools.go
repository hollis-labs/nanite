package main

import (
	"fmt"
	"os"

	"github.com/hollis-labs/nanite/toolregistry"
	"github.com/spf13/cobra"
)

func toolsCommand(dir string) *cobra.Command {
	var registryDir string
	command := &cobra.Command{Use: "tools", Short: "Publish or install pinned tool packages"}
	command.PersistentFlags().StringVar(&registryDir, "registry-dir", "", "Development-only filesystem fake registry (no authentication; not the shared service)")
	command.AddCommand(&cobra.Command{Use: "publish <name>", Short: "Publish a local package in this agent's Tether ID namespace (TETHER_AGENT_URN)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if registryDir == "" {
			return fmt.Errorf("registry adapter not configured; --registry-dir is development-only")
		}
		r, err := toolregistry.Publish(cmd.Context(), toolregistry.LocalClient(registryDir), dir, args[0], os.Getenv("TETHER_AGENT_URN"))
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", r.Ref(), r.Digest)
		return err
	}}, &cobra.Command{Use: "install <ref>", Short: "Install a package and pin its digest without granting execution approval", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if registryDir == "" {
			return fmt.Errorf("registry adapter not configured; --registry-dir is development-only")
		}
		r, err := toolregistry.Install(cmd.Context(), toolregistry.LocalClient(registryDir), dir, args[0])
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Installed %s %s; execution still requires package approval or AutoApprove.\n", r.Ref(), r.Digest)
		return err
	}})
	return command
}
