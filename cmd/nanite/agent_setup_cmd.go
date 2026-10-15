package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hollis-labs/nanite/internal/agentpolicy"
	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/providerkeys"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/substrate/mesh/agentdef"
)

const agentSetupUsage = "usage: nanite agent setup <definition.md> --provider P --model M [--runtime api] [--tools all|a,b] [--slug S] [--title T] [--directories d1,d2] [--resources manifest.json] [--reason R]"

// setupCLIActor is the accountable actor recorded for authority created from
// the offline CLI: whoever can write the database is the operator.
const setupCLIActor = "cli:agent-setup"

// runAgentSetup is `nanite agent setup`: install the definition, create host
// settings, review them and grant tools in one transaction, after checking
// every precondition that can be checked without writing.
func runAgentSetup(ctx context.Context, st *store.Store, keys *providerkeys.Manager, workspaceRoot string, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("agent setup", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	provider := fs.String("provider", "", "provider name (anthropic, openai)")
	model := fs.String("model", "", "model id")
	runtime := fs.String("runtime", "api", "api (cli arrives with slice B)")
	toolsFlag := fs.String("tools", "", "comma-separated tool names, or all")
	slug := fs.String("slug", "", "host settings slug (default: from the definition id)")
	dirsFlag := fs.String("directories", "", "comma-separated directories the agent may work in (default: a private workspace beside the database)")
	title := fs.String("title", "", "host settings title (default: the definition name)")
	resourcePath := fs.String("resources", "", "explicit URI-to-local-file JSON manifest")
	reason := fs.String("reason", "", "why these tools are granted (recorded)")
	// Flags may follow the definition path.
	var positional []string
	rest := args
	for len(rest) > 0 {
		if err := fs.Parse(rest); err != nil {
			return fmt.Errorf("%w\n%s", err, agentSetupUsage)
		}
		if fs.NArg() == 0 {
			break
		}
		positional = append(positional, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	if len(positional) != 1 {
		return errors.New("one authored definition file is required\n" + agentSetupUsage)
	}
	data, err := readAuthoredDefinitionFile(positional[0])
	if err != nil {
		return err
	}
	if _, err = agentdef.Parse(data, agentpolicy.Option()); err != nil {
		return fmt.Errorf("definition is not valid: %w", err)
	}

	// --- preflight: nothing below writes ---------------------------------
	switch *runtime {
	case "api":
	case "cli":
		return errors.New("runtime cli is not supported by setup yet; use --runtime api")
	default:
		return fmt.Errorf("runtime %q is not supported; use --runtime api", *runtime)
	}
	if *provider == "" || *model == "" {
		return errors.New("--provider and --model are required for runtime api\n" + agentSetupUsage)
	}
	if err = preflightProviderKey(ctx, keys, *provider); err != nil {
		return err
	}
	requested := splitTools(*toolsFlag)
	if len(requested) == 0 {
		return errors.New("--tools is required: name the tools to grant (see `" + brand.BinaryName + " authority tools`) or pass --tools all\n" + agentSetupUsage)
	}
	for i, t := range requested {
		if strings.EqualFold(t, "all") {
			requested[i] = store.AllAvailableTools
		}
	}
	var resources []store.DefinitionResource
	if *resourcePath != "" {
		if resources, err = readResourceManifest(*resourcePath); err != nil {
			return err
		}
	}

	dirs := splitTools(*dirsFlag)
	if len(dirs) == 0 {
		// An agent with no directories cannot run, so give each its own
		// workspace beside the database rather than the server's cwd.
		name := *slug
		if name == "" {
			d, _ := agentdef.Parse(data, agentpolicy.Option())
			name = store.AgentSetupSlug(d.DefinitionID)
		}
		dirs = []string{filepath.Join(workspaceRoot, name)}
	}
	for i, d := range dirs {
		abs, absErr := filepath.Abs(d)
		if absErr != nil {
			return absErr
		}
		dirs[i] = abs
	}
	for _, d := range dirs {
		if err = os.MkdirAll(d, 0o700); err != nil {
			return fmt.Errorf("create agent directory %s: %w", d, err)
		}
	}

	res, err := st.SetupAgent(ctx, store.AgentSetup{Definition: data, Resources: resources, Slug: *slug, Title: *title, Directories: dirs,
		Provider: *provider, Model: *model, Runtime: *runtime, Tools: requested, GrantedBy: setupCLIActor, Reason: *reason})
	if err != nil {
		return err
	}
	ref := service.DefinitionRef{DefinitionID: res.DefinitionRef.ID, Revision: res.DefinitionRef.Revision, SemanticDigest: res.DefinitionRef.Digest}
	hostRef := service.HostSettingsRef{ID: res.HostSettings.ID, Revision: res.HostSettings.Revision}
	return json.NewEncoder(out).Encode(map[string]any{
		"created":          res.Created,
		"definition_ref":   ref,
		"host_settings":    hostRef,
		"host_settings_id": res.HostSettings.ID,
		"slug":             res.HostSettings.Slug,
		"authority_id":     res.AuthorityID,
		"tools":            res.Tools,
		"directories":      res.HostSettings.Settings.Directories,
		"session_request": map[string]any{
			"method": "POST", "path": "/api/agent/v1/sessions",
			"body": map[string]any{"definition_ref": ref, "host_settings": hostRef},
		},
	})
}

// preflightProviderKey refuses when the provider is unknown or has no usable
// key, naming the remedy. It never reads or prints the key itself.
func preflightProviderKey(ctx context.Context, keys *providerkeys.Manager, name string) error {
	var names []string
	for _, spec := range service.APIProviderSpecs() {
		names = append(names, spec.Name)
		if spec.Name != name {
			continue
		}
		if keys == nil {
			return nil
		}
		st := keys.Status(ctx, spec.ProviderID)
		if st.Usable {
			return nil
		}
		return fmt.Errorf("provider %q has no usable API key (%s): %s\nsave one with: pbpaste | %s provider set-key %s", name, st.Code, st.Guidance, brand.BinaryName, name)
	}
	sort.Strings(names)
	return fmt.Errorf("provider %q is not a native API provider; choose one of: %s", name, strings.Join(names, ", "))
}

func splitTools(v string) []string {
	var out []string
	for _, t := range strings.Split(v, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}
