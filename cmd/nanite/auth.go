package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/hollis-labs/libs/agent/agentdir"
	"github.com/hollis-labs/nanite/internal/authored"
	"github.com/hollis-labs/nanite/internal/definition"
	"github.com/hollis-labs/nanite/internal/provider"
	"github.com/hollis-labs/substrate/module"
	"github.com/spf13/cobra"
)

// authStatus is `nanite auth status --json`: whether the chat model's provider key can be found,
// for a program deciding whether Nanite can run here. It never shows the key.
type authStatus struct {
	LoggedIn bool   `json:"loggedIn"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	// AuthMethod is where the key comes from (a key reference, "env:<NAME>", or "none" for a
	// provider that needs no key).
	AuthMethod string   `json:"authMethod,omitempty"`
	Gateway    string   `json:"gateway,omitempty"`
	AgentDir   string   `json:"agentDir,omitempty"`
	Tried      []string `json:"tried"`
	Error      string   `json:"error,omitempty"`
}

func authCommand(app *module.App, dir string) *cobra.Command {
	auth := &cobra.Command{Use: "auth", Short: "Provider credentials"}
	var asJSON bool
	status := &cobra.Command{Use: "status", Short: "Whether the chat model's provider key can be found (never shows it)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			gw, err := gatewaySettings(app)
			if err != nil {
				return err
			}
			st := authStatus{Provider: "anthropic", Model: firstNonEmpty(os.Getenv("NANITE_DEFAULT_MODEL"), "claude-sonnet-5-5"), Gateway: gw.BaseURL}
			var named *provider.Spec
			if _, err := agentdir.Load(dir); err == nil {
				def, err := definition.Load(dir, "-")
				if err != nil {
					return err
				}
				st.AgentDir, st.Provider, st.Model = dir, def.Runtime.Provider, def.Runtime.Model
				if sel, ok := def.Use[authored.UseChat]; ok {
					st.Provider, st.Model = firstNonEmpty(sel.Provider, st.Provider), firstNonEmpty(sel.Model, st.Model)
					if spec, ok := def.Providers[sel.Provider]; ok {
						named = &spec
					}
				}
			}
			// Never read a key here: a keychain read can raise an access dialog (keyHas).
			var ks provider.KeyStatus
			if named != nil {
				ks = provider.NamedKeyStatus(cmd.Context(), *named, keyHas(app))
				st.Gateway = named.BaseURL
			} else {
				ks = provider.KeyStatusFor(cmd.Context(), st.Provider, gw, keyHas(app))
			}
			st.LoggedIn, st.AuthMethod, st.Tried, st.Error = ks.Found, ks.Source, ks.Tried, ks.Error
			out := cmd.OutOrStdout()
			if asJSON {
				return json.NewEncoder(out).Encode(st)
			}
			if st.LoggedIn {
				_, err = fmt.Fprintf(out, "%s: key found (%s); model %s\n", st.Provider, st.AuthMethod, st.Model)
				return err
			}
			_, _ = fmt.Fprintf(out, "%s: no key found (tried %v)\n", st.Provider, st.Tried)
			return fmt.Errorf("no %s provider key: store one at keychain://nanite/provider/%s or set nanite.gateway.key_ref", st.Provider, st.Provider)
		}}
	status.Flags().BoolVar(&asJSON, "json", false, "Print JSON (exit status 0 either way; see loggedIn)")
	auth.AddCommand(status)
	return auth
}
