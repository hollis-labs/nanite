package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/hollis-labs/go-providers/provider"
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
func configuredNativeModel(st *store.Store, registry *provider.Registry) ModelAuthorizer {
	return ModelAuthorizerFunc(func(ctx context.Context, _ DefinitionRef, requested *ModelSelection) (ModelSelection, error) {
		p, m, err := st.ResolveProviderAndModel(ctx, "", "")
		if err != nil {
			return ModelSelection{}, fmt.Errorf("%w: configure the host default provider and model", ErrUnsupportedModel)
		}
		configured := ModelSelection{p, m}
		if _, ok := registry.Get(p); !ok || strings.TrimSpace(m) == "" {
			return ModelSelection{}, fmt.Errorf("%w: host default must be a registered native provider with a model", ErrUnsupportedModel)
		}
		if requested != nil && *requested != configured {
			return ModelSelection{}, ErrUnsupportedModel
		}
		return configured, nil
	})
}

type CognitiveViews struct {
	Store                *store.Store
	Resolver             DefinitionResolver
	Models               ModelAuthorizer
	DefaultDefinitionRef DefinitionRef
}
type CreateDefinedView struct {
	DefinitionRef              DefinitionRef
	ModelSelection             *ModelSelection
	ProjectID, Title, Metadata string
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
	cfg, err := MapChatDefinition(verified)
	if err != nil {
		return nil, err
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
	// The normal native host profile supplies tool grants. No definition or
	// request selects, creates or modifies an operator-managed profile.
	if err = v.Store.CreateDefinedCognitiveSession(ctx, view, "", store.CognitiveViewRecord{DefinitionRefJSON: string(refJSON), ChatConfigJSON: string(cfgJSON)}); err != nil {
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
