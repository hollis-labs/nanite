package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/hollis-labs/nanite/internal/providerkeys"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/substrate/harness/adapters/provider"
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
			return ModelSelection{}, unsupportedModelCause("model_not_selected", "select a provider and model in the agent's host settings or the launch request")
		}
		if _, ok := registry.Get(requested.Provider); !ok {
			return ModelSelection{}, unsupportedModelCause("provider_not_registered", "%s", unregisteredProviderReason(context.Background(), requested.Provider, keys))
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
			return nil, unsupportedCause("host_settings_disabled", "host settings %q are disabled; enable them or launch against enabled settings", h.ID)
		}
		if h.Settings.Runtime != "api" {
			return nil, unsupportedCause("runtime_not_api", "host settings %q use runtime %q, but native chat needs runtime \"api\"; set runtime to api on the host settings", h.ID, h.Settings.Runtime)
		}
		selected := ModelSelection{Provider: h.Settings.Provider, Model: h.Settings.Model}
		if req.ModelSelection != nil && *req.ModelSelection != selected {
			return nil, unsupportedModelCause("model_selection_mismatch", "the requested provider/model (%s/%s) differs from the host settings (%s/%s); omit the request selection or change the host settings", req.ModelSelection.Provider, req.ModelSelection.Model, selected.Provider, selected.Model)
		}
		req.ModelSelection = &selected
		cfg.HostSettings = &h.Settings
		cfg.HostSettingsRef = &HostSettingsRef{ID: h.ID, Revision: h.Revision}
	}
	cfg.Model, err = v.Models.AuthorizeModel(ctx, req.DefinitionRef, req.ModelSelection)
	if err != nil {
		return nil, err
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
