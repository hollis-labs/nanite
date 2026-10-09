package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/hollis-labs/substrate/mesh/agentdef"
)

type logicalProvisionResolver func(context.Context, DefinitionRef) (VerifiedDefinition, error)

func (f logicalProvisionResolver) Resolve(ctx context.Context, ref DefinitionRef) (VerifiedDefinition, error) {
	return f(ctx, ref)
}

func logicalProvisionHost(t *testing.T) (*CognitiveViews, VerifiedDefinition) {
	t.Helper()
	verified, err := EmbeddedDefinition()
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := NewFileDefinitionResolver("")
	if err != nil {
		t.Fatal(err)
	}
	return &CognitiveViews{Resolver: resolver, Models: ModelAuthorizerFunc(func(_ context.Context, pin DefinitionRef, requested *ModelSelection) (ModelSelection, error) {
		if pin != verified.Ref {
			return ModelSelection{}, ErrUnsupportedModel
		}
		model := ModelSelection{Provider: "fixture-provider", Model: "fixture-model"}
		if requested != nil && *requested != model {
			return ModelSelection{}, ErrUnsupportedModel
		}
		return model, nil
	})}, verified
}

func TestLogicalGeneralChatProvisionCreatesProfileNotSessionOrAuthority(t *testing.T) {
	svc, st, _ := newAgentConfigTestService(t)
	host, verified := logicalProvisionHost(t)
	result, err := svc.ProvisionGeneralChat(t.Context(), host, ProvisionGeneralChatRequest{Name: "General chat", Slug: "general-chat", DefinitionRef: verified.Ref})
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.GetAgent(t.Context(), result.Agent.Profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := MapChatDefinition(verified)
	if err != nil {
		t.Fatal(err)
	}
	if p.SystemPrompt != cfg.Instructions || p.DefaultProvider != "fixture-provider" || p.DefaultModel != "fixture-model" || p.Source != "user" || !result.Agent.Class.Editable() || p.Revision == "" {
		t.Fatal("logical profile lost verified mapped configuration")
	}
	if p.TetherManaged || p.TetherURN != "" || p.Durable || p.CanExecute || p.RoleID != "" || p.ConsumerID != "" {
		t.Fatal("provisioning invented identity/assignment/authority")
	}
	grants, err := st.ListAgentToolNames(t.Context(), p.ID)
	if err != nil || len(grants) != 0 {
		t.Fatal("provisioning granted tools", err)
	}
	var settings struct {
		DefinitionRef DefinitionRef `json:"provisioned_definition_ref"`
	}
	if err = json.Unmarshal([]byte(p.Settings), &settings); err != nil || settings.DefinitionRef != verified.Ref {
		t.Fatal("creation pin not retained", err)
	}
	var sessions int
	if err = st.DB.QueryRow(`SELECT count(*) FROM sessions`).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatal("logical provisioning created a session", err)
	}
	if _, err = svc.ProvisionGeneralChat(t.Context(), host, ProvisionGeneralChatRequest{Name: "Again", Slug: p.Slug, DefinitionRef: verified.Ref}); !errors.Is(err, ErrManagedSlugExists) {
		t.Fatal("provisioning replaced existing profile", err)
	}
}

