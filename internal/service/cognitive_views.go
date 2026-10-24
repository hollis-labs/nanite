package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/hollis-labs/libs/harness/core/adapters/provider"
	"github.com/hollis-labs/nanite/internal/agentpolicy"
	"github.com/hollis-labs/nanite/internal/harnessprofile"
	"github.com/hollis-labs/nanite/internal/providerkeys"
	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
	"github.com/hollis-labs/nanite/internal/store"
)

type ModelSelection struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

var ErrDefinedViewOperation = errors.New("defined views require native tracked operations")

type cognitiveViewReader interface {
	GetCognitiveView(context.Context, string) (store.CognitiveViewRecord, error)
}

func definedCognitiveTarget(ctx context.Context, backing any, viewID string) (bool, error) {
	reader, ok := backing.(cognitiveViewReader)
	if !ok {
		return false, nil // Legacy-only store adapters have no native view records.
	}
	_, err := reader.GetCognitiveView(ctx, viewID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func requireLegacyTarget(ctx context.Context, backing any, viewID string) error {
	defined, err := definedCognitiveTarget(ctx, backing, viewID)
	if err != nil {
		return err
	}
	if defined {
		return ErrDefinedViewOperation
	}
	return nil
}

// RequireLegacyTarget keeps retained operations from launching an untracked
// turn or replacing the verified configuration of a defined view.
func (v *CognitiveViews) RequireLegacyTarget(ctx context.Context, viewID string) error {
	return requireLegacyTarget(ctx, v.Store, viewID)
}

// ModelAuthorizer binds selection to host configuration, independently of
// definition extensions, claims and request metadata.
type ModelAuthorizer interface {
	AuthorizeModel(context.Context, DefinitionRef, *ModelSelection) (ModelSelection, error)
}
type ModelAuthorizerFunc func(context.Context, DefinitionRef, *ModelSelection) (ModelSelection, error)

func (f ModelAuthorizerFunc) AuthorizeModel(ctx context.Context, p DefinitionRef, m *ModelSelection) (ModelSelection, error) {
	return f(ctx, p, m)
}

// configuredNativeModel authorizes the selection the agent's host settings or
// the launch request carry. There is no user-level or app-level default to
// fall back on (CW-20261011-0031): a native API view without a provider and
// model is refused, naming what is missing, and a provider the host has not
// registered is refused.
//
// When the provider is one Nanite has an adapter for but is not registered,
// the refusal says why its key could not be used (not found, denied,
// unavailable) and what to do, from keys; it never reports a key.
func configuredNativeModel(registry *provider.Registry, keys *providerkeys.Manager) ModelAuthorizer {
	return ModelAuthorizerFunc(func(_ context.Context, _ DefinitionRef, requested *ModelSelection) (ModelSelection, error) {
		if requested == nil || strings.TrimSpace(requested.Provider) == "" || strings.TrimSpace(requested.Model) == "" {
			return ModelSelection{}, unsupportedModelCause(CauseModelNotSelected, "select a provider and model in the agent's host settings or the launch request")
		}
		if _, ok := registry.Get(requested.Provider); !ok {
			return ModelSelection{}, unsupportedModelCause(CauseProviderKeyUnavailable, "%s", unregisteredProviderReason(context.Background(), requested.Provider, keys))
		}
		return *requested, nil
	})
}

// unregisteredProviderReason explains why a provider is not registered. For an
// API provider with an adapter the cause is its key, reported with the typed
// code and remedy; otherwise it is the plain statement.
func unregisteredProviderReason(ctx context.Context, name string, keys *providerkeys.Manager) string {
	plain := fmt.Sprintf("provider %q is not a registered native provider", name)
	if keys == nil {
		return plain
	}
	for _, spec := range APIProviderSpecs() {
		if spec.Name != name {
			continue
		}
		st := keys.Status(ctx, spec.ProviderID)
		if st.Usable {
			return plain + " (its key resolves from " + st.Source + "; restart Nanite or save the key again to register it)"
		}
		return fmt.Sprintf("provider %q has no usable API key (%s): %s", name, st.Code, st.Guidance)
	}
	return plain
}

type CognitiveViews struct {
	Store                *store.Store
	Resolver             DefinitionResolver
	Models               ModelAuthorizer
	DefaultDefinitionRef DefinitionRef
}
type CreateDefinedView struct {
	DefinitionRef              DefinitionRef
	HostSettings               *HostSettingsRef
	ModelSelection             *ModelSelection
	ProjectID, Title, Metadata string
	// WorkRoot is where the agent works, chosen at create time, for a CLI or an
	// API host alike (an API session's file and shell tools lead with it). It must
	// be an existing directory inside one of the host settings' directories; the
	// host document, not the request, owns filesystem authority.
	WorkRoot string
}

// HostSettingsRef selects an already validated host document at one revision.
// It is neither an actor binding nor authority carried by request metadata.
type HostSettingsRef struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
}

func (v *CognitiveViews) Create(ctx context.Context, req CreateDefinedView) (*store.Session, error) {
	if err := req.DefinitionRef.Validate(); err != nil {
		return nil, err
	}
	verified, err := v.Resolver.Resolve(ctx, req.DefinitionRef)
	if err != nil {
		return nil, err
	}
	if verified.Ref != req.DefinitionRef || verified.Definition == nil || verified.Definition.DefinitionID != req.DefinitionRef.DefinitionID || verified.Definition.Revision != req.DefinitionRef.Revision {
		return nil, ErrDefinitionDigestMismatch
	}
	cfg, err := mapChatDefinition(ctx, verified)
	if err != nil {
		return nil, err
	}
	if req.HostSettings != nil {
		h, hostErr := v.Store.GetAgentHostSettings(ctx, req.HostSettings.ID)
		if hostErr != nil {
			return nil, hostErr
		}
		if req.HostSettings.Revision == "" || h.Revision != req.HostSettings.Revision {
			return nil, store.ErrAgentHostRevisionConflict
		}
		if DefinitionRefFromMesh(h.DefinitionRef) != req.DefinitionRef {
			return nil, ErrDefinitionDigestMismatch
		}
		if !h.Enabled {
			return nil, unsupportedCause(CauseHostDisabled, "host settings %q are disabled; enable them or launch against enabled settings", h.ID)
		}
		switch h.Settings.Runtime {
		case "api":
			// An api host's directories bound its file and shell tools, as a
			// CLI host's do, so a relative one is refused here and not
			// silently dropped when the session runs.
			for _, dir := range h.Settings.Directories {
				if expanded, dirErr := runtimeagent.ExpandUserHome(strings.TrimSpace(dir)); dirErr != nil || !filepath.IsAbs(expanded) {
					return nil, unsupportedCause(CauseHostDirectoryInvalid, "host settings directory %q must be an absolute path", dir)
				}
			}
			selected := ModelSelection{Provider: h.Settings.Provider, Model: h.Settings.Model}
			if req.ModelSelection != nil && *req.ModelSelection != selected {
				return nil, unsupportedModelCause(CauseModelSelectionMismatch, "the requested provider/model (%s/%s) differs from the host settings (%s/%s); omit the request selection or change the host settings", req.ModelSelection.Provider, req.ModelSelection.Model, selected.Provider, selected.Model)
			}
			req.ModelSelection = &selected
		case "cli":
			selected, cliErr := admitCLIHost(h.Settings, cfg.PermissionProfile, req.ModelSelection)
			if cliErr != nil {
				return nil, cliErr
			}
			if cliErr = v.checkHostMCPServers(ctx, h.Settings.MCPServers); cliErr != nil {
				return nil, cliErr
			}
			req.ModelSelection = &selected
		default:
			return nil, unsupportedCause(CauseRuntimeUnsupported, "host settings %q use runtime %q, which native sessions do not run; use \"api\" or \"cli\"", h.ID, h.Settings.Runtime)
		}
		cfg.HostSettings = &h.Settings
		cfg.HostSettingsRef = &HostSettingsRef{ID: h.ID, Revision: h.Revision}
		if err = v.deliverGrantedSkills(ctx, verified, h, &cfg); err != nil {
			return nil, err
		}
	}
	if req.WorkRoot != "" {
		if cfg.HostSettings == nil {
			return nil, unsupportedCause(CauseWorkRootUnsupported, "work_root needs host settings: it must lie inside the directories they grant")
		}
		root, rootErr := resolveWorkRoot(req.WorkRoot, cfg.HostSettings.Directories)
		if rootErr != nil {
			return nil, unsupportedCause(CauseWorkRootInvalid, "%v", rootErr)
		}
		cfg.WorkRoot = root
	}
	if cfg.HostSettings != nil && cfg.HostSettings.Runtime == "cli" {
		// A CLI agent's model is the CLI's own; there is no native provider
		// registry to authorize it against.
		cfg.Model = *req.ModelSelection
	} else {
		cfg.Model, err = v.Models.AuthorizeModel(ctx, req.DefinitionRef, req.ModelSelection)
		if err != nil {
			return nil, err
		}
	}
	refJSON, err := json.Marshal(req.DefinitionRef)
	if err != nil {
		return nil, err
	}
	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	view := &store.Session{ProjectID: req.ProjectID, Title: req.Title, Metadata: req.Metadata, Provider: cfg.Model.Provider, Model: cfg.Model.Model}
	// This local view is not a fabric enrollment. It borrows no profile grants.
	// Definition requests and caller metadata cannot create actor authority.
	record := store.CognitiveViewRecord{DefinitionRefJSON: string(refJSON), ChatConfigJSON: string(cfgJSON)}
	if req.HostSettings != nil {
		err = v.Store.CreateDefinedSessionWithHost(ctx, view, record, store.HostSettingsAdmission{ID: req.HostSettings.ID, Revision: req.HostSettings.Revision})
	} else {
		err = v.Store.CreateDefinedCognitiveSession(ctx, view, "", record)
	}
	if err != nil {
		return nil, err
	}
	return view, nil
}
func (v *CognitiveViews) Get(ctx context.Context, id string) (DefinitionRef, ChatDefinitionConfig, error) {
	record, err := v.Store.GetCognitiveView(ctx, id)
	if err != nil {
		return DefinitionRef{}, ChatDefinitionConfig{}, err
	}
	var ref DefinitionRef
	var cfg ChatDefinitionConfig
	if err = json.Unmarshal([]byte(record.DefinitionRefJSON), &ref); err != nil {
		return ref, cfg, err
	}
	err = json.Unmarshal([]byte(record.ChatConfigJSON), &cfg)
	return ref, cfg, err
}

func (v *CognitiveViews) LatestTurn(ctx context.Context, viewID string) (string, error) {
	return v.Store.LatestCognitiveTurn(ctx, viewID)
}

// admitCLIHost decides whether a runtime=cli host can run as a native session
// and returns the provider/model it runs on. It refuses, with a stated cause,
// anything a CLI process cannot honor: a read-only definition (the CLI has no
// enforced read-only posture), native-loop knobs (the CLI owns its loop), host
// MCP servers on a runtime whose tool grants Nanite cannot enforce (only Claude
// and Antigravity carry them), a model on a runtime whose
// model flag is unverified, and a host that grants no directory.
func admitCLIHost(h store.NativeHostSettings, permissionProfile string, requested *ModelSelection) (ModelSelection, error) {
	if h.Provider == "" {
		return ModelSelection{}, unsupportedCause(CauseProviderNotLaunchable, "host settings name no CLI provider; set provider (for example \"claude\")")
	}
	if err := runtimeagent.LaunchError(h.Provider); err != nil {
		return ModelSelection{}, unsupportedCause(CauseProviderNotLaunchable, "provider %q cannot be launched on this host: %v", h.Provider, err)
	}
	if permissionProfile != "" && permissionProfile != "default" {
		return ModelSelection{}, unsupportedCause(CauseReadOnlyUnenforceable, "the definition asks for permission profile %q, which a CLI process cannot enforce; use a definition with the default profile or runtime \"api\"", permissionProfile)
	}
	if field := cliUnsupportedField(h); field != "" {
		return ModelSelection{}, unsupportedCause(CauseCLIHostUnsupportedField, "host settings field %q does not apply to a CLI agent; remove it", field)
	}
	if len(h.MCPServers) > 0 && !runtimeagent.HostMCPSupported(h.Provider) {
		return ModelSelection{}, unsupportedCause(CauseCLIHostUnsupportedField, "host settings field \"mcp_servers\" is not supported for provider %q: Nanite cannot gate a tool call that runtime makes itself, so it will not plant the servers; use provider \"claude\" or \"antigravity\", or runtime \"api\"", h.Provider)
	}
	if len(h.Directories) == 0 {
		return ModelSelection{}, unsupportedCause(CauseHostDirectoriesMissing, "a CLI agent needs at least one directory in host settings; the first is its work root")
	}
	for _, dir := range h.Directories {
		if expanded, err := runtimeagent.ExpandUserHome(strings.TrimSpace(dir)); err != nil || !filepath.IsAbs(expanded) {
			return ModelSelection{}, unsupportedCause(CauseCLIHostUnsupportedField, "host settings directory %q must be an absolute path", dir)
		}
	}
	if h.Model != "" && !runtimeagent.ModelFlagVerified(h.Provider) {
		return ModelSelection{}, unsupportedCause(CauseCLIHostUnsupportedField, "host settings field \"model\" is not supported for provider %q yet; remove it", h.Provider)
	}
	selected := ModelSelection{Provider: h.Provider, Model: h.Model}
	if requested != nil && *requested != selected {
		return ModelSelection{}, unsupportedModelCause(CauseModelSelectionMismatch, "the requested provider/model (%s/%s) differs from the host settings (%s/%s); omit the request selection or change the host settings", requested.Provider, requested.Model, selected.Provider, selected.Model)
	}
	return selected, nil
}

// AdmitCLIHost applies session creation's CLI host admission, so callers that
// prepare host settings (agent setup) refuse exactly what a launch would.
func AdmitCLIHost(h store.NativeHostSettings, permissionProfile string) error {
	_, err := admitCLIHost(h, permissionProfile, nil)
	return err
}

// cliUnsupportedField names the first host field that is set but does not apply
// to a CLI agent, or "". Default (empty) values pass: the same host document
// shape serves both runtimes.
func cliUnsupportedField(h store.NativeHostSettings) string {
	switch {
	case h.HarnessProfile != "":
		return "harness_profile"
	case !reflect.DeepEqual(h.NativeLoop, harnessprofile.Layer{}):
		return "native_loop"
	case h.RecallLimit != nil:
		return "recall_limit"
	case h.RecallTimeoutMS != nil:
		return "recall_timeout_ms"
	case h.Protocol != "":
		return "protocol"
	case h.Transport != "":
		return "transport"
	}
	return ""
}

// resolveWorkRoot validates a requested work root against the host's allowed
// directories: absolute, an existing directory, and inside one of them after
// symlinks are resolved. It returns the resolved path.
func resolveWorkRoot(requested string, allowed []string) (string, error) {
	expanded, err := runtimeagent.ExpandUserHome(strings.TrimSpace(requested))
	if err != nil || !filepath.IsAbs(expanded) {
		return "", fmt.Errorf("work_root %q must be an absolute path", requested)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(expanded))
	if err != nil {
		return "", fmt.Errorf("work_root %q must be an existing directory", requested)
	}
	if fi, statErr := os.Stat(resolved); statErr != nil || !fi.IsDir() {
		return "", fmt.Errorf("work_root %q must be an existing directory", requested)
	}
	for _, dir := range allowed {
		base, baseErr := runtimeagent.ExpandUserHome(strings.TrimSpace(dir))
		if baseErr != nil {
			continue
		}
		base = filepath.Clean(base)
		if resolvedBase, evalErr := filepath.EvalSymlinks(base); evalErr == nil {
			base = resolvedBase
		}
		if rel, relErr := filepath.Rel(base, resolved); relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("work_root %q is outside the directories granted by the host settings", requested)
}

// checkHostMCPServers refuses, at admission, a CLI host whose mcp_servers name
// a server that launch could not project, so the cause surfaces at session
// creation and not as a failed boot.
func (v *CognitiveViews) checkHostMCPServers(ctx context.Context, names []string) error {
	for _, name := range names {
		row, err := v.Store.GetMCPServer(ctx, name)
		if err != nil {
			return err
		}
		if err := runtimeagent.ValidateHostMCPServer(name, row, "nanite"); err != nil {
			return unsupportedCause(CauseCLIHostUnsupportedField, "host settings mcp_servers: %v", err)
		}
	}
	return nil
}

// deliverGrantedSkills plants the skills the definition requests and the host
// revision's live authority has granted at the digest the definition pins, under
// agent/skills/<name>/ in the session's files, and reports them as applied. A
// skill with no grant, a grant at another digest, or no readable stored tree is
// not planted and stays unmet. Files are fixed when the session is created: a
// grant made later reaches the next session, and revoking one takes effect for
// sessions created after it.
func (v *CognitiveViews) deliverGrantedSkills(ctx context.Context, verified VerifiedDefinition, h store.AgentHostSettings, cfg *ChatDefinitionConfig) error {
	d := verified.Definition
	if d == nil || len(d.Requirements.Skills) == 0 {
		return nil
	}
	trees, err := v.Store.GrantedSkillTrees(ctx, d, h)
	if err != nil {
		return err
	}
	delivered := map[string]bool{}
	for _, name := range store.SortedKeys(trees) {
		for rel, content := range trees[name] {
			dest := path.Join(agentpolicy.FilesRoot, agentpolicy.SkillsDir, name, rel)
			if !strings.HasPrefix(dest, path.Join(agentpolicy.FilesRoot, agentpolicy.SkillsDir, name)+"/") {
				return fmt.Errorf("skill %q file %q escapes its folder", name, rel)
			}
			if cfg.Files == nil {
				cfg.Files = map[string][]byte{}
			}
			cfg.Files[dest] = content
		}
		delivered[name] = true
	}
	cfg.FieldReport = agentpolicy.ExecutionReportWith(d, delivered)
	return nil
}
