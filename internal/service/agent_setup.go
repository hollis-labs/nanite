package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hollis-labs/libs/mesh/core/agentdef"
	"github.com/hollis-labs/nanite/internal/agentdefs"
	"github.com/hollis-labs/nanite/internal/agentpolicy"
	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/providerkeys"
	"github.com/hollis-labs/nanite/internal/store"
)

// ErrAgentSetupInvalid marks a setup request refused by a precondition check
// before anything was written.
var ErrAgentSetupInvalid = errors.New("agent setup request is invalid")

func setupInvalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrAgentSetupInvalid, fmt.Sprintf(format, args...))
}

// AgentSetupService is the one place an agent is made launchable in a single
// all-or-nothing step: `nanite agent setup` and POST /api/agent-setup both call
// it, so they cannot drift.
type AgentSetupService struct {
	Store *store.Store
	Keys  *providerkeys.Manager
	// WorkspaceRoot holds one private directory per agent when the request names
	// none. Empty means beside the database.
	WorkspaceRoot string
	// ConfineDirectories restricts requested directories to the workspace root.
	// Set it for remote callers; the local CLI names any directory it likes.
	ConfineDirectories bool
}

// DirectoryRoot is where agent workspaces live, and where a confined request
// may place its directories.
func (s *AgentSetupService) DirectoryRoot(ctx context.Context) string {
	if s.WorkspaceRoot != "" {
		return s.WorkspaceRoot
	}
	return filepath.Join(filepath.Dir(s.Store.DBPath(ctx)), "agent-workspaces")
}

// AgentSetupRequest is one operator request. Exactly one of Definition and
// Archetype names the definition to install.
type AgentSetupRequest struct {
	Definition  []byte
	Archetype   string // id or name of an embedded archetype
	Resources   []store.DefinitionResource
	Slug        string
	Title       string
	Provider    string
	Model       string
	Runtime     string // "api" (default) or "cli"
	Directories []string
	Tools       []string // tool names, or store.AllAvailableTools
	Reason      string
}

// AgentSetupResponse is the setup outcome and the body to start a session.
type AgentSetupResponse struct {
	Created        bool            `json:"created"`
	DefinitionRef  DefinitionRef   `json:"definition_ref"`
	HostSettings   HostSettingsRef `json:"host_settings"`
	HostSettingsID string          `json:"host_settings_id"`
	Slug           string          `json:"slug"`
	AuthorityID    string          `json:"authority_id"`
	Tools          []string        `json:"tools"`
	ToolsAdded     []string        `json:"tools_added"`
	Directories    []string        `json:"directories"`
	SessionRequest map[string]any  `json:"session_request"`
}

// Setup checks every precondition that can be checked without writing, then
// installs, configures and authorizes the agent in one transaction. grantedBy
// is the accountable actor recorded on the authority.
func (s *AgentSetupService) Setup(ctx context.Context, req AgentSetupRequest, grantedBy string) (AgentSetupResponse, error) {
	var out AgentSetupResponse
	data, resources, err := s.definition(req)
	if err != nil {
		return out, err
	}
	parsed, err := agentdef.Parse(data, agentpolicy.Option())
	if err != nil {
		return out, setupInvalid("definition is not valid: %v", err)
	}
	runtime := req.Runtime
	if runtime == "" {
		runtime = "api"
	}
	switch runtime {
	case "api":
		if req.Provider == "" || req.Model == "" {
			return out, setupInvalid("provider and model are required for runtime api")
		}
		if err = s.preflightProviderKey(ctx, req.Provider); err != nil {
			return out, err
		}
	case "cli":
		if req.Provider == "" {
			return out, setupInvalid("provider is required for runtime cli (for example claude); model is optional")
		}
	default:
		return out, setupInvalid("runtime %q is not supported; use api or cli", runtime)
	}
	if len(req.Tools) == 0 {
		return out, setupInvalid("tools is required: name the tools to grant (see GET /api/native-authority/tools) or pass [\"*\"] for all")
	}
	name := req.Slug
	if name == "" {
		name = store.AgentSetupSlug(parsed.DefinitionID)
	}
	if err = store.ValidateHostSlug(name); err != nil {
		return out, fmt.Errorf("%w: %w", ErrAgentSetupInvalid, err)
	}
	// Refuse a definition the native runtime would refuse at launch, so setup
	// never returns a session body that cannot be admitted.
	byURI := map[string][]byte{}
	for _, r := range resources {
		byURI[r.URI] = r.Content
	}
	digest, err := agentdef.Digest(parsed)
	if err != nil {
		return out, err
	}
	chatCfg, err := MapChatDefinition(VerifiedDefinition{
		Ref:        DefinitionRef{DefinitionID: parsed.DefinitionID, Revision: parsed.Revision, SemanticDigest: digest},
		Definition: parsed,
		ReadResource: func(_ context.Context, ref agentdef.Ref) ([]byte, error) {
			if body, ok := byURI[ref.URI]; ok {
				return body, nil
			}
			return nil, fmt.Errorf("resource %q was not supplied", ref.URI)
		},
	})
	if err != nil {
		return out, setupInvalid("this definition cannot run on native chat: %v", err)
	}
	dirs := append([]string(nil), req.Directories...)
	if len(dirs) == 0 {
		// An agent with no directories cannot run, so give each its own
		// workspace beside the database rather than the server's cwd.
		dirs = []string{filepath.Join(s.DirectoryRoot(ctx), name)}
	}
	for i, d := range dirs {
		if !filepath.IsAbs(d) {
			return out, setupInvalid("directory %q must be an absolute path", d)
		}
		dirs[i] = filepath.Clean(d)
		if s.ConfineDirectories {
			if err = confineDirectory(s.DirectoryRoot(ctx), dirs[i]); err != nil {
				return out, err
			}
		}
	}
	if runtime == "cli" {
		if err = AdmitCLIHost(store.NativeHostSettings{Version: "1", Runtime: "cli", Provider: req.Provider, Model: req.Model, Directories: dirs}, chatCfg.PermissionProfile); err != nil {
			return out, setupInvalid("this agent cannot run as a CLI agent: %v", err)
		}
	}
	// Refuse unknown tools before creating directories or writing rows.
	if _, err = s.Store.ResolveSetupTools(ctx, req.Tools); err != nil {
		return out, err
	}
	res, err := s.Store.SetupAgent(ctx, store.AgentSetup{Definition: data, Resources: resources, Slug: req.Slug, Title: req.Title, Directories: dirs,
		Provider: req.Provider, Model: req.Model, Runtime: runtime, Tools: req.Tools, GrantedBy: grantedBy, Reason: req.Reason})
	if err != nil {
		return out, err
	}
	// Directories are created only once the rows are committed, so a refused
	// request leaves nothing behind. If this fails the agent exists without its
	// directory; running setup again is idempotent and retries it.
	for _, d := range res.HostSettings.Settings.Directories {
		if err = os.MkdirAll(d, 0o700); err != nil {
			return out, fmt.Errorf("create agent directory %s: %w", d, err)
		}
	}
	ref := DefinitionRef{DefinitionID: res.DefinitionRef.ID, Revision: res.DefinitionRef.Revision, SemanticDigest: res.DefinitionRef.Digest}
	hostRef := HostSettingsRef{ID: res.HostSettings.ID, Revision: res.HostSettings.Revision}
	return AgentSetupResponse{
		Created: res.Created, DefinitionRef: ref, HostSettings: hostRef, HostSettingsID: res.HostSettings.ID,
		Slug: res.HostSettings.Slug, AuthorityID: res.AuthorityID, Tools: res.Tools, ToolsAdded: res.Added,
		Directories: res.HostSettings.Settings.Directories,
		SessionRequest: map[string]any{
			"method": "POST", "path": "/api/agent/v1/sessions",
			"body": map[string]any{"definition_ref": ref, "host_settings": hostRef},
		},
	}, nil
}

