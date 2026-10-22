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
	"strings"

	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/providerkeys"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
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
	provider := fs.String("provider", "", "provider name (api: anthropic, openai; cli: claude, codex)")
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
	// --- flag-level checks; the service checks the rest before writing -----
	switch *runtime {
	case "api":
		if *provider == "" || *model == "" {
			return errors.New("--provider and --model are required for runtime api\n" + agentSetupUsage)
		}
	case "cli":
		if *provider == "" {
			return errors.New("--provider is required for runtime cli (for example claude); --model is optional\n" + agentSetupUsage)
		}
	default:
		return fmt.Errorf("runtime %q is not supported; use --runtime api or cli", *runtime)
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
	dirs := splitTools(*dirsFlag)
	for i, d := range dirs {
		abs, absErr := filepath.Abs(d)
		if absErr != nil {
			return absErr
		}
		dirs[i] = abs
	}
	svc := &service.AgentSetupService{Store: st, Keys: keys, WorkspaceRoot: workspaceRoot}
	res, err := svc.Setup(ctx, service.AgentSetupRequest{Definition: data, Resources: resources, Slug: *slug, Title: *title, Directories: dirs,
		Provider: *provider, Model: *model, Runtime: *runtime, Tools: requested, Reason: *reason}, setupCLIActor)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(res)
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
	return service.ArchetypeDefinition(arg)
}