func TestLogicalGeneralChatProvisionAppliesNanitePolicyExtensions(t *testing.T) {
	svc, st, _ := newAgentConfigTestService(t)
	host, verified := logicalProvisionHost(t)
	raw, err := json.Marshal(verified.Definition)
	if err != nil {
		t.Fatal(err)
	}
	var definition agentdef.Definition
	if err = json.Unmarshal(raw, &definition); err != nil {
		t.Fatal(err)
	}
	definition.Extensions = map[string]agentdef.Extension{
		"com.hollislabs.nanite/native-policy": {
			Version:   "1",
			Area:      "harness_profile",
			Mandatory: true,
			Data: map[string]any{"model_selection": map[string]any{
				"provider": "fixture-provider",
				"model":    "fixture-model",
			}},
		},
		"com.hollislabs.nanite/reflex-policy": {
			Version:   "1",
			Area:      "behavior",
			Mandatory: true,
			Data: map[string]any{"reflexes": []any{map[string]any{
				"name":         "agentdef-reminder",
				"trigger_kind": "predicate",
				"trigger_spec": map[string]any{"kind": "always"},
				"action_kind":  "inject_reminder",
				"action_spec":  map[string]any{"message": "stay on task"},
				"priority":     float64(12),
			}}},
		},
	}
	digest, err := agentdef.Digest(&definition)
	if err != nil {
		t.Fatal(err)
	}
	verified.Definition = &definition
	verified.Ref.SemanticDigest = digest
	host.Resolver = logicalProvisionResolver(func(context.Context, DefinitionRef) (VerifiedDefinition, error) { return verified, nil })
	host.Models = ModelAuthorizerFunc(func(_ context.Context, pin DefinitionRef, requested *ModelSelection) (ModelSelection, error) {
		model := ModelSelection{Provider: "fixture-provider", Model: "fixture-model"}
		if pin != verified.Ref || requested == nil || *requested != model {
			return ModelSelection{}, ErrUnsupportedModel
		}
		return model, nil
	})

	result, err := svc.ProvisionGeneralChat(t.Context(), host, ProvisionGeneralChatRequest{Name: "General chat", Slug: "general-chat", DefinitionRef: verified.Ref})
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.GetAgent(t.Context(), result.Agent.Profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.DefaultProvider != "fixture-provider" || p.DefaultModel != "fixture-model" {
		t.Fatalf("native-policy model not authorized/applied: %s/%s", p.DefaultProvider, p.DefaultModel)
	}
	reflexes, err := st.ListAgentReflexesForAgent(t.Context(), p.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(reflexes) != 1 || reflexes[0].Name != "agentdef-reminder" || reflexes[0].Priority != 12 || reflexes[0].CreatedBy != "operator:agentdef" {
		t.Fatalf("reflex-policy not materialized: %#v", reflexes)
	}
}

func TestLogicalGeneralChatProvisionRefusesUnappliedSemanticsAndForgedContent(t *testing.T) {
	for _, mutation := range []string{"readonly", "body-with-old-digest", "capability", "foreign-id"} {
		t.Run(mutation, func(t *testing.T) {
			svc, st, _ := newAgentConfigTestService(t)
			host, verified := logicalProvisionHost(t)
			raw, err := json.Marshal(verified.Definition)
			if err != nil {
				t.Fatal(err)
			}
			var definition agentdef.Definition
			if err = json.Unmarshal(raw, &definition); err != nil {
				t.Fatal(err)
			}
			original := verified.Ref
			switch mutation {
			case "readonly":
				definition.HarnessProfile.Permissions.Profile = "read-only"
			case "body-with-old-digest":
				definition.Body += "\nChanged content"
			case "capability":
				definition.Requirements.Tools = []string{"filesystem.write"}
			case "foreign-id":
				definition.DefinitionID = "def:foreign"
			}
			if mutation == "readonly" || mutation == "capability" {
				digest, e := agentdef.Digest(&definition)
				if e != nil {
					t.Fatal(e)
				}
				verified.Ref.SemanticDigest = digest
			}
			verified.Definition = &definition
			host.Resolver = logicalProvisionResolver(func(context.Context, DefinitionRef) (VerifiedDefinition, error) { return verified, nil })
			req := ProvisionGeneralChatRequest{Name: "Refused", Slug: "refused", DefinitionRef: verified.Ref}
			if mutation == "foreign-id" {
				req.DefinitionRef = original
			}
			_, err = svc.ProvisionGeneralChat(t.Context(), host, req)
			if err == nil {
				t.Fatal("unapplied or forged definition accepted")
			}
			var count int
			if e := st.DB.QueryRow(`SELECT count(*) FROM agent_profiles WHERE slug = 'refused'`).Scan(&count); e != nil || count != 0 {
				t.Fatal("refusal changed logical profiles", e)
			}
		})
	}
}

func TestLogicalGeneralChatProvisionRequiresExplicitPinAndHostModelAndHonorsCancel(t *testing.T) {
	svc, st, _ := newAgentConfigTestService(t)
	host, verified := logicalProvisionHost(t)
	base := ProvisionGeneralChatRequest{Name: "General", Slug: "general", DefinitionRef: verified.Ref}
	missing := base
	missing.DefinitionRef = DefinitionRef{}
	if _, err := svc.ProvisionGeneralChat(t.Context(), host, missing); !errors.Is(err, ErrLogicalAgentProvisionInput) {
		t.Fatal("missing pin accepted", err)
	}
	foreign := base
	foreign.ModelSelection = &ModelSelection{Provider: "unconfigured", Model: "unconfigured"}
	if _, err := svc.ProvisionGeneralChat(t.Context(), host, foreign); !errors.Is(err, ErrUnsupportedModel) {
		t.Fatal("unapproved model accepted", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	host.Models = ModelAuthorizerFunc(func(context.Context, DefinitionRef, *ModelSelection) (ModelSelection, error) {
		cancel()
		return ModelSelection{"fixture-provider", "fixture-model"}, nil
	})
	if _, err := svc.ProvisionGeneralChat(ctx, host, base); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled admission accepted", err)
	}
	var count int
	if err := st.DB.QueryRow(`SELECT count(*) FROM agent_profiles WHERE slug = 'general'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("rejected admission changed database", err)
	}
}