func (s *AgentSetupService) definition(req AgentSetupRequest) ([]byte, []store.DefinitionResource, error) {
	switch {
	case len(req.Definition) > 0 && req.Archetype != "":
		return nil, nil, setupInvalid("give either a definition or an archetype, not both")
	case len(req.Definition) > 0:
		return req.Definition, req.Resources, nil
	case req.Archetype == "":
		return nil, nil, setupInvalid("a definition or an archetype is required")
	}
	return ArchetypeDefinition(req.Archetype)
}

// ArchetypeDefinition returns an embedded archetype by definition id or name,
// with the resources it pins.
func ArchetypeDefinition(arg string) ([]byte, []store.DefinitionResource, error) {
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
	return nil, nil, setupInvalid("%q is not an embedded archetype; archetypes: %s", arg, strings.Join(ids, ", "))
}

// preflightProviderKey refuses when the provider is unknown or has no usable
// key, naming the remedy. It never reads or prints the key itself.
func (s *AgentSetupService) preflightProviderKey(ctx context.Context, name string) error {
	var names []string
	for _, spec := range APIProviderSpecs() {
		names = append(names, spec.Name)
		if spec.Name != name {
			continue
		}
		if s.Keys == nil {
			return nil
		}
		st := s.Keys.Status(ctx, spec.ProviderID)
		if st.Usable {
			return nil
		}
		return setupInvalid("provider %q has no usable API key (%s): %s\nsave one with: pbpaste | %s provider set-key %s", name, st.Code, st.Guidance, brand.BinaryName, name)
	}
	sort.Strings(names)
	return setupInvalid("provider %q is not a native API provider; choose one of: %s", name, strings.Join(names, ", "))
}

// List returns every set-up agent with its active authority.
func (s *AgentSetupService) List(ctx context.Context) ([]store.AgentView, error) {
	return s.Store.ListAgentViews(ctx)
}

// Get returns one agent by host-settings id or slug.
func (s *AgentSetupService) Get(ctx context.Context, idOrSlug string) (store.AgentView, error) {
	return s.Store.GetAgentView(ctx, idOrSlug)
}

// confineDirectory refuses a directory outside root, including one that gets
// out through a symlink in its existing part. dir is already clean and absolute.
func confineDirectory(root, dir string) error {
	root = filepath.Clean(root)
	inside := func(base, p string) bool {
		rel, err := filepath.Rel(base, p)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	if !inside(root, dir) {
		return setupInvalid("directory %q must be inside %s", dir, root)
	}
	// Resolve the deepest ancestor that exists and check where it really is.
	existing := dir
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return nil
		}
		existing = parent
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return setupInvalid("directory %q cannot be resolved", dir)
	}
	realRoot := root
	if r, rerr := filepath.EvalSymlinks(root); rerr == nil {
		realRoot = r
	} else if !os.IsNotExist(rerr) {
		return setupInvalid("directory root cannot be resolved")
	}
	if inside(root, existing) && !inside(realRoot, resolved) {
		return setupInvalid("directory %q resolves outside %s", dir, root)
	}
	return nil
}
