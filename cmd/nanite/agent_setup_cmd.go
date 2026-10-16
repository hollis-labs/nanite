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

	"github.com/hollis-labs/nanite/internal/agentdefs"
	"github.com/hollis-labs/nanite/internal/agentpolicy"
	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/providerkeys"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/substrate/mesh/agentdef"
)

const agentSetupUsage = "usage: nanite agent setup <definition.md|archetype-id> --provider P [--model M] [--runtime api|cli] [--tools all|a,b] [--slug S] [--title T] [--directories d1,d2] [--resources manifest.json] [--reason R]"

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
	runtime := fs.String("runtime", "api", "api (native provider) or cli (a local CLI agent such as claude)")
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
	data, archetypeResources, err := loadSetupDefinition(positional[0])
	if err != nil {
		return err
	}
	parsed, err := agentdef.Parse(data, agentpolicy.Option())
	if err != nil {
		return fmt.Errorf("definition is not valid: %w", err)
	}

	// --- preflight: nothing below writes ---------------------------------
	switch *runtime {
	case "api":
	case "cli":
	default:
		return fmt.Errorf("runtime %q is not supported; use --runtime api or cli", *runtime)
	}
	if *runtime == "api" {
		if *provider == "" || *model == "" {
			return errors.New("--provider and --model are required for runtime api\n" + agentSetupUsage)
		}
		if err = preflightProviderKey(ctx, keys, *provider); err != nil {
			return err
		}
	} else if *provider == "" {
		return errors.New("--provider is required for runtime cli (for example claude); --model is optional\n" + agentSetupUsage)
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
	resources := archetypeResources
	if *resourcePath != "" {
		if resources, err = readResourceManifest(*resourcePath); err != nil {
			return err
		}
	}
	// The slug names a directory below: validate it before any path is joined.
	name := *slug
	if name == "" {
		name = store.AgentSetupSlug(parsed.DefinitionID)
	}
	if err = store.ValidateHostSlug(name); err != nil {
		return err
	}
	// Refuse a definition the native runtime would refuse at launch, so setup
	// never returns a session body that cannot be admitted.
	byURI := map[string][]byte{}
	for _, r := range resources {
		byURI[r.URI] = r.Content
	}
	digest, err := agentdef.Digest(parsed)
	if err != nil {
		return err
	}
	chatCfg, err := service.MapChatDefinition(service.VerifiedDefinition{
		Ref:        service.DefinitionRef{DefinitionID: parsed.DefinitionID, Revision: parsed.Revision, SemanticDigest: digest},
		Definition: parsed,
		ReadResource: func(_ context.Context, ref agentdef.Ref) ([]byte, error) {
			if body, ok := byURI[ref.URI]; ok {
				return body, nil
			}
			return nil, fmt.Errorf("resource %q was not supplied; pass --resources", ref.URI)
		},
	})
	if err != nil {
		return fmt.Errorf("this definition cannot run on native chat: %w", err)
	}

	dirs := splitTools(*dirsFlag)
	if len(dirs) == 0 {
		// An agent with no directories cannot run, so give each its own
		// workspace beside the database rather than the server's cwd.
		dirs = []string{filepath.Join(workspaceRoot, name)}
	}
	for i, d := range dirs {
		abs, absErr := filepath.Abs(d)
		if absErr != nil {
			return absErr
		}
		dirs[i] = abs
	}
	if *runtime == "cli" {
		// The same admission session creation applies, before any write.
		if err = service.AdmitCLIHost(store.NativeHostSettings{Version: "1", Runtime: "cli", Provider: *provider, Model: *model, Directories: dirs}, chatCfg.PermissionProfile); err != nil {
			return fmt.Errorf("this agent cannot run as a CLI agent: %w", err)
		}
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
		"tools_added":      res.Added,
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

// loadSetupDefinition reads the definition setup installs: an authored file if
// the argument names one, else the id or name of an embedded archetype (with
// the resources it pins).
func loadSetupDefinition(arg string) ([]byte, []store.DefinitionResource, error) {
	if _, statErr := os.Stat(arg); statErr == nil {
		data, err := readAuthoredDefinitionFile(arg)
		return data, nil, err
	}
	catalog, err := agentdefs.Catalog()
	if err != nil {
		return nil, nil, err
	}
	var ids []string
	for _, a := range catalog {
		d, parseErr := agentdef.Parse(a.Data, agentpolicy.Option())
		if parseErr != nil {
			continue
		}
		ids = append(ids, d.DefinitionID)
		if d.DefinitionID == arg || d.Name == arg {
			var resources []store.DefinitionResource
			for _, r := range a.Resources {
				resources = append(resources, store.DefinitionResource{URI: r.URI, Content: r.Content})
			}
			return a.Data, resources, nil
		}
	}
	sort.Strings(ids)
	return nil, nil, fmt.Errorf("%q is neither a definition file nor an embedded archetype; archetypes: %s", arg, strings.Join(ids, ", "))
}
