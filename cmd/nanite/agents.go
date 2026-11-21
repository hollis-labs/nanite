package main

import (
	"fmt"
	"strings"

	"github.com/hollis-labs/nanite/registryagent"
	"github.com/hollis-labs/nanite/toolregistry"
	"github.com/spf13/cobra"
)

func agentsCommand() *cobra.Command {
	var registryDir, authority string
	var force bool
	command := &cobra.Command{Use: "agents", Short: "Materialize Registry definitions into local agent folders"}
	materialize := &cobra.Command{Use: "materialize <agent:ref> <dir>", Short: "Stage and validate a local folder; never fetch Registry records at runtime", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		if registryDir == "" {
			return fmt.Errorf("registry adapter not configured; --registry-dir is development-only")
		}
		id, ok := strings.CutPrefix(args[0], "agent:")
		if !ok || id == "" {
			return fmt.Errorf("expected agent:<native-id>")
		}
		client := toolregistry.LocalClient(registryDir)
		result, err := registryagent.Materialize(cmd.Context(), registryagent.Local{Dir: registryDir}, registryagent.Reference{Authority: authority, Kind: "agent", ID: id}, args[1], registryagent.Options{Force: force, Tools: &client})
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "materialized=%t directory=%s backup=%s\n", result.Changed, args[1], result.Backup)
		return err
	}}
	materialize.Flags().StringVar(&registryDir, "registry-dir", "", "Development-only filesystem fake registry (records.json; no authentication)")
	materialize.Flags().StringVar(&authority, "authority", "registry", "Authority of the native agent reference")
	materialize.Flags().BoolVar(&force, "force", false, "Replace an unmanaged/offline folder, retaining its previous contents as a backup; stop its runtime first")
	command.AddCommand(materialize)
	return command
}
